package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
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

func TestLoadClientCertificate(t *testing.T) {
	// Generate self-signed ECDSA cert for testing
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"F5 Test"},
			CommonName:   "admin",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "client.crt")
	keyFile := filepath.Join(tmpDir, "client.key")
	combinedFile := filepath.Join(tmpDir, "combined.pem")

	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("failed to write cert file: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}
	if err := os.WriteFile(combinedFile, append(certPEM, keyPEM...), 0600); err != nil {
		t.Fatalf("failed to write combined file: %v", err)
	}

	// 1. Load separate files
	cert, err := LoadClientCertificate(certFile, keyFile)
	if err != nil {
		t.Fatalf("LoadClientCertificate(certFile, keyFile) failed: %v", err)
	}
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("expected valid certificate loaded")
	}

	// 2. Load combined file
	combinedCert, err := LoadClientCertificate(combinedFile, "")
	if err != nil {
		t.Fatalf("LoadClientCertificate(combinedFile, \"\") failed: %v", err)
	}
	if combinedCert == nil || len(combinedCert.Certificate) == 0 {
		t.Fatalf("expected valid certificate from combined file")
	}

	// 3. Error cases
	if _, err := LoadClientCertificate("", keyFile); err == nil {
		t.Errorf("expected error when certFile is empty but keyFile is provided")
	}
	nilCert, err := LoadClientCertificate("", "")
	if err != nil || nilCert != nil {
		t.Errorf("expected (nil, nil) when both paths are empty, got (%v, %v)", nilCert, err)
	}

	// 4. Test client constructor with cert
	clientWithCert := NewInsecureHTTPClientWithCert(10*time.Second, cert)
	if clientWithCert == nil {
		t.Fatalf("expected valid client with cert")
	}
	transport, ok := clientWithCert.Transport.(*http.Transport)
	if !ok || len(transport.TLSClientConfig.Certificates) != 1 {
		t.Errorf("expected transport to have 1 certificate configured")
	}
}
