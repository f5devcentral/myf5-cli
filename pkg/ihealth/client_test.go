package ihealth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The list endpoint returns ONLY ids, and the vendor media type needs its
// +json suffix or the API answers 200 with XML. Both were discovered live on
// 2026-10-02 and neither is inferable from the Go code, so they are pinned
// here against a server that behaves like the real one.
func TestListQKViewsFetchesDetailPerID(t *testing.T) {
	var sawAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAccept = r.Header.Get("Accept")

		// A server that honours the header the way iHealth does: the bare
		// vendor type yields XML, which must not be what the client asks for.
		if r.Header.Get("Accept") == "application/vnd.f5.ihealth.api" {
			w.Header().Set("Content-Type", "application/vnd.f5.ihealth.api")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><qkview_ids><id>26894783</id></qkview_ids>`))
			return
		}

		w.Header().Set("Content-Type", "application/vnd.f5.ihealth.api+json")
		switch r.URL.Path {
		case QKViewsPath:
			_, _ = w.Write([]byte(`{"id":["26894783"]}`))
		case QKViewsPath + "/26894783":
			_, _ = w.Write([]byte(`{"file_name":"test_diag.qkview","hostname":"host.example","gui_uri":"https://ihealth.f5.com/qkview-analyzer/qv/26894783","f5_support_case":"","processing_status":"COMPLETE","file_size":43928363,"upload":{"date":1789750903000}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient("tok", server.URL)
	c.HTTPClient = server.Client()

	got, err := c.ListQKViews(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawAccept != IHealthAccept {
		t.Errorf("Accept = %q, want %q", sawAccept, IHealthAccept)
	}
	if !strings.HasSuffix(IHealthAccept, "+json") {
		t.Errorf("IHealthAccept %q lacks the +json suffix; the API returns XML without it", IHealthAccept)
	}
	if len(got) != 1 {
		t.Fatalf("got %d summaries, want 1", len(got))
	}

	// Metadata comes from the detail resource — the collection carries none,
	// so an empty FileName here means the per-id fetch was skipped.
	q := got[0]
	if q.ID != "26894783" {
		t.Errorf("ID = %q", q.ID)
	}
	if q.FileName != "test_diag.qkview" {
		t.Errorf("FileName = %q; detail fetch did not populate it", q.FileName)
	}
	if q.Status != "COMPLETE" {
		t.Errorf("Status = %q, want COMPLETE (from processing_status)", q.Status)
	}
	if q.Hostname != "host.example" {
		t.Errorf("Hostname = %q", q.Hostname)
	}
	// Epoch milliseconds, not seconds: as seconds this lands in 2026-09-18.
	if !strings.HasPrefix(q.UploadDate, "2026-09-18") {
		t.Errorf("UploadDate = %q, want a 2026-09-18 timestamp decoded from epoch ms", q.UploadDate)
	}
}

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
