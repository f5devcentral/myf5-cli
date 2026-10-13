package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestScrubSecrets(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bearer token scrubbing",
			input:    "Making request with Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.xyz.123 to endpoint",
			expected: "Making request with Bearer [REDACTED] to endpoint",
		},
		{
			name:     "Basic auth scrubbing",
			input:    "Authorization header set: Basic YWRtaW46c2VjcmV0MTIz",
			expected: "Authorization header set: Basic [REDACTED]",
		},
		{
			name:     "JSON client_secret scrubbing",
			input:    `{"client_id": "my-id", "client_secret": "super_secret_val_123", "scope": "read"}`,
			expected: `{"client_id": "my-id", "client_secret": "[REDACTED]", "scope": "read"}`,
		},
		{
			name:     "JSON password scrubbing",
			input:    `{"username": "admin", "password": "superPassword!#1"}`,
			expected: `{"username": "admin", "password": "[REDACTED]"}`,
		},
		{
			name:     "URL credentials scrubbing",
			input:    "Connecting to https://admin:mypassword123@bigip.internal.net:8443/mgmt/tm/sys",
			expected: "Connecting to https://admin:REDACTED@bigip.internal.net:8443/mgmt/tm/sys",
		},
		{
			name:     "Non-secret text unchanged",
			input:    "Connecting to appliance 10.1.1.4:443 successfully",
			expected: "Connecting to appliance 10.1.1.4:443 successfully",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ScrubSecrets(tc.input)
			if got != tc.expected {
				t.Errorf("ScrubSecrets() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestFallbackLogger(t *testing.T) {
	var buf bytes.Buffer
	l := NewFallbackLogger(&buf)

	l.Info("User logged in with token: Bearer abcdef123456")
	l.Warn("Invalid password entered: password: 'secretpassword'")
	l.Error("Connection failed to https://admin:mysecret@host:443")

	out := buf.String()

	if strings.Contains(out, "abcdef123456") {
		t.Errorf("logger leaked bearer token: %s", out)
	}
	if strings.Contains(out, "secretpassword") {
		t.Errorf("logger leaked password: %s", out)
	}
	if strings.Contains(out, "mysecret") {
		t.Errorf("logger leaked URL password: %s", out)
	}

	if !strings.Contains(out, "[INFO]") || !strings.Contains(out, "Bearer [REDACTED]") {
		t.Errorf("missing sanitized INFO log: %s", out)
	}
	if !strings.Contains(out, "[WARN]") || !strings.Contains(out, "[REDACTED]") {
		t.Errorf("missing sanitized WARN log: %s", out)
	}
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "REDACTED") {
		t.Errorf("missing sanitized ERROR log: %s", out)
	}
}

func TestGetDefaultLogger(t *testing.T) {
	l := Get()
	if l == nil {
		t.Fatal("expected non-nil default OS logger")
	}
	// Verify calling logging methods does not panic
	l.Info("myf5 test info message")
	l.Warn("myf5 test warning message")
	l.Error("myf5 test error message")
}
