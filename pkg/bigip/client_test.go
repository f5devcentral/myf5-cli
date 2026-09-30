package bigip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
