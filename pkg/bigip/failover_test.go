package bigip

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPushFileWithFailover_IControlSuccess(t *testing.T) {
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/mgmt/shared/file-transfer/uploads/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"localFilePath":"/var/config/rest/downloads/test.txt"}`))
			return
		}
		if r.URL.Path == "/mgmt/tm/util/bash" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"command":"run"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	tempDir := t.TempDir()
	localFile := filepath.Join(tempDir, "test.txt")
	_ = os.WriteFile(localFile, []byte("data"), 0600)

	opts := TransferOptions{
		Method:     MethodAuto,
		Username:   "admin",
		Password:   "secret",
		Port:       port,
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	}

	var logMessages []string
	logFn := func(format string, args ...any) {
		logMessages = append(logMessages, fmt.Sprintf(format, args...))
	}

	destPath, method, err := PushFileWithFailover(context.Background(), server.URL, localFile, opts, logFn, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if method != MethodIControl {
		t.Errorf("expected MethodIControl, got %v", method)
	}
	if destPath != "/shared/images/test.txt" {
		t.Errorf("expected /shared/images/test.txt, got %s", destPath)
	}
	if len(logMessages) > 0 {
		t.Errorf("expected no failover logs, got: %v", logMessages)
	}
}

func TestPushFileWithFailover_BothFail(t *testing.T) {
	// Point to dead port
	opts := TransferOptions{
		Method:   MethodAuto,
		Username: "admin",
		Password: "secret",
		Port:     65530,
		Timeout:  100 * time.Millisecond,
	}

	var logged bool
	logFn := func(format string, args ...any) {
		logged = true
	}

	tmpFile := filepath.Join(t.TempDir(), "dummy.txt")
	_ = os.WriteFile(tmpFile, []byte("test"), 0600)

	_, _, err := PushFileWithFailover(context.Background(), "127.0.0.1", tmpFile, opts, logFn, nil)
	if err == nil {
		t.Fatalf("expected error when both fail, got nil")
	}
	if !strings.Contains(err.Error(), "both transfer methods failed") {
		t.Errorf("expected error message to mention both transfer methods failed, got: %v", err)
	}
	if !logged {
		t.Errorf("expected failover logger to be invoked")
	}
}

func TestDownloadFileWithFailover_IControlSuccess(t *testing.T) {
	testData := []byte("downloadable-content")
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Range", fmt.Sprintf("0-%d/%d", len(testData)-1, len(testData)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testData)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	tempDir := t.TempDir()
	localTarget := filepath.Join(tempDir, "received.txt")

	opts := TransferOptions{
		Method:     MethodAuto,
		Username:   "admin",
		Password:   "secret",
		Port:       port,
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	}

	savedPath, method, err := DownloadFileWithFailover(context.Background(), server.URL, "/shared/images/image.iso", localTarget, opts, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if method != MethodIControl {
		t.Errorf("expected MethodIControl, got %v", method)
	}
	if savedPath != localTarget {
		t.Errorf("expected savedPath %s, got %s", localTarget, savedPath)
	}

	content, _ := os.ReadFile(localTarget)
	if string(content) != string(testData) {
		t.Errorf("expected content %q, got %q", string(testData), string(content))
	}
}

func TestDownloadFileWithFailover_BothFail(t *testing.T) {
	opts := TransferOptions{
		Method:   MethodAuto,
		Username: "admin",
		Password: "secret",
		Port:     65530,
		Timeout:  100 * time.Millisecond,
	}

	var logged bool
	logFn := func(format string, args ...any) {
		logged = true
	}

	_, _, err := DownloadFileWithFailover(context.Background(), "127.0.0.1", "/shared/images/test.iso", filepath.Join(t.TempDir(), "f.iso"), opts, logFn, nil)
	if err == nil {
		t.Fatalf("expected error when both fail, got nil")
	}
	if !strings.Contains(err.Error(), "both download methods failed") {
		t.Errorf("expected error message to mention both download methods failed, got: %v", err)
	}
	if !logged {
		t.Errorf("expected failover logger to be invoked")
	}
}

func TestTransferOptionsPasswordBytesNotZeroedAcrossCalls(t *testing.T) {
	opts := TransferOptions{
		PasswordBytes:      []byte("secret123"),
		SSHPassphraseBytes: []byte("pass123"),
	}

	// 1. Verify client creation with newClientFromOptions does not zero opts.PasswordBytes
	client := newClientFromOptions("10.0.0.1", opts, 443)
	if client == nil {
		t.Fatalf("expected non-nil client")
	}
	client.Zero()

	if string(opts.PasswordBytes) != "secret123" {
		t.Errorf("expected opts.PasswordBytes to remain %q, got %q", "secret123", string(opts.PasswordBytes))
	}

	// 2. Verify newSCPOptions and scpOpts.Zero() do not zero opts.PasswordBytes or opts.SSHPassphraseBytes
	scpOpts := newSCPOptions("10.0.0.1", opts)
	scpOpts.Zero()

	if string(opts.PasswordBytes) != "secret123" {
		t.Errorf("expected opts.PasswordBytes to remain %q after scpOpts.Zero(), got %q", "secret123", string(opts.PasswordBytes))
	}
	if string(opts.SSHPassphraseBytes) != "pass123" {
		t.Errorf("expected opts.SSHPassphraseBytes to remain %q after scpOpts.Zero(), got %q", "pass123", string(opts.SSHPassphraseBytes))
	}
}

func TestGenerateAndDeleteQKViewWithFailover(t *testing.T) {
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/mgmt/tm/util/qkview" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"command":"run","status":"completed"}`))
			return
		}
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/mgmt/cm/autodeploy/qkview/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())

	opts := TransferOptions{
		Method:     MethodIControl,
		Username:   "admin",
		Password:   "secret",
		Port:       port,
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	}

	fn, method, err := GenerateQKViewWithFailover(context.Background(), server.URL, "test.qkview", true, opts, nil, nil)
	if err != nil {
		t.Fatalf("GenerateQKViewWithFailover failed: %v", err)
	}
	if method != MethodIControl || fn != "test.qkview" {
		t.Errorf("got (%s, %v), want (test.qkview, MethodIControl)", fn, method)
	}

	if err := DeleteQKViewWithFailover(context.Background(), server.URL, "test.qkview", opts); err != nil {
		t.Fatalf("DeleteQKViewWithFailover failed: %v", err)
	}

	optsAuto := opts
	optsAuto.Method = MethodAuto
	fn2, method2, err := GenerateQKViewWithFailover(context.Background(), server.URL, "test2.qkview", true, optsAuto, nil, nil)
	if err != nil {
		t.Fatalf("GenerateQKViewWithFailover auto failed: %v", err)
	}
	if method2 != MethodIControl || fn2 != "test2.qkview" {
		t.Errorf("got (%s, %v), want (test2.qkview, MethodIControl)", fn2, method2)
	}

	if err := DeleteQKViewWithFailover(context.Background(), server.URL, "test2.qkview", optsAuto); err != nil {
		t.Fatalf("DeleteQKViewWithFailover auto failed: %v", err)
	}
}

func TestFailoverBranches(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "dummy.txt")
	_ = os.WriteFile(tmpFile, []byte("data"), 0600)
	tmpDest := filepath.Join(t.TempDir(), "out.txt")

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// 1. PushFileWithFailover: unsupported method
	_, _, err := PushFileWithFailover(context.Background(), "127.0.0.1", tmpFile, TransferOptions{Method: "unsupported"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported transfer method") {
		t.Errorf("expected unsupported method error, got %v", err)
	}

	// 2. PushFileWithFailover: MethodSCP failure
	_, _, err = PushFileWithFailover(context.Background(), "127.0.0.1", tmpFile, TransferOptions{Method: MethodSCP, Port: 65530, Timeout: 50 * time.Millisecond}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "SCP upload failed") {
		t.Errorf("expected SCP upload failed error, got %v", err)
	}

	// 3. PushFileWithFailover: MethodIControl failure
	_, _, err = PushFileWithFailover(context.Background(), "127.0.0.1", tmpFile, TransferOptions{Method: MethodIControl, Port: 65530, Timeout: 50 * time.Millisecond}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "iControl REST upload failed") {
		t.Errorf("expected iControl upload failed error, got %v", err)
	}

	// 4. PushFileWithFailover: trySCPFirst (IdentityFile) both fail
	_, _, err = PushFileWithFailover(context.Background(), "127.0.0.1", tmpFile, TransferOptions{Method: MethodAuto, IdentityFile: "/dummy/key", Port: 65530, Timeout: 50 * time.Millisecond}, func(f string, a ...any) {}, nil)
	if err == nil || !strings.Contains(err.Error(), "both transfer methods failed") {
		t.Errorf("expected both failed error, got %v", err)
	}

	// 5. PushFileWithFailover: canceled context
	_, _, err = PushFileWithFailover(canceledCtx, "127.0.0.1", tmpFile, TransferOptions{Method: MethodAuto, Port: 65530, Timeout: 50 * time.Millisecond}, nil, nil)
	if err == nil {
		t.Errorf("expected error with canceled context, got nil")
	}

	// 6. DownloadFileWithFailover: unsupported method
	_, _, err = DownloadFileWithFailover(context.Background(), "127.0.0.1", "/shared/images/test.iso", tmpDest, TransferOptions{Method: "unsupported"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported transfer method") {
		t.Errorf("expected unsupported method error, got %v", err)
	}

	// 7. DownloadFileWithFailover: MethodSCP failure
	_, _, err = DownloadFileWithFailover(context.Background(), "127.0.0.1", "/shared/images/test.iso", tmpDest, TransferOptions{Method: MethodSCP, Port: 65530, Timeout: 50 * time.Millisecond}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "SCP download failed") {
		t.Errorf("expected SCP download failed error, got %v", err)
	}

	// 8. DownloadFileWithFailover: MethodIControl failure
	_, _, err = DownloadFileWithFailover(context.Background(), "127.0.0.1", "/shared/images/test.iso", tmpDest, TransferOptions{Method: MethodIControl, Port: 65530, Timeout: 50 * time.Millisecond}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "iControl REST download failed") {
		t.Errorf("expected iControl download failed error, got %v", err)
	}

	// 9. DownloadFileWithFailover: trySCPFirst both fail
	_, _, err = DownloadFileWithFailover(context.Background(), "127.0.0.1", "/shared/images/test.iso", tmpDest, TransferOptions{Method: MethodAuto, IdentityFile: "/dummy/key", Port: 65530, Timeout: 50 * time.Millisecond}, func(f string, a ...any) {}, nil)
	if err == nil || !strings.Contains(err.Error(), "both download methods failed") {
		t.Errorf("expected both failed error, got %v", err)
	}

	// 10. DownloadFileWithFailover: canceled context
	_, _, err = DownloadFileWithFailover(canceledCtx, "127.0.0.1", "/shared/images/test.iso", tmpDest, TransferOptions{Method: MethodAuto, Port: 65530, Timeout: 50 * time.Millisecond}, nil, nil)
	if err == nil {
		t.Errorf("expected error with canceled context, got nil")
	}

	// 11. GenerateQKViewWithFailover: unsupported method
	_, _, err = GenerateQKViewWithFailover(context.Background(), "127.0.0.1", "test.qkview", true, TransferOptions{Method: "unsupported"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported transfer method") {
		t.Errorf("expected unsupported method error, got %v", err)
	}

	// 12. GenerateQKViewWithFailover: MethodSCP failure
	_, _, err = GenerateQKViewWithFailover(context.Background(), "127.0.0.1", "test.qkview", true, TransferOptions{Method: MethodSCP, Port: 65530, Timeout: 50 * time.Millisecond}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "SSH QKView generation failed") {
		t.Errorf("expected SSH generation failed error, got %v", err)
	}

	// 13. GenerateQKViewWithFailover: trySSHFirst (IdentityFile)
	_, _, err = GenerateQKViewWithFailover(context.Background(), "127.0.0.1", "test.qkview", true, TransferOptions{Method: MethodAuto, IdentityFile: "/dummy/key", Port: 65530, Timeout: 50 * time.Millisecond}, func(f string, a ...any) {}, nil)
	if err == nil || !strings.Contains(err.Error(), "both QKView generation methods failed") {
		t.Errorf("expected both failed error, got %v", err)
	}

	// 14. GenerateQKViewWithFailover: canceled context
	_, _, err = GenerateQKViewWithFailover(canceledCtx, "127.0.0.1", "test.qkview", true, TransferOptions{Method: MethodAuto, Port: 65530, Timeout: 50 * time.Millisecond}, nil, nil)
	if err == nil {
		t.Errorf("expected error with canceled context, got nil")
	}

	// 15. GenerateQKViewWithFailover: MethodIControl error
	_, _, _ = GenerateQKViewWithFailover(context.Background(), "127.0.0.1", "test.qkview", true, TransferOptions{Method: MethodIControl, Port: 65530, Timeout: 50 * time.Millisecond}, nil, nil)

	// 16. DeleteQKViewWithFailover branches
	_ = DeleteQKViewWithFailover(context.Background(), "127.0.0.1", "test.qkview", TransferOptions{Method: "unsupported"})
	_ = DeleteQKViewWithFailover(context.Background(), "127.0.0.1", "test.qkview", TransferOptions{Method: MethodSCP, Port: 65530, Timeout: 50 * time.Millisecond})
	_ = DeleteQKViewWithFailover(context.Background(), "127.0.0.1", "test.qkview", TransferOptions{Method: MethodIControl, Port: 65530, Timeout: 50 * time.Millisecond})
	_ = DeleteQKViewWithFailover(context.Background(), "127.0.0.1", "test.qkview", TransferOptions{Method: MethodAuto, Port: 65530, Timeout: 50 * time.Millisecond})

	// 17. TransferOptions.Zero
	optsToZero := TransferOptions{PasswordBytes: []byte("secret"), SSHPassphraseBytes: []byte("pass")}
	optsToZero.Zero()
	if len(optsToZero.PasswordBytes) != 0 || len(optsToZero.SSHPassphraseBytes) != 0 {
		t.Errorf("expected PasswordBytes and SSHPassphraseBytes to be zeroed")
	}
}
