package bigip

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPushFileWithFailover_IControlSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		Method:   MethodAuto,
		Username: "admin",
		Password: "secret",
		Port:     port,
		Timeout:  2 * time.Second,
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		Method:   MethodAuto,
		Username: "admin",
		Password: "secret",
		Port:     port,
		Timeout:  2 * time.Second,
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
