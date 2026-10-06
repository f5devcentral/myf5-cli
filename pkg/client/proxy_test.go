package client

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"
)

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
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start backend listener: %v", err)
	}
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
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start proxy listener: %v", err)
	}
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
			targetConn, err := net.Dial("tcp", req.Host)
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
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start backend listener: %v", err)
	}
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
	socksListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start socks5 listener: %v", err)
	}
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

		targetConn, err := net.Dial("tcp", target)
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

func TestHTTPTransportHonorsProxy(t *testing.T) {
	proxyHit := false
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHit = true
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxied-response"))
	}))
	defer proxyServer.Close()

	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatalf("failed to parse proxy URL: %v", err)
	}

	transport := NewProductionTransportWithProxy(SecureTLSConfig(true), proxyURL)
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
