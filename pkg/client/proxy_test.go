package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	memListenersMu sync.Mutex
	memListeners   = make(map[string]*inMemoryListener)
	portSeq        = 10000
)

type inMemoryListener struct {
	addr  string
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newMemListener() *inMemoryListener {
	memListenersMu.Lock()
	portSeq++
	addr := fmt.Sprintf("127.0.0.1:%d", portSeq)
	l := &inMemoryListener{
		addr:  addr,
		conns: make(chan net.Conn),
		done:  make(chan struct{}),
	}
	memListeners[addr] = l
	memListenersMu.Unlock()
	return l
}

func (l *inMemoryListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *inMemoryListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		memListenersMu.Lock()
		delete(memListeners, l.addr)
		memListenersMu.Unlock()
	})
	return nil
}

func (l *inMemoryListener) Addr() net.Addr {
	return &testAddr{addr: l.addr}
}

type testAddr struct {
	addr string
}

func (a *testAddr) Network() string { return "tcp" }
func (a *testAddr) String() string  { return a.addr }

func memDial(ctx context.Context, network, addr string) (net.Conn, error) {
	memListenersMu.Lock()
	l, ok := memListeners[addr]
	memListenersMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("connect: operation not permitted (mock %s)", addr)
	}
	c1, c2 := net.Pipe()
	select {
	case l.conns <- c2:
		return c1, nil
	case <-ctx.Done():
		c1.Close()
		c2.Close()
		return nil, ctx.Err()
	case <-l.done:
		c1.Close()
		c2.Close()
		return nil, net.ErrClosed
	}
}

func init() {
	proxyDialContext = memDial
}

func TestParseProxyURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantURL string
		wantErr bool
	}{
		{
			name:    "http explicit",
			input:   "http://proxy.corp.example.com:8080",
			wantURL: "http://proxy.corp.example.com:8080",
			wantErr: false,
		},
		{
			name:    "http implicit scheme",
			input:   "proxy.corp.example.com:8080",
			wantURL: "http://proxy.corp.example.com:8080",
			wantErr: false,
		},
		{
			name:    "https scheme",
			input:   "https://secure-proxy.corp.example.com:8443",
			wantURL: "https://secure-proxy.corp.example.com:8443",
			wantErr: false,
		},
		{
			name:    "socks5 scheme",
			input:   "socks5://127.0.0.1:1080",
			wantURL: "socks5://127.0.0.1:1080",
			wantErr: false,
		},
		{
			name:    "socks5h scheme",
			input:   "socks5h://127.0.0.1:1080",
			wantURL: "socks5h://127.0.0.1:1080",
			wantErr: false,
		},
		{
			name:    "proxy with auth",
			input:   "http://user:secret@proxy.corp.example.com:8080",
			wantURL: "http://user:secret@proxy.corp.example.com:8080",
			wantErr: false,
		},
		{
			name:    "unsupported ftp scheme",
			input:   "ftp://proxy.corp.example.com:21",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProxyURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseProxyURL(%q) err = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.wantURL {
				t.Errorf("ParseProxyURL(%q) = %q, want %q", tt.input, got.String(), tt.wantURL)
			}
		})
	}
}

func TestRedactProxyURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "http://admin:supersecret@proxy.example.com:8080",
			want:  "http://admin:xxxxx@proxy.example.com:8080",
		},
		{
			input: "socks5://proxy.example.com:1080",
			want:  "socks5://proxy.example.com:1080",
		},
		{
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		got := RedactProxyURL(tt.input)
		if got != tt.want {
			t.Errorf("RedactProxyURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestShouldBypassProxy(t *testing.T) {
	tests := []struct {
		targetHost string
		noProxy    string
		wantBypass bool
	}{
		{"localhost", "localhost", true},
		{"localhost:443", "localhost", true},
		{"127.0.0.1", "127.0.0.1", true},
		{"127.0.0.1:8443", "127.0.0.1", true},
		{"10.220.10.96", "10.0.0.0/8", true},
		{"192.168.1.50", "192.168.0.0/16,10.0.0.0/8", true},
		{"internal.corp.com", ".corp.com", true},
		{"internal.corp.com", "corp.com", true},
		{"api.f5.com", "corp.com,.internal", false},
		{"54.243.25.230", "10.0.0.0/8,192.168.0.0/16", false},
		{"anything.com", "*", true},
		{"api.f5.com", "", false},

		// IPv6 test cases
		{"::1", "::1", true},
		{"[::1]:8443", "::1", true},
		{"[::1]:443", "0:0:0:0:0:0:0:1", true},
		{"[2001:db8::1]:443", "2001:db8::/32", true},
		{"[2001:db9::1]:443", "2001:db8::/32", false},
		{"[fe80::1%eth0]:8443", "fe80::/10", true},
		{"[fe80::1%eth0]:8443", "fe80::1", true},
		{"2001:db8::1", "[2001:db8::1]", true},
		{"[2001:db8::1]:8443", "[2001:db8::1]:8443", true},
		{"[2001:db8::1]:8443", "corp.local", false},
	}

	for _, tt := range tests {
		got := ShouldBypassProxy(tt.targetHost, tt.noProxy)
		if got != tt.wantBypass {
			t.Errorf("ShouldBypassProxy(%q, %q) = %v, want %v", tt.targetHost, tt.noProxy, got, tt.wantBypass)
		}
	}
}

func TestHTTPConnectProxyTunnel(t *testing.T) {
	// Start target backend echo server
	backendReceived := make(chan string, 1)
	backendListener := newMemListener()
	defer backendListener.Close()

	go func() {
		conn, err := backendListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		backendReceived <- string(buf[:n])
		_, _ = conn.Write([]byte("PONG"))
	}()

	backendAddr := backendListener.Addr().String()

	// Start mock HTTP CONNECT proxy
	connectHostReceived := make(chan string, 1)
	proxyListener := newMemListener()
	defer proxyListener.Close()

	go func() {
		clientConn, err := proxyListener.Accept()
		if err != nil {
			return
		}
		defer clientConn.Close()

		req, err := http.ReadRequest(bufioReader(clientConn))
		if err != nil {
			return
		}

		if req.Method == http.MethodConnect {
			connectHostReceived <- req.Host
			// Dial target
			targetConn, err := memDial(context.Background(), "tcp", req.Host)
			if err != nil {
				_, _ = clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
				return
			}
			defer targetConn.Close()

			_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

			// Bridge client and target
			go func() {
				_, _ = io.Copy(targetConn, clientConn)
			}()
			_, _ = io.Copy(clientConn, targetConn)
		}
	}()

	proxyURL, _ := ParseProxyURL("http://" + proxyListener.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := DialProxy(ctx, "tcp", backendAddr, proxyURL)
	if err != nil {
		t.Fatalf("DialProxy failed: %v", err)
	}
	defer conn.Close()

	// Verify proxy handled the CONNECT request
	select {
	case host := <-connectHostReceived:
		if host != backendAddr {
			t.Errorf("Proxy received CONNECT for %s, want %s", host, backendAddr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for proxy CONNECT")
	}

	// Send data through tunnel
	if _, err := conn.Write([]byte("PING")); err != nil {
		t.Fatalf("Failed to write to proxied conn: %v", err)
	}

	select {
	case msg := <-backendReceived:
		if msg != "PING" {
			t.Errorf("Backend received %q, want PING", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for backend to receive ping")
	}

	replyBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, replyBuf); err != nil {
		t.Fatalf("Failed to read reply: %v", err)
	}
	if string(replyBuf) != "PONG" {
		t.Errorf("Expected PONG, got %s", string(replyBuf))
	}
}

func TestSOCKS5ProxyTunnel(t *testing.T) {
	// Start target backend echo server
	backendListener := newMemListener()
	defer backendListener.Close()

	go func() {
		conn, err := backendListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		_, _ = conn.Write(buf[:n])
	}()

	backendAddr := backendListener.Addr().String()

	// Start mock SOCKS5 server
	socksListener := newMemListener()
	defer socksListener.Close()

	go func() {
		clientConn, err := socksListener.Accept()
		if err != nil {
			return
		}
		defer clientConn.Close()

		// Read greeting: [VER, NMETHODS, METHODS...]
		greet := make([]byte, 3)
		if _, err := io.ReadFull(clientConn, greet); err != nil {
			return
		}
		// Reply NO AUTH: [VER=5, METHOD=0]
		if _, err := clientConn.Write([]byte{0x05, 0x00}); err != nil {
			return
		}

		// Read request: [VER=5, CMD=1, RSV=0, ATYP=1, IP4, PORT]
		req := make([]byte, 4)
		if _, err := io.ReadFull(clientConn, req); err != nil {
			return
		}
		if req[3] != 0x01 { // IPv4
			return
		}
		addrPort := make([]byte, 6)
		if _, err := io.ReadFull(clientConn, addrPort); err != nil {
			return
		}

		ip := net.IPv4(addrPort[0], addrPort[1], addrPort[2], addrPort[3])
		port := int(addrPort[4])<<8 | int(addrPort[5])
		target := net.JoinHostPort(ip.String(), strconv.Itoa(port))

		targetConn, err := memDial(context.Background(), "tcp", target)
		if err != nil {
			_, _ = clientConn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return
		}
		defer targetConn.Close()

		// Success reply
		_, _ = clientConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})

		go func() {
			_, _ = io.Copy(targetConn, clientConn)
		}()
		_, _ = io.Copy(clientConn, targetConn)
	}()

	proxyURL, _ := ParseProxyURL("socks5://" + socksListener.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := DialProxy(ctx, "tcp", backendAddr, proxyURL)
	if err != nil {
		t.Fatalf("SOCKS5 DialProxy failed: %v", err)
	}
	defer conn.Close()

	testMsg := "HELLO_SOCKS5"
	if _, err := conn.Write([]byte(testMsg)); err != nil {
		t.Fatalf("Failed to write to socks conn: %v", err)
	}

	replyBuf := make([]byte, len(testMsg))
	if _, err := io.ReadFull(conn, replyBuf); err != nil {
		t.Fatalf("Failed to read echo reply: %v", err)
	}
	if string(replyBuf) != testMsg {
		t.Errorf("Expected %s, got %s", testMsg, string(replyBuf))
	}
}

func TestSOCKS5ProxyTunnel_IPv6Target(t *testing.T) {
	// Start mock SOCKS5 server that verifies ATYP=4 (IPv6) request serialization
	socksListener := newMemListener()
	defer socksListener.Close()

	reqReceived := make(chan []byte, 1)

	go func() {
		clientConn, err := socksListener.Accept()
		if err != nil {
			return
		}
		defer clientConn.Close()

		// Read greeting
		greet := make([]byte, 3)
		if _, err := io.ReadFull(clientConn, greet); err != nil {
			return
		}
		// Reply NO AUTH
		if _, err := clientConn.Write([]byte{0x05, 0x00}); err != nil {
			return
		}

		// Read request header: [VER=5, CMD=1, RSV=0, ATYP=4]
		reqHeader := make([]byte, 4)
		if _, err := io.ReadFull(clientConn, reqHeader); err != nil {
			return
		}

		// Read 16 bytes IPv6 + 2 bytes port
		addrPort := make([]byte, 18)
		if _, err := io.ReadFull(clientConn, addrPort); err != nil {
			return
		}

		reqReceived <- append(reqHeader, addrPort...)

		// Reply success: [VER=5, REP=0, RSV=0, ATYP=1, BND.ADDR=0.0.0.0, BND.PORT=0]
		_, _ = clientConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})

		// Echo loop
		buf := make([]byte, 1024)
		n, _ := clientConn.Read(buf)
		_, _ = clientConn.Write(buf[:n])
	}()

	proxyURL, _ := ParseProxyURL("socks5://" + socksListener.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Dial an IPv6 target address with port and brackets
	targetAddr := "[2001:db8::1]:8443"
	conn, err := DialProxy(ctx, "tcp", targetAddr, proxyURL)
	if err != nil {
		t.Fatalf("SOCKS5 IPv6 DialProxy failed: %v", err)
	}
	defer conn.Close()

	select {
	case req := <-reqReceived:
		if len(req) != 22 {
			t.Fatalf("expected 22 bytes in SOCKS5 IPv6 request, got %d", len(req))
		}
		if req[3] != 0x04 {
			t.Errorf("expected ATYP=0x04 (IPv6), got 0x%02x", req[3])
		}
		parsedIP := net.IP(req[4:20])
		expectedIP := net.ParseIP("2001:db8::1")
		if !parsedIP.Equal(expectedIP) {
			t.Errorf("expected IPv6 %v, got %v", expectedIP, parsedIP)
		}
		port := int(req[20])<<8 | int(req[21])
		if port != 8443 {
			t.Errorf("expected port 8443, got %d", port)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SOCKS5 IPv6 request")
	}

	testMsg := "IPV6_ECHO"
	if _, err := conn.Write([]byte(testMsg)); err != nil {
		t.Fatalf("failed to write through socks tunnel: %v", err)
	}
	reply := make([]byte, len(testMsg))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("failed to read reply: %v", err)
	}
	if string(reply) != testMsg {
		t.Errorf("got %q, want %q", string(reply), testMsg)
	}
}

func TestHTTPTransportHonorsProxy(t *testing.T) {
	proxyHit := false
	proxyServerListener := newMemListener()
	proxyServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proxyHit = true
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("proxied-response"))
		}),
	}
	go func() {
		_ = proxyServer.Serve(proxyServerListener)
	}()
	defer proxyServer.Close()

	proxyURL, err := url.Parse("http://" + proxyServerListener.Addr().String())
	if err != nil {
		t.Fatalf("failed to parse proxy URL: %v", err)
	}

	transport := NewProductionTransportWithProxy(SecureTLSConfig(true), proxyURL)
	transport.DialContext = memDial
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	req, err := http.NewRequest(http.MethodGet, "http://target.example.invalid/test", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if !proxyHit {
		t.Error("expected request to pass through the configured proxy")
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "proxied-response" {
		t.Errorf("expected body 'proxied-response', got %s", string(body))
	}
}

func TestGlobalProxy(t *testing.T) {
	defer ClearGlobalProxy()

	if err := SetGlobalProxy("http://corp.proxy:8080"); err != nil {
		t.Fatalf("SetGlobalProxy failed: %v", err)
	}
	gp := GetGlobalProxy()
	if gp == nil || gp.String() != "http://corp.proxy:8080" {
		t.Fatalf("GetGlobalProxy() = %v, want http://corp.proxy:8080", gp)
	}

	ClearGlobalProxy()
	if gp := GetGlobalProxy(); gp != nil {
		t.Fatalf("expected nil after ClearGlobalProxy(), got %v", gp)
	}
}

func TestResolveEnvProxy(t *testing.T) {
	os.Setenv("MYF5_PROXY", "http://env-proxy:9000")
	defer os.Unsetenv("MYF5_PROXY")

	u := ResolveEnvProxy()
	if u == nil || u.String() != "http://env-proxy:9000" {
		t.Fatalf("ResolveEnvProxy() = %v, want http://env-proxy:9000", u)
	}
}

func bufioReader(conn net.Conn) *bufio.Reader {
	return bufio.NewReader(conn)
}

func TestProxyFuncAndCheckNoProxy(t *testing.T) {
	os.Setenv("NO_PROXY", "example.com,.internal")
	defer os.Unsetenv("NO_PROXY")

	explicit, _ := url.Parse("http://explicit-proxy:8080")
	fn := ProxyFunc(explicit)

	req1, _ := http.NewRequest(http.MethodGet, "https://api.f5.com/path", nil)
	u, err := fn(req1)
	if err != nil || u == nil || u.String() != "http://explicit-proxy:8080" {
		t.Errorf("expected explicit proxy, got %v (err: %v)", u, err)
	}

	req2, _ := http.NewRequest(http.MethodGet, "https://example.com/path", nil)
	u2, err := fn(req2)
	if err != nil || u2 != nil {
		t.Errorf("expected nil proxy for NO_PROXY target, got %v", u2)
	}

	uNilTarget, err := checkNoProxy(explicit, nil)
	if err != nil || uNilTarget != explicit {
		t.Errorf("checkNoProxy with nil target should return proxyURL")
	}

	fnGlobal := ProxyFunc(nil)
	SetGlobalProxy("http://global-proxy:8080")
	defer ClearGlobalProxy()

	uGlobal, err := fnGlobal(req1)
	if err != nil || uGlobal == nil || uGlobal.String() != "http://global-proxy:8080" {
		t.Errorf("expected global proxy, got %v", uGlobal)
	}
	ClearGlobalProxy()

	os.Setenv("MYF5_PROXY", "http://env-myf5-proxy:8080")
	defer os.Unsetenv("MYF5_PROXY")
	uEnv, err := fnGlobal(req1)
	if err != nil || uEnv == nil || uEnv.String() != "http://env-myf5-proxy:8080" {
		t.Errorf("expected MYF5_PROXY, got %v", uEnv)
	}
	os.Unsetenv("MYF5_PROXY")

	os.Setenv("F5_PROXY", "http://env-f5-proxy:8080")
	defer os.Unsetenv("F5_PROXY")
	uF5Env, err := fnGlobal(req1)
	if err != nil || uF5Env == nil || uF5Env.String() != "http://env-f5-proxy:8080" {
		t.Errorf("expected F5_PROXY, got %v", uF5Env)
	}
}

func TestDialProxyBranches(t *testing.T) {
	ClearGlobalProxy()
	for _, envKey := range []string{"MYF5_PROXY", "F5_PROXY", "ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		oldVal := os.Getenv(envKey)
		defer os.Setenv(envKey, oldVal)
		os.Unsetenv(envKey)
	}

	ctx := context.Background()

	listener := newMemListener()
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	conn, err := DialProxy(ctx, "tcp", listener.Addr().String(), nil)
	if err != nil {
		t.Fatalf("DialProxy direct dial failed: %v", err)
	}
	conn.Close()

	badScheme, _ := url.Parse("ftp://proxy.example.com:21")
	_, err = DialProxy(ctx, "tcp", "example.com:80", badScheme)
	if err == nil || !strings.Contains(err.Error(), "unsupported proxy protocol") {
		t.Errorf("expected unsupported proxy protocol error, got %v", err)
	}

	os.Setenv("NO_PROXY", "127.0.0.1")
	dummyProxy, _ := url.Parse("http://192.0.2.1:8080")
	connBypass, err := DialProxy(ctx, "tcp", listener.Addr().String(), dummyProxy)
	if err != nil {
		t.Fatalf("DialProxy with NO_PROXY bypass failed: %v", err)
	}
	connBypass.Close()
}

func TestHTTPConnectProxyErrorsAndAuth(t *testing.T) {
	proxy407 := newMemListener()
	defer proxy407.Close()

	go func() {
		for {
			conn, err := proxy407.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				req, err := http.ReadRequest(r)
				if err != nil {
					return
				}
				if req.Header.Get("Proxy-Authorization") == "" {
					io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
				} else {
					io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
				}
			}(conn)
		}
	}()

	proxyURL407, _ := ParseProxyURL("http://" + proxy407.Addr().String())
	ctx := context.Background()

	_, err := DialProxy(ctx, "tcp", "target.corp:443", proxyURL407)
	if err == nil || !strings.Contains(err.Error(), "HTTP 407") {
		t.Errorf("expected HTTP 407 error, got %v", err)
	}

	proxyWithAuth, _ := ParseProxyURL("http://user:pass@" + proxy407.Addr().String())
	connAuth, err := DialProxy(ctx, "tcp", "target.corp:443", proxyWithAuth)
	if err != nil {
		t.Fatalf("expected proxy with auth to succeed, got %v", err)
	}
	connAuth.Close()

	proxy500 := newMemListener()
	defer proxy500.Close()
	go func() {
		conn, err := proxy500.Accept()
		if err == nil {
			defer conn.Close()
			r := bufio.NewReader(conn)
			_, _ = http.ReadRequest(r)
			io.WriteString(conn, "HTTP/1.1 500 Internal Server Error\r\n\r\n")
		}
	}()
	proxyURL500, _ := ParseProxyURL("http://" + proxy500.Addr().String())
	_, err = DialProxy(ctx, "tcp", "target.corp:443", proxyURL500)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 error from proxy, got %v", err)
	}
}

func TestSOCKS5ErrorsAndAuth(t *testing.T) {
	socksAuth := newMemListener()
	defer socksAuth.Close()

	go func() {
		for {
			conn, err := socksAuth.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				greet := make([]byte, 128)
				n, err := c.Read(greet)
				if err != nil || n < 3 {
					return
				}
				c.Write([]byte{0x05, 0x02})

				authBuf := make([]byte, 128)
				an, err := c.Read(authBuf)
				if err != nil || an < 2 {
					return
				}
				c.Write([]byte{0x01, 0x00})

				reqBuf := make([]byte, 128)
				rn, err := c.Read(reqBuf)
				if err != nil || rn < 4 {
					return
				}
				c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			}(conn)
		}
	}()

	proxyAuthURL, _ := ParseProxyURL("socks5://testuser:testpass@" + socksAuth.Addr().String())
	ctx := context.Background()

	conn, err := DialProxy(ctx, "tcp", "127.0.0.1:8080", proxyAuthURL)
	if err != nil {
		t.Fatalf("SOCKS5 auth connection failed: %v", err)
	}
	conn.Close()

	socksReject := newMemListener()
	defer socksReject.Close()
	go func() {
		c, err := socksReject.Accept()
		if err == nil {
			defer c.Close()
			buf := make([]byte, 128)
			c.Read(buf)
			c.Write([]byte{0x05, 0xFF})
		}
	}()
	proxyRejectURL, _ := ParseProxyURL("socks5://" + socksReject.Addr().String())
	_, err = DialProxy(ctx, "tcp", "127.0.0.1:8080", proxyRejectURL)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("expected SOCKS5 rejected auth error, got %v", err)
	}

	socksBadVer := newMemListener()
	defer socksBadVer.Close()
	go func() {
		c, err := socksBadVer.Accept()
		if err == nil {
			defer c.Close()
			buf := make([]byte, 128)
			c.Read(buf)
			c.Write([]byte{0x04, 0x00})
		}
	}()
	proxyBadVerURL, _ := ParseProxyURL("socks5://" + socksBadVer.Addr().String())
	_, err = DialProxy(ctx, "tcp", "127.0.0.1:8080", proxyBadVerURL)
	if err == nil || !strings.Contains(err.Error(), "invalid SOCKS version") {
		t.Errorf("expected invalid SOCKS version error, got %v", err)
	}
}

func TestSetDialContext(t *testing.T) {
	t.Cleanup(func() {
		SetDialContext(memDial)
	})
	customDialed := false
	customFn := func(ctx context.Context, network, addr string) (net.Conn, error) {
		customDialed = true
		c1, _ := net.Pipe()
		return c1, nil
	}

	SetDialContext(customFn)
	conn, err := proxyDialContext(context.Background(), "tcp", "127.0.0.1:9999")
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	conn.Close()
	if !customDialed {
		t.Errorf("expected custom dialer to be invoked")
	}

	// Reset to nil
	SetDialContext(nil)
	if proxyDialContext == nil {
		t.Errorf("expected non-nil default dialer after SetDialContext(nil)")
	}
}
