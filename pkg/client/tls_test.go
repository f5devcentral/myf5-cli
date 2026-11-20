package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	// Assert 256-bit ciphers are prioritized over 128-bit
	if cfgSecure.CipherSuites[0] != tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384 {
		t.Errorf("expected TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384 as first cipher, got %x", cfgSecure.CipherSuites[0])
	}
	// Assert Post-Quantum Cryptography (PQC) hybrid key exchange is first preference
	if len(cfgSecure.CurvePreferences) == 0 || cfgSecure.CurvePreferences[0] != tls.X25519MLKEM768 {
		t.Errorf("expected X25519MLKEM768 as first curve preference for PQC, got %v", cfgSecure.CurvePreferences)
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
	if secClient.CheckRedirect == nil {
		t.Errorf("expected CheckRedirect policy configured on secure client")
	}

	insecClient := NewInsecureHTTPClient(5 * time.Second)
	if insecClient == nil || insecClient.Timeout != 5*time.Second {
		t.Fatalf("expected client with 5s timeout, got %v", insecClient)
	}
	if insecClient.CheckRedirect == nil {
		t.Errorf("expected CheckRedirect policy configured on insecure client")
	}
}

func TestNewProductionTransportWithProxy(t *testing.T) {
	customDialed := false
	SetDialContext(func(ctx context.Context, network, addr string) (net.Conn, error) {
		customDialed = true
		c1, c2 := net.Pipe()
		c2.Close()
		return c1, nil
	})
	defer SetDialContext(memDial)

	tr := NewProductionTransport(SecureTLSConfig(true))
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	conn, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:80")
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	conn.Close()
	if !customDialed {
		t.Errorf("expected custom dialer to be invoked from transport")
	}
}

func TestSecureRedirectPolicy(t *testing.T) {
	origReq, err := http.NewRequest(http.MethodGet, "https://api.example.com/cases", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	// 1. Insecure downgrade: HTTPS -> HTTP
	httpReq, err := http.NewRequest(http.MethodGet, "http://api.example.com/cases", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	err = SecureRedirectPolicy(httpReq, []*http.Request{origReq})
	if err == nil || !strings.Contains(err.Error(), "refusing insecure HTTP redirect") {
		t.Errorf("expected insecure downgrade to be rejected, got: %v", err)
	}

	// 2. Cross-host redirect strips Authorization & X-F5-Auth-Token
	crossHostReq, err := http.NewRequest(http.MethodGet, "https://other.example.com/cases", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	crossHostReq.Header.Set("Authorization", "Bearer secret")
	crossHostReq.Header.Set("X-F5-Auth-Token", "f5secret")
	err = SecureRedirectPolicy(crossHostReq, []*http.Request{origReq})
	if err != nil {
		t.Fatalf("unexpected redirect error: %v", err)
	}
	if crossHostReq.Header.Get("Authorization") != "" {
		t.Errorf("expected Authorization header to be stripped on cross-host redirect")
	}
	if crossHostReq.Header.Get("X-F5-Auth-Token") != "" {
		t.Errorf("expected X-F5-Auth-Token header to be stripped on cross-host redirect")
	}

	// 3. Cross-host redirect with query credential 'k' on original URL
	origWithK, err := http.NewRequest(http.MethodGet, "https://api.example.com/cases?k=my-secret-key", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	err = SecureRedirectPolicy(crossHostReq, []*http.Request{origWithK})
	if err == nil || !strings.Contains(err.Error(), "refusing cross-origin redirect that would leak query credentials") {
		t.Errorf("expected leak prevention on cross-host redirect with k, got: %v", err)
	}

	// 4. Cross-host redirect with query credential 'k' on destination URL
	crossWithK, err := http.NewRequest(http.MethodGet, "https://other.example.com/cases?k=my-secret-key", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	err = SecureRedirectPolicy(crossWithK, []*http.Request{origReq})
	if err == nil || !strings.Contains(err.Error(), "refusing cross-origin redirect that would leak query credentials") {
		t.Errorf("expected leak prevention when redirect URL has k, got: %v", err)
	}

	// 5. Same host redirect preserves Authorization
	sameHostReq, err := http.NewRequest(http.MethodGet, "https://api.example.com/v2/cases", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	sameHostReq.Header.Set("Authorization", "Bearer secret")
	err = SecureRedirectPolicy(sameHostReq, []*http.Request{origReq})
	if err != nil {
		t.Fatalf("unexpected redirect error: %v", err)
	}
	if sameHostReq.Header.Get("Authorization") != "Bearer secret" {
		t.Errorf("expected Authorization to be retained for same-host redirect")
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

func TestSecureTLSConfig_CustomCABundle(t *testing.T) {
	// 1. Generate ephemeral Root CA
	caPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed generating CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject: pkix.Name{
			Organization: []string{"F5 Test Corp Inspection CA"},
			CommonName:   "F5 Test Root CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatalf("failed creating CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("failed parsing CA cert: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	// 2. Generate server leaf certificate signed by ephemeral Root CA for 127.0.0.1
	srvPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed generating server key: %v", err)
	}
	srvTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(101),
		Subject: pkix.Name{
			Organization: []string{"F5 Test Server"},
			CommonName:   "127.0.0.1",
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost"},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTemplate, caCert, &srvPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatalf("failed creating server cert: %v", err)
	}
	srvKeyBytes, err := x509.MarshalECPrivateKey(srvPriv)
	if err != nil {
		t.Fatalf("failed marshaling server key: %v", err)
	}
	srvTLSCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: srvKeyBytes}),
	)
	if err != nil {
		t.Fatalf("failed building server key pair: %v", err)
	}

	// 3. Launch in-memory TLS server backed by memListener/memDial
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("trusted-via-custom-ca"))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.Listener = newMemListener()
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{srvTLSCert},
	}
	srv.StartTLS()
	defer srv.Close()

	tmpDir := t.TempDir()
	caFile := filepath.Join(tmpDir, "corp-ca-bundle.pem")
	if err := os.WriteFile(caFile, caPEM, 0600); err != nil {
		t.Fatalf("failed writing CA bundle: %v", err)
	}
	emptyFile := filepath.Join(tmpDir, "empty.pem")
	if err := os.WriteFile(emptyFile, nil, 0600); err != nil {
		t.Fatalf("failed writing empty file: %v", err)
	}
	invalidFile := filepath.Join(tmpDir, "invalid.pem")
	if err := os.WriteFile(invalidFile, []byte("not-a-valid-pem-certificate"), 0600); err != nil {
		t.Fatalf("failed writing invalid file: %v", err)
	}

	// Case A: Without MYF5_CA_BUNDLE or SSL_CERT_FILE, verification must fail
	t.Setenv("MYF5_CA_BUNDLE", "")
	t.Setenv("SSL_CERT_FILE", "")
	untrustedClient := NewSecureHTTPClient(500 * time.Millisecond)
	if resp, err := untrustedClient.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Fatalf("expected TLS verification failure without custom CA bundle, but request succeeded")
	}

	// Case B: With MYF5_CA_BUNDLE pointing to ephemeral CA PEM, verification succeeds
	t.Setenv("MYF5_CA_BUNDLE", caFile)
	t.Setenv("SSL_CERT_FILE", "/nonexistent/ignored-when-myf5-ca-bundle-set.pem")
	trustedByMyF5 := NewSecureHTTPClient(2 * time.Second)
	resp, err := trustedByMyF5.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected TLS verification to succeed with MYF5_CA_BUNDLE, got: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected HTTP 200 with MYF5_CA_BUNDLE, got %d", resp.StatusCode)
	}

	// Case C: With SSL_CERT_FILE fallback when MYF5_CA_BUNDLE is unset
	t.Setenv("MYF5_CA_BUNDLE", "")
	t.Setenv("SSL_CERT_FILE", caFile)
	trustedBySSLCertFile := NewSecureHTTPClient(2 * time.Second)
	resp2, err := trustedBySSLCertFile.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected TLS verification to succeed with SSL_CERT_FILE, got: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected HTTP 200 with SSL_CERT_FILE, got %d", resp2.StatusCode)
	}

	// Case D: Graceful fallback when bundle path is missing, empty, or invalid PEM
	for _, badPath := range []string{
		filepath.Join(tmpDir, "missing.pem"),
		emptyFile,
		invalidFile,
	} {
		t.Setenv("MYF5_CA_BUNDLE", badPath)
		t.Setenv("SSL_CERT_FILE", "")
		cfg := SecureTLSConfig(false)
		if cfg.RootCAs != nil {
			t.Errorf("expected nil RootCAs fallback for invalid bundle %q, got non-nil", badPath)
		}
		fallbackClient := NewSecureHTTPClient(500 * time.Millisecond)
		if r, err := fallbackClient.Get(srv.URL); err == nil {
			r.Body.Close()
			t.Errorf("expected untrusted server to fail TLS verification for invalid bundle %q", badPath)
		}
	}
}
