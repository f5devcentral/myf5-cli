package auth

import (
	"os"
	"path/filepath"
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

func TestReadCredentialsFile(t *testing.T) {
	tempDir := t.TempDir()
	kvFile := filepath.Join(tempDir, ".f5api_credentials")
	content := []byte("username=test@f5.com\nclient_id=file-client-id\nclient_secret=file-client-secret\n")
	if err := os.WriteFile(kvFile, content, 0600); err != nil {
		t.Fatal(err)
	}

	creds, err := readCredentialsFile(kvFile, "default")
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

	credsProd, err := readCredentialsFile(iniFile, "prod")
	if err != nil {
		t.Fatalf("unexpected error reading ini prod: %v", err)
	}
	if credsProd.ClientID != "ini-prod-id" || credsProd.ClientSecret != "ini-prod-sec" {
		t.Errorf("unexpected credentials: %+v", credsProd)
	}
}
