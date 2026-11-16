package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/term"

	"myf5-cli/pkg/cases"
	"myf5-cli/pkg/client"
	"myf5-cli/pkg/downloads"
)

func TestExtractProxyFlag(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantArgs  []string
		wantProxy string
	}{
		{
			name:      "space separated proxy",
			args:      []string{"bigip", "--proxy", "http://proxy.corp:8080", "ready", "10.1.1.4"},
			wantArgs:  []string{"bigip", "ready", "10.1.1.4"},
			wantProxy: "http://proxy.corp:8080",
		},
		{
			name:      "equals separated proxy",
			args:      []string{"--proxy=socks5://127.0.0.1:1080", "cases", "list"},
			wantArgs:  []string{"cases", "list"},
			wantProxy: "socks5://127.0.0.1:1080",
		},
		{
			name:      "proxy at end without value",
			args:      []string{"doctor", "--proxy"},
			wantArgs:  []string{"doctor"},
			wantProxy: "",
		},
		{
			name:      "no proxy flag",
			args:      []string{"version"},
			wantArgs:  []string{"version"},
			wantProxy: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotArgs, gotProxy := extractProxyFlag(tc.args)
			if gotProxy != tc.wantProxy {
				t.Errorf("extractProxyFlag() proxy = %q, want %q", gotProxy, tc.wantProxy)
			}
			if len(gotArgs) != len(tc.wantArgs) {
				t.Fatalf("extractProxyFlag() args length = %d, want %d (%v)", len(gotArgs), len(tc.wantArgs), gotArgs)
			}
			for i := range gotArgs {
				if gotArgs[i] != tc.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], tc.wantArgs[i])
				}
			}
		})
	}
}

func TestUsagePrinters(t *testing.T) {
	// Exercise all printer functions to ensure they execute without panics.
	printers := []func(){
		printInteractiveUsage,
		printCasesUsage,
		printIHealthUsage,
		func() { printApplianceUsage("BIG-IP") },
		func() { printApplianceUsage("BIG-IQ") },
		func() { printApplianceUsage("auto") },
		printPushUsage,
		printPullUsage,
		printProxyUsage,
		printDoctorUsage,
		printUsage,
		printDownloadsUsage,
	}

	for i, fn := range printers {
		t.Run(fmt.Sprintf("printer_%d", i), func(t *testing.T) {
			fn()
		})
	}

	// Exercise printCommandHelp branches
	helpCommands := []string{
		"downloads", "download", "cases", "case", "wizard", "ihealth",
		"bigip", "geoip", "bigiq", "push", "pull", "fetch",
		"proxy", "doctor", "interactive", "shell", "repl", "i", "other",
	}
	for _, cmd := range helpCommands {
		t.Run("printCommandHelp_"+cmd, func(t *testing.T) {
			printCommandHelp(cmd)
		})
	}
}

func TestSuggestRemediationAndCheckErr(t *testing.T) {
	remediations := []struct {
		errStr   string
		expected string
	}{
		{"unencrypted HTTP is forbidden", "Appliance management strictly requires HTTPS"},
		{"Appliance Mode is enabled", "The TMOS bash utility is restricted"},
		{"/mgmt/tm/util/bash disabled", "The TMOS bash utility is restricted"},
		{"401 Unauthorized: invalid_client credentials", "Run 'myf5 doctor'"},
		{"HTTP 429 Too Many Requests", "Wait 60 seconds"},
		{"dial tcp: connection refused", "Verify hostname DNS resolution"},
		{"network timeout occurred", "Verify hostname DNS resolution"},
		{"directory traversal detected outside destination", "illegal directory traversal"},
		{"unrelated generic failure", ""},
	}

	for _, r := range remediations {
		rem := suggestRemediation(errors.New(r.errStr))
		if r.expected == "" && rem != "" {
			t.Errorf("expected empty remediation for %q, got %q", r.errStr, rem)
		}
		if r.expected != "" && !strings.Contains(rem, r.expected) {
			t.Errorf("expected remediation to contain %q, got %q", r.expected, rem)
		}
	}
	if rem := suggestRemediation(nil); rem != "" {
		t.Errorf("expected nil error to return empty remediation, got %q", rem)
	}

	// printDetailedError with and without prefix
	printDetailedError("TestPrefix", errors.New("401 Unauthorized"))
	printDetailedError("", errors.New("generic error"))

	// checkErr: nil error does not exit
	checkErr(context.Background(), nil, "ignored")

	// checkErr: cancelled context exits with 130
	code, exited := runWithExitCapture(func() {
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		checkErr(canceledCtx, context.Canceled, "cancelled op")
	})
	if !exited || code != 130 {
		t.Errorf("expected exit code 130 for cancelled context, got code=%d exited=%v", code, exited)
	}

	// checkErr: general error exits with 1
	code, exited = runWithExitCapture(func() {
		checkErr(context.Background(), errors.New("something failed"), "general error")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit code 1 for generic error, got code=%d exited=%v", code, exited)
	}
}

func TestHandleProxyCommand(t *testing.T) {
	origProxy := client.GetGlobalProxy()
	defer func() {
		if origProxy != nil {
			_ = client.SetGlobalProxy(origProxy.String())
		} else {
			client.ClearGlobalProxy()
		}
	}()

	// 1. Status / show when none configured
	client.ClearGlobalProxy()
	handleProxyCommand([]string{})
	handleProxyCommand([]string{"show"})
	handleProxyCommand([]string{"status"})

	// 2. Set valid proxy
	handleProxyCommand([]string{"set", "http://proxy.corp.internal:8080"})
	if p := client.GetGlobalProxy(); p == nil || !strings.Contains(p.String(), "8080") {
		t.Fatalf("expected proxy to be set, got %v", p)
	}

	// Status when configured
	handleProxyCommand([]string{"status"})

	// 3. Clear proxy
	handleProxyCommand([]string{"clear"})
	if p := client.GetGlobalProxy(); p != nil {
		t.Errorf("expected proxy to be cleared, got %v", p)
	}
	handleProxyCommand([]string{"unset"})
	handleProxyCommand([]string{"off"})

	// 4. Help flag
	handleProxyCommand([]string{"--help"})

	// 5. Bare URL syntax
	handleProxyCommand([]string{"socks5://127.0.0.1:1080"})
	if p := client.GetGlobalProxy(); p == nil || !strings.Contains(p.String(), "socks5") {
		t.Errorf("expected socks5 proxy set, got %v", p)
	}

	// 6. Invalid URLs exit 1
	code, exited := runWithExitCapture(func() {
		handleProxyCommand([]string{"set", ":::invalid-url"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on invalid proxy set, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		handleProxyCommand([]string{":::invalid-url"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on invalid bare proxy, got %d", code)
	}

	client.ClearGlobalProxy()
}

func TestDefaultDownloadsDir(t *testing.T) {
	dir := getDefaultDownloadsDir()
	if dir == "" {
		t.Errorf("expected non-empty default downloads dir")
	}
}

func TestRequireFlagValueAndRejectSecretFlag(t *testing.T) {
	// Valid value
	val := requireFlagValue([]string{"--user", "admin"}, 0, "--user")
	if val != "admin" {
		t.Errorf("expected admin, got %q", val)
	}

	// Missing value at end
	code, exited := runWithExitCapture(func() {
		requireFlagValue([]string{"--user"}, 0, "--user")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for missing flag value, got %d", code)
	}

	// Next arg is another flag
	code, exited = runWithExitCapture(func() {
		requireFlagValue([]string{"--user", "--port", "443"}, 0, "--user")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for next flag value, got %d", code)
	}

	// Reject secret flag
	code, exited = runWithExitCapture(func() {
		rejectSecretFlag("--password")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for rejectSecretFlag, got %d", code)
	}
}

func TestResolveApplianceCreds_Validations(t *testing.T) {
	// Invalid host
	code, exited := runWithExitCapture(func() {
		resolveApplianceCreds("http://invalid:format:host", "BIG-IP", "", "", "", "", false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for invalid host, got %d", code)
	}

	// Existing file avoids host parse check
	tmpFile := filepath.Join(t.TempDir(), "localfile.txt")
	os.WriteFile(tmpFile, []byte("data"), 0600)
	creds := resolveApplianceCreds(tmpFile, "auto", "custom-user", "", "", "", false)
	if creds.User != "custom-user" {
		t.Errorf("expected custom-user, got %q", creds.User)
	}

	// Invalid username
	code, exited = runWithExitCapture(func() {
		resolveApplianceCreds("10.1.1.1", "BIG-IP", "bad\nuser", "", "", "", false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for invalid username, got %d", code)
	}

	// Cert validation failure
	code, exited = runWithExitCapture(func() {
		resolveApplianceCreds("10.1.1.1", "BIG-IP", "admin", "/nonexistent/cert.pem", "", "", false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for nonexistent cert, got %d", code)
	}

	// Key validation failure
	code, exited = runWithExitCapture(func() {
		resolveApplianceCreds("10.1.1.1", "BIG-IP", "admin", "", "/nonexistent/key.pem", "", false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for nonexistent key, got %d", code)
	}

	// Identity validation failure
	code, exited = runWithExitCapture(func() {
		resolveApplianceCreds("10.1.1.1", "BIG-IP", "admin", "", "", "/nonexistent/id_rsa", false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for nonexistent identity, got %d", code)
	}

	// Valid cert, key, and identity files
	certPath := filepath.Join(t.TempDir(), "client.crt")
	keyPath := filepath.Join(t.TempDir(), "client.key")
	idPath := filepath.Join(t.TempDir(), "id_rsa")
	os.WriteFile(certPath, []byte("cert"), 0600)
	os.WriteFile(keyPath, []byte("key"), 0600)
	os.WriteFile(idPath, []byte("id"), 0600)

	validCreds := resolveApplianceCreds("10.1.1.1", "bigiq", "testuser", certPath, keyPath, idPath, true)
	if validCreds.TargetKind != "BIG-IQ" {
		t.Errorf("expected BIG-IQ, got %q", validCreds.TargetKind)
	}
	if validCreds.Cert != certPath || validCreds.Key != keyPath || validCreds.Identity != idPath {
		t.Errorf("paths mismatched in ApplianceCreds: %+v", validCreds)
	}
}

func TestHasDefaultSSHAuth(t *testing.T) {
	// 1. With SSH_AUTH_SOCK set
	t.Setenv("SSH_AUTH_SOCK", "/tmp/fake-ssh-agent.sock")
	if !hasDefaultSSHAuth() {
		t.Errorf("expected hasDefaultSSHAuth to be true with SSH_AUTH_SOCK")
	}
	t.Setenv("SSH_AUTH_SOCK", "")

	// 2. With clean temp home directory without keys
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	if hasDefaultSSHAuth() {
		t.Errorf("expected hasDefaultSSHAuth to be false in empty home")
	}

	// 3. With id_rsa created
	sshDir := filepath.Join(fakeHome, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("failed to create sshDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa"), []byte("dummy-key"), 0600); err != nil {
		t.Fatalf("failed to write id_rsa: %v", err)
	}

	if !hasDefaultSSHAuth() {
		t.Errorf("expected hasDefaultSSHAuth to be true with id_rsa present")
	}
}

func TestHandleDoctor(t *testing.T) {
	// Help flag
	handleDoctor(context.Background(), []string{"--help"})
	handleDoctor(context.Background(), []string{"-h"})

	// Canceled context exits 130
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	code, exited := runWithExitCapture(func() {
		handleDoctor(canceledCtx, nil)
	})
	if !exited || code != 130 {
		t.Errorf("expected exit 130 on canceled doctor run, got %d", code)
	}

	// Outbound proxy failure audit
	origProxy := client.GetGlobalProxy()
	defer func() {
		if origProxy != nil {
			_ = client.SetGlobalProxy(origProxy.String())
		} else {
			client.ClearGlobalProxy()
		}
	}()

	_ = client.SetGlobalProxy("http://127.0.0.1:1")
	os.Setenv("F5_CLIENT_ID", "dummy-client-id")
	os.Setenv("F5_CLIENT_SECRET", "dummy-client-secret")
	defer func() {
		os.Unsetenv("F5_CLIENT_ID")
		os.Unsetenv("F5_CLIENT_SECRET")
	}()

	code, exited = runWithExitCapture(func() {
		handleDoctor(context.Background(), nil)
	})
	if !exited || code != 1 {
		t.Errorf("expected doctor to exit 1 when endpoints fail, got %d", code)
	}
}

func TestInteractiveRunSafely(t *testing.T) {
	// Empty args
	runSafely([]string{})

	// Proxy argument with command
	runSafely([]string{"--proxy", "http://127.0.0.1:8080", "version"})

	// Proxy parse error
	runSafely([]string{"--proxy", ":::invalid-proxy"})

	// Unexpected panic rethrown
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected runSafely to rethrow unexpected panic")
		}
	}()
	// Trigger unexpected non-exitPanic panic
	prevExit := exitFunc
	exitFunc = func(code int) {
		panic("non-exit panic")
	}
	defer func() {
		exitFunc = prevExit
	}()
	runSafely([]string{"nonexistent_subcommand_to_trigger_exit"})
}

func TestStartInteractive_NonTTY(t *testing.T) {
	code, exited := runWithExitCapture(func() {
		startInteractive()
	})
	if !exited || code != 1 {
		t.Errorf("expected startInteractive to exit 1 in non-terminal, got %d", code)
	}
}

func TestExecuteSubcommand_HelpRoutes(t *testing.T) {
	subcommands := []string{
		"downloads", "download", "cases", "case", "wizard", "ihealth",
		"bigip", "bigiq", "appliance", "device", "ready", "info", "serial",
		"qkview", "push", "pull", "geoip", "proxy", "doctor",
	}

	for _, sub := range subcommands {
		t.Run("help_"+sub, func(t *testing.T) {
			code, exited := runWithExitCapture(func() {
				executeSubcommand(context.Background(), sub, []string{"--help"})
			})
			if !exited || code != 0 {
				t.Errorf("expected exit 0 for %s --help, got code=%d exited=%v", sub, code, exited)
			}
		})
	}

	// Test non-help subcommands dispatching
	t.Run("version", func(t *testing.T) {
		executeSubcommand(context.Background(), "version", nil)
		executeSubcommand(context.Background(), "--version", nil)
		executeSubcommand(context.Background(), "-v", nil)
	})

	t.Run("top_level_help", func(t *testing.T) {
		executeSubcommand(context.Background(), "help", nil)
		executeSubcommand(context.Background(), "--help", nil)
	})

	t.Run("unknown_subcommand", func(t *testing.T) {
		code, exited := runWithExitCapture(func() {
			executeSubcommand(context.Background(), "nonexistent_cmd_xyz", nil)
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for unknown subcommand, got %d", code)
		}
	})

	t.Run("subcommand_empty_args", func(t *testing.T) {
		emptyArgsCmds := []string{
			"downloads", "cases", "ihealth", "bigip", "bigiq",
			"appliance", "device", "ready", "info", "serial",
			"qkview", "push", "pull", "geoip",
		}
		for _, cmd := range emptyArgsCmds {
			code, exited := runWithExitCapture(func() {
				executeSubcommand(context.Background(), cmd, []string{})
			})
			if !exited || code != 1 {
				t.Errorf("expected exit 1 for bare %s, got %d", cmd, code)
			}
		}
	})
}

func TestHandleDownloads_Validation(t *testing.T) {
	// Empty args
	code, exited := runWithExitCapture(func() {
		handleDownloads(context.Background(), []string{})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for empty downloads args, got %d", code)
	}

	// Missing sub-arguments
	actionsMissingArgs := [][]string{
		{"lines"},
		{"versions"},
		{"containers"},
		{"files"},
		{"get"},
		{"unknown-action"},
	}

	for _, args := range actionsMissingArgs {
		t.Run("downloads_"+args[0], func(t *testing.T) {
			code, exited := runWithExitCapture(func() {
				handleDownloads(context.Background(), args)
			})
			if !exited || code != 1 {
				t.Errorf("expected exit 1 for downloads %v, got %d", args, code)
			}
		})
	}
}

func TestHandleCasesAndDetail(t *testing.T) {
	// Bare invocation
	code, exited := runWithExitCapture(func() {
		handleCases(context.Background(), []string{})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for bare handleCases, got %d", code)
	}

	// Unknown action
	code, exited = runWithExitCapture(func() {
		handleCases(context.Background(), []string{"unknown-action"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for unknown cases action, got %d", code)
	}

	// Get without case number
	code, exited = runWithExitCapture(func() {
		handleCases(context.Background(), []string{"get"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for cases get without number, got %d", code)
	}

	// printCaseDetail
	detail := &cases.CaseDetail{
		CaseNumber:       "C-123456",
		Subject:          "High CPU usage on TMM",
		Status:           "In Progress",
		Priority:         "2 - Site At Risk",
		Product:          "BIG-IP LTM",
		Version:          "17.5.1",
		ReasonForContact: "Software Bug",
		SerialNumber:     "test-serial-123",
		HostName:         "bigip-01.corp.internal",
		DateOpened:       "2026-11-01T12:00:00Z",
		CaseOwnerContact: cases.CaseContact{
			FirstName: "Jane",
			LastName:  "Doe",
			Email:     "jane.doe@example.com",
		},
		AlternateContact: cases.CaseContact{
			FirstName: "John",
			LastName:  "Smith",
			Email:     "john.smith@example.com",
		},
		Description: "Detailed description of the TMM core dump.\nMultiple lines.\n",
	}
	detail.Account.AccountName = "ACME Corp"
	detail.Account.AccountID = "ACT-9999"
	detail.Links = []struct {
		Rel  string `json:"rel"`
		Href string `json:"href"`
	}{
		{Rel: "case-detail", Href: "https://my.f5.com/cases/C-123456"},
	}
	printCaseDetail(detail)

	// Minimal detail
	printCaseDetail(&cases.CaseDetail{
		CaseNumber: "C-000000",
		Subject:    "Simple question",
		Status:     "Open",
	})
}

func TestHandleCaseCreate_FlagsAndDryRun(t *testing.T) {
	// Help flag
	handleCaseCreate(context.Background(), nil, []string{"--help"})
	handleCaseCreate(context.Background(), nil, []string{"-h"})

	// Unknown flag
	code, exited := runWithExitCapture(func() {
		handleCaseCreate(context.Background(), nil, []string{"--nonexistent-flag"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for unknown flag in case create, got %d", code)
	}

	// Missing value for flag
	code, exited = runWithExitCapture(func() {
		handleCaseCreate(context.Background(), nil, []string{"--from-appliance"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for missing value for --from-appliance, got %d", code)
	}

	// Dry run with missing required fields
	code, exited = runWithExitCapture(func() {
		handleCaseCreate(context.Background(), nil, []string{"--dry-run", "--family", "BIG-IP"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for dry-run with missing fields, got %d", code)
	}

	// Dry run with all required fields (BIG-IP)
	code, exited = runWithExitCapture(func() {
		handleCaseCreate(context.Background(), nil, []string{
			"--dry-run",
			"--family", "BIG-IP",
			"--product", "BIG-IP LTM",
			"--version", "17.5.1",
			"--serial", "f5-serial-test-123",
			"--priority", "4",
			"--reason", "General Problem",
			"--subject", "Testing Dry Run Complete",
			"--description", "Everything filled out properly",
			"--email", "user@example.com",
			"--timezone", "(GMT -05:00) EST",
		})
	})
	if exited && code != 0 {
		t.Errorf("expected dry-run with all fields to succeed without error, got code=%d", code)
	}
}

func TestHandleIHealth_Validation(t *testing.T) {
	// Empty args
	code, exited := runWithExitCapture(func() {
		handleIHealth(context.Background(), []string{})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for bare handleIHealth, got %d", code)
	}

	// Upload without file
	code, exited = runWithExitCapture(func() {
		handleIHealth(context.Background(), []string{"upload"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for ihealth upload without file, got %d", code)
	}

	// Upload nonexistent file
	code, exited = runWithExitCapture(func() {
		handleIHealth(context.Background(), []string{"upload", "/nonexistent/test.qkview"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for nonexistent qkview upload, got %d", code)
	}

	// Unknown action
	code, exited = runWithExitCapture(func() {
		handleIHealth(context.Background(), []string{"unknown-action"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for unknown ihealth action, got %d", code)
	}
}

func TestMockApplianceTLSServer_Operations(t *testing.T) {
	client.ClearGlobalProxy()

	// 1. detectApplianceKind fallback behavior
	kind1 := detectApplianceKind(context.Background(), "127.0.0.1", 443, "admin", "", nil, "", "", "BIG-IP")
	if kind1 != "BIG-IP" {
		t.Errorf("expected detectApplianceKind fallback = BIG-IP, got %q", kind1)
	}

	kind2 := detectApplianceKind(context.Background(), "127.0.0.1", 443, "admin", "", nil, "", "", "auto")
	if kind2 != "Appliance" {
		t.Errorf("expected detectApplianceKind fallback = Appliance, got %q", kind2)
	}

	// 2. resolveApplianceSystemInfo error handling
	_, errIC := resolveApplianceSystemInfo(context.Background(), "127.0.0.1", 1, "admin", "secret", nil, "", "icontrol", "BIG-IP", "", "")
	if errIC == nil {
		t.Errorf("expected resolveApplianceSystemInfo icontrol error on closed port")
	}

	_, errSSH := resolveApplianceSystemInfo(context.Background(), "127.0.0.1", 1, "admin", "secret", nil, "", "scp", "BIG-IP", "", "")
	if errSSH == nil {
		t.Errorf("expected resolveApplianceSystemInfo scp error on closed port")
	}

	_, errAuto := resolveApplianceSystemInfo(context.Background(), "127.0.0.1", 1, "admin", "secret", nil, "", "auto", "BIG-IP", "", "")
	if errAuto == nil {
		t.Errorf("expected resolveApplianceSystemInfo auto error on closed port")
	}

	// 3. getSystemInfoTarget with valid qkview file and nonexistent qkview
	qkviewFile := filepath.Join(t.TempDir(), "test.qkview")
	createMockQKViewArchive(t, qkviewFile)

	sysInfo := getSystemInfoTarget(context.Background(), qkviewFile, "BIG-IP", "admin", 0, "", "auto", "", "")
	if sysInfo == nil || sysInfo.Version != "17.5.1" {
		t.Fatalf("getSystemInfoTarget on qkview failed: %+v", sysInfo)
	}

	code, exited := runWithExitCapture(func() {
		getSystemInfoTarget(context.Background(), "nonexistent.qkview", "BIG-IP", "admin", 0, "", "auto", "", "")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for nonexistent qkview, got %d", code)
	}

	// 4. handleAppliance: info and serial using local QKView (tests successful paths!)
	handleAppliance(context.Background(), "BIG-IP", []string{"info", qkviewFile})
	handleAppliance(context.Background(), "BIG-IP", []string{"info", qkviewFile, "--json"})
	handleAppliance(context.Background(), "BIG-IP", []string{"serial", qkviewFile})
	handleAppliance(context.Background(), "BIG-IP", []string{"serial", qkviewFile, "--json"})

	// 5. handleAppliance: ready check network failure
	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"ready", "127.0.0.1", "--port", "1"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for ready check to closed port, got %d", code)
	}

	// 6. handleAppliance: qkview, geoip, push, and download errors to closed port
	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"qkview", "127.0.0.1", "--port", "1"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for qkview to closed port, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IQ", []string{"geoip", "127.0.0.1", "geo.zip"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for BIG-IQ geoip rejection, got %d", code)
	}

	// 7. handleBIGIP and handleBIGIQ wrappers with local qkview
	handleBIGIP(context.Background(), []string{"info", qkviewFile})
	handleBIGIQ(context.Background(), []string{"serial", qkviewFile})

	// 8. handleAppliance error validation branches
	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for empty appliance args, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"ready"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for ready without host, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"unknown-action", "127.0.0.1"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for unknown appliance action, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"ready", "127.0.0.1", "--password", "secret"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for rejected password flag, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"ready", "127.0.0.1", "--bogus-flag"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for unknown flag, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"ready", "127.0.0.1", "--method", "invalid-method"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for invalid method, got %d", code)
	}

	// 9. Push & Pull error validation
	code, exited = runWithExitCapture(func() {
		handlePush(context.Background(), []string{"127.0.0.1"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for push with insufficient args, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		handlePull(context.Background(), []string{"127.0.0.1"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for pull with insufficient args, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		executePushToAppliance(context.Background(), "127.0.0.1", "/nonexistent/path/file.txt", "icontrol", "/tmp", "admin", 1, "", "BIG-IP", "", "", false, false, false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for nonexistent file push, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		executePushToAppliance(context.Background(), "127.0.0.1", qkviewFile, "invalid-method", "/tmp", "admin", 1, "", "BIG-IP", "", "", false, false, false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for invalid method push, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		executePushToAppliance(context.Background(), "127.0.0.1", qkviewFile, "icontrol", "/tmp", "admin", 1, "", "BIG-IQ", "", "", true, false, false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for geoip on bigiq push, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		executeDownloadFromAppliance(context.Background(), "127.0.0.1", "../../../etc/shadow", "", "icontrol", "admin", 1, "", "BIG-IP", "", "")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for illegal path traversal download, got %d", code)
	}

	code, exited = runWithExitCapture(func() {
		executeDownloadFromAppliance(context.Background(), "127.0.0.1", "/shared/images/file.iso", "", "invalid-method", "admin", 1, "", "BIG-IP", "", "")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for invalid method download, got %d", code)
	}
}

func TestWizardAndSelectorExtensions(t *testing.T) {
	// 1. promptRequired
	in := bufio.NewReader(strings.NewReader("\n\nProvided Value\n"))
	var out bytes.Buffer
	val, err := promptRequired(in, &out, "Field", "")
	if err != nil {
		t.Fatalf("promptRequired failed: %v", err)
	}
	if val != "Provided Value" {
		t.Errorf("expected 'Provided Value', got %q", val)
	}

	// promptRequired with EOF
	inEOF := bufio.NewReader(strings.NewReader("\n\n"))
	_, errEOF := promptRequired(inEOF, &out, "Field", "")
	if errEOF == nil {
		t.Errorf("expected error on EOF in promptRequired")
	}

	// 2. editInExternalEditor with EDITOR=true
	os.Setenv("EDITOR", "true")
	defer os.Unsetenv("EDITOR")
	content, err := editInExternalEditor("initial test line")
	if err != nil {
		t.Fatalf("editInExternalEditor failed with EDITOR=true: %v", err)
	}
	if content != "initial test line" {
		t.Errorf("expected initial test line, got %q", content)
	}

	// editInExternalEditor with failing editor
	os.Setenv("EDITOR", "nonexistent-cmd-12345")
	_, errFail := editInExternalEditor("fail content")
	if errFail == nil {
		t.Errorf("expected failure with nonexistent editor")
	}

	// 3. editWizardField
	fieldsToTest := []struct {
		fieldNum int
		input    string
	}{
		{1, "BIG-IP\n"},
		{2, "BIG-IP LTM\n"},
		{3, "17.5.1\n"},
		{4, "test-serial-123\n"},
		{5, "2\n"},
		{6, "1\n"},
		{7, "New Subject\n"},
		{8, "Updated description line 1\n.\n"},
		{9, "admin@example.com\n"},
		{10, "Email\n"},
		{11, "(GMT -05:00) EST\n"},
		{12, "bigip-01.corp\n"},
		{13, "INC-12345\n"},
		{99, ""}, // default invalid field
	}

	opts := &CaseWizardOptions{
		ProductFamily: "BIG-IP",
		Product:       "BIG-IP LTM",
		Version:       "17.5.1",
	}

	for _, tc := range fieldsToTest {
		reader := bufio.NewReader(strings.NewReader(tc.input))
		var buf bytes.Buffer
		_ = editWizardField(reader, &buf, tc.fieldNum, opts)
	}

	// Test field 4 for NGINX subscription
	opts.ProductFamily = "NGINX"
	readerSub := bufio.NewReader(strings.NewReader("A-SUB-9999\n"))
	var bufSub bytes.Buffer
	_ = editWizardField(readerSub, &bufSub, 4, opts)
	if opts.SubscriptionID != "A-SUB-9999" {
		t.Errorf("expected SubscriptionID to be A-SUB-9999, got %q", opts.SubscriptionID)
	}

	// 4. SelectMenu with empty items
	_, _, errEmpty := SelectMenu(SelectConfig{Items: nil})
	if errEmpty == nil {
		t.Errorf("expected error for empty SelectMenu items")
	}

	// SelectMenu non-TTY fallback
	items := []SelectItem{
		{ID: "opt1", Title: "Option 1"},
		{ID: "opt2", Title: "Option 2"},
	}
	rPipe, wPipe, _ := os.Pipe()
	go func() {
		wPipe.WriteString("1\n")
		wPipe.Close()
	}()
	idx, chosen, sErr := SelectMenu(SelectConfig{
		Items: items,
		In:    rPipe,
	})
	rPipe.Close()
	if sErr != nil || idx != 0 || chosen.ID != "opt1" {
		t.Errorf("SelectMenu fallback pipe failed: idx=%d chosen=%+v err=%v", idx, chosen, sErr)
	}

	// 5. selectNavField
	rNav := bufio.NewReader(strings.NewReader("b\n"))
	_, navErr := selectNavField(rNav, nil, nil, "Title", "Prompt", items, "1")
	if !errors.Is(navErr, ErrWizardBack) {
		t.Errorf("expected ErrWizardBack on 'b', got %v", navErr)
	}

	// 6. runStep1AutoDetection: Option 3 (Skip)
	optsDetect := &CaseWizardOptions{}
	step, errStep := runStep1AutoDetection(context.Background(), bufio.NewReader(strings.NewReader("3\n")), &bytes.Buffer{}, optsDetect)
	if errStep != nil || step != 2 {
		t.Errorf("runStep1AutoDetection option 3 failed: step=%d err=%v", step, errStep)
	}

	// runStep1AutoDetection: Option 2 (Local QKView)
	qkviewFile := filepath.Join(t.TempDir(), "test.qkview")
	createMockQKViewArchive(t, qkviewFile)
	readerOpt2 := bufio.NewReader(strings.NewReader("2\n" + qkviewFile + "\n"))
	step, errStep = runStep1AutoDetection(context.Background(), readerOpt2, &bytes.Buffer{}, optsDetect)
	if errStep != nil || step != 2 {
		t.Fatalf("runStep1AutoDetection option 2 failed: step=%d err=%v", step, errStep)
	}
	if optsDetect.SerialNumber != "test-serial-999" {
		t.Errorf("expected SerialNumber=test-serial-999, got %q", optsDetect.SerialNumber)
	}
}

func createMockQKViewArchive(t *testing.T, filePath string) {
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
	_ = tw.Close()
	_ = gw.Close()

	if err := os.WriteFile(filePath, buf.Bytes(), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func TestHandlePush_ComprehensiveFlags(t *testing.T) {
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")
	idFile := filepath.Join(tmpDir, "id_rsa")
	localFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(certFile, []byte("cert"), 0600)
	os.WriteFile(keyFile, []byte("key"), 0600)
	os.WriteFile(idFile, []byte("id"), 0600)
	os.WriteFile(localFile, []byte("content"), 0600)

	// Valid flag combinations that fail at network dial
	testFlags := [][]string{
		{"--user", "admin1", "-p", "8443", "-m", "icontrol", "-d", "/tmp", "-i", idFile, "-t", "bigip", "--cert", certFile, "--cert-key", keyFile, "--apply-geoip", "--keep-extracted", "--json", "127.0.0.1", localFile},
		{"--user=admin2", "--port=8443", "--method=scp", "--dest=/tmp", "--identity=" + idFile, "--target=bigiq", "--cert=" + certFile, "--key-file=" + keyFile, "127.0.0.1", localFile},
		{"-u", "admin3", "-o", "/tmp", "--target=auto", "--cert-key=" + keyFile, "--", "127.0.0.1", localFile},
	}

	for _, flags := range testFlags {
		code, exited := runWithExitCapture(func() {
			handlePush(context.Background(), flags)
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for push %v, got %d", flags, code)
		}
	}

	// Invalid flags
	invalidArgs := [][]string{
		{"-p", "invalid-port", "127.0.0.1", localFile},
		{"--port=invalid-port", "127.0.0.1", localFile},
		{"-t", "invalid-target", "127.0.0.1", localFile},
		{"--target=invalid-target", "127.0.0.1", localFile},
		{"--password", "secret", "127.0.0.1", localFile},
		{"--bogus-flag", "127.0.0.1", localFile},
		{"127.0.0.1", localFile, "extra-arg"},
	}

	for _, flags := range invalidArgs {
		code, exited := runWithExitCapture(func() {
			handlePush(context.Background(), flags)
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for push error %v, got %d", flags, code)
		}
	}
}

func TestHandlePull_ComprehensiveFlags(t *testing.T) {
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")
	idFile := filepath.Join(tmpDir, "id_rsa")
	os.WriteFile(certFile, []byte("cert"), 0600)
	os.WriteFile(keyFile, []byte("key"), 0600)
	os.WriteFile(idFile, []byte("id"), 0600)

	testFlags := [][]string{
		{"--user", "admin1", "-p", "8443", "-m", "icontrol", "-d", "/tmp/out.txt", "-i", idFile, "-t", "bigip", "--cert", certFile, "--cert-key", keyFile, "127.0.0.1", "/shared/file.iso"},
		{"--user=admin2", "--port=8443", "--method=scp", "--dest=/tmp/out.txt", "--identity=" + idFile, "--target=bigiq", "--cert=" + certFile, "--key-file=" + keyFile, "127.0.0.1", "/shared/file.iso"},
		{"-u", "admin3", "-o", "/tmp/out.txt", "--output=/tmp/out.txt", "--target=auto", "--cert-key=" + keyFile, "--", "127.0.0.1", "/shared/file.iso"},
	}

	for _, flags := range testFlags {
		code, exited := runWithExitCapture(func() {
			handlePull(context.Background(), flags)
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for pull %v, got %d", flags, code)
		}
	}

	invalidArgs := [][]string{
		{"-p", "invalid-port", "127.0.0.1", "/file.iso"},
		{"--port=invalid-port", "127.0.0.1", "/file.iso"},
		{"-t", "invalid-target", "127.0.0.1", "/file.iso"},
		{"--target=invalid-target", "127.0.0.1", "/file.iso"},
		{"--password", "secret", "127.0.0.1", "/file.iso"},
		{"--bogus-flag", "127.0.0.1", "/file.iso"},
		{"127.0.0.1", "/file.iso", "extra-arg"},
	}

	for _, flags := range invalidArgs {
		code, exited := runWithExitCapture(func() {
			handlePull(context.Background(), flags)
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for pull error %v, got %d", flags, code)
		}
	}
}

func TestHandleAppliance_ComprehensiveFlags(t *testing.T) {
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")
	idFile := filepath.Join(tmpDir, "id_rsa")
	qkviewFile := filepath.Join(tmpDir, "test.qkview")
	createMockQKViewArchive(t, qkviewFile)
	os.WriteFile(certFile, []byte("cert"), 0600)
	os.WriteFile(keyFile, []byte("key"), 0600)
	os.WriteFile(idFile, []byte("id"), 0600)

	// TargetKind aliases
	for _, kind := range []string{"device", "appliance", "bigiq", "big-iq", "bigip", "big-ip"} {
		handleAppliance(context.Background(), kind, []string{"info", qkviewFile})
	}

	// Flag parsing variations on valid qkview info
	applianceFlags := [][]string{
		{"info", qkviewFile, "--user", "admin", "-p", "443", "-m", "auto", "-d", "/tmp", "-i", idFile, "--cert", certFile, "--cert-key", keyFile, "--keep-remote", "--apply-geoip", "--keep-extracted", "--json"},
		{"info", qkviewFile, "--user=admin", "--port=443", "--method=ssh", "--dest=/tmp", "--identity=" + idFile, "--cert=" + certFile, "--key-file=" + keyFile, "--no-delete"},
		{"info", qkviewFile, "-u", "admin", "-o", "/tmp", "--output=/tmp", "--cert-key=" + keyFile, "--no-cleanup", "--geoip"},
		{"info", "--", qkviewFile},
	}

	for _, args := range applianceFlags {
		handleAppliance(context.Background(), "BIG-IP", args)
	}

	// Invalid ports
	for _, portFlag := range []string{"-p", "--port="} {
		code, exited := runWithExitCapture(func() {
			if portFlag == "-p" {
				handleAppliance(context.Background(), "BIG-IP", []string{"info", qkviewFile, "-p", "invalid-port"})
			} else {
				handleAppliance(context.Background(), "BIG-IP", []string{"info", qkviewFile, "--port=invalid-port"})
			}
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for invalid appliance port %s", portFlag)
		}
	}

	// Extra argument validations
	extraArgsTests := [][]string{
		{"ready", "127.0.0.1", "extra"},
		{"info", qkviewFile, "extra"},
		{"serial", qkviewFile, "extra"},
		{"push", "127.0.0.1"},
		{"download", "127.0.0.1"},
		{"geoip", "127.0.0.1"},
		{"qkview"},
		{"qkview", "127.0.0.1", "extra"},
	}

	for _, args := range extraArgsTests {
		code, exited := runWithExitCapture(func() {
			handleAppliance(context.Background(), "BIG-IP", args)
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for appliance extra/missing args %v, got %d", args, code)
		}
	}
}

func TestHandleDownloads_FlagsAndBrowse(t *testing.T) {
	// Browse command requires terminal TTY
	code, exited := runWithExitCapture(func() {
		handleDownloads(context.Background(), []string{"browse"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for downloads browse non-TTY, got %d", code)
	}

	// Flag variations parsing with missing target
	flagVariations := [][]string{
		{"lines", "-o", "/tmp", "--profile", "custom", "--push", "127.0.0.1", "--push-method", "icontrol", "--push-dest", "/tmp", "--push-user", "admin", "--push-port", "443", "--push-target", "BIG-IP", "--push-cert", "/tmp/cert", "--push-cert-key", "/tmp/key", "--push-apply-geoip", "--push-keep-extracted"},
		{"lines", "-o=/tmp", "--output=/tmp", "--profile=custom", "--push=127.0.0.1", "--push-method=scp", "--push-dest=/tmp", "--push-user=admin", "--push-port=443", "--push-target=BIG-IQ", "--push-cert=/tmp/c", "--push-cert-key=/tmp/k"},
	}

	for _, args := range flagVariations {
		code, exited := runWithExitCapture(func() {
			handleDownloads(context.Background(), args)
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for downloads flags %v, got %d", args, code)
		}
	}
}

func TestHandleCaseCreate_Advanced(t *testing.T) {
	qkviewFile := filepath.Join(t.TempDir(), "test.qkview")
	createMockQKViewArchive(t, qkviewFile)

	// Dry run with --from-appliance / --from-device / --from-bigip / --from-bigiq pointing to local qkview
	for _, flag := range []string{"--from-appliance", "--from-device", "--from-bigip", "--from-bigiq"} {
		code, exited := runWithExitCapture(func() {
			handleCaseCreate(context.Background(), nil, []string{
				"--dry-run",
				flag, qkviewFile,
				"--priority", "3",
				"--reason", "Configuration Assistance",
				"--subject", "Advance dry run test",
				"--description", "Testing appliance detection in case create",
				"--email", "admin@corp.test",
				"--contact-method", "Phone",
				"--timezone", "UTC",
				"--hostname", "bigip01.corp",
				"--customer-ticket", "INC-8888",
			})
		})
		if exited && code != 0 {
			t.Errorf("expected dry run with %s to succeed, got %d", flag, code)
		}
	}

	// Dry run for NGINX with subscriptionId
	code, exited := runWithExitCapture(func() {
		handleCaseCreate(context.Background(), nil, []string{
			"--dry-run",
			"--family", "NGINX",
			"--product", "NGINX Plus",
			"--version", "R30",
			"--subscription", "A-S00038458",
			"--priority", "2",
			"--reason", "Software Bug",
			"--subject", "NGINX upstream crash",
			"--description", "Core dump analysis needed",
			"--email", "user@example.com",
			"--timezone", "UTC",
		})
	})
	if exited && code != 0 {
		t.Errorf("expected NGINX dry-run to succeed, got %d", code)
	}
}

func TestWizardEditField_DetailedBranches(t *testing.T) {
	opts := &CaseWizardOptions{
		ProductFamily: "BIG-IP",
		Product:       "BIG-IP LTM",
		Version:       "15.1.8", // triggers EOL warning
	}

	// Field 3 EOL warning branch
	var buf bytes.Buffer
	_ = editWizardField(bufio.NewReader(strings.NewReader("15.1.8\n")), &buf, 3, opts)
	if !strings.Contains(buf.String(), "End of Life") {
		t.Errorf("expected EOL warning in field 3 edit output")
	}

	// Field 5 (Priority) back command
	_ = editWizardField(bufio.NewReader(strings.NewReader("b\n")), &buf, 5, opts)

	// Field 5 (Priority) all choices
	for _, p := range []string{"1\n", "2\n", "3\n", "4\n"} {
		_ = editWizardField(bufio.NewReader(strings.NewReader(p)), &buf, 5, opts)
	}

	// Field 6 (Reason) back command
	_ = editWizardField(bufio.NewReader(strings.NewReader("b\n")), &buf, 6, opts)

	// Field 6 (Reason) all choices
	for _, r := range []string{"1\n", "2\n", "3\n", "4\n", "5\n"} {
		_ = editWizardField(bufio.NewReader(strings.NewReader(r)), &buf, 6, opts)
	}

	// Field 8 (Description) multiline buffer with line and end
	descInput := "First description line\nSecond description line\n.\n"
	_ = editWizardField(bufio.NewReader(strings.NewReader(descInput)), &buf, 8, opts)

	// Nav back on fields 10, 11, 12, 13
	for _, f := range []int{10, 11, 12, 13} {
		_ = editWizardField(bufio.NewReader(strings.NewReader("b\n")), &buf, f, opts)
	}
}

func TestRunStep1AutoDetection_EdgeCases(t *testing.T) {
	opts := &CaseWizardOptions{}

	// Option ':b' (back from step 1) -> returns step 1, nil
	step, err := runStep1AutoDetection(context.Background(), bufio.NewReader(strings.NewReader("b\n")), &bytes.Buffer{}, opts)
	if err != nil || step != 1 {
		t.Errorf("expected step 1 on back, got step=%d err=%v", step, err)
	}

	// Option 1 with back on host prompt -> advances to step 2
	step, err = runStep1AutoDetection(context.Background(), bufio.NewReader(strings.NewReader("1\nb\n")), &bytes.Buffer{}, opts)
	if err != nil || step != 2 {
		t.Errorf("expected step 2 on option 1 host back, got step=%d err=%v", step, err)
	}

	// Option 2 with invalid qkview file -> prints error and advances to step 2
	step, err = runStep1AutoDetection(context.Background(), bufio.NewReader(strings.NewReader("2\n/nonexistent/bad.qkview\n")), &bytes.Buffer{}, opts)
	if err != nil || step != 2 {
		t.Errorf("expected step 2 on option 2 bad qkview, got step=%d err=%v", step, err)
	}

	// Option 2 with back on qkview prompt -> advances to step 2
	step, err = runStep1AutoDetection(context.Background(), bufio.NewReader(strings.NewReader("2\nb\n")), &bytes.Buffer{}, opts)
	if err != nil || step != 2 {
		t.Errorf("expected step 2 on option 2 back, got step=%d err=%v", step, err)
	}
}

func TestSubcommandAliasesAndInteractive(t *testing.T) {
	// Aliases in executeSubcommand
	aliases := []struct {
		cmd  string
		args []string
	}{
		{"download", []string{}},
		{"case", []string{}},
		{"upload", []string{}},
		{"fetch", []string{}},
		{"sysinfo", []string{}},
		{"system-info", []string{}},
		{"sn", []string{}},
		{"serialnumber", []string{}},
		{"interactive", []string{}},
		{"shell", []string{}},
		{"repl", []string{}},
		{"i", []string{}},
	}

	for _, a := range aliases {
		code, exited := runWithExitCapture(func() {
			executeSubcommand(context.Background(), a.cmd, a.args)
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for subcommand alias %s, got %d", a.cmd, code)
		}
	}
}

func TestHandleCases_AdditionalBranches(t *testing.T) {
	// wizard / new action with cancel input in Step 5
	cancelLines := []string{
		"3",                 // Step 1: skip auto-detect
		"",                  // Step 2: Product family (default)
		"",                  // Product (default)
		"",                  // Version (default)
		"sn-test-12345",     // Serial number
		"4",                 // Step 3: Priority 4
		"1",                 // Reason 1
		"Cancel test",       // Subject
		"Testing cancel\n.", // Description
		"user@example.com",  // Step 4: Email
		"",                  // Contact Method (default)
		"",                  // Timezone (default)
		"",                  // Hostname
		"",                  // Ticket Ref
		"c",                 // Step 5: Cancel
	}

	origStdin := os.Stdin
	rPipe, wPipe, _ := os.Pipe()
	for _, l := range cancelLines {
		wPipe.WriteString(l + "\n")
	}
	wPipe.Close()
	os.Stdin = rPipe
	defer func() { os.Stdin = origStdin }()

	code, exited := runWithExitCapture(func() {
		handleCases(context.Background(), []string{"wizard"})
	})
	if exited && code != 0 {
		t.Errorf("expected wizard to handle cancel gracefully, got %d", code)
	}

	// create action via handleCases dispatching to handleCaseCreate
	code, exited = runWithExitCapture(func() {
		handleCases(context.Background(), []string{"create", "--help"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 when no creds for create, got %d", code)
	}

	// Actions requiring authentication when no credentials present
	for _, action := range []string{"list", "metadata"} {
		code, exited = runWithExitCapture(func() {
			handleCases(context.Background(), []string{action})
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for handleCases %s without creds, got %d", action, code)
		}
	}

	// With credentials set, authentication fails cleanly
	os.Setenv("F5_CLIENT_ID", "dummy-id")
	os.Setenv("F5_CLIENT_SECRET", "dummy-secret")
	defer func() {
		os.Unsetenv("F5_CLIENT_ID")
		os.Unsetenv("F5_CLIENT_SECRET")
	}()

	for _, action := range []string{"list", "metadata"} {
		code, exited = runWithExitCapture(func() {
			handleCases(context.Background(), []string{action})
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for handleCases %s with failing auth, got %d", action, code)
		}
	}
}

func TestHandleDownloads_AuthBranches(t *testing.T) {
	// With credentials set, calling families/lines/versions/containers/files triggers getDownloadsClient
	os.Setenv("F5_CLIENT_ID", "dummy-id")
	os.Setenv("F5_CLIENT_SECRET", "dummy-secret")
	defer func() {
		os.Unsetenv("F5_CLIENT_ID")
		os.Unsetenv("F5_CLIENT_SECRET")
	}()

	downloadsActions := [][]string{
		{"families"},
		{"lines", "BIG-IP"},
		{"versions", "BIG-IP", "BIG-IP LTM"},
		{"containers", "BIG-IP", "BIG-IP LTM"},
		{"files", "BIG-IP", "BIG-IP LTM"},
		{"get", "BIG-IP", "BIG-IP LTM", "17.5.1", "Base ISO", "test.iso"},
	}

	for _, args := range downloadsActions {
		code, exited := runWithExitCapture(func() {
			handleDownloads(context.Background(), args)
		})
		if !exited || code != 1 {
			t.Errorf("expected exit 1 for handleDownloads %v with failing auth, got %d", args, code)
		}
	}
}

func TestHandleIHealth_AuthBranches(t *testing.T) {
	// With credentials set, list triggers authentication
	os.Setenv("F5_CLIENT_ID", "dummy-id")
	os.Setenv("F5_CLIENT_SECRET", "dummy-secret")
	defer func() {
		os.Unsetenv("F5_CLIENT_ID")
		os.Unsetenv("F5_CLIENT_SECRET")
	}()

	code, exited := runWithExitCapture(func() {
		handleIHealth(context.Background(), []string{"list"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for ihealth list with failing auth, got %d", code)
	}
}

func TestSelector_EdgeCases(t *testing.T) {
	// TruncateRunes with small limit <= 3 returns s unchanged
	if got := truncateRunes("Hello", 2); got != "Hello" {
		t.Errorf("expected 'Hello', got %q", got)
	}
	if got := truncateRunes("Hello", 3); got != "Hello" {
		t.Errorf("expected 'Hello', got %q", got)
	}
	// TruncateRunes with length > maxRunes > 3
	if got := truncateRunes("Hello World", 8); got != "Hello..." {
		t.Errorf("expected 'Hello...', got %q", got)
	}

	// SelectMenu default index normalization
	items := []SelectItem{{ID: "1", Title: "One"}}
	rPipe, wPipe, _ := os.Pipe()
	go func() {
		wPipe.WriteString("1\n")
		wPipe.Close()
	}()
	idx, _, err := SelectMenu(SelectConfig{
		Items:        items,
		In:           rPipe,
		DefaultIndex: -10, // will normalize to 0
	})
	rPipe.Close()
	if err != nil || idx != 0 {
		t.Errorf("expected index 0 after normalization, got %d (err: %v)", idx, err)
	}

	rPipe2, wPipe2, _ := os.Pipe()
	go func() {
		wPipe2.WriteString("1\n")
		wPipe2.Close()
	}()
	idx2, _, err2 := SelectMenu(SelectConfig{
		Items:        items,
		In:           rPipe2,
		DefaultIndex: 50, // will normalize to 0
	})
	rPipe2.Close()
	if err2 != nil || idx2 != 0 {
		t.Errorf("expected index 0 after normalization, got %d (err: %v)", idx2, err2)
	}
}

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

func TestRunInteractiveBrowser_Coverage(t *testing.T) {
	// 1. Error fetching metadata
	failClient := downloads.NewClientWithKey("token", "https://downloads.test", "key")
	failClient.HTTPClient = &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("network dial failure")
		}),
	}
	code, exited := runWithExitCapture(func() {
		runInteractiveBrowser(context.Background(), failClient)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on metadata fetch error, got %d", code)
	}

	// 2. Canceled context on metadata
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	code, exited = runWithExitCapture(func() {
		runInteractiveBrowser(canceledCtx, failClient)
	})
	if !exited || code != 130 {
		t.Errorf("expected exit 130 on canceled context, got %d", code)
	}

	// 3. Successful metadata and navigation
	mockClient := downloads.NewClientWithKey("token", "https://downloads.test", "key")
	mockClient.HTTPClient = &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			path := req.URL.Path
			if strings.Contains(path, "/metadata") {
				metaJSON := `{
					"data": {
						"productFamilies": [
							{
								"name": "BIG-IP",
								"displayName": "BIG-IP Platforms",
								"productLines": [
									{
										"name": "BIG-IP v17.x",
										"displayName": "Version 17"
									}
								]
							}
						]
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(metaJSON)),
					Header:     make(http.Header),
				}, nil
			}
			if strings.Contains(path, "BIGIP-17.5.1.iso") {
				linksJSON := `{
					"downloadLinks": [
						{
							"href": "https://cdn.example.com/BIGIP-17.5.1.iso",
							"region": "US East"
						}
					]
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(linksJSON)),
					Header:     make(http.Header),
				}, nil
			}
			if strings.Contains(path, "/product/BIG-IP/") {
				verJSON := `{
					"data": {
						"productVersions": [
							{
								"name": "17.5.1",
								"releaseDate": "2024-01-01",
								"containers": [
									{
										"name": "Base ISO",
										"files": [
											{
												"name": "BIGIP-17.5.1.iso",
												"fileSize": 1048576
											}
										]
									}
								]
							}
						]
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(verJSON)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	// Navigate:
	// Select family 1 -> Select line 1 -> Select version 1 -> Select category 1 -> Select file 1 -> decline 'n' -> 'b' back to category -> 'b' back to version -> 'b' back to line -> 'b' back to family -> 'q' quit
	navInput := "1\n1\n1\n1\n1\nn\nb\nb\nb\nb\nq\n"
	origStdin := os.Stdin
	rPipe, wPipe, _ := os.Pipe()
	wPipe.WriteString(navInput)
	wPipe.Close()
	os.Stdin = rPipe
	defer func() { os.Stdin = origStdin }()

	runInteractiveBrowser(context.Background(), mockClient)
}

func TestGetSystemInfoTarget_ApplianceBranch(t *testing.T) {
	code, exited := runWithExitCapture(func() {
		getSystemInfoTarget(context.Background(), "127.0.0.1", "BIG-IP", "admin", 1, "", "auto", "", "")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for getSystemInfoTarget on unreachable host, got %d", code)
	}
}

func TestExecutePushAndDownload_AdditionalBranches(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "upload.txt")
	os.WriteFile(tmpFile, []byte("data"), 0600)

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Push with canceled context -> exit 130
	code, exited := runWithExitCapture(func() {
		executePushToAppliance(canceledCtx, "127.0.0.1", tmpFile, "icontrol", "/tmp", "admin", 1, "", "BIG-IP", "", "", false, false, false)
	})
	if !exited || code != 130 {
		t.Errorf("expected exit 130 on canceled push, got %d", code)
	}

	// Download with canceled context -> exit 130
	code, exited = runWithExitCapture(func() {
		executeDownloadFromAppliance(canceledCtx, "127.0.0.1", "/shared/file.iso", "", "icontrol", "admin", 1, "", "BIG-IP", "", "")
	})
	if !exited || code != 130 {
		t.Errorf("expected exit 130 on canceled download, got %d", code)
	}

	// Push with empty method and targetKind (tests default fallbacks)
	code, exited = runWithExitCapture(func() {
		executePushToAppliance(context.Background(), "127.0.0.1", tmpFile, "", "", "admin", 1, "", "", "", "", false, false, false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on default push to closed port, got %d", code)
	}

	// Download with empty method and targetKind (tests default fallbacks)
	code, exited = runWithExitCapture(func() {
		executeDownloadFromAppliance(context.Background(), "127.0.0.1", "/shared/file.iso", "", "", "admin", 1, "", "", "", "")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on default download to closed port, got %d", code)
	}

	// Download with method="ssh" (tests method="scp" normalization)
	code, exited = runWithExitCapture(func() {
		executeDownloadFromAppliance(context.Background(), "127.0.0.1", "/shared/file.iso", "", "ssh", "admin", 1, "", "BIG-IP", "", "")
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on ssh method download, got %d", code)
	}

	// Push with method="ssh" (tests method="scp" normalization)
	code, exited = runWithExitCapture(func() {
		executePushToAppliance(context.Background(), "127.0.0.1", tmpFile, "ssh", "/tmp", "admin", 1, "", "BIG-IP", "", "", false, false, false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on ssh method push, got %d", code)
	}

	// Push with applyGeoIP=true and default destPath="" (tests destPath="/var/tmp" default)
	code, exited = runWithExitCapture(func() {
		executePushToAppliance(context.Background(), "127.0.0.1", tmpFile, "icontrol", "", "admin", 1, "", "BIG-IP", "", "", true, false, false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on geoip push to closed port, got %d", code)
	}
}

func TestHandleAppliance_QKViewAndSerialEdgeCases(t *testing.T) {
	// 1. QKView with closed port and icontrol
	code, exited := runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"qkview", "127.0.0.1", "--method", "icontrol", "--port", "1"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for qkview icontrol to closed port, got %d", code)
	}

	// 2. QKView with closed port and scp
	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"qkview", "127.0.0.1", "--method", "scp", "--port", "1"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for qkview scp to closed port, got %d", code)
	}

	// 3. QKView with invalid method
	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"qkview", "127.0.0.1", "--method", "invalid-qkview-method", "--port", "1"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for qkview invalid method, got %d", code)
	}

	// 4. Serial on QKView without serial number
	noSnQkview := filepath.Join(t.TempDir(), "nosn.qkview")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{Name: "VERSION", Mode: 0600, Size: 26}
	tw.WriteHeader(hdr)
	tw.Write([]byte("Product: BIG-IP\nVersion: 17\n"))
	tw.Close()
	gw.Close()
	os.WriteFile(noSnQkview, buf.Bytes(), 0600)

	code, exited = runWithExitCapture(func() {
		handleAppliance(context.Background(), "BIG-IP", []string{"serial", noSnQkview})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 for qkview without serial, got %d", code)
	}
}

func TestHandleCaseCreate_MockClientResponses(t *testing.T) {
	// 1. API returns error (isError=true)
	mockClientErr := cases.NewClientWithKey("token", "https://cases.test", "key")
	mockClientErr.HTTPClient = &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			res := `{
				"isError": true,
				"message": "account entitlement expired"
			}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(res)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	args := []string{
		"--family", "BIG-IP",
		"--product", "BIG-IP LTM",
		"--version", "17.5.1",
		"--serial", "test-12345",
		"--priority", "4",
		"--reason", "General Problem",
		"--subject", "Test issue",
		"--description", "Desc",
		"--email", "user@test.corp",
		"--timezone", "UTC",
	}
	code, exited := runWithExitCapture(func() {
		handleCaseCreate(context.Background(), mockClientErr, args)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on case create API error response, got %d", code)
	}

	// 2. API returns success with case number
	mockClientSuccess := cases.NewClientWithKey("token", "https://cases.test", "key")
	mockClientSuccess.HTTPClient = &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			res := `{
				"isError": false,
				"data": {
					"caseNumber": "C-998877"
				}
			}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(res)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	handleCaseCreate(context.Background(), mockClientSuccess, args)

	// 3. API returns success with no case number
	mockClientNoNum := cases.NewClientWithKey("token", "https://cases.test", "key")
	mockClientNoNum.HTTPClient = &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			res := `{
				"isError": false,
				"data": {}
			}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(res)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	handleCaseCreate(context.Background(), mockClientNoNum, args)
}

func TestMainAndRunMain(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// Test main() with version
	os.Args = []string{"myf5", "version"}
	main()

	// Test runMain with security error (--password)
	code, exited := runWithExitCapture(func() {
		runMain([]string{"myf5", "--password", "secret"})
	})
	if !exited || code != 2 {
		t.Errorf("expected exit 2 on secret flag, got %d", code)
	}

	// Test runMain with invalid proxy
	code, exited = runWithExitCapture(func() {
		runMain([]string{"myf5", "--proxy", "invalid proxy with space", "version"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on bad proxy, got %d", code)
	}

	// Test runMain with empty args -> usage
	code, exited = runWithExitCapture(func() {
		runMain([]string{"myf5"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on empty args, got %d", code)
	}
}

func TestStartInteractiveFullSession(t *testing.T) {
	origIsTerm := isTerminalFunc
	origMakeRaw := makeRawFunc
	origRestore := restoreTerminalFunc
	origIn := interactiveIn
	origOut := interactiveOut
	defer func() {
		isTerminalFunc = origIsTerm
		makeRawFunc = origMakeRaw
		restoreTerminalFunc = origRestore
		interactiveIn = origIn
		interactiveOut = origOut
	}()

	// 1. Terminal mode: enter commands, empty line, clear, and quit
	isTerminalFunc = func(fd int) bool { return true }
	makeRawFunc = func(fd int) (*term.State, error) { return &term.State{}, nil }
	restoreTerminalFunc = func(fd int, s *term.State) error { return nil }

	interactiveIn = strings.NewReader("help\nversion\n \nclear\nexit\n")
	var outBuf bytes.Buffer
	interactiveOut = &outBuf

	startInteractive()

	// 2. Terminal mode: makeRaw error branch
	makeRawFunc = func(fd int) (*term.State, error) { return nil, errors.New("cannot make raw") }
	startInteractive()

	// 3. Non-terminal error branch
	isTerminalFunc = func(fd int) bool { return false }
	code, exited := runWithExitCapture(func() {
		startInteractive()
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on non-terminal, got %d", code)
	}
}

func TestRunStep1AutoDetectionOptions(t *testing.T) {
	ctx := context.Background()
	qkviewFile := filepath.Join(t.TempDir(), "autodetect.qkview")
	createMockQKViewArchive(t, qkviewFile)

	// Option 1: connect to host (using mock qkview path which getSystemInfoTarget supports)
	var out1 bytes.Buffer
	in1 := bufio.NewReader(strings.NewReader("1\n" + qkviewFile + "\nadmin\n"))
	opts1 := &CaseWizardOptions{}
	step1, err := runStep1AutoDetection(ctx, in1, &out1, opts1)
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if step1 != 2 {
		t.Errorf("expected step 2, got %d", step1)
	}
	if opts1.SerialNumber != "test-serial-999" {
		t.Errorf("expected SerialNumber=test-serial-999, got %q", opts1.SerialNumber)
	}

	// Option 2: local qkview (with empty or invalid path)
	var out bytes.Buffer
	in2 := bufio.NewReader(strings.NewReader("2\n/nonexistent/file.qkview\n"))
	opts2 := &CaseWizardOptions{}
	step, err := runStep1AutoDetection(ctx, in2, &out, opts2)
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if step != 2 {
		t.Errorf("expected step 2, got %d", step)
	}

	// Back error navigation
	inBack := bufio.NewReader(strings.NewReader("b\n"))
	step, err = runStep1AutoDetection(ctx, inBack, &out, opts2)
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if step != 1 {
		t.Errorf("expected step 1 on back, got %d", step)
	}
}

func TestExecutePushToApplianceBranches(t *testing.T) {
	ctx := context.Background()

	// 1. Invalid method
	code, exited := runWithExitCapture(func() {
		executePushToAppliance(ctx, "10.0.0.1", "test.iso", "invalid-method", "", "admin", 443, "", "BIG-IP", "", "", false, false, false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on invalid method, got %d", code)
	}

	// 2. applyGeoIP on BIG-IQ
	code, exited = runWithExitCapture(func() {
		executePushToAppliance(ctx, "10.0.0.1", "test.iso", "auto", "", "admin", 443, "", "BIG-IQ", "", "", true, false, false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on BIG-IQ geoip push, got %d", code)
	}

	// 3. Local file does not exist
	code, exited = runWithExitCapture(func() {
		executePushToAppliance(ctx, "10.0.0.1", "/nonexistent/path/file.iso", "auto", "", "admin", 443, "", "BIG-IP", "", "", false, false, false)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on missing file, got %d", code)
	}

	// 4. File exists, transfer failure with json output
	tmpFile := filepath.Join(t.TempDir(), "test.iso")
	_ = os.WriteFile(tmpFile, []byte("dummy-data"), 0600)
	code, exited = runWithExitCapture(func() {
		executePushToAppliance(ctx, "127.0.0.1", tmpFile, "auto", "", "admin", 65530, "", "BIG-IP", "", "", false, false, true)
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on failed push with json, got %d", code)
	}

	// 5. File exists, transfer canceled context
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	code, exited = runWithExitCapture(func() {
		executePushToAppliance(cancelCtx, "127.0.0.1", tmpFile, "auto", "", "admin", 65530, "", "BIG-IP", "", "", false, false, false)
	})
	if !exited || (code != 1 && code != 130) {
		t.Errorf("expected exit 1 or 130 on canceled push, got %d", code)
	}
}

func TestExecutePushToApplianceSuccess(t *testing.T) {
	mockMux := http.NewServeMux()
	mockMux.HandleFunc("/mgmt/tm/sys/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind": "tm:sys:ready"}`))
	})
	mockMux.HandleFunc("/mgmt/tm/sys/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mockMux.HandleFunc("/mgmt/shared/file-transfer/uploads/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"localFilePath": "/var/config/rest/downloads/test.iso"}`))
	})
	mockMux.HandleFunc("/mgmt/tm/util/bash", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"commandResult": "success"}`))
	})

	dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c1, c2 := net.Pipe()
		go func() {
			req, err := http.ReadRequest(bufio.NewReader(c2))
			if err != nil {
				c2.Close()
				return
			}
			rec := httptest.NewRecorder()
			mockMux.ServeHTTP(rec, req)
			resp := rec.Result()
			_ = resp.Write(c2)
			c2.Close()
		}()
		return c1, nil
	}

	client.SetDialContext(dialer)
	defer client.SetDialContext(nil)

	tmpFile := filepath.Join(t.TempDir(), "test.iso")
	_ = os.WriteFile(tmpFile, []byte("iso data"), 0600)

	t.Setenv("BIGIP_PASSWORD", "secret")
	ctx := context.Background()

	// 1. Success without json
	executePushToAppliance(ctx, "http://127.0.0.1", tmpFile, "icontrol", "/shared/images", "admin", 80, "", "BIG-IP", "", "", false, false, false)

	// 2. Success with json
	executePushToAppliance(ctx, "http://127.0.0.1", tmpFile, "icontrol", "/shared/images", "admin", 80, "", "BIG-IP", "", "", false, false, true)
}

func TestHandleCasesUsageAndErrors(t *testing.T) {
	ctx := context.Background()

	// Empty args -> usage
	code, exited := runWithExitCapture(func() {
		handleCases(ctx, []string{})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on empty handleCases args, got %d", code)
	}

	// Missing credentials
	code, exited = runWithExitCapture(func() {
		handleCases(ctx, []string{"list"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on missing creds for list, got %d", code)
	}
}

func TestHandleIHealthUsageAndErrors(t *testing.T) {
	ctx := context.Background()

	// Empty args -> usage
	code, exited := runWithExitCapture(func() {
		handleIHealth(ctx, []string{})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on empty handleIHealth args, got %d", code)
	}

	// Missing credentials
	code, exited = runWithExitCapture(func() {
		handleIHealth(ctx, []string{"list"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on missing creds for ihealth, got %d", code)
	}
}

func TestHandleCasesWithMockServer(t *testing.T) {
	mockMux := http.NewServeMux()
	mockMux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-token-xyz",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mockMux.HandleFunc("/case-management/v1/cases", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"isError": false,
			"data": []map[string]any{
				{
					"caseNumber": "C-12345",
					"status":     "Open",
					"priority":   "3 - Standard",
					"subject":    "Test Support Case",
				},
			},
		})
	})
	mockMux.HandleFunc("/case-management/v1/cases/metadata", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"isError": false,
			"data": map[string]any{
				"rootKey": "rootVal",
			},
		})
	})
	mockMux.HandleFunc("/case-management/v1/cases/C-12345", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"isError": false,
				"data": map[string]any{
					"caseNumber":  "C-12345",
					"updatedDate": "2026-11-16T12:00:00Z",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"isError": false,
			"data": []map[string]any{
				{
					"caseNumber": "C-12345",
					"status":     "Open",
					"priority":   "3 - Standard",
					"subject":    "Test Support Case",
				},
			},
		})
	})

	dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c1, c2 := net.Pipe()
		go func() {
			req, err := http.ReadRequest(bufio.NewReader(c2))
			if err != nil {
				c2.Close()
				return
			}
			rec := httptest.NewRecorder()
			mockMux.ServeHTTP(rec, req)
			resp := rec.Result()
			_ = resp.Write(c2)
			c2.Close()
		}()
		return c1, nil
	}

	client.SetDialContext(dialer)
	defer client.SetDialContext(nil)

	t.Setenv("F5_CLIENT_ID", "test-client")
	t.Setenv("F5_CLIENT_SECRET", "test-secret")
	t.Setenv("F5_AUTH_FQDN", "http://auth.test")
	t.Setenv("F5_SUPPORT_FQDN", "http://cases.test")

	ctx := context.Background()

	// 1. handleCases list (open cases)
	handleCases(ctx, []string{"list"})

	// 2. handleCases list --all (open & closed)
	handleCases(ctx, []string{"list", "--all"})

	// 3. handleCases metadata
	handleCases(ctx, []string{"metadata"})

	// 4. handleCases get
	handleCases(ctx, []string{"get", "C-12345"})

	// 5. handleCases comment
	handleCases(ctx, []string{"comment", "C-12345", "test comment note"})
}

func TestHandleIHealthWithMockServer(t *testing.T) {
	mockMux := http.NewServeMux()
	mockMux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-ihealth-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mockMux.HandleFunc("/qkview-analyzer/api/qkviews", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.f5.ihealth.api+json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"26894783","processing_status":"COMPLETE"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":["26894783"]}`))
	})
	mockMux.HandleFunc("/qkview-analyzer/api/qkviews/26894783", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.f5.ihealth.api+json")
		_, _ = w.Write([]byte(`{"file_name":"test_diag.qkview","hostname":"host.example","gui_uri":"https://ihealth.f5.com/qkview-analyzer/qv/26894783","f5_support_case":"","processing_status":"COMPLETE","file_size":43928363,"upload":{"date":1789750903000}}`))
	})

	dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c1, c2 := net.Pipe()
		go func() {
			req, err := http.ReadRequest(bufio.NewReader(c2))
			if err != nil {
				c2.Close()
				return
			}
			rec := httptest.NewRecorder()
			mockMux.ServeHTTP(rec, req)
			resp := rec.Result()
			_ = resp.Write(c2)
			c2.Close()
		}()
		return c1, nil
	}

	client.SetDialContext(dialer)
	defer client.SetDialContext(nil)

	t.Setenv("IHEALTH_CLIENT_ID", "test-ihealth-client")
	t.Setenv("IHEALTH_CLIENT_SECRET", "test-ihealth-secret")
	t.Setenv("F5_AUTH_FQDN", "http://auth.test")
	t.Setenv("F5_IHEALTH_FQDN", "http://ihealth.test")

	ctx := context.Background()

	// 1. handleIHealth list
	handleIHealth(ctx, []string{"list"})

	// 2. handleIHealth upload with no args -> exit 1
	code, exited := runWithExitCapture(func() {
		handleIHealth(ctx, []string{"upload"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on empty upload args, got %d", code)
	}

	// 3. handleIHealth upload missing file -> exit 1
	code, exited = runWithExitCapture(func() {
		handleIHealth(ctx, []string{"upload", "/nonexistent/path/f.qkview"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on missing file upload, got %d", code)
	}

	// 4. handleIHealth upload valid file -> success
	tmpQkview := filepath.Join(t.TempDir(), "test.qkview")
	_ = os.WriteFile(tmpQkview, []byte("fake-qkview"), 0600)
	handleIHealth(ctx, []string{"upload", tmpQkview, "C-12345", "test description"})

	// 5. handleIHealth unknown action -> exit 1
	code, exited = runWithExitCapture(func() {
		handleIHealth(ctx, []string{"unknown"})
	})
	if !exited || code != 1 {
		t.Errorf("expected exit 1 on unknown action, got %d", code)
	}
}
