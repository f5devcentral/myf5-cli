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
	"strings"
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
		{"http://127.0.0.1:8080", 8080, "http://127.0.0.1:8080"},
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

func TestValidateHost(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		wantHost string
		wantIPv6 bool
		wantErr  bool
	}{
		{"IPv4 standard", "10.1.1.4", "10.1.1.4", false, false},
		{"IPv4 loopback", "127.0.0.1", "127.0.0.1", false, false},
		{"IPv6 unbracketed", "2001:db8::1", "2001:db8::1", true, false},
		{"IPv6 bracketed", "[2001:db8::1]", "2001:db8::1", true, false},
		{"IPv6 loopback unbracketed", "::1", "::1", true, false},
		{"IPv6 loopback bracketed", "[::1]", "::1", true, false},
		{"RFC 1123 hostname", "bigip.lab.local", "bigip.lab.local", false, false},
		{"RFC 1123 single label", "localhost", "localhost", false, false},
		{"RFC 1123 with hyphen", "f5-appliance-01.prod.example.com", "f5-appliance-01.prod.example.com", false, false},

		// Rejections
		{"Empty string", "", "", false, true},
		{"Whitespace", "   ", "", false, true},
		{"Shell injection semicolon", "10.1.1.4; rm -rf /", "", false, true},
		{"Shell injection pipe", "10.1.1.4 | cat /etc/passwd", "", false, true},
		{"Shell injection backtick", "10.1.1.4`id`", "", false, true},
		{"Shell injection subshell", "10.1.1.4$(reboot)", "", false, true},
		{"Prohibited slash", "10.1.1.4/path", "", false, true},
		{"Bracketed IPv4", "[10.1.1.4]", "", false, true},
		{"Invalid IPv4 out of range", "999.999.999.999", "", false, true},
		{"Invalid IPv4 extra octet", "1.2.3.4.5", "", false, true},
		{"Invalid IPv4 partial", "10.1.1", "", false, true},
		{"Invalid IPv6 hex", "[2001:xyz::1]", "", false, true},
		{"Invalid IPv6 double double-colon", "2001::db8::1", "", false, true},
		{"Hostname with special char", "bad@hostname.domain.com", "", false, true},
		{"Hostname with leading hyphen", "-bad-hostname.domain.com", "", false, true},
		{"Hostname with trailing hyphen", "bad-hostname-.domain.com", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHost, gotIPv6, err := ValidateHost(tt.host)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateHost(%q) err = %v, wantErr = %v", tt.host, err, tt.wantErr)
			}
			if !tt.wantErr {
				if gotHost != tt.wantHost {
					t.Errorf("ValidateHost(%q) gotHost = %q, want %q", tt.host, gotHost, tt.wantHost)
				}
				if gotIPv6 != tt.wantIPv6 {
					t.Errorf("ValidateHost(%q) gotIPv6 = %v, want %v", tt.host, gotIPv6, tt.wantIPv6)
				}
			}
		})
	}
}

func TestParseAndValidateApplianceHost(t *testing.T) {
	tests := []struct {
		name        string
		rawHost     string
		defaultPort int
		wantScheme  string
		wantHost    string
		wantPort    int
		wantIPv6    bool
		wantErr     bool
	}{
		{"Plain IPv4", "10.1.1.4", 443, "https", "10.1.1.4", 443, false, false},
		{"HTTP non-loopback forbidden", "http://appliance.lab.local", 0, "", "", 0, false, true},
		{"HTTP loopback IPv4 allowed for mock tests", "http://127.0.0.1:8080", 0, "http", "127.0.0.1", 8080, false, false},
		{"HTTP loopback localhost allowed for mock tests", "http://localhost:8080", 0, "http", "localhost", 8080, false, false},
		{"SSH URL custom port", "ssh://[2001:db8::1]:2222/", 22, "ssh", "2001:db8::1", 2222, true, false},
		{"IPv6 bracketed with port", "[::1]:9443", 443, "https", "::1", 9443, true, false},
		{"IPv6 unbracketed without port", "2001:db8::1", 443, "https", "2001:db8::1", 443, true, false},
		{"Invalid host injection", "10.1.1.4; rm -rf /", 443, "", "", 0, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme, host, port, isIPv6, err := ParseAndValidateApplianceHost(tt.rawHost, tt.defaultPort)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseAndValidateApplianceHost(%q) err = %v, wantErr = %v", tt.rawHost, err, tt.wantErr)
			}
			if !tt.wantErr {
				if scheme != tt.wantScheme {
					t.Errorf("scheme = %q, want %q", scheme, tt.wantScheme)
				}
				if host != tt.wantHost {
					t.Errorf("host = %q, want %q", host, tt.wantHost)
				}
				if port != tt.wantPort {
					t.Errorf("port = %d, want %d", port, tt.wantPort)
				}
				if isIPv6 != tt.wantIPv6 {
					t.Errorf("isIPv6 = %v, want %v", isIPv6, tt.wantIPv6)
				}
			}
		})
	}
}

func TestNewClientWithOptions(t *testing.T) {
	// Valid options with IPv6 bracketed host
	opts := ClientOptions{
		Host:     "[2001:db8::1]:8443",
		Port:     443,
		Username: "admin",
		Password: "secretpassword",
	}
	c, err := NewClientWithOptions(opts)
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}
	if c.Host != "2001:db8::1" {
		t.Errorf("client host = %q, want %q", c.Host, "2001:db8::1")
	}
	if c.Port != 8443 {
		t.Errorf("client port = %d, want %d", c.Port, 8443)
	}
	if wantBase := "https://[2001:db8::1]:8443"; c.BaseURL() != wantBase {
		t.Errorf("client BaseURL() = %q, want %q", c.BaseURL(), wantBase)
	}

	// Invalid host rejection
	badOpts := ClientOptions{
		Host: "invalid;host",
	}
	_, err = NewClientWithOptions(badOpts)
	if err == nil {
		t.Errorf("expected error for invalid host")
	}
}

func TestCreateQKViewTaskWithOptions(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mgmt/cm/autodeploy/qkview" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		b, _ := os.ReadFile("/dev/null")
		_ = b
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"task-12345","status":"IN_PROGRESS","name":"qkview_test.qkview"}`))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c := NewClient(u.Hostname(), port, "admin", "secret")
	c.Scheme = "http"
	c.HTTPClient = server.Client()

	task, err := c.CreateQKViewTaskWithOptions(context.Background(), "qkview_test.qkview", []string{"-s0"})
	if err != nil {
		t.Fatalf("CreateQKViewTaskWithOptions failed: %v", err)
	}
	if task.ID != "task-12345" {
		t.Errorf("task ID = %q, want task-12345", task.ID)
	}
	if !strings.Contains(receivedBody, `"-s0"`) {
		t.Errorf("expected request body to contain -s0 option, got %s", receivedBody)
	}
}

func TestGetEndpointExample(t *testing.T) {
	mockJSON := `{"name":"pool_1","members":[{"name":"node1:80"}]}`
	var requestedPath string
	var authHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		authHeader = r.Header.Get("Authorization")
		if r.URL.Path == "/mgmt/tm/ltm/pool/example" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(mockJSON))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c := NewClient(u.Hostname(), port, "admin", "secret")
	c.Scheme = "http"
	c.HTTPClient = server.Client()

	// 1. Test normalization with "tm/ltm/pool"
	out, err := c.GetEndpointExample(context.Background(), "tm/ltm/pool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requestedPath != "/mgmt/tm/ltm/pool/example" {
		t.Errorf("requestedPath = %q, want /mgmt/tm/ltm/pool/example", requestedPath)
	}
	if !strings.HasPrefix(authHeader, "Basic ") {
		t.Errorf("missing or invalid auth header: %q", authHeader)
	}
	if !strings.Contains(string(out), "  \"name\": \"pool_1\"") {
		t.Errorf("expected pretty-printed JSON, got:\n%s", string(out))
	}

	// 2. Test already with /mgmt/ and /example
	_, err = c.GetEndpointExample(context.Background(), "/mgmt/tm/ltm/pool/example/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requestedPath != "/mgmt/tm/ltm/pool/example" {
		t.Errorf("requestedPath = %q, want /mgmt/tm/ltm/pool/example", requestedPath)
	}

	// 3. Test 404
	_, err = c.GetEndpointExample(context.Background(), "tm/nonexistent")
	if err == nil {
		t.Errorf("expected 404 error, got nil")
	}

	// 4. Test empty endpoint
	_, err = c.GetEndpointExample(context.Background(), "   ")
	if err == nil {
		t.Errorf("expected error on empty endpoint, got nil")
	}
}

func TestClientZero(t *testing.T) {
	t.Run("scrubs PasswordBytes and clears Password", func(t *testing.T) {
		pw := []byte("super-secret-password-xyz")
		client, err := NewClientWithOptions(ClientOptions{
			Host:          "192.0.2.1",
			Port:          443,
			Username:      "admin",
			Password:      string(pw),
			PasswordBytes: pw,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		client.Zero()

		if client.Password != "" {
			t.Errorf("Password was not cleared: %q", client.Password)
		}
		if client.PasswordBytes != nil {
			t.Errorf("PasswordBytes was not cleared to nil")
		}
		for i, v := range pw {
			if v != 0 {
				t.Errorf("byte %d was not zeroed: %d", i, v)
			}
		}
	})

	t.Run("handles nil safely", func(t *testing.T) {
		var nilClient *Client
		nilClient.Zero() // Must not panic
	})
}

func TestSCPOptionsZero(t *testing.T) {
	pw := []byte("ssh-pass-123")
	passphrase := []byte("passphrase-456")

	opts := &SCPOptions{
		Host:            "192.0.2.1",
		Port:            22,
		Username:        "admin",
		Password:        string(pw),
		PasswordBytes:   pw,
		Passphrase:      string(passphrase),
		PassphraseBytes: passphrase,
	}

	opts.Zero()

	if opts.Password != "" {
		t.Errorf("Password not cleared: %q", opts.Password)
	}
	if opts.Passphrase != "" {
		t.Errorf("Passphrase not cleared: %q", opts.Passphrase)
	}
	if opts.PasswordBytes != nil {
		t.Errorf("PasswordBytes not nil")
	}
	if opts.PassphraseBytes != nil {
		t.Errorf("PassphraseBytes not nil")
	}

	for i, v := range pw {
		if v != 0 {
			t.Errorf("pw byte %d not zeroed: %d", i, v)
		}
	}
	for i, v := range passphrase {
		if v != 0 {
			t.Errorf("passphrase byte %d not zeroed: %d", i, v)
		}
	}

	var nilOpts *SCPOptions
	nilOpts.Zero() // Must not panic
}

func TestTransferOptionsZero(t *testing.T) {
	pw := []byte("secret_pw_transfer")
	passphrase := []byte("secret_pass_transfer")

	opts := &TransferOptions{
		TargetType:         "bigip",
		Method:             MethodAuto,
		Username:           "admin",
		Password:           string(pw),
		PasswordBytes:      pw,
		SSHPassphrase:      string(passphrase),
		SSHPassphraseBytes: passphrase,
	}

	opts.Zero()

	if opts.Password != "" {
		t.Errorf("Password not cleared: %q", opts.Password)
	}
	if opts.SSHPassphrase != "" {
		t.Errorf("SSHPassphrase not cleared: %q", opts.SSHPassphrase)
	}
	if opts.PasswordBytes != nil {
		t.Errorf("PasswordBytes not nil")
	}
	if opts.SSHPassphraseBytes != nil {
		t.Errorf("SSHPassphraseBytes not nil")
	}

	for i, v := range pw {
		if v != 0 {
			t.Errorf("pw byte %d not zeroed: %d", i, v)
		}
	}
	for i, v := range passphrase {
		if v != 0 {
			t.Errorf("passphrase byte %d not zeroed: %d", i, v)
		}
	}

	var nilOpts *TransferOptions
	nilOpts.Zero() // Must not panic
}
