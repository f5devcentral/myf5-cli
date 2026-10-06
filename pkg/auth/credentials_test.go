package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckNoCLISecrets(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr bool
	}{
		{args: []string{"myf5", "downloads", "families"}, wantErr: false},
		{args: []string{"myf5", "--client-secret", "abc"}, wantErr: true},
		{args: []string{"myf5", "--password=123"}, wantErr: true},
		{args: []string{"myf5", "-pw", "secret"}, wantErr: true},
		{args: []string{"myf5", "--profile", "prod"}, wantErr: false},

		// SSH key path flags. "--key" is forbidden: it is close enough to a
		// secret-bearing flag that it is rejected outright, so an SSH key
		// *path* must be passed as --identity/-i (appliance push/download) or
		// --push-key (download-then-push). Documentation advertised "--key"
		// at one point; these cases pin which spellings actually work so the
		// docs and the validator cannot drift apart again.
		{args: []string{"myf5", "--key", "/home/u/.ssh/id_ed25519"}, wantErr: true},
		{args: []string{"myf5", "--key=/home/u/.ssh/id_ed25519"}, wantErr: true},
		{args: []string{"myf5", "-key", "/home/u/.ssh/id_ed25519"}, wantErr: true},
		{args: []string{"myf5", "--identity", "/home/u/.ssh/id_ed25519"}, wantErr: false},
		{args: []string{"myf5", "-i", "/home/u/.ssh/id_ed25519"}, wantErr: false},
		{args: []string{"myf5", "--push-key", "/home/u/.ssh/id_ed25519"}, wantErr: false},

		// "-p" is the port flag on appliance commands and is deliberately NOT
		// forbidden; only "-pw" is. Two documents claimed "-p" was trapped as a
		// secret flag, which would have made "bigip push -p 8443" impossible.
		{args: []string{"myf5", "bigip", "push", "-p", "8443"}, wantErr: false},
		{args: []string{"myf5", "-pw", "hunter2"}, wantErr: true},
	}

	for _, tt := range tests {
		err := CheckNoCLISecrets(tt.args)
		if (err != nil) != tt.wantErr {
			t.Errorf("CheckNoCLISecrets(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
		}
	}
}

func TestResolveCredentialsEnv(t *testing.T) {
	os.Setenv("F5_CLIENT_ID", "env-client-id")
	os.Setenv("F5_CLIENT_SECRET", "env-client-secret")
	defer func() {
		os.Unsetenv("F5_CLIENT_ID")
		os.Unsetenv("F5_CLIENT_SECRET")
	}()

	creds, err := ResolveCredentials("default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.ClientID != "env-client-id" || creds.ClientSecret != "env-client-secret" {
		t.Errorf("unexpected credentials: %+v", creds)
	}
}

// The `k` API usage key is a second credential, separate from the OAuth pair,
// and is issued per entitlement. These cover each way it can be supplied, plus
// its redaction.
func TestResolveAPIKey(t *testing.T) {
	t.Run("from environment alongside env client creds", func(t *testing.T) {
		os.Setenv("F5_CLIENT_ID", "env-client-id")
		os.Setenv("F5_CLIENT_SECRET", "env-client-secret")
		os.Setenv("F5_API_KEY", "env-api-key")
		defer func() {
			os.Unsetenv("F5_CLIENT_ID")
			os.Unsetenv("F5_CLIENT_SECRET")
			os.Unsetenv("F5_API_KEY")
		}()

		creds, err := ResolveCredentials("default")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if creds.APIKey != "env-api-key" {
			t.Errorf("APIKey = %q, want env-api-key", creds.APIKey)
		}
	})

	t.Run("absent leaves APIKey empty so the client falls back", func(t *testing.T) {
		os.Setenv("F5_CLIENT_ID", "env-client-id")
		os.Setenv("F5_CLIENT_SECRET", "env-client-secret")
		os.Unsetenv("F5_API_KEY")
		defer func() {
			os.Unsetenv("F5_CLIENT_ID")
			os.Unsetenv("F5_CLIENT_SECRET")
		}()

		creds, err := ResolveCredentials("default")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if creds.APIKey != "" {
			t.Errorf("APIKey = %q, want empty", creds.APIKey)
		}
	})

	t.Run("from flat file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), ".f5api_credentials")
		content := []byte("client_id=file-id\nclient_secret=file-sec\napi_key=file-api-key\n")
		if err := os.WriteFile(f, content, 0600); err != nil {
			t.Fatal(err)
		}
		creds, err := readCredentialsFile(f, "default", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if creds.APIKey != "file-api-key" {
			t.Errorf("APIKey = %q, want file-api-key", creds.APIKey)
		}
	})

	t.Run("profile key overrides top-level, top-level serves other profiles", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "ini_creds")
		content := []byte("api_key = shared-key\n\n[default]\nclient_id = d-id\nclient_secret = d-sec\n\n[prod]\nclient_id = p-id\nclient_secret = p-sec\napi_key = prod-only-key\n")
		if err := os.WriteFile(f, content, 0600); err != nil {
			t.Fatal(err)
		}

		prod, err := readCredentialsFile(f, "prod", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prod.APIKey != "prod-only-key" {
			t.Errorf("prod APIKey = %q, want prod-only-key", prod.APIKey)
		}

		def, err := readCredentialsFile(f, "default", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if def.APIKey != "shared-key" {
			t.Errorf("default APIKey = %q, want shared-key (top-level fallback)", def.APIKey)
		}
	})

	t.Run("never appears in String output", func(t *testing.T) {
		c := Credentials{ClientID: "id", ClientSecret: "sec", APIKey: "super-secret-key"}
		if s := c.String(); strings.Contains(s, "super-secret-key") {
			t.Errorf("String() leaked the API key: %s", s)
		}
	})

	t.Run("proxy password never appears in String output", func(t *testing.T) {
		c := Credentials{
			ClientID:     "id",
			ClientSecret: "sec",
			Proxy:        "http://proxyuser:supersecretpass@proxy.corp.net:8080",
		}
		s := c.String()
		if strings.Contains(s, "supersecretpass") {
			t.Errorf("String() leaked the proxy password: %s", s)
		}
		if !strings.Contains(s, "[REDACTED]") {
			t.Errorf("String() should contain [REDACTED] for proxy password: %s", s)
		}
	})
}

func TestResolveProxyCredentials(t *testing.T) {
	t.Run("reads proxy from credentials file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "ini_creds")
		content := []byte("[default]\nclient_id = d-id\nclient_secret = d-sec\nproxy = socks5://proxy.internal:1080\n")
		if err := os.WriteFile(f, content, 0600); err != nil {
			t.Fatal(err)
		}

		creds, err := readCredentialsFile(f, "default", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if creds.Proxy != "socks5://proxy.internal:1080" {
			t.Errorf("Proxy = %q, want socks5://proxy.internal:1080", creds.Proxy)
		}
	})
}

// iHealth's OAuth client is granted the "ihealth" scope, which the general
// MyF5 client is not; reusing one client for both fails with
// 403 "Client has not been granted scopes: ihealth". One credentials file
// therefore has to hold both pairs, keyed by service prefix.
func TestResolveCredentialsForService(t *testing.T) {
	t.Run("prefixed keys win over unprefixed", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), ".f5api_credentials")
		content := []byte("client_id=general-id\nclient_secret=general-sec\n" +
			"ihealth_client_id=ih-id\nihealth_client_secret=ih-sec\n")
		if err := os.WriteFile(f, content, 0600); err != nil {
			t.Fatal(err)
		}

		ih, err := readCredentialsFile(f, "default", "ihealth")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ih.ClientID != "ih-id" || ih.ClientSecret != "ih-sec" {
			t.Errorf("ihealth creds = %q/%q, want ih-id/ih-sec", ih.ClientID, ih.ClientSecret)
		}

		gen, err := readCredentialsFile(f, "default", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gen.ClientID != "general-id" {
			t.Errorf("general creds = %q, want general-id", gen.ClientID)
		}
	})

	t.Run("falls back to unprefixed when service keys absent", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), ".f5api_credentials")
		if err := os.WriteFile(f, []byte("client_id=only-id\nclient_secret=only-sec\n"), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := readCredentialsFile(f, "default", "ihealth")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.ClientID != "only-id" {
			t.Errorf("ClientID = %q, want only-id fallback", c.ClientID)
		}
	})

	t.Run("service environment variables win", func(t *testing.T) {
		os.Setenv("F5_CLIENT_ID", "env-general")
		os.Setenv("F5_CLIENT_SECRET", "env-general-sec")
		os.Setenv("IHEALTH_CLIENT_ID", "env-ih")
		os.Setenv("IHEALTH_CLIENT_SECRET", "env-ih-sec")
		defer func() {
			os.Unsetenv("F5_CLIENT_ID")
			os.Unsetenv("F5_CLIENT_SECRET")
			os.Unsetenv("IHEALTH_CLIENT_ID")
			os.Unsetenv("IHEALTH_CLIENT_SECRET")
		}()

		c, err := ResolveCredentialsForService("default", "ihealth")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.ClientID != "env-ih" || c.ClientSecret != "env-ih-sec" {
			t.Errorf("creds = %q/%q, want env-ih/env-ih-sec", c.ClientID, c.ClientSecret)
		}
	})
}

func TestReadCredentialsFile(t *testing.T) {
	tempDir := t.TempDir()
	kvFile := filepath.Join(tempDir, ".f5api_credentials")
	content := []byte("username=test@f5.com\nclient_id=file-client-id\nclient_secret=file-client-secret\n")
	if err := os.WriteFile(kvFile, content, 0600); err != nil {
		t.Fatal(err)
	}

	creds, err := readCredentialsFile(kvFile, "default", "")
	if err != nil {
		t.Fatalf("unexpected error reading kv file: %v", err)
	}
	if creds.ClientID != "file-client-id" || creds.ClientSecret != "file-client-secret" {
		t.Errorf("unexpected credentials: %+v", creds)
	}

	// Test INI format
	iniFile := filepath.Join(tempDir, "ini_creds")
	iniContent := []byte("[default]\nclient_id = ini-def-id\nclient_secret = ini-def-sec\n\n[prod]\nclient_id = ini-prod-id\nclient_secret = ini-prod-sec\n")
	if err := os.WriteFile(iniFile, iniContent, 0600); err != nil {
		t.Fatal(err)
	}

	credsProd, err := readCredentialsFile(iniFile, "prod", "")
	if err != nil {
		t.Fatalf("unexpected error reading ini prod: %v", err)
	}
	if credsProd.ClientID != "ini-prod-id" || credsProd.ClientSecret != "ini-prod-sec" {
		t.Errorf("unexpected credentials: %+v", credsProd)
	}
}

func TestZeroBytes(t *testing.T) {
	b := []byte("super-secret-password-12345")
	ZeroBytes(b)
	for i, v := range b {
		if v != 0 {
			t.Errorf("byte at index %d was not zeroed: %d", i, v)
		}
	}
}

func TestResolveCredentialsCustomFile(t *testing.T) {
	tempDir := t.TempDir()
	customPath := filepath.Join(tempDir, "custom_creds.txt")
	content := []byte("client_id=custom-id\nclient_secret=custom-sec\n")
	if err := os.WriteFile(customPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	os.Setenv("F5_CREDENTIALS_FILE", customPath)
	defer os.Unsetenv("F5_CREDENTIALS_FILE")

	creds, err := ResolveCredentials("default")
	if err != nil {
		t.Fatalf("unexpected error resolving custom file: %v", err)
	}
	if creds.ClientID != "custom-id" || creds.ClientSecret != "custom-sec" {
		t.Errorf("got %v, want custom-id/custom-sec", creds)
	}
}
