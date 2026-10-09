package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

var (
	// ErrSelectBack is returned when the user presses 'b' or Esc to navigate back.
	ErrSelectBack = errors.New("selection cancelled: back requested")

	// ErrSelectQuit is returned when the user presses 'q' or Ctrl+C to abort.
	ErrSelectQuit = errors.New("selection aborted: quit requested")
)

// SelectItem represents an individual entry in a selection list.
type SelectItem struct {
	ID          string      // Optional ID or key (e.g. "1", "BIG-IP")
	Title       string      // Primary display label
	Description string      // Optional description or secondary info
	Value       interface{} // Underlying payload
}

// SelectConfig configures the interactive selection menu.
type SelectConfig struct {
	Title        string        // Header title displayed above items
	Items        []SelectItem  // List of items to select from
	DefaultIndex int           // Initial selected index (0-based)
	MaxVisible   int           // Max visible rows in viewport (default: 10 or auto-sized)
	AllowFilter  bool          // Enable '/' live keyword filtering
	AllowBack    bool          // Allow 'b' or Esc to navigate back
	BackLabel    string        // Label for back option (default: "Back")
	Prompt       string        // Optional custom prompt text
	Reader       *bufio.Reader // Fallback reader for non-TTY mode
	In           io.Reader     // Input source (default: os.Stdin)
	Out          io.Writer     // Output sink (default: os.Stdout)
}

// SelectMenu presents an interactive selection list.
// In terminal TTY mode, it provides arrow-key (↑/↓, j/k) navigation, scrolling viewports
// for long lists, scroll indicators (▲/▼), live keyword filtering ('/'), direct number jumping,
// and Enter to confirm.
// In non-TTY environments (pipes, scripts, automated tests), it falls back seamlessly to
// a standard numbered prompt.
func SelectMenu(cfg SelectConfig) (int, SelectItem, error) {
	if len(cfg.Items) == 0 {
		return -1, SelectItem{}, errors.New("cannot display selection menu with empty items")
	}

	// Normalize defaults
	if cfg.In == nil {
		cfg.In = os.Stdin
	}
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	if cfg.Reader == nil {
		cfg.Reader = bufio.NewReader(cfg.In)
	}
	if cfg.DefaultIndex < 0 || cfg.DefaultIndex >= len(cfg.Items) {
		cfg.DefaultIndex = 0
	}

	// Detect if both input and output are interactive TTYs
	inFile, inIsFile := cfg.In.(*os.File)
	outFile, outIsFile := cfg.Out.(*os.File)
	if !inIsFile || !outIsFile || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return selectFallback(cfg)
	}

	inFd := int(inFile.Fd())
	outFd := int(outFile.Fd())

	// Put terminal in raw mode for single-keystroke reading and ANSI rendering
	oldState, err := term.MakeRaw(inFd)
	if err != nil {
		return selectFallback(cfg)
	}
	defer func() {
		_ = term.Restore(inFd, oldState)
	}()

	// Hide cursor during interactive menu navigation; ensure cursor is always restored on exit
	fmt.Fprint(cfg.Out, "\033[?25l")
	defer func() {
		fmt.Fprint(cfg.Out, "\033[?25h")
	}()

	return runInteractiveLoop(cfg, inFd, outFd)
}

// visibleItem links a visible/filtered item back to its original index in cfg.Items.
type visibleItem struct {
	originalIndex int
	item          SelectItem
}

// runInteractiveLoop executes the raw-mode event loop for arrow navigation and scrolling.
func runInteractiveLoop(cfg SelectConfig, inFd, outFd int) (int, SelectItem, error) {
	// Query terminal dimensions
	termWidth, termHeight, err := term.GetSize(outFd)
	if err != nil || termWidth <= 0 {
		termWidth = 80
	}
	if err != nil || termHeight <= 0 {
		termHeight = 24
	}

	// Calculate viewport height
	maxVisible := cfg.MaxVisible
	if maxVisible <= 0 {
		maxVisible = 10
	}
	if termHeight > 8 && maxVisible > termHeight-7 {
		maxVisible = termHeight - 7
	}
	if maxVisible < 3 {
		maxVisible = 3
	}

	// Build initial visible items list
	visible := make([]visibleItem, len(cfg.Items))
	for i, it := range cfg.Items {
		visible[i] = visibleItem{originalIndex: i, item: it}
	}

	selectedIndex := cfg.DefaultIndex
	if selectedIndex >= len(visible) {
		selectedIndex = 0
	}
	topIndex := 0
	if selectedIndex >= maxVisible {
		topIndex = selectedIndex - maxVisible + 1
	}

	filterMode := false
	filterQuery := ""
	numBuffer := ""
	numBufferTime := time.Time{}
	lastLineCount := 0

	bufReader := cfg.Reader
	if bufReader == nil {
		bufReader = bufio.NewReader(cfg.In)
	}

	updateFilter := func() {
		if strings.TrimSpace(filterQuery) == "" {
			visible = make([]visibleItem, len(cfg.Items))
			for i, it := range cfg.Items {
				visible[i] = visibleItem{originalIndex: i, item: it}
			}
		} else {
			q := strings.ToLower(filterQuery)
			visible = nil
			for i, it := range cfg.Items {
				if strings.Contains(strings.ToLower(it.Title), q) ||
					strings.Contains(strings.ToLower(it.Description), q) ||
					strings.Contains(strings.ToLower(it.ID), q) {
					visible = append(visible, visibleItem{originalIndex: i, item: it})
				}
			}
		}
		selectedIndex = 0
		topIndex = 0
	}

	clampScroll := func() {
		if len(visible) == 0 {
			selectedIndex = 0
			topIndex = 0
			return
		}
		if selectedIndex < 0 {
			selectedIndex = 0
		}
		if selectedIndex >= len(visible) {
			selectedIndex = len(visible) - 1
		}
		if selectedIndex < topIndex {
			topIndex = selectedIndex
		}
		if selectedIndex >= topIndex+maxVisible {
			topIndex = selectedIndex - maxVisible + 1
		}
		if topIndex < 0 {
			topIndex = 0
		}
		maxTop := len(visible) - maxVisible
		if maxTop < 0 {
			maxTop = 0
		}
		if topIndex > maxTop {
			topIndex = maxTop
		}
	}

	for {
		clampScroll()

		// Erase previous frame if any
		if lastLineCount > 0 {
			fmt.Fprintf(cfg.Out, "\033[%dA\033[0J", lastLineCount)
		}

		// Render current frame
		currentLines := 0

		// 1. Header / Title
		if cfg.Title != "" {
			titleText := truncateRunes(cfg.Title, termWidth-1)
			fmt.Fprintf(cfg.Out, "\033[1m%s\033[0m\r\n", titleText)
			currentLines++
		}

		// 2. Filter Bar / Help Hints
		if filterMode {
			hint := fmt.Sprintf("  [Filter: %s_] (%d/%d matches) - Esc to clear, Enter to select",
				filterQuery, len(visible), len(cfg.Items))
			fmt.Fprintf(cfg.Out, "\033[1;33m%s\033[0m\r\n", truncateRunes(hint, termWidth-1))
			currentLines++
		} else if filterQuery != "" {
			hint := fmt.Sprintf("  [Filter: %s] (%d/%d matches) - '/' to edit, Esc to clear",
				filterQuery, len(visible), len(cfg.Items))
			fmt.Fprintf(cfg.Out, "\033[33m%s\033[0m\r\n", truncateRunes(hint, termWidth-1))
			currentLines++
		} else {
			help := "  (Use ↑/↓ or j/k to navigate, Enter to select, '/' to filter, 'b' back, 'q' quit)"
			if !cfg.AllowBack {
				help = "  (Use ↑/↓ or j/k to navigate, Enter to select, '/' to filter, 'q' quit)"
			}
			fmt.Fprintf(cfg.Out, "\033[2m%s\033[0m\r\n", truncateRunes(help, termWidth-1))
			currentLines++
		}

		// 3. Scroll Up Indicator
		if topIndex > 0 {
			upHint := fmt.Sprintf("    ▲ (%d more above)", topIndex)
			fmt.Fprintf(cfg.Out, "\033[2m%s\033[0m\r\n", truncateRunes(upHint, termWidth-1))
			currentLines++
		}

		// 4. Viewport Items
		if len(visible) == 0 {
			emptyHint := "    (No matching items)"
			fmt.Fprintf(cfg.Out, "\033[33m%s\033[0m\r\n", truncateRunes(emptyHint, termWidth-1))
			currentLines++
		} else {
			end := topIndex + maxVisible
			if end > len(visible) {
				end = len(visible)
			}
			for i := topIndex; i < end; i++ {
				vi := visible[i]
				itemNum := vi.originalIndex + 1
				desc := ""
				if vi.item.Description != "" {
					desc = " - " + vi.item.Description
				}

				if i == selectedIndex {
					line := fmt.Sprintf("  \033[1;36m❯ %2d) %s%s\033[0m", itemNum, vi.item.Title, desc)
					plainLen := utf8.RuneCountInString(fmt.Sprintf("  ❯ %2d) %s%s", itemNum, vi.item.Title, desc))
					if plainLen > termWidth-1 {
						truncatedTitle := truncateRunes(vi.item.Title+desc, termWidth-10)
						line = fmt.Sprintf("  \033[1;36m❯ %2d) %s\033[0m", itemNum, truncatedTitle)
					}
					fmt.Fprintf(cfg.Out, "%s\r\n", line)
				} else {
					plain := fmt.Sprintf("    %2d) %s%s", itemNum, vi.item.Title, desc)
					if utf8.RuneCountInString(plain) > termWidth-1 {
						truncatedTitle := truncateRunes(vi.item.Title+desc, termWidth-10)
						plain = fmt.Sprintf("    %2d) %s", itemNum, truncatedTitle)
					}
					fmt.Fprintf(cfg.Out, "%s\r\n", plain)
				}
				currentLines++
			}
		}

		// 5. Scroll Down Indicator
		remainingBelow := len(visible) - (topIndex + maxVisible)
		if remainingBelow > 0 {
			downHint := fmt.Sprintf("    ▼ (%d more below)", remainingBelow)
			fmt.Fprintf(cfg.Out, "\033[2m%s\033[0m\r\n", truncateRunes(downHint, termWidth-1))
			currentLines++
		}

		// 6. Status line
		if len(visible) > 0 {
			status := fmt.Sprintf("  [Item %d of %d]", selectedIndex+1, len(visible))
			fmt.Fprintf(cfg.Out, "\033[2m%s\033[0m\r\n", truncateRunes(status, termWidth-1))
			currentLines++
		}

		lastLineCount = currentLines

		// Read and process key event
		key, err := readKey(bufReader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return -1, SelectItem{}, ErrSelectQuit
			}
			return -1, SelectItem{}, err
		}

		// Handle key actions
		switch key.Type {
		case KeyEnter:
			if len(visible) == 0 {
				continue
			}
			// Clear interactive menu and display clean confirmation
			if lastLineCount > 0 {
				fmt.Fprintf(cfg.Out, "\033[%dA\033[0J", lastLineCount)
			}
			chosen := visible[selectedIndex]
			fmt.Fprintf(cfg.Out, "\033[1;32m✓\033[0m \033[1mSelected:\033[0m %s\r\n", chosen.item.Title)
			return chosen.originalIndex, chosen.item, nil

		case KeyUp:
			if selectedIndex > 0 {
				selectedIndex--
			}

		case KeyDown:
			if selectedIndex < len(visible)-1 {
				selectedIndex++
			}

		case KeyPageUp:
			selectedIndex -= maxVisible
			if selectedIndex < 0 {
				selectedIndex = 0
			}

		case KeyPageDown:
			selectedIndex += maxVisible
			if selectedIndex >= len(visible) {
				selectedIndex = len(visible) - 1
			}

		case KeyHome:
			selectedIndex = 0
			topIndex = 0

		case KeyEnd:
			if len(visible) > 0 {
				selectedIndex = len(visible) - 1
			}

		case KeyBack:
			if filterMode {
				filterMode = false
				filterQuery = ""
				updateFilter()
				continue
			}
			if filterQuery != "" {
				filterQuery = ""
				updateFilter()
				continue
			}
			if cfg.AllowBack {
				if lastLineCount > 0 {
					fmt.Fprintf(cfg.Out, "\033[%dA\033[0J", lastLineCount)
				}
				return -1, SelectItem{}, ErrSelectBack
			}

		case KeyQuit:
			if lastLineCount > 0 {
				fmt.Fprintf(cfg.Out, "\033[%dA\033[0J", lastLineCount)
			}
			return -1, SelectItem{}, ErrSelectQuit

		case KeyBackspace:
			if filterMode && len(filterQuery) > 0 {
				r := []rune(filterQuery)
				filterQuery = string(r[:len(r)-1])
				updateFilter()
			}

		case KeySearch:
			if cfg.AllowFilter && !filterMode {
				filterMode = true
			}

		case KeyRune:
			if filterMode {
				filterQuery += string(key.Rune)
				updateFilter()
				continue
			}

			// Normal mode key bindings
			switch key.Rune {
			case 'k', 'K':
				if selectedIndex > 0 {
					selectedIndex--
				}
			case 'j', 'J':
				if selectedIndex < len(visible)-1 {
					selectedIndex++
				}
			case 'g':
				selectedIndex = 0
				topIndex = 0
			case 'G':
				if len(visible) > 0 {
					selectedIndex = len(visible) - 1
				}
			case 'b', 'B':
				if cfg.AllowBack {
					if lastLineCount > 0 {
						fmt.Fprintf(cfg.Out, "\033[%dA\033[0J", lastLineCount)
					}
					return -1, SelectItem{}, ErrSelectBack
				}
			case 'q', 'Q':
				if lastLineCount > 0 {
					fmt.Fprintf(cfg.Out, "\033[%dA\033[0J", lastLineCount)
				}
				return -1, SelectItem{}, ErrSelectQuit
			case '/':
				if cfg.AllowFilter {
					filterMode = true
				}
			default:
				// Number jumping in normal mode (1-9)
				if key.Rune >= '0' && key.Rune <= '9' {
					if time.Since(numBufferTime) > 800*time.Millisecond {
						numBuffer = ""
					}
					numBuffer += string(key.Rune)
					numBufferTime = time.Now()
					if val, pErr := strconv.Atoi(numBuffer); pErr == nil && val >= 1 && val <= len(visible) {
						selectedIndex = val - 1
					}
				}
			}
		}
	}
}

// selectFallback handles non-TTY environments (pipes, redirected input, automated tests).
func selectFallback(cfg SelectConfig) (int, SelectItem, error) {
	for {
		if cfg.Title != "" {
			fmt.Fprintln(cfg.Out, cfg.Title)
		}
		for i, it := range cfg.Items {
			desc := ""
			if it.Description != "" {
				desc = " - " + it.Description
			}
			fmt.Fprintf(cfg.Out, "  %2d) %s%s\n", i+1, it.Title, desc)
		}

		prompt := cfg.Prompt
		if prompt == "" {
			if cfg.AllowBack {
				prompt = fmt.Sprintf("Enter selection [1-%d] (or 'b' to go back, 'q' to quit)", len(cfg.Items))
			} else {
				prompt = fmt.Sprintf("Enter selection [1-%d] (or 'q' to quit)", len(cfg.Items))
			}
		}

		defStr := ""
		if cfg.DefaultIndex >= 0 && cfg.DefaultIndex < len(cfg.Items) {
			defStr = fmt.Sprintf("%d", cfg.DefaultIndex+1)
			fmt.Fprintf(cfg.Out, "\n%s [%s]: ", prompt, defStr)
		} else {
			fmt.Fprintf(cfg.Out, "\n%s: ", prompt)
		}

		line, err := cfg.Reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && strings.TrimSpace(line) != "" {
				// Process trailing line before EOF
			} else {
				return -1, SelectItem{}, err
			}
		}

		line = strings.TrimSpace(line)
		if line == "" && defStr != "" {
			line = defStr
		}

		if (line == "b" || line == ":b" || strings.EqualFold(line, "back")) && cfg.AllowBack {
			return -1, SelectItem{}, ErrSelectBack
		}
		if line == "q" || line == ":q" || strings.EqualFold(line, "quit") || strings.EqualFold(line, "exit") {
			return -1, SelectItem{}, ErrSelectQuit
		}

		idx, aErr := strconv.Atoi(line)
		if aErr == nil && idx >= 1 && idx <= len(cfg.Items) {
			return idx - 1, cfg.Items[idx-1], nil
		}

		// Also allow matching by title or ID directly
		matched := false
		for i, it := range cfg.Items {
			if strings.EqualFold(it.Title, line) || (it.ID != "" && strings.EqualFold(it.ID, line)) {
				return i, it, nil
			}
		}

		if !matched {
			fmt.Fprintln(cfg.Out, "Invalid selection. Please choose a valid item number.")
		}
	}
}

// KeyType enumerates categorized terminal key events.
type KeyType int

const (
	KeyNone KeyType = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyPageUp
	KeyPageDown
	KeyHome
	KeyEnd
	KeyEnter
	KeyBack
	KeyQuit
	KeyBackspace
	KeyTab
	KeySearch
	KeyRune
)

// KeyEvent represents a decoded keyboard event.
type KeyEvent struct {
	Type KeyType
	Rune rune
}

// readKey decodes single keypresses and ANSI escape sequences from a *bufio.Reader.
func readKey(r *bufio.Reader) (KeyEvent, error) {
	b, err := r.ReadByte()
	if err != nil {
		return KeyEvent{Type: KeyQuit}, err
	}

	// Single control bytes
	switch b {
	case 3, 4: // Ctrl+C, Ctrl+D
		return KeyEvent{Type: KeyQuit}, nil
	case 10, 13: // Enter (\n, \r)
		return KeyEvent{Type: KeyEnter}, nil
	case 8, 127: // Backspace
		return KeyEvent{Type: KeyBackspace}, nil
	case 9: // Tab
		return KeyEvent{Type: KeyTab}, nil
	case 27: // Escape or ANSI escape sequence
		if r.Buffered() == 0 {
			return KeyEvent{Type: KeyBack}, nil
		}
		peek, _ := r.Peek(1)
		if len(peek) == 0 || (peek[0] != '[' && peek[0] != 'O') {
			return KeyEvent{Type: KeyBack}, nil
		}
		// Consume '[' or 'O'
		_, _ = r.ReadByte()
		// Read sequence identifier byte
		b3, err := r.ReadByte()
		if err != nil {
			return KeyEvent{Type: KeyBack}, nil
		}
		switch b3 {
		case 'A':
			return KeyEvent{Type: KeyUp}, nil
		case 'B':
			return KeyEvent{Type: KeyDown}, nil
		case 'C':
			return KeyEvent{Type: KeyRight}, nil
		case 'D':
			return KeyEvent{Type: KeyLeft}, nil
		case 'H':
			return KeyEvent{Type: KeyHome}, nil
		case 'F':
			return KeyEvent{Type: KeyEnd}, nil
		case '1', '7':
			if r.Buffered() > 0 {
				if tilde, _ := r.Peek(1); len(tilde) > 0 && tilde[0] == '~' {
					_, _ = r.ReadByte()
				}
			}
			return KeyEvent{Type: KeyHome}, nil
		case '4', '8':
			if r.Buffered() > 0 {
				if tilde, _ := r.Peek(1); len(tilde) > 0 && tilde[0] == '~' {
					_, _ = r.ReadByte()
				}
			}
			return KeyEvent{Type: KeyEnd}, nil
		case '5':
			if r.Buffered() > 0 {
				if tilde, _ := r.Peek(1); len(tilde) > 0 && tilde[0] == '~' {
					_, _ = r.ReadByte()
				}
			}
			return KeyEvent{Type: KeyPageUp}, nil
		case '6':
			if r.Buffered() > 0 {
				if tilde, _ := r.Peek(1); len(tilde) > 0 && tilde[0] == '~' {
					_, _ = r.ReadByte()
				}
			}
			return KeyEvent{Type: KeyPageDown}, nil
		case '3':
			if r.Buffered() > 0 {
				if tilde, _ := r.Peek(1); len(tilde) > 0 && tilde[0] == '~' {
					_, _ = r.ReadByte()
				}
			}
			return KeyEvent{Type: KeyBackspace}, nil // Delete
		}
		return KeyEvent{Type: KeyBack}, nil
	}

	// ASCII printable or UTF-8
	if b < 0x80 {
		return KeyEvent{Type: KeyRune, Rune: rune(b)}, nil
	}

	// Multi-byte UTF-8 rune decoding
	_ = r.UnreadByte()
	rChar, _, err := r.ReadRune()
	if err != nil {
		return KeyEvent{Type: KeyNone}, err
	}
	return KeyEvent{Type: KeyRune, Rune: rChar}, nil
}

// truncateRunes clamps string length to maxRunes, appending "..." if exceeded.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 3 {
		return s
	}
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-3]) + "..."
	}
	return s
}

// selectNavField provides a wizard-compatible selection prompt with navigation support.
// In terminal TTY mode, it provides arrow-key selection; in non-TTY mode, it falls back
// to a numbered prompt compatible with automated test harnesses.
func selectNavField(reader *bufio.Reader, in io.Reader, out io.Writer, title string, prompt string, items []SelectItem, defChoice string) (string, error) {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	if reader == nil {
		reader = bufio.NewReader(in)
	}

	defIdx := -1
	for i, it := range items {
		if it.ID == defChoice || strings.EqualFold(it.Title, defChoice) {
			defIdx = i
			break
		}
	}
	if defIdx < 0 {
		if num, err := strconv.Atoi(defChoice); err == nil && num >= 1 && num <= len(items) {
			defIdx = num - 1
		}
	}

	cfg := SelectConfig{
		Title:        title,
		Items:        items,
		DefaultIndex: defIdx,
		AllowBack:    true,
		Prompt:       prompt,
		Reader:       reader,
		In:           in,
		Out:          out,
	}

	_, item, err := SelectMenu(cfg)
	if err != nil {
		if errors.Is(err, ErrSelectBack) {
			return "", ErrWizardBack
		}
		return "", err
	}
	if item.ID != "" {
		return item.ID, nil
	}
	return item.Title, nil
}
