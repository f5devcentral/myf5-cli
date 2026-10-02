package ihealth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadQKView(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		// No "v1" segment: the live API 404s on /api/v1/qkviews. This test
		// previously pinned that broken path, so it passed while the command
		// could not work at all. Assert the documented contract (K000130498)
		// rather than whatever the code happened to send.
		if r.URL.Path != QKViewsPath {
			t.Errorf("path = %s, want %s", r.URL.Path, QKViewsPath)
		}
		if got := r.Header.Get("Accept"); got != IHealthAccept {
			t.Errorf("Accept = %q, want %q (the API versions by media type, not URL)", got, IHealthAccept)
		}
		if r.Header.Get("Authorization") != "Bearer test-tok" {
			t.Errorf("unexpected auth header")
		}

		resp := UploadQKViewResponse{
			ID:     "12345",
			Status: "queued",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	sampleQKView := filepath.Join(tempDir, "test.qkview")
	if err := os.WriteFile(sampleQKView, []byte("fake qkview content"), 0600); err != nil {
		t.Fatal(err)
	}

	c := NewClient("test-tok", server.URL)
	c.HTTPClient = server.Client()

	resp, err := c.UploadQKView(context.Background(), sampleQKView, "C12345", "Test upload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "12345" || resp.Status != "queued" {
		t.Errorf("unexpected upload response: %+v", resp)
	}
}
