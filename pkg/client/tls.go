package client

import (
	"crypto/tls"
	"net/http"
	"time"
)

// SecureTLSConfig returns a tls.Config enforcing TLS 1.2+ with modern PFS/AEAD ciphers.
func SecureTLSConfig(insecureSkipVerify bool) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecureSkipVerify,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
	}
}

// NewSecureHTTPClient returns an *http.Client configured with modern TLS and transport settings.
func NewSecureHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		TLSClientConfig:     SecureTLSConfig(false),
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

// NewInsecureHTTPClient returns an *http.Client with InsecureSkipVerify for self-signed BIG-IP lab devices.
func NewInsecureHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		TLSClientConfig:     SecureTLSConfig(true),
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}
