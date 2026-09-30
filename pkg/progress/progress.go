package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

var (
	brailleFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

// Bar represents a terminal progress bar for bounded transfers (uploads/downloads).
type Bar struct {
	Total       int64
	Current     int64
	Label       string
	StartTime   time.Time
	LastUpdate  time.Time
	Writer      io.Writer
	isTTY       bool
	mu          sync.Mutex
	lastLogPct  int
	lastLogTime time.Time
	finished    bool
	width       int
}

// NewBar creates a new progress bar with a total byte count and label.
func NewBar(total int64, label string) *Bar {
	w := os.Stdout
	isTTY := term.IsTerminal(int(w.Fd()))
	return &Bar{
		Total:       total,
		Label:       label,
		StartTime:   time.Now(),
		LastUpdate:  time.Now(),
		Writer:      w,
		isTTY:       isTTY,
		width:       25,
		lastLogPct:  -1,
		lastLogTime: time.Now(),
	}
}

// Update updates the progress meter with the current byte count.
func (b *Bar) Update(current int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.finished {
		return
	}
	b.Current = current
	now := time.Now()

	// Rate limit updates to 100ms on TTY unless complete
	if b.isTTY && now.Sub(b.LastUpdate) < 100*time.Millisecond && current < b.Total {
		return
	}
	b.LastUpdate = now

	elapsed := now.Sub(b.StartTime).Seconds()
	var speed float64
	if elapsed > 0 {
		speed = float64(current) / elapsed
	}

	var pct float64
	if b.Total > 0 {
		pct = float64(current) / float64(b.Total) * 100.0
		if pct > 100.0 {
			pct = 100.0
		}
	}

	var etaStr string
	if speed > 0 && b.Total > current {
		remainingSecs := float64(b.Total-current) / speed
		etaStr = fmt.Sprintf("ETA %s", FormatDuration(time.Duration(remainingSecs)*time.Second))
	} else if current >= b.Total {
		etaStr = "ETA 0s"
	} else {
		etaStr = "ETA --"
	}

	if b.isTTY {
		// Render visual bar
		filled := 0
		if b.Total > 0 {
			filled = int((float64(current) / float64(b.Total)) * float64(b.width))
			if filled > b.width {
				filled = b.width
			}
		}
		empty := b.width - filled
		var barStr string
		if filled > 0 {
			barStr = strings.Repeat("=", filled-1) + ">" + strings.Repeat(" ", empty)
		} else {
			barStr = strings.Repeat(" ", b.width)
		}

		line := fmt.Sprintf("\r%-12s [%s] %5.1f%% (%s / %s) %s/s  %-10s",
			b.Label,
			barStr,
			pct,
			FormatBytes(current),
			FormatBytes(b.Total),
			FormatBytes(int64(speed)),
			etaStr,
		)
		_, _ = fmt.Fprint(b.Writer, line)
	} else {
		// Non-TTY (piped / CI): log at 10% milestones or every 5 seconds
		intPct := int(pct)
		if intPct/10 != b.lastLogPct || now.Sub(b.lastLogTime) >= 5*time.Second || current >= b.Total {
			b.lastLogPct = intPct / 10
			b.lastLogTime = now
			fmt.Fprintf(b.Writer, "%s: %5.1f%% (%s / %s) [%s/s] %s\n",
				b.Label, pct, FormatBytes(current), FormatBytes(b.Total), FormatBytes(int64(speed)), etaStr)
		}
	}
}

// Finish completes the progress bar and prints a summary message.
func (b *Bar) Finish(summary string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.finished {
		return
	}
	b.finished = true
	elapsed := time.Since(b.StartTime)
	var speed float64
	if elapsed.Seconds() > 0 {
		speed = float64(b.Current) / elapsed.Seconds()
	}

	if b.isTTY {
		// Clear current line and print final summary
		fmt.Fprintf(b.Writer, "\r\033[K[✔] %s (%s in %s, %s/s)\n",
			summary, FormatBytes(b.Current), FormatDuration(elapsed), FormatBytes(int64(speed)))
	} else {
		fmt.Fprintf(b.Writer, "[✔] %s (%s in %s, %s/s)\n",
			summary, FormatBytes(b.Current), FormatDuration(elapsed), FormatBytes(int64(speed)))
	}
}

// Spinner represents an animated terminal spinner for indeterminate operations (e.g. QKView generation).
type Spinner struct {
	Label     string
	Message   string
	StartTime time.Time
	Writer    io.Writer
	isTTY     bool
	stopChan  chan struct{}
	doneChan  chan struct{}
	mu        sync.Mutex
	active    bool
}

// NewSpinner creates a new indeterminate spinner.
func NewSpinner(label string) *Spinner {
	w := os.Stdout
	isTTY := term.IsTerminal(int(w.Fd()))
	return &Spinner{
		Label:    label,
		Writer:   w,
		isTTY:    isTTY,
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
	}
}

// Start begins animating the spinner.
func (s *Spinner) Start() {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.StartTime = time.Now()
	s.mu.Unlock()

	if !s.isTTY {
		fmt.Fprintf(s.Writer, "[*] %s...\n", s.Label)
		return
	}

	go func() {
		defer close(s.doneChan)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		frameIdx := 0
		for {
			select {
			case <-s.stopChan:
				return
			case now := <-ticker.C:
				s.mu.Lock()
				msg := s.Message
				elapsed := now.Sub(s.StartTime)
				frame := brailleFrames[frameIdx%len(brailleFrames)]
				frameIdx++
				s.mu.Unlock()

				var line string
				if msg != "" {
					line = fmt.Sprintf("\r\033[K[%s] %s... [%s elapsed] (%s)", frame, s.Label, FormatDuration(elapsed), msg)
				} else {
					line = fmt.Sprintf("\r\033[K[%s] %s... [%s elapsed]", frame, s.Label, FormatDuration(elapsed))
				}
				_, _ = fmt.Fprint(s.Writer, line)
			}
		}
	}()
}

// SetMessage updates the secondary status message on the spinner line.
func (s *Spinner) SetMessage(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Message = msg
	if !s.isTTY && msg != "" {
		fmt.Fprintf(s.Writer, "    -> Status: %s\n", msg)
	}
}

// Success stops the spinner and marks the operation successful.
func (s *Spinner) Success(finalMsg string) {
	s.stop()
	elapsed := time.Since(s.StartTime)
	if s.isTTY {
		fmt.Fprintf(s.Writer, "\r\033[K[✔] %s [%s]\n", finalMsg, FormatDuration(elapsed))
	} else {
		fmt.Fprintf(s.Writer, "[✔] %s [%s]\n", finalMsg, FormatDuration(elapsed))
	}
}

// Fail stops the spinner and marks the operation failed.
func (s *Spinner) Fail(errMsg string) {
	s.stop()
	elapsed := time.Since(s.StartTime)
	if s.isTTY {
		fmt.Fprintf(s.Writer, "\r\033[K[✖] %s [%s]\n", errMsg, FormatDuration(elapsed))
	} else {
		fmt.Fprintf(s.Writer, "[✖] %s [%s]\n", errMsg, FormatDuration(elapsed))
	}
}

func (s *Spinner) stop() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	s.mu.Unlock()

	if s.isTTY {
		close(s.stopChan)
		<-s.doneChan
	}
}

// FormatBytes formats byte counts into human-readable strings (e.g. 2.45 GB).
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatDuration formats durations compactly into mm:ss or hh:mm:ss.
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
