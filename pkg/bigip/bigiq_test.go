package bigip

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestBigIQTokenAuth_ProactiveWithoutLoginProviderName(t *testing.T) {
	var loginCalls int32
	var sawBasicAuth atomic.Bool
	var sawProviderField atomic.Bool

	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			sawBasicAuth.Store(true)
		}
		switch r.URL.Path {
		case "/mgmt/shared/authn/login":
			atomic.AddInt32(&loginCalls, 1)
			var raw map[string]any
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				t.Errorf("failed decoding login body: %v", err)
			}
			if _, hasProvider := raw["loginProviderName"]; hasProvider {
				sawProviderField.Store(true)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":401,"message":"Authentication failed."}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":{"token":"bigiq-tok-123"},"refreshToken":{"token":"bigiq-ref-123"}}`))
		case "/mgmt/tm/sys/ready":
			if r.Header.Get("X-F5-Auth-Token") != "bigiq-tok-123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case "/mgmt/shared/echo":
			if r.Header.Get("X-F5-Auth-Token") != "bigiq-tok-123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"stage":"STARTED"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c, err := NewClientWithOptions(ClientOptions{
		Host:       u.Hostname(),
		Port:       port,
		Username:   "admin",
		Password:   "SecretPass123",
		TargetType: "BIG-IQ",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClientWithOptions failed: %v", err)
	}
	c.Scheme = "http"
	defer c.Zero()

	kind, err := c.CheckReadyWithKind(context.Background())
	if err != nil {
		t.Fatalf("CheckReadyWithKind failed: %v", err)
	}
	if kind != "BIG-IQ" {
		t.Errorf("expected kind BIG-IQ, got %q", kind)
	}
	if sawBasicAuth.Load() {
		t.Errorf("expected BIG-IQ client not to send HTTP Basic Auth header")
	}
	if sawProviderField.Load() {
		t.Errorf("expected BIG-IQ login payload to omit loginProviderName")
	}
	if got := atomic.LoadInt32(&loginCalls); got != 1 {
		t.Errorf("expected exactly 1 login call, got %d", got)
	}
}

func TestBigIQTokenAuth_AutoFallbackWhenTargetUnset(t *testing.T) {
	var loginPayloads []map[string]any

	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mgmt/shared/authn/login":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			loginPayloads = append(loginPayloads, body)
			if _, hasProvider := body["loginProviderName"]; hasProvider {
				// Simulate BIG-IQ rejecting "loginProviderName": "tmos"
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":401,"message":"Auth provider tmos not found"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":{"token":"auto-bigiq-token"}}`))
		case "/mgmt/tm/sys/ready":
			if r.Header.Get("X-F5-Auth-Token") != "auto-bigiq-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case "/mgmt/shared/echo":
			if r.Header.Get("X-F5-Auth-Token") != "auto-bigiq-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c := NewClient(u.Hostname(), port, "admin", "SecretPass123")
	c.Scheme = "http"
	c.HTTPClient = server.Client()
	defer c.Zero()

	kind, err := c.CheckReadyWithKind(context.Background())
	if err != nil {
		t.Fatalf("CheckReadyWithKind auto-fallback failed: %v", err)
	}
	if kind != "BIG-IQ" {
		t.Errorf("expected BIG-IQ, got %q", kind)
	}
	if len(loginPayloads) != 2 {
		t.Fatalf("expected 2 login attempts (tmos then local omitted), got %d", len(loginPayloads))
	}
	if loginPayloads[0]["loginProviderName"] != "tmos" {
		t.Errorf("expected first attempt to try tmos, got %v", loginPayloads[0])
	}
	if _, exists := loginPayloads[1]["loginProviderName"]; exists {
		t.Errorf("expected second attempt to omit loginProviderName, got %v", loginPayloads[1])
	}
}

func TestBigIQTokenRefresh_ExchangeAndReLogin(t *testing.T) {
	var exchangeCalls int32
	var loginCalls int32
	failExchange := false

	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mgmt/shared/authn/login":
			n := atomic.AddInt32(&loginCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				_, _ = w.Write([]byte(`{"token":{"token":"expired-tok-1"},"refreshToken":{"token":"refresh-tok-1"}}`))
			} else {
				_, _ = w.Write([]byte(`{"token":{"token":"relogin-tok-3"},"refreshToken":{"token":"refresh-tok-3"}}`))
			}
		case "/mgmt/shared/authn/exchange":
			atomic.AddInt32(&exchangeCalls, 1)
			if failExchange {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":401,"message":"Refresh token expired"}`))
				return
			}
			var payload struct {
				RefreshToken struct {
					Token string `json:"token"`
				} `json:"refreshToken"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.RefreshToken.Token != "refresh-tok-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":{"token":"refreshed-tok-2"},"refreshToken":{"token":"refresh-tok-2"}}`))
		case "/mgmt/shared/echo":
			tok := r.Header.Get("X-F5-Auth-Token")
			// Simulate expired-tok-1 and refreshed-tok-2 (in phase 2) returning 401
			if tok == "expired-tok-1" || (failExchange && tok == "refreshed-tok-2") {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":401,"message":"Token expired"}`))
				return
			}
			if tok == "refreshed-tok-2" || tok == "relogin-tok-3" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"tokenUsed":"` + tok + `"}`))
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c, err := NewClientWithOptions(ClientOptions{
		Host:       u.Hostname(),
		Port:       port,
		Username:   "admin",
		Password:   "SecretPass123",
		TargetType: "BIG-IQ",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClientWithOptions failed: %v", err)
	}
	c.Scheme = "http"
	defer c.Zero()

	// Phase 1: Initial login returns expired-tok-1 -> echo returns 401 -> exchange refreshes to refreshed-tok-2 -> retry succeeds
	req1, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, c.BaseURL()+"/mgmt/shared/echo", nil)
	resp1, err := c.doWithAuth(req1)
	if err != nil {
		t.Fatalf("doWithAuth phase 1 failed: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK || !strings.Contains(string(body1), "refreshed-tok-2") {
		t.Fatalf("expected 200 with refreshed-tok-2, got %d (%s)", resp1.StatusCode, string(body1))
	}
	if atomic.LoadInt32(&exchangeCalls) != 1 {
		t.Errorf("expected 1 exchange call, got %d", exchangeCalls)
	}

	// Phase 2: Exchange itself fails (expired refresh token) -> falls back to full re-login -> retry succeeds
	failExchange = true
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, c.BaseURL()+"/mgmt/shared/echo", nil)
	resp2, err := c.doWithAuth(req2)
	if err != nil {
		t.Fatalf("doWithAuth phase 2 failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || !strings.Contains(string(body2), "relogin-tok-3") {
		t.Fatalf("expected 200 with relogin-tok-3, got %d (%s)", resp2.StatusCode, string(body2))
	}
	if atomic.LoadInt32(&loginCalls) != 2 {
		t.Errorf("expected 2 total login calls after re-login fallback, got %d", loginCalls)
	}
}

func TestBigIQLockoutProtection_SingleLoginAttempt(t *testing.T) {
	var loginAttempts int32
	var apiHits int32

	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mgmt/shared/authn/login" {
			atomic.AddInt32(&loginAttempts, 1)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":401,"message":"Authentication failed."}`))
			return
		}
		atomic.AddInt32(&apiHits, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c, err := NewClientWithOptions(ClientOptions{
		Host:       u.Hostname(),
		Port:       port,
		Username:   "admin",
		Password:   "WrongPassword123",
		TargetType: "BIG-IQ",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClientWithOptions failed: %v", err)
	}
	c.Scheme = "http"
	defer c.Zero()

	_, err = c.GetSystemInfo(context.Background())
	if err == nil {
		t.Fatal("expected GetSystemInfo to fail on 401 bad password")
	}
	if got := atomic.LoadInt32(&loginAttempts); got != 1 {
		t.Errorf("lockout protection violated: expected 1 login attempt, got %d", got)
	}
	if got := atomic.LoadInt32(&apiHits); got != 0 {
		t.Errorf("expected 0 unauthenticated API endpoint probes after BIG-IQ login 401, got %d", got)
	}
}

func TestCheckReadyWithKind_BIGIQPublicEndpointFallback(t *testing.T) {
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mgmt/shared/authn/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":{"token":"tok-ready"}}`))
		case "/mgmt/tm/sys/ready":
			// Simulate BIG-IQ webd HTML 403
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("<html><head><title>403 Forbidden</title></head><body><center><h1>403 Forbidden</h1></center><hr><center>webd</center></body></html>"))
		case "/mgmt/shared/echo":
			w.WriteHeader(http.StatusForbidden)
		case "/mgmt/cm/system/machineid-resolver":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c, err := NewClientWithOptions(ClientOptions{
		Host:       u.Hostname(),
		Port:       port,
		Username:   "admin",
		Password:   "SecretPass123",
		TargetType: "BIG-IQ",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClientWithOptions failed: %v", err)
	}
	c.Scheme = "http"
	defer c.Zero()

	kind, err := c.CheckReadyWithKind(context.Background())
	if err != nil {
		t.Fatalf("CheckReadyWithKind failed: %v", err)
	}
	if kind != "BIG-IQ" {
		t.Errorf("expected BIG-IQ via machineid-resolver fallback, got %q", kind)
	}
}

func TestBigIQGetSystemInfo_DeviceInfoAndResolver(t *testing.T) {
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mgmt/shared/authn/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":{"token":"tok-info"}}`))
		case "/mgmt/shared/identified-devices/config/device-info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"machineId": "d41d8cd9-8f00-b204-e980-0998ecf8427e",
				"chassisSerialNumber": "f5-bigiq-ch-9001",
				"baseRegKey": "ABCDE-FGHIJ-KLMNO-PQRST-UVWXYZA",
				"platform": "Z100",
				"baseMac": "02:01:0a:fa:00:15"
			}`))
		case "/mgmt/shared/resolver/device-groups/cm-shared-all-big-iqs/devices":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [
					{
						"uuid": "d41d8cd9-8f00-b204-e980-0998ecf8427e",
						"address": "127.0.0.1",
						"hostname": "bigiq-cm1.lab.local",
						"product": "BIG-IQ",
						"version": "8.4.0",
						"build": "0.0.42",
						"edition": "Final"
					}
				]
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c, err := NewClientWithOptions(ClientOptions{
		Host:       u.Hostname(),
		Port:       port,
		Username:   "admin",
		Password:   "SecretPass123",
		TargetType: "BIG-IQ",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClientWithOptions failed: %v", err)
	}
	c.Scheme = "http"
	defer c.Zero()

	info, err := c.GetSystemInfo(context.Background())
	if err != nil {
		t.Fatalf("GetSystemInfo failed: %v", err)
	}
	if info.ChassisSerialNumber != "f5-bigiq-ch-9001" {
		t.Errorf("expected ChassisSerialNumber 'f5-bigiq-ch-9001' (distinct from UUID machineId), got %q", info.ChassisSerialNumber)
	}
	if info.RegistrationKey != "ABCDE-FGHIJ-KLMNO-PQRST-UVWXYZA" {
		t.Errorf("expected RegistrationKey 'ABCDE-FGHIJ-KLMNO-PQRST-UVWXYZA', got %q", info.RegistrationKey)
	}
	if info.Product != "BIG-IQ" || info.Version != "8.4.0" || info.Build != "0.0.42" {
		t.Errorf("unexpected product/version/build: %+v", info)
	}
	if info.Hostname != "bigiq-cm1.lab.local" {
		t.Errorf("expected hostname 'bigiq-cm1.lab.local', got %q", info.Hostname)
	}

	// Also verify UUID machineId is never conflated with ChassisSerialNumber when chassisSerialNumber is empty
	var infoUUIDOnly SystemInfo
	parseDeviceInfoJSON([]byte(`{"machineId":"d41d8cd9-8f00-b204-e980-0998ecf8427e","baseRegKey":"REGKEY-ONLY-123"}`), &infoUUIDOnly)
	if infoUUIDOnly.ChassisSerialNumber != "" {
		t.Errorf("expected empty ChassisSerialNumber when only UUID machineId is present, got %q", infoUUIDOnly.ChassisSerialNumber)
	}
	if infoUUIDOnly.PreferredSerialNumber() != "REGKEY-ONLY-123" {
		t.Errorf("expected PreferredSerialNumber to fall back to RegistrationKey, got %q", infoUUIDOnly.PreferredSerialNumber())
	}
}

func TestBigIQDownloadFile_SharedFileTransfer200OK(t *testing.T) {
	expectedPayload := []byte("BIG-IQ-REST-DOWNLOAD-200-OK-BODY-PAYLOAD")

	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mgmt/shared/authn/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":{"token":"tok-dl"}}`))
		case "/mgmt/shared/file-transfer/downloads/archive.iso":
			// BIG-IQ file-transfer/downloads returns 200 OK full body without Content-Range
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(expectedPayload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	c, err := NewClientWithOptions(ClientOptions{
		Host:       u.Hostname(),
		Port:       port,
		Username:   "admin",
		Password:   "SecretPass123",
		TargetType: "BIG-IQ",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClientWithOptions failed: %v", err)
	}
	c.Scheme = "http"
	defer c.Zero()

	outFile := filepath.Join(t.TempDir(), "downloaded.iso")
	var lastTransferred, lastTotal int64
	err = c.DownloadFile(context.Background(), "/var/config/rest/downloads/archive.iso", outFile, func(transferred, total int64) {
		lastTransferred = transferred
		lastTotal = total
	})
	if err != nil {
		t.Fatalf("DownloadFile from /var/config/rest/downloads failed: %v", err)
	}
	gotBytes, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed reading downloaded file: %v", err)
	}
	if string(gotBytes) != string(expectedPayload) {
		t.Errorf("downloaded payload mismatch: got %q, want %q", string(gotBytes), string(expectedPayload))
	}
	if lastTransferred != int64(len(expectedPayload)) || lastTotal != int64(len(expectedPayload)) {
		t.Errorf("expected progress (%d, %d), got (%d, %d)", len(expectedPayload), len(expectedPayload), lastTransferred, lastTotal)
	}

	// BIG-IQ REST must reject /var/tmp/qkviews/*.qkview with actionable message
	err = c.DownloadFile(context.Background(), "/var/tmp/qkviews/diag.qkview", filepath.Join(t.TempDir(), "diag.qkview"), nil)
	if err == nil || !strings.Contains(err.Error(), "BIG-IQ does not expose /mgmt/cm/autodeploy/qkview-downloads") {
		t.Errorf("expected clear BIG-IQ qkview REST error, got: %v", err)
	}
}

func startMockBigIQSSHServer(t *testing.T, scpDownloadPayload []byte) (*pipeSSHServer, int, *int32) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed generating ed25519 key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed creating signer: %v", err)
	}

	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "admin" && string(pass) == "SecretPass123" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
	}
	serverConfig.AddHostKey(signer)

	listener, port := newPipeSSHServer()
	var sshSessions int32

	go func() {
		for {
			tcpConn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				sshConn, chans, reqs, err := ssh.NewServerConn(conn, serverConfig)
				if err != nil {
					return
				}
				defer sshConn.Close()
				go ssh.DiscardRequests(reqs)

				for newChannel := range chans {
					if newChannel.ChannelType() != "session" {
						_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					ch, requests, err := newChannel.Accept()
					if err != nil {
						return
					}
					atomic.AddInt32(&sshSessions, 1)
					go func(in <-chan *ssh.Request) {
						defer ch.Close()
						for req := range in {
							if req.Type != "exec" {
								_ = req.Reply(false, nil)
								continue
							}
							var execPayload struct {
								Command string
							}
							_ = ssh.Unmarshal(req.Payload, &execPayload)
							cmd := execPayload.Command
							_ = req.Reply(true, nil)

							switch {
							case strings.Contains(cmd, "scp -t"):
								_, _ = ch.Write([]byte{0})
								reader := bufio.NewReader(ch)
								hdr, err := reader.ReadString('\n')
								if err == nil {
									parts := strings.Fields(hdr)
									if len(parts) >= 2 {
										length, _ := strconv.ParseInt(parts[1], 10, 64)
										_, _ = ch.Write([]byte{0})
										buf := make([]byte, length)
										_, _ = io.ReadFull(reader, buf)
										_, _ = reader.ReadByte()
										_, _ = ch.Write([]byte{0})
									}
								}
							case strings.Contains(cmd, "scp -f"):
								reader := bufio.NewReader(ch)
								_, _ = reader.ReadByte()
								hdr := fmt.Sprintf("C0644 %d diag.qkview\n", len(scpDownloadPayload))
								_, _ = ch.Write([]byte(hdr))
								_, _ = reader.ReadByte()
								_, _ = ch.Write(scpDownloadPayload)
								_, _ = ch.Write([]byte{0})
								_, _ = reader.ReadByte()
							case strings.Contains(cmd, "qkview"):
								_, _ = ch.Write([]byte("Gathering System Diagnostics: Complete\n"))
							}
							_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
							return
						}
					}(requests)
				}
			}(tcpConn)
		}
	}()

	return listener, port, &sshSessions
}

func TestBigIQFailoverRouting(t *testing.T) {
	var sawAutodeployISO atomic.Bool
	var sawUtilBash atomic.Bool

	restServer := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/mgmt/shared/authn/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":{"token":"tok-route"}}`))
		case strings.HasPrefix(r.URL.Path, "/mgmt/cm/autodeploy/software-image-uploads/"):
			sawAutodeployISO.Store(true)
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"remainingByteCount":0}`))
		case r.URL.Path == "/mgmt/tm/util/bash":
			sawUtilBash.Store(true)
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("<html><head><title>403 Forbidden</title></head><body><center>webd</center></body></html>"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer restServer.Close()

	u, _ := url.Parse(restServer.URL)
	restPort, _ := strconv.Atoi(u.Port())

	tmpDir := t.TempDir()
	isoFile := filepath.Join(tmpDir, "BIG-IQ-8.4.0.iso")
	if err := os.WriteFile(isoFile, []byte("ISO-IMAGE-BYTES"), 0600); err != nil {
		t.Fatal(err)
	}

	// 1. Push /shared/images/*.iso on BIG-IQ uses /mgmt/cm/autodeploy/software-image-uploads directly without bash
	finalISO, methodISO, err := PushFileWithFailover(context.Background(), "http://127.0.0.1", isoFile, TransferOptions{
		TargetType:            "BIG-IQ",
		Method:                MethodAuto,
		Username:              "admin",
		Password:              "SecretPass123",
		Port:                  restPort,
		DestDir:               "/shared/images",
		HTTPClient:            restServer.Client(),
		InsecureIgnoreHostKey: true,
		Timeout:               5 * time.Second,
	}, nil, nil)
	if err != nil {
		t.Fatalf("PushFileWithFailover ISO to BIG-IQ failed: %v", err)
	}
	if methodISO != MethodIControl || finalISO != "/shared/images/BIG-IQ-8.4.0.iso" {
		t.Errorf("expected (icontrol, /shared/images/BIG-IQ-8.4.0.iso), got (%s, %s)", methodISO, finalISO)
	}
	if !sawAutodeployISO.Load() {
		t.Errorf("expected BIG-IQ ISO push to hit /mgmt/cm/autodeploy/software-image-uploads/")
	}
	if sawUtilBash.Load() {
		t.Errorf("expected BIG-IQ ISO push NOT to call /mgmt/tm/util/bash")
	}

	// Start mock SSH/SCP server for BIG-IQ SCP/SSH routing tests
	qkPayload := []byte("MOCK-BIGIQ-QKVIEW-BYTES")
	sshSrv, sshPort, sshSessions := startMockBigIQSSHServer(t, qkPayload)
	defer sshSrv.Close()

	// 2. Push to /var/tmp on BIG-IQ with MethodAuto automatically selects SCP (since webd blocks /mgmt/tm/util/bash)
	miscFile := filepath.Join(tmpDir, "bundle.tar.gz")
	if err := os.WriteFile(miscFile, []byte("TARBALL-DATA"), 0600); err != nil {
		t.Fatal(err)
	}
	finalTmp, methodTmp, err := PushFileWithFailover(context.Background(), "127.0.0.1", miscFile, TransferOptions{
		TargetType:            "BIG-IQ",
		Method:                MethodAuto,
		Username:              "admin",
		Password:              "SecretPass123",
		Port:                  sshPort,
		DestDir:               "/var/tmp",
		HTTPClient:            restServer.Client(),
		InsecureIgnoreHostKey: true,
		Timeout:               5 * time.Second,
	}, nil, nil)
	if err != nil {
		t.Fatalf("PushFileWithFailover /var/tmp on BIG-IQ failed: %v", err)
	}
	if methodTmp != MethodSCP || finalTmp != "/var/tmp/bundle.tar.gz" {
		t.Errorf("expected SCP push to /var/tmp/bundle.tar.gz, got (%s, %s)", methodTmp, finalTmp)
	}

	// 3. Download /var/tmp/qkviews/diag.qkview on BIG-IQ with MethodAuto automatically selects SCP
	dlDest := filepath.Join(tmpDir, "pulled-diag.qkview")
	savedQK, dlMethod, err := DownloadFileWithFailover(context.Background(), "127.0.0.1", "/var/tmp/qkviews/diag.qkview", dlDest, TransferOptions{
		TargetType:            "BIG-IQ",
		Method:                MethodAuto,
		Username:              "admin",
		Password:              "SecretPass123",
		Port:                  sshPort,
		HTTPClient:            restServer.Client(),
		InsecureIgnoreHostKey: true,
		Timeout:               5 * time.Second,
	}, nil, nil)
	if err != nil {
		t.Fatalf("DownloadFileWithFailover BIG-IQ qkview failed: %v", err)
	}
	if dlMethod != MethodSCP || savedQK != dlDest {
		t.Errorf("expected SCP download to %s, got (%s, %s)", dlDest, dlMethod, savedQK)
	}
	gotQK, err := os.ReadFile(dlDest)
	if err != nil || string(gotQK) != string(qkPayload) {
		t.Errorf("unexpected downloaded qkview content: %q (err=%v)", string(gotQK), err)
	}

	// 4. GenerateQKViewWithFailover on BIG-IQ with MethodAuto automatically selects SSH without hitting /mgmt/tm/util/bash
	sawUtilBash.Store(false)
	genName, genMethod, err := GenerateQKViewWithFailover(context.Background(), "127.0.0.1", "bigiq_diag.qkview", true, TransferOptions{
		TargetType:            "BIG-IQ",
		Method:                MethodAuto,
		Username:              "admin",
		Password:              "SecretPass123",
		Port:                  sshPort,
		HTTPClient:            restServer.Client(),
		InsecureIgnoreHostKey: true,
		Timeout:               5 * time.Second,
	}, nil, nil)
	if err != nil {
		t.Fatalf("GenerateQKViewWithFailover on BIG-IQ failed: %v", err)
	}
	if genMethod != MethodSCP || genName != "bigiq_diag.qkview" {
		t.Errorf("expected SSH/SCP QKView generation of bigiq_diag.qkview, got (%s, %s)", genMethod, genName)
	}
	if sawUtilBash.Load() {
		t.Errorf("expected BIG-IQ GenerateQKViewWithFailover NOT to call /mgmt/tm/util/bash")
	}
	if atomic.LoadInt32(sshSessions) < 3 {
		t.Errorf("expected at least 3 SSH sessions across push, download, and qkview, got %d", atomic.LoadInt32(sshSessions))
	}
}

func TestSummarizeHTTPErrorBody(t *testing.T) {
	webd403 := []byte("<html>\r\n<head><title>403 Forbidden</title></head>\r\n<body>\r\n<center><h1>403 Forbidden</h1></center>\r\n<hr><center>webd</center>\r\n</body>\r\n</html>")
	if got := summarizeHTTPErrorBody(webd403); got != "403 Forbidden (webd)" {
		t.Errorf("expected '403 Forbidden (webd)', got %q", got)
	}

	apache404 := []byte("<!DOCTYPE HTML PUBLIC \"-//IETF//DTD HTML 2.0//EN\">\n<html><head>\n<title>404 Not Found</title>\n</head><body>\n<h1>Not Found</h1>\n<p>The requested URL was not found on this server.</p>\n</body></html>")
	if got := summarizeHTTPErrorBody(apache404); !strings.Contains(got, "404 Not Found") || strings.Contains(got, "<html>") {
		t.Errorf("expected sanitized single-line 404 summary, got %q", got)
	}

	jsonBody := []byte(`{"code":400,"message":"Invalid parameter"}`)
	if got := summarizeHTTPErrorBody(jsonBody); got != string(jsonBody) {
		t.Errorf("expected JSON body preserved intact, got %q", got)
	}
}

func TestBigIQTargetTypeNormalization_UnhyphenatedBigIQ(t *testing.T) {
	// Callers passing "bigiq" (unhyphenated) must trigger identical BIG-IQ auth, failover, and GeoIP guards as "BIG-IQ".
	var sawProvider atomic.Bool
	srv := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/mgmt/shared/authn/login":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["loginProviderName"]; ok {
				sawProvider.Store(true)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":{"token":"tok-norm"}}`))
		case strings.HasPrefix(r.URL.Path, "/mgmt/cm/autodeploy/software-image-uploads/"):
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"remainingByteCount":0}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	isoFile := filepath.Join(t.TempDir(), "BIG-IQ-8.4.iso")
	if err := os.WriteFile(isoFile, []byte("ISO-DATA"), 0600); err != nil {
		t.Fatal(err)
	}

	dest, method, err := PushFileWithFailover(context.Background(), "http://127.0.0.1", isoFile, TransferOptions{
		TargetType: "bigiq",
		Method:     MethodIControl,
		Username:   "admin",
		Password:   "SecretPass123",
		Port:       port,
		DestDir:    "/shared/images",
		HTTPClient: srv.Client(),
	}, nil, nil)
	if err != nil {
		t.Fatalf("expected TargetType 'bigiq' ISO upload to succeed, got: %v", err)
	}
	if method != MethodIControl || dest != "/shared/images/BIG-IQ-8.4.iso" {
		t.Errorf("unexpected result (%s, %s)", method, dest)
	}
	if sawProvider.Load() {
		t.Errorf("expected TargetType 'bigiq' to omit loginProviderName on login")
	}

	if _, err := ApplyGeoIP(context.Background(), "127.0.0.1", "/shared/images/geoip.zip", GeoIPOptions{
		TargetType: "bigiq",
	}, nil); err == nil {
		t.Errorf("expected ApplyGeoIP to reject TargetType 'bigiq'")
	}
}
