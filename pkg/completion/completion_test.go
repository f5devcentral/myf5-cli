package completion

import (
	"bytes"
	"strings"
	"testing"
)

func TestSupportedShells(t *testing.T) {
	shells := SupportedShells()
	if len(shells) != 4 {
		t.Fatalf("expected 4 supported shells, got %d", len(shells))
	}
	expected := map[string]bool{"bash": true, "zsh": true, "fish": true, "powershell": true}
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

	// The script must REGISTER the completion function, not invoke it.
	//
	// A bare trailing `_myf5 "$@"` calls the function at source time, outside
	// any completion context, so the first _arguments call aborts with
	//   _arguments:comparguments: can only be called from completion function
	// and tab completion never works. Registration via compdef is the correct
	// idiom. Checking only for the presence of "#compdef"/"_myf5" does not
	// catch this — both survive the broken form — so assert on the call.
	if !strings.Contains(out, "compdef _myf5 myf5") {
		t.Errorf("zsh completion script must register via 'compdef _myf5 myf5'")
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == `_myf5 "$@"` {
			t.Errorf("zsh completion script invokes _myf5 at source time; it must only define and register it")
		}
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

func TestGeneratePowerShell(t *testing.T) {
	for _, sh := range []string{"powershell", "pwsh"} {
		var buf bytes.Buffer
		err := Generate(sh, &buf)
		if err != nil {
			t.Fatalf("unexpected error generating %s completion: %v", sh, err)
		}
		out := buf.String()
		if !strings.Contains(out, "Register-ArgumentCompleter") {
			t.Errorf("%s completion script missing Register-ArgumentCompleter", sh)
		}
		if !strings.Contains(out, "-CommandName myf5") {
			t.Errorf("%s completion script missing -CommandName myf5", sh)
		}
	}
}

func TestGenerateUnsupported(t *testing.T) {
	var buf bytes.Buffer
	err := Generate("csh", &buf)
	if err == nil {
		t.Fatalf("expected error for unsupported shell 'csh', got nil")
	}
}
