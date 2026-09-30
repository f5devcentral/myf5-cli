package client

import (
	"crypto/tls"
	"net"
	"net/http"
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
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}
}

// NewSecureHTTPClient returns an *http.Client configured with modern TLS and transport settings.
func NewSecureHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: NewProductionTransport(SecureTLSConfig(false)),
		Timeout:   timeout,
	}
}

// NewInsecureHTTPClient returns an *http.Client with InsecureSkipVerify for self-signed BIG-IP lab devices.
func NewInsecureHTTPClient(timeout time.Duration) *http.Client {
	// #nosec G402 -- InsecureSkipVerify is purposefully allowed for lab BIG-IPs with self-signed certs
	tlsCfg := SecureTLSConfig(true)
	return &http.Client{
		Transport: NewProductionTransport(tlsCfg),
		Timeout:   timeout,
	}
}
