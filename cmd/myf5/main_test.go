package main

import (
	"testing"

	"myf5-cli/pkg/cases"
)

func TestVersion(t *testing.T) {
	if Version != "1.1.0" {
		t.Errorf("expected Version to be '1.1.0', got %s", Version)
	}
}

// The top-level usage text promises `myf5 <command> --help` works. These pin
// that promise: every command either has its arguments blanked so its handler
// prints usage, or is on the short list that inspects the flag itself.
func TestNormalizeHelpArgs(t *testing.T) {
	// Commands whose handlers print usage when called with no arguments.
	blanked := []string{"downloads", "cases", "ihealth", "bigip", "bigiq", "push", "pull"}
	for _, cmd := range blanked {
		for _, flag := range []string{"--help", "-h", "help"} {
			if got := normalizeHelpArgs(cmd, []string{flag}); got != nil {
				t.Errorf("normalizeHelpArgs(%q, [%q]) = %v, want nil so the handler prints usage", cmd, flag, got)
			}
		}
	}

	// doctor runs its audit on a bare invocation, so blanking its arguments
	// would make `doctor --help` perform a live credential and network audit.
	// completion takes a shell name and documents itself when called bare.
	for _, cmd := range []string{"doctor", "completion"} {
		got := normalizeHelpArgs(cmd, []string{"--help"})
		if len(got) != 1 || got[0] != "--help" {
			t.Errorf("normalizeHelpArgs(%q, [--help]) = %v, want the flag preserved for the handler to see", cmd, got)
		}
	}

	// Real arguments must never be mistaken for a help request.
	args := []string{"list"}
	if got := normalizeHelpArgs("cases", args); len(got) != 1 || got[0] != "list" {
		t.Errorf("normalizeHelpArgs passed through %v as %v", args, got)
	}
	// A help flag alongside other arguments is not a bare help request.
	multi := []string{"upload", "--help"}
	if got := normalizeHelpArgs("ihealth", multi); len(got) != 2 {
		t.Errorf("normalizeHelpArgs(%v) = %v, want it left alone", multi, got)
	}
}

// CreateCase must reject an incomplete payload locally. The API's own message
// names three fields regardless of what is actually missing, so local
// validation is what gives the user an accurate list — and it costs nothing
// against a 10-requests-per-minute rate limit.
func TestCaseCreateRequiredFields(t *testing.T) {
	required := cases.RequiredCreateFields()
	if len(required) == 0 {
		t.Fatal("RequiredCreateFields returned nothing")
	}

	// serialNumber is the field the published OpenAPI document calls optional
	// and the live API demands. If it ever leaves this list, `cases create`
	// starts failing against the real API with a misleading message.
	var hasSerial bool
	for _, f := range required {
		if f == "serialNumber" {
			hasSerial = true
		}
	}
	if !hasSerial {
		t.Error("serialNumber must be required: the live API rejects a create without it, despite the spec calling it optional")
	}

	// The returned slice must be a copy; mutating it must not affect the next
	// caller's validation.
	required[0] = "mutated"
	if cases.RequiredCreateFields()[0] == "mutated" {
		t.Error("RequiredCreateFields leaked its backing array")
	}
}
