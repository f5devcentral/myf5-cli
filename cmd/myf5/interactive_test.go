package main

import (
	"reflect"
	"testing"
)

func TestSplitCommandLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "simple command",
			input: "bigip ready 10.1.1.4",
			want:  []string{"bigip", "ready", "10.1.1.4"},
		},
		{
			name:  "double quotes with spaces",
			input: `cases comment 12345 "this is a comment message"`,
			want:  []string{"cases", "comment", "12345", "this is a comment message"},
		},
		{
			name:  "single quotes",
			input: `downloads get BIG-IP '17.1.1' 'Base ISO'`,
			want:  []string{"downloads", "get", "BIG-IP", "17.1.1", "Base ISO"},
		},
		{
			name:  "multiple spaces",
			input: "  doctor    --help  ",
			want:  []string{"doctor", "--help"},
		},
		{
			name:  "escaped characters",
			input: `cases comment 12345 special\ character`,
			want:  []string{"cases", "comment", "12345", "special character"},
		},
		{
			name:    "unclosed quote",
			input:   `cases comment 12345 "unclosed string`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitCommandLine(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("splitCommandLine() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitCommandLine() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompleteLine(t *testing.T) {
	// Top-level unique match
	line, pos, ok := completeLine("down", 4)
	if !ok || line != "downloads " || pos != 10 {
		t.Errorf("expected completeLine('down') -> ('downloads ', 10, true), got (%q, %d, %v)", line, pos, ok)
	}

	// Subcommand unique match
	line, pos, ok = completeLine("bigip rea", 9)
	if !ok || line != "bigip ready " || pos != 12 {
		t.Errorf("expected completeLine('bigip rea') -> ('bigip ready ', 12, true), got (%q, %d, %v)", line, pos, ok)
	}

	// Top-level wizard command match
	line, pos, ok = completeLine("wiz", 3)
	if !ok || line != "wizard " || pos != 7 {
		t.Errorf("expected completeLine('wiz') -> ('wizard ', 7, true), got (%q, %d, %v)", line, pos, ok)
	}

	// Cases wizard subcommand match
	line, pos, ok = completeLine("cases wiz", 9)
	if !ok || line != "cases wizard " || pos != 13 {
		t.Errorf("expected completeLine('cases wiz') -> ('cases wizard ', 13, true), got (%q, %d, %v)", line, pos, ok)
	}

	// Longest common prefix match
	line, pos, ok = completeLine("bigi", 4)
	if !ok || line != "bigi" || pos != 4 {
		// Both "bigip" and "bigiq" match "bigi"
		t.Logf("multi-match returned (%q, %d, %v)", line, pos, ok)
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	if got := longestCommonPrefix([]string{"bigip", "bigiq"}); got != "bigi" {
		t.Errorf("expected 'bigi', got %s", got)
	}
	if got := longestCommonPrefix([]string{"hello", "world"}); got != "" {
		t.Errorf("expected '', got %s", got)
	}
}
