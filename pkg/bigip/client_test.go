package bigip

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func TestCheckReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mgmt/tm/sys/ready" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"kind": "tm:sys:ready"}`))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c := NewClient(u.Hostname(), port, "admin", "secret")
	c.Scheme = "http"
	c.HTTPClient = server.Client()

	ready, err := c.CheckReady(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ready {
		t.Errorf("expected appliance to be ready")
	}
}

func TestBaseURLAndURLNormalization(t *testing.T) {
	tests := []struct {
		inputHost string
		inputPort int
		wantBase  string
	}{
		{"10.1.1.4", 443, "https://10.1.1.4"},
		{"https://aa68f6ba-a751-4740-9382-85f7cd7940f2.access.udf.f5.com/", 443, "https://aa68f6ba-a751-4740-9382-85f7cd7940f2.access.udf.f5.com"},
		{"aa68f6ba-a751-4740-9382-85f7cd7940f2.access.udf.f5.com", 443, "https://aa68f6ba-a751-4740-9382-85f7cd7940f2.access.udf.f5.com"},
		{"http://10.1.1.4", 80, "http://10.1.1.4"},
		{"10.1.1.4:8443", 0, "https://10.1.1.4:8443"},
		{"https://bigip.lab.local:8443/", 0, "https://bigip.lab.local:8443"},
	}

	for _, tc := range tests {
		c := NewClient(tc.inputHost, tc.inputPort, "admin", "secret")
		if got := c.BaseURL(); got != tc.wantBase {
			t.Errorf("NewClient(%q, %d).BaseURL() = %q, want %q", tc.inputHost, tc.inputPort, got, tc.wantBase)
		}
	}
}

func TestValidateQKViewFilename(t *testing.T) {
	valid := []string{
		"myqkview.qkview",
		"qkview_10.1.1.4_1600000000.qkview",
		"backup-2026-09-30.qkview",
		"my_appliance_qkview",
	}
	for _, f := range valid {
		if err := ValidateQKViewFilename(f); err != nil {
			t.Errorf("expected valid filename %q, got error: %v", f, err)
		}
	}

	invalid := []string{
		"qkview; rm -rf /; .qkview",
		"qkview$(reboot).qkview",
		"qkview`id`.qkview",
		"../../etc/passwd",
		"qkview|cat /etc/shadow",
		"qkview&touch /tmp/pwned",
		"",
	}
	for _, f := range invalid {
		if err := ValidateQKViewFilename(f); err == nil {
			t.Errorf("expected rejection for malicious/invalid filename %q, got nil error", f)
		}
	}
}

func TestDownloadFromURI(t *testing.T) {
	testData := []byte("f5-mock-download-content-54321")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Range", fmt.Sprintf("0-%d/%d", len(testData)-1, len(testData)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testData)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c := NewClient(u.Hostname(), port, "admin", "secret")
	c.Scheme = "http"
	c.HTTPClient = server.Client()

	tempDir := t.TempDir()
	destFile := filepath.Join(tempDir, "downloaded.iso")

	var progressCalled bool
	err := c.DownloadFromURI(context.Background(), "/mgmt/cm/autodeploy/software-image-downloads/BIGIP-17.1.1.iso", destFile, func(transferred, total int64) {
		progressCalled = true
	})
	if err != nil {
		t.Fatalf("DownloadFromURI failed: %v", err)
	}

	content, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("failed to read destFile: %v", err)
	}
	if string(content) != string(testData) {
		t.Errorf("expected content %q, got %q", string(testData), string(content))
	}
	if !progressCalled {
		t.Errorf("expected progress callback to be called")
	}
}

func TestDeleteQKView(t *testing.T) {
	var receivedMethod, receivedPath, receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c := NewClient(u.Hostname(), port, "admin", "secret")
	c.Scheme = "http"
	c.HTTPClient = server.Client()

	err := c.DeleteQKView(context.Background(), "test.qkview")
	if err != nil {
		t.Fatalf("DeleteQKView failed: %v", err)
	}

	if receivedMethod != http.MethodDelete {
		t.Errorf("expected DELETE method, got %s", receivedMethod)
	}
	if receivedPath != "/mgmt/cm/autodeploy/qkview/test.qkview" {
		t.Errorf("unexpected path: %s", receivedPath)
	}
	if receivedAuth == "" {
		t.Errorf("expected basic auth header")
	}

	// Test invalid filename rejection
	if err := c.DeleteQKView(context.Background(), "../invalid"); err == nil {
		t.Errorf("expected error for path traversal filename")
	}
}

func TestGenerateQKViewDefaultFilenameISO8601(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"command":"run","utilCmdArgs":"-f /var/tmp/qkviews/test.qkview","status":"completed"}`))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c := NewClient(u.Hostname(), port, "admin", "secret")
	c.Scheme = "http"
	c.HTTPClient = server.Client()

	filename, err := c.GenerateQKViewWithStatus(context.Background(), "", true, nil)
	if err != nil {
		t.Fatalf("GenerateQKViewWithStatus failed: %v", err)
	}

	re := regexp.MustCompile(`^qkview_.*_\d{8}T\d{6}Z\.qkview$`)
	if !re.MatchString(filename) {
		t.Errorf("expected filename matching ISO-8601 UTC timestamp pattern, got %q", filename)
	}
}
