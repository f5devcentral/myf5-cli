package client

import (
	"crypto/tls"
	"testing"
	"time"
)

func TestSecureTLSConfig(t *testing.T) {
	cfgSecure := SecureTLSConfig(false)
	if cfgSecure.InsecureSkipVerify {
		t.Errorf("expected InsecureSkipVerify=false, got true")
	}
	if cfgSecure.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion TLS 1.2 (%x), got %x", tls.VersionTLS12, cfgSecure.MinVersion)
	}
	if len(cfgSecure.CipherSuites) == 0 {
		t.Errorf("expected non-empty cipher suites")
	}

	cfgInsecure := SecureTLSConfig(true)
	if !cfgInsecure.InsecureSkipVerify {
		t.Errorf("expected InsecureSkipVerify=true, got false")
	}
}

func TestNewClients(t *testing.T) {
	secClient := NewSecureHTTPClient(10 * time.Second)
	if secClient == nil || secClient.Timeout != 10*time.Second {
		t.Fatalf("expected client with 10s timeout, got %v", secClient)
	}

	insecClient := NewInsecureHTTPClient(5 * time.Second)
	if insecClient == nil || insecClient.Timeout != 5*time.Second {
		t.Fatalf("expected client with 5s timeout, got %v", insecClient)
	}
}
