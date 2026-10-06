package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"golang.org/x/term"
)

type exitPanic struct {
	code int
}

// exitFunc is the process termination function, replaced during interactive mode
// so failed commands return to the shell prompt instead of exiting the process.
var exitFunc = os.Exit

// startInteractive launches the interactive CLI shell with command history
// and tab-completion.
func startInteractive(ctx context.Context) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fmt.Fprintln(os.Stderr, "[ERROR] Interactive mode requires a terminal TTY.")
		exitFunc(1)
		return
	}

	fmt.Println(`========================================================================
  myf5 Interactive Shell (v1.1.0)
  Type 'help' for available commands, 'exit' or Ctrl-D to quit.
  Press TAB to autocomplete commands, subcommands, and flags.
========================================================================`)

	// Intercept exitFunc so commands do not kill the interactive shell
	prevExit := exitFunc
	exitFunc = func(code int) {
		panic(exitPanic{code: code})
	}
	defer func() {
		exitFunc = prevExit
	}()

	for {
		// Put terminal in raw mode for line editing, history, and tab completion
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Failed to set raw terminal: %v\n", err)
			return
		}

		terminal := term.NewTerminal(struct {
			io.Reader
			io.Writer
		}{os.Stdin, os.Stdout}, "myf5> ")

		terminal.AutoCompleteCallback = func(line string, pos int, key rune) (string, int, bool) {
			if key != '\t' {
				return "", 0, false
			}
			return completeLine(line, pos)
		}

		line, err := terminal.ReadLine()
		_ = term.Restore(fd, oldState)

		if err != nil {
			// EOF (Ctrl-D) or terminal closed
			fmt.Println("\nExiting interactive shell.")
			return
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if trimmed == "exit" || trimmed == "quit" || trimmed == "q" {
			fmt.Println("Goodbye.")
			return
		}

		if trimmed == "clear" || trimmed == "cls" {
			fmt.Print("\033[H\033[2J")
			continue
		}

		args, err := splitCommandLine(trimmed)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Syntax error: %v\n", err)
			continue
		}
		if len(args) == 0 {
			continue
		}

		// Execute command in-process with panic recovery for exitFunc
		runSafely(ctx, args)
	}
}

// runSafely executes a command line, recovering from exitPanic.
func runSafely(ctx context.Context, args []string) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(exitPanic); ok {
				// Handled command exit; return to prompt
				return
			}
			// Unexpected panic: rethrow
			panic(r)
		}
	}()

	executeSubcommand(ctx, args[0], args[1:])
}

// splitCommandLine splits a string into arguments respecting single and double quotes.
func splitCommandLine(line string) ([]string, error) {
	var args []string
	var current strings.Builder
	var inQuote rune
	var escaped bool

	for _, r := range line {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if r == '"' || r == '\'' {
			inQuote = r
			continue
		}
		if unicode.IsSpace(r) {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}

	if inQuote != 0 {
		return nil, fmt.Errorf("unclosed quotation mark (%c)", inQuote)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args, nil
}

// completeLine returns the autocompletion replacement for line up to pos.
func completeLine(line string, pos int) (string, int, bool) {
	prefix := line[:pos]
	lastSpace := strings.LastIndex(prefix, " ")

	var currentWord string
	var head string
	if lastSpace == -1 {
		currentWord = prefix
		head = ""
	} else {
		currentWord = prefix[lastSpace+1:]
		head = prefix[:lastSpace+1]
	}

	words := strings.Fields(prefix)
	var candidates []string

	// If completing the first word (or line is empty)
	if lastSpace == -1 || (len(words) == 1 && !strings.HasSuffix(prefix, " ")) {
		candidates = []string{
			"downloads",
			"cases",
			"wizard",
			"ihealth",
			"bigip",
			"bigiq",
			"push",
			"pull",
			"geoip",
			"doctor",
			"interactive",
			"version",
			"help",
			"clear",
			"exit",
			"quit",
		}
	} else if len(words) >= 1 {
		cmd := strings.ToLower(words[0])
		switch cmd {
		case "downloads", "download":
			candidates = []string{
				"get", "list", "lines", "versions", "containers", "files", "metadata",
				"--output", "--profile", "--push-to", "--push-method", "--push-dest",
				"--push-user", "--push-port", "--push-key", "--push-cert", "--push-cert-key",
			}
		case "cases", "case":
			candidates = []string{
				"list", "get", "comment", "create", "wizard", "metadata",
				"--json", "--all", "--family", "--product", "--version", "--serial",
				"--subscription", "--subject", "--description", "--priority", "--reason", "--email",
				"--contact-method", "--timezone", "--hostname", "--customer-ticket", "--dry-run", "--wizard",
			}
		case "ihealth":
			candidates = []string{"list", "upload", "--json"}
		case "bigip":
			candidates = []string{
				"ready", "info", "serial", "qkview", "push", "download", "geoip",
				"--method", "--user", "--port", "--dest", "-i", "--cert", "--cert-key",
				"--apply-geoip", "--keep-extracted",
				"--json", "--keep-remote",
			}
		case "bigiq":
			candidates = []string{
				"ready", "info", "serial", "push", "download",
				"--method", "--user", "--port", "--dest", "-i", "--cert", "--cert-key",
				"--json",
			}
		case "push":
			candidates = []string{
				"--method", "--dest", "--target", "--user", "--port", "-i",
				"--cert", "--cert-key", "--apply-geoip", "--keep-extracted", "--json",
			}
		case "pull", "fetch":
			candidates = []string{
				"-o", "--method", "--target", "--user", "--port", "-i",
				"--cert", "--cert-key",
			}
		case "geoip":
			candidates = []string{
				"--method", "--dest", "--user", "--port", "-i",
				"--cert", "--cert-key", "--keep-extracted", "--json",
			}
		case "wizard", "doctor", "interactive", "version", "help", "clear", "exit", "quit":
			candidates = []string{"--help"}
		}
	}

	// Filter matching candidates
	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(c, currentWord) {
			matches = append(matches, c)
		}
	}

	if len(matches) == 0 {
		return "", 0, false
	}

	if len(matches) == 1 {
		replacement := head + matches[0] + " " + line[pos:]
		newPos := len(head) + len(matches[0]) + 1
		return replacement, newPos, true
	}

	// Multiple matches: find longest common prefix
	lcp := longestCommonPrefix(matches)
	if len(lcp) > len(currentWord) {
		replacement := head + lcp + line[pos:]
		newPos := len(head) + len(lcp)
		return replacement, newPos, true
	}

	// Cannot extend further: display options cleanly
	fmt.Printf("\n%s\n", strings.Join(matches, "  "))
	return line, pos, true
}

func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, prefix) {
			if len(prefix) == 0 {
				return ""
			}
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}
