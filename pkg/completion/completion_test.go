package completion

import (
	"bytes"
	"strings"
	"testing"
)

func TestSupportedShells(t *testing.T) {
	shells := SupportedShells()
	if len(shells) != 3 {
		t.Fatalf("expected 3 supported shells, got %d", len(shells))
	}
	expected := map[string]bool{"bash": true, "zsh": true, "fish": true}
	for _, s := range shells {
		if !expected[s] {
			t.Errorf("unexpected shell %q in SupportedShells", s)
		}
	}
}

func TestGenerateBash(t *testing.T) {
	var buf bytes.Buffer
	err := Generate("bash", &buf)
	if err != nil {
		t.Fatalf("unexpected error generating bash completion: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "_myf5_completions()") {
		t.Errorf("bash completion script missing _myf5_completions()")
	}
	if !strings.Contains(out, "complete -F _myf5_completions myf5") {
		t.Errorf("bash completion script missing complete -F statement")
	}
}

func TestGenerateZsh(t *testing.T) {
	var buf bytes.Buffer
	err := Generate("zsh", &buf)
	if err != nil {
		t.Fatalf("unexpected error generating zsh completion: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "#compdef myf5") {
		t.Errorf("zsh completion script missing #compdef myf5")
	}
	if !strings.Contains(out, "_myf5") {
		t.Errorf("zsh completion script missing _myf5 function")
	}
}

func TestGenerateFish(t *testing.T) {
	var buf bytes.Buffer
	err := Generate("fish", &buf)
	if err != nil {
		t.Fatalf("unexpected error generating fish completion: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "complete -c myf5") {
		t.Errorf("fish completion script missing complete -c myf5")
	}
}

func TestGenerateUnsupported(t *testing.T) {
	var buf bytes.Buffer
	err := Generate("powershell", &buf)
	if err == nil {
		t.Fatalf("expected error for unsupported shell 'powershell', got nil")
	}
}
