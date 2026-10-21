package bigip

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadFile(t *testing.T) {
	// Create temporary local file
	tempDir := t.TempDir()
	localFile := filepath.Join(tempDir, "BIGIP-17.1.1.iso")
	testData := make([]byte, 1024*1024+500) // ~1.0005 MB, spans 3 chunks of 512KB
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	if err := os.WriteFile(localFile, testData, 0600); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	var uploadedBytes []byte
	var receivedRanges []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/mgmt/shared/file-transfer/uploads/") {
			crange := r.Header.Get("Content-Range")
			receivedRanges = append(receivedRanges, crange)

			b, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			uploadedBytes = append(uploadedBytes, b...)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"localFilePath":"/var/config/rest/downloads/BIGIP-17.1.1.iso"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, 0, "admin", "secret")
	var lastTransferred, lastTotal int64
	progressCb := func(transferred, total int64) {
		lastTransferred = transferred
		lastTotal = total
	}

	remotePath, err := client.UploadFile(context.Background(), localFile, "", progressCb)
	if err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	expectedPath := "/var/config/rest/downloads/BIGIP-17.1.1.iso"
	if remotePath != expectedPath {
		t.Errorf("expected remotePath %q, got %q", expectedPath, remotePath)
	}

	if len(uploadedBytes) != len(testData) {
		t.Errorf("expected %d uploaded bytes, got %d", len(testData), len(uploadedBytes))
	}

	if len(receivedRanges) != 3 {
		t.Errorf("expected 3 chunk upload requests, got %d: %v", len(receivedRanges), receivedRanges)
	}

	if lastTransferred != int64(len(testData)) || lastTotal != int64(len(testData)) {
		t.Errorf("expected final progress %d/%d, got %d/%d", len(testData), len(testData), lastTransferred, lastTotal)
	}
}

func TestUploadFileErrors(t *testing.T) {
	client := NewClient("127.0.0.1", 443, "admin", "secret")

	// Missing local file
	_, err := client.UploadFile(context.Background(), "/nonexistent/path/file.iso", "", nil)
	if err == nil {
		t.Errorf("expected error for non-existent file, got nil")
	}

	// Server returning 500 Internal Server Error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("disk full"))
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "test.iso")
	_ = os.WriteFile(tmpFile, []byte("data"), 0600)

	errorClient := NewClient(server.URL, 0, "admin", "secret")
	_, err = errorClient.UploadFile(context.Background(), tmpFile, "", nil)
	if err == nil {
		t.Errorf("expected error on HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("expected error to mention HTTP 500, got: %v", err)
	}
}

func TestCopyRemoteFile(t *testing.T) {
	var receivedCommand string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mgmt/tm/util/bash" && r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			receivedCommand = string(b)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"command":"run"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, 0, "admin", "secret")
	destPath, err := client.CopyRemoteFile(context.Background(), "/var/config/rest/downloads/BIGIP-17.1.1.iso", "/shared/images")
	if err != nil {
		t.Fatalf("CopyRemoteFile failed: %v", err)
	}

	expectedDest := "/shared/images/BIGIP-17.1.1.iso"
	if destPath != expectedDest {
		t.Errorf("expected destPath %q, got %q", expectedDest, destPath)
	}

	if !strings.Contains(receivedCommand, "cp -f /var/config/rest/downloads/BIGIP-17.1.1.iso /shared/images/BIGIP-17.1.1.iso") {
		t.Errorf("unexpected bash util payload: %s", receivedCommand)
	}
}

func TestValidateRemoteAppliancePath(t *testing.T) {
	tests := []struct {
		path    string
		wantErr bool
	}{
		{"/shared/images", false},
		{"/shared/images/BIGIP-17.1.1.iso", false},
		{"/var/tmp/qkviews/test.qkview", false},
		{"/config/bigip.conf", false},
		{"", true},
		{"relative/path", true},
		{"/shared/../etc/passwd", true},
		{"/shared/images/file;rm -rf /", true},
		{"/shared/images/file$(whoami)", true},
		{"/shared/images/file`id`", true},
		{"/shared/images/file name with spaces", true},
		{"/shared/images/file'quote", true},
	}

	for _, tt := range tests {
		err := ValidateRemoteAppliancePath(tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateRemoteAppliancePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
		}
	}
}

func TestCopyRemoteFileValidation(t *testing.T) {
	client := NewClient("https://192.0.2.1", 443, "admin", "secret")
	// Test malicious source path
	_, err := client.CopyRemoteFile(context.Background(), "/var/tmp/file;reboot", "/shared/images")
	if err == nil {
		t.Fatal("expected error on invalid source path, got nil")
	}

	// Test malicious destination directory
	_, err = client.CopyRemoteFile(context.Background(), "/var/tmp/good.iso", "/shared/images/$(touch /tmp/pwn)")
	if err == nil {
		t.Fatal("expected error on invalid dest dir, got nil")
	}
}

func TestValidateApplianceDownloadPath(t *testing.T) {
	tests := []struct {
		path    string
		wantErr bool
	}{
		{"/var/tmp/qkviews/test.qkview", false},
		{"/var/tmp/box_20261005.QKVIEW", false},
		{"/shared/images/BIGIP-17.1.1.iso", false},
		{"/var/tmp/image.ISO", false},
		{"/etc/shadow", true},
		{"/config/bigip.conf", true},
		{"/var/tmp/file.txt", true},
		{"/var/tmp/script.sh", true},
		{"", true},
		{"../var/tmp/test.qkview", true},
		{"/var/tmp/../etc/shadow", true},
		{"/var/tmp/test.qkview;rm -rf /", true},
		{"/var/tmp/test file.qkview", true},
	}

	for _, tt := range tests {
		err := ValidateApplianceDownloadPath(tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateApplianceDownloadPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
		}
	}
}
