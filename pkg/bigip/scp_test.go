package bigip

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestCleanSSHHost(t *testing.T) {
	tests := []struct {
		input        string
		expectedHost string
		expectedPort int
	}{
		{"10.1.1.4", "10.1.1.4", 22},
		{"https://10.1.1.4/", "10.1.1.4", 22},
		{"http://192.168.1.10:8022", "192.168.1.10", 8022},
		{"ssh://appliance.lab.local:2222/", "appliance.lab.local", 2222},
		{"[::1]:2222", "::1", 2222},
	}

	for _, tt := range tests {
		h, p := CleanSSHHost(tt.input)
		if h != tt.expectedHost || p != tt.expectedPort {
			t.Errorf("CleanSSHHost(%q) = (%q, %d), expected (%q, %d)", tt.input, h, p, tt.expectedHost, tt.expectedPort)
		}
	}
}

func TestCheckAck(t *testing.T) {
	// Success ack (0)
	bufSuccess := bufio.NewReader(bytes.NewReader([]byte{0}))
	if err := checkAck(bufSuccess); err != nil {
		t.Errorf("expected nil error on 0 ack, got: %v", err)
	}

	// Warning ack (1)
	bufWarn := bufio.NewReader(bytes.NewReader([]byte{1, 'p', 'e', 'r', 'm', 'i', 's', 's', 'i', 'o', 'n', ' ', 'd', 'e', 'n', 'i', 'e', 'd', '\n'}))
	if err := checkAck(bufWarn); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected permission denied error, got: %v", err)
	}

	// Fatal error ack (2)
	bufFatal := bufio.NewReader(bytes.NewReader([]byte{2, 'f', 'a', 't', 'a', 'l', '\n'}))
	if err := checkAck(bufFatal); err == nil || !strings.Contains(err.Error(), "fatal") {
		t.Errorf("expected fatal error, got: %v", err)
	}
}

func TestBuildSSHClientConfig(t *testing.T) {
	// No auth should return error
	_, err := BuildSSHClientConfig(SCPOptions{
		Host:     "127.0.0.1",
		Username: "admin",
		KeyPath:  "/nonexistent/key.pem",
	})
	if err == nil {
		t.Errorf("expected error with missing key file, got nil")
	}

	// Password auth
	cfg, err := BuildSSHClientConfig(SCPOptions{
		Host:     "127.0.0.1",
		Username: "admin",
		Password: "testpassword",
	})
	if err != nil {
		t.Fatalf("expected valid config with password, got: %v", err)
	}
	if cfg.User != "admin" {
		t.Errorf("expected user 'admin', got %q", cfg.User)
	}
	if len(cfg.Auth) == 0 {
		t.Errorf("expected auth methods to be present")
	}
}

func TestSCPUploadMockServer(t *testing.T) {
	// 1. Generate SSH server host key
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
			if c.User() == "admin" && string(pass) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, pubKey ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, fmt.Errorf("public key rejected")
		},
	}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on local port: %v", err)
	}
	defer listener.Close()

	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	var receivedHeader string
	var receivedBody []byte

	// Handle incoming SSH connection in goroutine
	serverErrCh := make(chan error, 1)
	go func() {
		tcpConn, err := listener.Accept()
		if err != nil {
			serverErrCh <- err
			return
		}
		defer tcpConn.Close()

		sshConn, chans, reqs, err := ssh.NewServerConn(tcpConn, serverConfig)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer sshConn.Close()

		go ssh.DiscardRequests(reqs)

		for newChannel := range chans {
			if newChannel.ChannelType() != "session" {
				_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
				continue
			}
			channel, requests, err := newChannel.Accept()
			if err != nil {
				serverErrCh <- err
				return
			}
			defer channel.Close()

			go func(in <-chan *ssh.Request) {
				for req := range in {
					switch req.Type {
					case "exec":
						_ = req.Reply(true, nil)
						// Step 1: Send ready ack (0)
						_, _ = channel.Write([]byte{0})

						// Step 2: Read file header line
						reader := bufio.NewReader(channel)
						hdr, err := reader.ReadString('\n')
						if err != nil {
							serverErrCh <- err
							return
						}
						receivedHeader = hdr

						// Parse length from header (e.g. "C0644 14 test.txt\n")
						parts := strings.Fields(hdr)
						if len(parts) >= 2 {
							length, _ := strconv.ParseInt(parts[1], 10, 64)

							// Step 3: Send header ack (0)
							_, _ = channel.Write([]byte{0})

							// Step 4: Read content
							bodyBuf := make([]byte, length)
							_, _ = io.ReadFull(reader, bodyBuf)
							receivedBody = bodyBuf

							// Step 5: Read EOF null byte
							_, _ = reader.ReadByte()

							// Step 6: Send final ack (0)
							_, _ = channel.Write([]byte{0})
						}
						_, _ = channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
						_ = channel.Close()
						serverErrCh <- nil
						return
					default:
						_ = req.Reply(false, nil)
					}
				}
			}(requests)
		}
	}()

	// Create test file to upload
	tmpFile := filepath.Join(t.TempDir(), "test-file.zip")
	testContent := []byte("hello-f5-tmos!")
	if err := os.WriteFile(tmpFile, testContent, 0644); err != nil {
		t.Fatalf("failed to create tmp file: %v", err)
	}

	opts := SCPOptions{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "admin",
		Password: "secret",
		Timeout:  5 * time.Second,
	}

	var progressCalled bool
	destPath, err := SCPUpload(context.Background(), tmpFile, "/var/tmp", opts, func(transferred, total int64) {
		progressCalled = true
	})
	if err != nil {
		t.Fatalf("SCPUpload failed: %v", err)
	}

	// Verify server error
	select {
	case sErr := <-serverErrCh:
		if sErr != nil {
			t.Fatalf("server returned error: %v", sErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for server")
	}

	expectedDest := "/var/tmp/test-file.zip"
	if destPath != expectedDest {
		t.Errorf("expected destPath %q, got %q", expectedDest, destPath)
	}

	if !strings.HasPrefix(receivedHeader, "C0644 14 test-file.zip") && !strings.HasPrefix(receivedHeader, "C0666 14 test-file.zip") {
		t.Errorf("unexpected scp header: %q", receivedHeader)
	}

	if string(receivedBody) != string(testContent) {
		t.Errorf("expected body %q, got %q", string(testContent), string(receivedBody))
	}

	if !progressCalled {
		t.Errorf("expected progress callback to be invoked")
	}
}

func TestSCPDownloadMockServer(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed generating ed25519 key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed creating signer: %v", err)
	}

	testPayload := []byte("downloadable-remote-tmos-content-12345")
	serverErrCh := make(chan error, 1)

	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "admin" && string(pass) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	go func() {
		nConn, err := listener.Accept()
		if err != nil {
			return
		}
		defer nConn.Close()

		sConn, chans, reqs, err := ssh.NewServerConn(nConn, serverConfig)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer sConn.Close()
		go ssh.DiscardRequests(reqs)

		for newChannel := range chans {
			if newChannel.ChannelType() != "session" {
				_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
				continue
			}
			channel, requests, err := newChannel.Accept()
			if err != nil {
				serverErrCh <- err
				return
			}

			go func(reqs <-chan *ssh.Request) {
				defer channel.Close()
				reader := bufio.NewReader(channel)
				for req := range reqs {
					switch req.Type {
					case "exec":
						_ = req.Reply(true, nil)
						cmd := string(req.Payload[4:])
						if strings.HasPrefix(cmd, "scp -f") {
							// Step 1: Wait for initial client 0 byte
							initByte, _ := reader.ReadByte()
							if initByte != 0 {
								serverErrCh <- fmt.Errorf("expected initial 0 byte, got: %d", initByte)
								return
							}

							// Step 2: Send file header
							header := fmt.Sprintf("C0644 %d remote-file.txt\n", len(testPayload))
							_, _ = channel.Write([]byte(header))

							// Step 3: Wait for client ack
							ack1, _ := reader.ReadByte()
							if ack1 != 0 {
								serverErrCh <- fmt.Errorf("expected client ack 0, got: %d", ack1)
								return
							}

							// Step 4: Send file content
							_, _ = channel.Write(testPayload)

							// Step 5: Send server EOF 0 byte
							_, _ = channel.Write([]byte{0})

							// Step 6: Wait for final client ack
							ack2, _ := reader.ReadByte()
							if ack2 != 0 {
								serverErrCh <- fmt.Errorf("expected client final ack 0, got: %d", ack2)
								return
							}
						}
						_, _ = channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
						serverErrCh <- nil
						return
					default:
						_ = req.Reply(false, nil)
					}
				}
			}(requests)
		}
	}()

	tempDir := t.TempDir()
	localTarget := filepath.Join(tempDir, "downloaded.txt")

	opts := SCPOptions{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "admin",
		Password: "secret",
		Timeout:  5 * time.Second,
	}

	var progressCalled bool
	savedPath, err := SCPDownload(context.Background(), "/var/tmp/remote-file.txt", localTarget, opts, func(transferred, total int64) {
		progressCalled = true
	})
	if err != nil {
		t.Fatalf("SCPDownload failed: %v", err)
	}

	select {
	case sErr := <-serverErrCh:
		if sErr != nil {
			t.Fatalf("server returned error: %v", sErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for server")
	}

	if savedPath != localTarget {
		t.Errorf("expected savedPath %q, got %q", localTarget, savedPath)
	}

	data, err := os.ReadFile(localTarget)
	if err != nil {
		t.Fatalf("failed reading downloaded file: %v", err)
	}

	if string(data) != string(testPayload) {
		t.Errorf("expected downloaded data %q, got %q", string(testPayload), string(data))
	}

	if !progressCalled {
		t.Errorf("expected progress callback to be called")
	}
}
