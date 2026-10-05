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
// names three fields regardless of what is actually missing, and is phrased
// for BIG-IP even when another family was sent, so local validation is what
// gives the user an accurate list — and it costs nothing against a
// 10-requests-per-minute rate limit.
func TestCaseCreateRequiredFields(t *testing.T) {
	required := cases.RequiredCreateFields()
	if len(required) == 0 {
		t.Fatal("RequiredCreateFields returned nothing")
	}

	// The entitlement identifier is NOT in the fixed list, because which one
	// the API demands depends on the product family.
	for _, f := range required {
		if f == "serialNumber" || f == "subscriptionId" {
			t.Errorf("%q must not be in the fixed required list: the identifier is family-dependent, so a fixed entry would demand a serial from NGINX", f)
		}
	}

	// The returned slice must be a copy; mutating it must not affect the next
	// caller's validation.
	required[0] = "mutated"
	if cases.RequiredCreateFields()[0] == "mutated" {
		t.Error("RequiredCreateFields leaked its backing array")
	}
}

// Each product family is verified by a different entitlement identifier.
// Both rules below were observed live on 2026-10-05:
//
//	BIG-IP without serialNumber   -> 400 "Serial Number,Product Family and
//	                                      Product are required to create Case."
//	NGINX  without subscriptionId -> 403 "Unable to verify Subscription..."
//
// Getting this wrong means demanding a serial number from an NGINX user who
// does not have one, so it is pinned.
func TestEntitlementFieldPerFamily(t *testing.T) {
	cases_ := map[string]string{
		"BIG-IP":     "serialNumber",
		"F5OS":       "serialNumber",
		"NGINX":      "subscriptionId",
		"Aspen Mesh": "subscriptionId",
		// An unknown family falls back to the most common case rather than
		// skipping the check entirely.
		"Something New": "serialNumber",
	}
	for family, want := range cases_ {
		if got := cases.EntitlementFieldFor(family); got != want {
			t.Errorf("EntitlementFieldFor(%q) = %q, want %q", family, got, want)
		}
	}
}
