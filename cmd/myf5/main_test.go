package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	blanked := []string{"downloads", "cases", "ihealth", "bigip", "bigiq", "push", "pull", "geoip"}
	for _, cmd := range blanked {
		for _, flag := range []string{"--help", "-h", "help"} {
			if got := normalizeHelpArgs(cmd, []string{flag}); got != nil {
				t.Errorf("normalizeHelpArgs(%q, [%q]) = %v, want nil so the handler prints usage", cmd, flag, got)
			}
		}
	}

	// doctor runs its audit on a bare invocation, and wizard starts interactive mode,
	// so blanking their arguments would make --help perform an action instead of printing help.
	for _, cmd := range []string{"doctor", "wizard"} {
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

func TestGetSystemInfoTarget_QKViewFile(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	files := map[string]string{
		"VERSION":              "Product: BIG-IP\nVersion: 17.5.1\nBuild: 0.0.1\nEdition: Final\n",
		"config/bigip.license": "Registration Key : TEST-REG-KEY-12345\nSerial Number : test-serial-999\n",
	}
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0600,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader failed: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close tar failed: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("Close gzip failed: %v", err)
	}

	tmpFile := filepath.Join(t.TempDir(), "test.qkview")
	if err := os.WriteFile(tmpFile, buf.Bytes(), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	info := getSystemInfoTarget(context.Background(), tmpFile, "BIG-IP", "", 0, "", "auto", "", "")
	if info == nil {
		t.Fatal("getSystemInfoTarget returned nil")
	}
	if info.Product != "BIG-IP" {
		t.Errorf("Product = %q; want BIG-IP", info.Product)
	}
	if info.Version != "17.5.1" {
		t.Errorf("Version = %q; want 17.5.1", info.Version)
	}
	if info.PreferredSerialNumber() != "test-serial-999" {
		t.Errorf("PreferredSerialNumber = %q; want test-serial-999", info.PreferredSerialNumber())
	}
}

func runWithExitCapture(fn func()) (exitCode int, exited bool) {
	prevExit := exitFunc
	defer func() {
		exitFunc = prevExit
		if r := recover(); r != nil {
			if ep, ok := r.(exitPanic); ok {
				exitCode = ep.code
				exited = true
			} else {
				panic(r)
			}
		}
	}()
	exitFunc = func(code int) {
		panic(exitPanic{code: code})
	}
	fn()
	return 0, false
}

func TestExecuteSubcommand_GeoIPUsage(t *testing.T) {
	code, exited := runWithExitCapture(func() {
		executeSubcommand(context.Background(), "geoip", []string{})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit code 1 for empty geoip args, got code=%d exited=%v", code, exited)
	}

	code, exited = runWithExitCapture(func() {
		executeSubcommand(context.Background(), "bigiq", []string{"geoip", "192.0.2.1", "file.zip"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit code 1 for bigiq geoip rejection, got code=%d exited=%v", code, exited)
	}
}

func TestCaseComment_Usage(t *testing.T) {
	code, exited := runWithExitCapture(func() {
		handleCases(context.Background(), []string{"comment"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit code 1 for comment without args, got code=%d exited=%v", code, exited)
	}
}

func TestCaseCreate_NoArgsNonTTY(t *testing.T) {
	code, exited := runWithExitCapture(func() {
		handleCaseCreate(context.Background(), nil, []string{})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit code 1 for case create without args, got code=%d exited=%v", code, exited)
	}
}

func TestCaseCreate_LifecycleWarnings(t *testing.T) {
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stderr = w

	args := []string{
		"--dry-run",
		"--family", "BIG-IP",
		"--product", "BIG-IP 2000s",
		"--version", "15.1.8",
		"--serial", "test-serial-12345",
		"--priority", "4",
		"--reason", "General Problem",
		"--subject", "Testing EOL",
		"--description", "EOL description",
		"--email", "user@example.com",
		"--timezone", "(GMT -05:00) EST",
	}

	handleCaseCreate(context.Background(), nil, args)

	w.Close()
	os.Stderr = origStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)
	errOutput := buf.String()

	if !strings.Contains(errOutput, "[WARNING] TMOS version 15.1.8 reached End of Life") {
		t.Errorf("expected TMOS EOL warning in stderr, got: %q", errOutput)
	}
	if !strings.Contains(errOutput, "[WARNING] Hardware platform BIG-IP 2000s reached End of Life") {
		t.Errorf("expected Hardware EOL warning in stderr, got: %q", errOutput)
	}
	if !strings.Contains(errOutput, "Software Rec:") {
		t.Errorf("expected Software Rec in stderr, got: %q", errOutput)
	}
	if !strings.Contains(errOutput, "Hardware Rec:") {
		t.Errorf("expected Hardware Rec in stderr, got: %q", errOutput)
	}
}
