package progress

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.00 KB"},
		{1024 * 1024, "1.00 MB"},
		{1024 * 1024 * 1024 * 2, "2.00 GB"},
	}

	for _, tc := range tests {
		got := FormatBytes(tc.input)
		if got != tc.expected {
			t.Errorf("FormatBytes(%d) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input    time.Duration
		expected string
	}{
		{5 * time.Second, "00:05"},
		{65 * time.Second, "01:05"},
		{3665 * time.Second, "01:01:05"},
	}

	for _, tc := range tests {
		got := FormatDuration(tc.input)
		if got != tc.expected {
			t.Errorf("FormatDuration(%v) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestProgressBarNonTTY(t *testing.T) {
	var buf bytes.Buffer
	bar := NewBar(1000, "Downloading")
	bar.Writer = &buf
	bar.isTTY = false

	bar.Update(500)
	bar.Update(1000)
	bar.Finish("Download completed")

	out := buf.String()
	if !strings.Contains(out, "Downloading:  50.0%") {
		t.Errorf("expected 50%% log, got: %s", out)
	}
	if !strings.Contains(out, "[✔] Download completed") {
		t.Errorf("expected completion message, got: %s", out)
	}
}

func TestSpinnerNonTTY(t *testing.T) {
	var buf bytes.Buffer
	spin := NewSpinner("Generating QKView")
	spin.Writer = &buf
	spin.isTTY = false

	spin.Start()
	spin.SetMessage("Gathering system logs")
	spin.Success("QKView ready")

	out := buf.String()
	if !strings.Contains(out, "[*] Generating QKView...") {
		t.Errorf("expected start message, got: %s", out)
	}
	if !strings.Contains(out, "Status: Gathering system logs") {
		t.Errorf("expected status update, got: %s", out)
	}
	if !strings.Contains(out, "[✔] QKView ready") {
		t.Errorf("expected success message, got: %s", out)
	}
}

func TestProgressBarAbort(t *testing.T) {
	var buf bytes.Buffer
	bar := NewBar(1000, "Downloading")
	bar.Writer = &buf
	bar.isTTY = false

	bar.Update(250)
	bar.Abort("Cancelled by user")

	out := buf.String()
	if !strings.Contains(out, "[✖] Cancelled by user") {
		t.Errorf("expected abort message, got: %s", out)
	}
}

func TestSpinnerAbort(t *testing.T) {
	var buf bytes.Buffer
	spin := NewSpinner("Generating QKView")
	spin.Writer = &buf
	spin.isTTY = false

	spin.Start()
	spin.Abort("Aborted by user")

	out := buf.String()
	if !strings.Contains(out, "[✖] Aborted by user") {
		t.Errorf("expected abort message, got: %s", out)
	}
}
