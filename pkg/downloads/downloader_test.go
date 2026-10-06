package downloads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDownloadFileWithChecksum(t *testing.T) {
	testData := []byte("The quick brown fox jumps over the lazy dog - F5 ISO Test Payload")
	hasher := sha256.New()
	hasher.Write(testData)
	expectedSHA256 := hex.EncodeToString(hasher.Sum(nil))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(testData)))
		w.Write(testData)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outFile := filepath.Join(tempDir, "test_image.iso")

	var progressCalled bool
	err := DownloadFile(
		context.Background(),
		server.URL,
		outFile,
		expectedSHA256,
		"sha256",
		func(transferred, total int64, speed float64) {
			progressCalled = true
		},
	)
	if err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}

	if !progressCalled {
		t.Errorf("expected progress callback to be invoked")
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if string(content) != string(testData) {
		t.Errorf("downloaded content mismatch")
	}

	// Verify checksum mismatch failure
	badFile := filepath.Join(tempDir, "bad_image.iso")
	err = DownloadFile(context.Background(), server.URL, badFile, "badchecksum12345", "sha256", nil)
	if err == nil {
		t.Errorf("expected error on checksum mismatch, got nil")
	}
	if _, err := os.Stat(badFile); !os.IsNotExist(err) {
		t.Errorf("bad file should not exist on checksum failure")
	}
}

func TestDownloadFile_DirectoryTarget(t *testing.T) {
	testData := []byte("directory-target-data")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(testData)))
		w.Write(testData)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	// Download into directory with trailing slash
	err := DownloadFile(context.Background(), server.URL+"/sample.iso", tempDir+"/", "", "", nil)
	if err != nil {
		t.Fatalf("DownloadFile into dir trailing slash failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(tempDir, "sample.iso"))
	if err != nil {
		t.Fatalf("failed to read downloaded file in dir: %v", err)
	}
	if string(content) != string(testData) {
		t.Errorf("content mismatch, got %s, want %s", string(content), string(testData))
	}

	// Download into existing directory without trailing slash
	subDir := filepath.Join(tempDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	err = DownloadFile(context.Background(), server.URL+"/another.iso", subDir, "", "", nil)
	if err != nil {
		t.Fatalf("DownloadFile into existing subdir failed: %v", err)
	}
	content2, err := os.ReadFile(filepath.Join(subDir, "another.iso"))
	if err != nil {
		t.Fatalf("failed to read downloaded file in subdir: %v", err)
	}
	if string(content2) != string(testData) {
		t.Errorf("content mismatch in subdir")
	}
}

func TestFormatBytes(t *testing.T) {
	if FormatBytes(500) != "500 B" {
		t.Errorf("FormatBytes(500) = %s, want 500 B", FormatBytes(500))
	}
	if FormatBytes(1024*1024) != "1.00 MB" {
		t.Errorf("FormatBytes(1MB) = %s, want 1.00 MB", FormatBytes(1024*1024))
	}
}
