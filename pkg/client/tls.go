package client

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// SecureTLSConfig returns a tls.Config enforcing TLS 1.2+ with modern PFS/AEAD ciphers.
func SecureTLSConfig(insecureSkipVerify bool) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecureSkipVerify, // #nosec G402 -- isolated to lab appliances when explicitly set
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
			tls.CurveP384,
		},
	}
}

// NewProductionTransport returns a hardened http.Transport with explicit phase timeouts.
func NewProductionTransport(tlsConfig *tls.Config) *http.Transport {
	return &http.Transport{
		// Restrict TCP connect and DNS dial phases to prevent slowloris hanging
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig: tlsConfig,
		// Limit TLS negotiation phase duration
		TLSHandshakeTimeout: 10 * time.Second,
		// Bound time spent waiting for server response headers after request is written
		ResponseHeaderTimeout: 30 * time.Second,
		// Timeout waiting for 100-continue before streaming request body
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}
}

// SecureTLSConfigWithCerts returns a tls.Config enforcing TLS 1.2+ with modern PFS/AEAD ciphers
// and optional client certificates for mTLS authentication.
func SecureTLSConfigWithCerts(insecureSkipVerify bool, certs ...tls.Certificate) *tls.Config {
	cfg := SecureTLSConfig(insecureSkipVerify)
	if len(certs) > 0 {
		cfg.Certificates = certs
	}
	return cfg
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// LoadClientCertificate loads a TLS client certificate and private key from file paths.
// If keyFile is empty, it attempts to load both the certificate and private key from certFile.
func LoadClientCertificate(certFile, keyFile string) (*tls.Certificate, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" && keyFile != "" {
		return nil, fmt.Errorf("client certificate file is required when private key is provided")
	}

	certBytes, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read client certificate file %s: %w", certFile, err)
	}

	var keyBytes []byte
	if keyFile != "" && keyFile != certFile {
		kb, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read client private key file %s: %w", keyFile, err)
		}
		keyBytes = kb
	} else {
		keyBytes = certBytes
	}
	defer func() {
		if keyBytes != nil && keyFile != certFile {
			zeroBytes(keyBytes)
		}
	}()

	cert, err := tls.X509KeyPair(certBytes, keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse client certificate and key: %w", err)
	}
	return &cert, nil
}

// NewSecureHTTPClient returns an *http.Client configured with modern TLS and transport settings.
func NewSecureHTTPClient(timeout time.Duration) *http.Client {
	return NewSecureHTTPClientWithCert(timeout, nil)
}

// NewSecureHTTPClientWithCert returns an *http.Client configured with client certificate auth.
func NewSecureHTTPClientWithCert(timeout time.Duration, cert *tls.Certificate) *http.Client {
	var certs []tls.Certificate
	if cert != nil {
		certs = []tls.Certificate{*cert}
	}
	return &http.Client{
		Transport: NewProductionTransport(SecureTLSConfigWithCerts(false, certs...)),
		Timeout:   timeout,
	}
}

// NewInsecureHTTPClient returns an *http.Client with InsecureSkipVerify for self-signed BIG-IP lab devices.
func NewInsecureHTTPClient(timeout time.Duration) *http.Client {
	return NewInsecureHTTPClientWithCert(timeout, nil)
}

// NewInsecureHTTPClientWithCert returns an *http.Client with InsecureSkipVerify and client cert auth.
func NewInsecureHTTPClientWithCert(timeout time.Duration, cert *tls.Certificate) *http.Client {
	var certs []tls.Certificate
	if cert != nil {
		certs = []tls.Certificate{*cert}
	}
	// #nosec G402 -- InsecureSkipVerify is purposefully allowed for lab BIG-IPs with self-signed certs
	return &http.Client{
		Transport: NewProductionTransport(SecureTLSConfigWithCerts(true, certs...)),
		Timeout:   timeout,
	}
}
