//go:build windows

package downloads

import "testing"

func TestFileNameFromURL_WindowsReserved(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://cdn.example/NUL", "download.bin"},
		{"https://cdn.example/CON", "download.bin"},
		{"https://cdn.example/COM1", "download.bin"},
		{"https://cdn.example/ok.iso", "ok.iso"},
	}

	for _, tt := range tests {
		got := fileNameFromURL(tt.input)
		if got != tt.expected {
			t.Errorf("fileNameFromURL(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}
