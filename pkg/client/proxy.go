package client

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	globalProxyMu  sync.RWMutex
	globalProxyURL *url.URL
)

// SetGlobalProxy sets the global proxy URL for all HTTP clients and dialers.
func SetGlobalProxy(proxyStr string) error {
	globalProxyMu.Lock()
	defer globalProxyMu.Unlock()

	trimmed := strings.TrimSpace(proxyStr)
	if trimmed == "" {
		globalProxyURL = nil
		return nil
	}

	u, err := ParseProxyURL(trimmed)
	if err != nil {
		return err
	}
	globalProxyURL = u
	return nil
}

// GetGlobalProxy returns the currently configured global proxy URL, or nil if none is set.
func GetGlobalProxy() *url.URL {
	globalProxyMu.RLock()
	defer globalProxyMu.RUnlock()
	return globalProxyURL
}

// ClearGlobalProxy clears any configured global proxy URL.
func ClearGlobalProxy() {
	globalProxyMu.Lock()
	defer globalProxyMu.Unlock()
	globalProxyURL = nil
}

// ParseProxyURL parses and validates a proxy URL string (e.g. http://, https://, socks5://).
// If no scheme is provided, http:// is assumed.
func ParseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty proxy URL")
	}

	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL %q: %w", raw, err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "socks5", "socks5h":
		// Supported schemes
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (supported: http, https, socks5, socks5h)", u.Scheme)
	}

	if u.Host == "" {
		return nil, fmt.Errorf("proxy URL %q is missing host", raw)
	}

	return u, nil
}

// RedactProxyURL returns a sanitized string representation of a proxy URL
// with any cleartext password replaced by [REDACTED].
func RedactProxyURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "[MALFORMED_URL]"
	}
	return u.Redacted()
}

// ResolveEnvProxy returns a proxy URL from environment variables, checking in order:
// 1. MYF5_PROXY
// 2. F5_PROXY
// 3. ALL_PROXY / all_proxy
// 4. HTTPS_PROXY / https_proxy
// 5. HTTP_PROXY / http_proxy
func ResolveEnvProxy() *url.URL {
	for _, envKey := range []string{"MYF5_PROXY", "F5_PROXY", "ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if val := strings.TrimSpace(os.Getenv(envKey)); val != "" {
			if u, err := ParseProxyURL(val); err == nil {
				return u
			}
		}
	}
	return nil
}

// ProxyFunc returns an http.Transport.Proxy function honoring:
// 1. Explicit proxy URL (if provided)
// 2. Global proxy URL (via SetGlobalProxy)
// 3. Environment variables (MYF5_PROXY, F5_PROXY, ALL_PROXY, HTTPS_PROXY, HTTP_PROXY, NO_PROXY)
func ProxyFunc(explicitProxy *url.URL) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if explicitProxy != nil {
			return checkNoProxy(explicitProxy, req.URL)
		}
		if gp := GetGlobalProxy(); gp != nil {
			return checkNoProxy(gp, req.URL)
		}
		// Check application-specific environment overrides before standard Go proxy logic
		for _, envKey := range []string{"MYF5_PROXY", "F5_PROXY"} {
			if val := strings.TrimSpace(os.Getenv(envKey)); val != "" {
				if u, err := ParseProxyURL(val); err == nil {
					return checkNoProxy(u, req.URL)
				}
			}
		}
		// Fallback to standard Go http.ProxyFromEnvironment (which respects NO_PROXY)
		return http.ProxyFromEnvironment(req)
	}
}

func checkNoProxy(proxyURL *url.URL, targetURL *url.URL) (*url.URL, error) {
	if targetURL == nil {
		return proxyURL, nil
	}
	noProxy := getNoProxyEnv()
	if ShouldBypassProxy(targetURL.Host, noProxy) {
		return nil, nil
	}
	return proxyURL, nil
}

func getNoProxyEnv() string {
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// ShouldBypassProxy determines whether the target host matches NO_PROXY rules.
// Supports comma-separated entries with wildcards (*), domain suffixes (.example.com),
// IP addresses, and CIDR blocks (192.168.0.0/16, 10.0.0.0/8).
func ShouldBypassProxy(targetHost, noProxy string) bool {
	noProxy = strings.TrimSpace(noProxy)
	if noProxy == "" {
		return false
	}
	if noProxy == "*" {
		return true
	}

	host, _, err := net.SplitHostPort(targetHost)
	if err != nil {
		host = targetHost
	}
	host = strings.Trim(host, "[]")
	targetIP := net.ParseIP(host)

	for _, token := range strings.Split(noProxy, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if token == "*" {
			return true
		}

		// CIDR check (e.g. 192.168.0.0/16, 10.0.0.0/8)
		if _, ipNet, err := net.ParseCIDR(token); err == nil && targetIP != nil {
			if ipNet.Contains(targetIP) {
				return true
			}
			continue
		}

		// Strip port if present in token
		tokenHost, _, err := net.SplitHostPort(token)
		if err != nil {
			tokenHost = token
		}
		tokenHost = strings.Trim(tokenHost, "[]")

		// Exact match
		if strings.EqualFold(host, tokenHost) {
			return true
		}

		// Domain suffix match (e.g. .corp.local or corp.local)
		tokenDomain := strings.TrimPrefix(tokenHost, ".")
		if strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(tokenDomain)) {
			return true
		}
	}
	return false
}

type bufferedConn struct {
	net.Conn
	br *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) {
	if c.br != nil {
		if c.br.Buffered() > 0 {
			return c.br.Read(b)
		}
		c.br = nil
	}
	return c.Conn.Read(b)
}

// DialProxy establishes a TCP connection to targetAddr, tunneling through an HTTP CONNECT
// or SOCKS5 proxy if one is configured and not bypassed by NO_PROXY.
// If explicitProxy is nil, the global proxy and environment proxies are consulted.
func DialProxy(ctx context.Context, network, targetAddr string, explicitProxy *url.URL) (net.Conn, error) {
	proxyURL := explicitProxy
	if proxyURL == nil {
		proxyURL = GetGlobalProxy()
	}
	if proxyURL == nil {
		proxyURL = ResolveEnvProxy()
	}

	// If no proxy is configured, or targetAddr is bypassed via NO_PROXY, dial directly
	if proxyURL == nil || ShouldBypassProxy(targetAddr, getNoProxyEnv()) {
		var d net.Dialer
		return d.DialContext(ctx, network, targetAddr)
	}

	scheme := strings.ToLower(proxyURL.Scheme)
	switch scheme {
	case "http", "https":
		return dialHTTPConnect(ctx, proxyURL, targetAddr)
	case "socks5", "socks5h":
		return dialSOCKS5(ctx, proxyURL, targetAddr)
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", proxyURL.Scheme)
	}
}

// dialHTTPConnect establishes an HTTP CONNECT tunnel through an HTTP/HTTPS proxy.
func dialHTTPConnect(ctx context.Context, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	proxyHost := proxyURL.Host
	if !strings.Contains(proxyHost, ":") {
		if strings.ToLower(proxyURL.Scheme) == "https" {
			proxyHost += ":443"
		} else {
			proxyHost += ":80"
		}
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxyHost)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to proxy %s: %w", proxyHost, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	// Prepare HTTP CONNECT request
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: myf5-cli-tunnel/1.0\r\n", targetAddr, targetAddr)
	if proxyURL.User != nil {
		user := proxyURL.User.Username()
		pass, _ := proxyURL.User.Password()
		auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", auth)
	}
	connectReq += "\r\n"

	if _, err := io.WriteString(conn, connectReq); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed writing CONNECT request to proxy: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed reading proxy response from %s: %w", proxyHost, err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		_ = conn.SetDeadline(time.Time{})
		return &bufferedConn{Conn: conn, br: br}, nil
	}

	conn.Close()
	if resp.StatusCode == http.StatusProxyAuthRequired {
		return nil, fmt.Errorf("proxy authentication required by %s (HTTP 407)", proxyHost)
	}
	return nil, fmt.Errorf("proxy CONNECT to %s failed: %s", targetAddr, resp.Status)
}

// dialSOCKS5 establishes a SOCKS5 tunnel (RFC 1928 / RFC 1929) to targetAddr.
func dialSOCKS5(ctx context.Context, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	proxyHost := proxyURL.Host
	if !strings.Contains(proxyHost, ":") {
		proxyHost += ":1080"
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxyHost)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to SOCKS5 proxy %s: %w", proxyHost, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	// 1. Initial greeting: negotiate authentication method
	var methods []byte
	methods = append(methods, 0x00) // NO AUTH
	if proxyURL.User != nil {
		methods = append(methods, 0x02) // USERNAME/PASSWORD
	}

	greeting := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed writing SOCKS5 greeting: %w", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed reading SOCKS5 greeting reply: %w", err)
	}

	if reply[0] != 0x05 {
		conn.Close()
		return nil, fmt.Errorf("invalid SOCKS version %d received from proxy", reply[0])
	}

	chosenMethod := reply[1]
	if chosenMethod == 0xFF {
		conn.Close()
		return nil, errors.New("SOCKS5 proxy rejected authentication methods")
	}

	// 2. Handle username/password authentication (RFC 1929) if required
	if chosenMethod == 0x02 {
		if proxyURL.User == nil {
			conn.Close()
			return nil, errors.New("SOCKS5 proxy requested authentication but no credentials were provided")
		}
		user := proxyURL.User.Username()
		pass, _ := proxyURL.User.Password()

		authReq := []byte{0x01, byte(len(user))}
		authReq = append(authReq, []byte(user)...)
		authReq = append(authReq, byte(len(pass)))
		authReq = append(authReq, []byte(pass)...)

		if _, err := conn.Write(authReq); err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed writing SOCKS5 auth request: %w", err)
		}

		authReply := make([]byte, 2)
		if _, err := io.ReadFull(conn, authReply); err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed reading SOCKS5 auth reply: %w", err)
		}
		if authReply[1] != 0x00 {
			conn.Close()
			return nil, fmt.Errorf("SOCKS5 authentication failed with proxy %s (status 0x%02x)", proxyHost, authReply[1])
		}
	} else if chosenMethod != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("unsupported SOCKS5 authentication method: 0x%02x", chosenMethod)
	}

	// 3. Connect request: [VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT]
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("invalid target address %q: %w", targetAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		conn.Close()
		return nil, fmt.Errorf("invalid port in target address %q: %w", targetAddr, err)
	}

	req := []byte{0x05, 0x01, 0x00} // VER=5, CMD=1 (CONNECT), RSV=0
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		req = append(req, 0x01) // ATYP=1 (IPv4)
		req = append(req, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil {
		req = append(req, 0x04) // ATYP=4 (IPv6)
		req = append(req, ip6...)
	} else {
		req = append(req, 0x03, byte(len(host))) // ATYP=3 (Domain name)
		req = append(req, []byte(host)...)
	}
	req = append(req, byte(port>>8), byte(port&0xFF))

	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed sending SOCKS5 connect request: %w", err)
	}

	// 4. Connect reply
	respHeader := make([]byte, 4)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed reading SOCKS5 connect reply header: %w", err)
	}

	if respHeader[1] != 0x00 {
		conn.Close()
		var msg string
		switch respHeader[1] {
		case 0x01:
			msg = "general SOCKS server failure"
		case 0x02:
			msg = "connection not allowed by ruleset"
		case 0x03:
			msg = "network unreachable"
		case 0x04:
			msg = "host unreachable"
		case 0x05:
			msg = "connection refused"
		case 0x06:
			msg = "TTL expired"
		case 0x07:
			msg = "command not supported"
		case 0x08:
			msg = "address type not supported"
		default:
			msg = fmt.Sprintf("error code 0x%02x", respHeader[1])
		}
		return nil, fmt.Errorf("SOCKS5 connection to %s failed: %s", targetAddr, msg)
	}

	// Consume bound address and port
	switch respHeader[3] {
	case 0x01: // IPv4 (4 bytes) + port (2 bytes)
		discard := make([]byte, 6)
		_, _ = io.ReadFull(conn, discard)
	case 0x04: // IPv6 (16 bytes) + port (2 bytes)
		discard := make([]byte, 18)
		_, _ = io.ReadFull(conn, discard)
	case 0x03: // Domain length (1 byte) + domain + port (2 bytes)
		lenBuf := make([]byte, 1)
		_, _ = io.ReadFull(conn, lenBuf)
		discard := make([]byte, int(lenBuf[0])+2)
		_, _ = io.ReadFull(conn, discard)
	}

	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
