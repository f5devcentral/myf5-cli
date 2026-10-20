package ihealth

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestListQKViewsFetchesDetailPerID verifies that ListQKViews negotiates JSON
// via the +json vendor media type and populates summary metadata by fetching
// each QKView detail resource.
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
		// Verify the unversioned collection path per K000130498.
		if r.URL.Path != QKViewsPath {
			t.Errorf("path = %s, want %s", r.URL.Path, QKViewsPath)
		}
		if got := r.Header.Get("Accept"); got != IHealthAccept {
			t.Errorf("Accept = %q, want %q (the API versions by media type, not URL)", got, IHealthAccept)
		}
		if r.Header.Get("Authorization") != "Bearer test-tok" {
			t.Errorf("unexpected auth header")
		}

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
		}
		if got := r.FormValue("visible_in_gui"); got != "True" {
			t.Errorf("visible_in_gui = %q, want True (K000130498)", got)
		}
		if got := r.FormValue("f5_support_case"); got != "01234567" {
			t.Errorf("f5_support_case = %q, want 01234567", got)
		}
		if got := r.FormValue("case_number"); got != "01234567" {
			t.Errorf("case_number = %q, want 01234567", got)
		}
		if got := r.FormValue("description"); got != "Test upload" {
			t.Errorf("description = %q, want 'Test upload'", got)
		}
		file, header, err := r.FormFile("qkview")
		if err != nil {
			t.Errorf("missing qkview form file: %v", err)
		} else {
			file.Close()
			if header.Filename != "test.qkview" {
				t.Errorf("filename = %q, want test.qkview", header.Filename)
			}
		}

		// The live API returns an XML-derived JSON wrapper with integer id.
		w.Header().Set("Content-Type", "application/vnd.f5.ihealth.api+json")
		w.Header().Set("Location", "https://ihealth-api.f5.com/qkview-analyzer/api/qkviews/26991271")
		_, _ = w.Write([]byte(`{"qkview_upload":{"result":"OK","id":26991271,"location":"https://ihealth-api.f5.com/qkview-analyzer/api/qkviews/26991271"}}`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	sampleQKView := filepath.Join(tempDir, "test.qkview")
	if err := os.WriteFile(sampleQKView, []byte("fake qkview content"), 0600); err != nil {
		t.Fatal(err)
	}

	c := NewClient("test-tok", server.URL)
	c.HTTPClient = server.Client()

	resp, err := c.UploadQKView(context.Background(), sampleQKView, "01234567", "Test upload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "26991271" || resp.Status != "OK" {
		t.Errorf("unexpected upload response: %+v", resp)
	}
}

// A failed upload must not strand the goroutine feeding the request body.
//
// The body is produced by a writer goroutine through an io.Pipe. If the
// request fails and nobody closes the read half, that goroutine blocks
// forever on its next Write, holding the open QKView file with it. QKViews
// are large and uploads are long, so a mid-transfer network failure is the
// likely case, not the exotic one.
//
// The leak is invisible to a test that only checks the returned error, so
// this one counts goroutines after forcing a failure.
func TestUploadQKViewReleasesWriterOnRequestFailure(t *testing.T) {
	// A server that hangs up immediately, so Do() fails mid-request.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("closing connection to force a client-side failure")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	defer server.Close()

	tempDir := t.TempDir()
	sampleQKView := filepath.Join(tempDir, "big.qkview")
	// Comfortably larger than the 64 KiB copy buffer, so the writer goroutine
	// is still mid-loop when the request collapses.
	if err := os.WriteFile(sampleQKView, make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}

	c := NewClient("test-tok", server.URL)
	c.HTTPClient = server.Client()

	before := runtime.NumGoroutine()

	if _, err := c.UploadQKView(context.Background(), sampleQKView, "", ""); err == nil {
		t.Fatal("expected an error when the server drops the connection")
	}

	// Give the writer goroutine a moment to observe the closed pipe and exit.
	var after int
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		after = runtime.NumGoroutine()
		if after <= before {
			return
		}
	}
	t.Errorf("goroutine leaked after a failed upload: %d before, %d after", before, after)
}

func TestParseUploadResponse(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		location   string
		wantID     string
		wantStatus string
	}{
		{
			name:       "live XML-mapped JSON wrapper with integer id",
			body:       `{"qkview_upload":{"result":"OK","id":26991271,"location":"https://ihealth-api.f5.com/qkview-analyzer/api/qkviews/26991271"}}`,
			wantID:     "26991271",
			wantStatus: "OK",
		},
		{
			name:       "XML-mapped JSON wrapper with string id",
			body:       `{"qkview_upload":{"result":"OK","id":"26991271","location":"https://ihealth-api.f5.com/qkview-analyzer/api/qkviews/26991271"}}`,
			wantID:     "26991271",
			wantStatus: "OK",
		},
		{
			name:       "flat JSON response with string id and status",
			body:       `{"id":"12345","status":"queued"}`,
			wantID:     "12345",
			wantStatus: "queued",
		},
		{
			name:       "flat JSON response with numeric id and result",
			body:       `{"id":98765,"result":"OK"}`,
			wantID:     "98765",
			wantStatus: "OK",
		},
		{
			name:       "Location header fallback when body is empty",
			body:       "",
			location:   "https://ihealth-api.f5.com/qkview-analyzer/api/qkviews/34567",
			wantID:     "34567",
			wantStatus: "OK",
		},
		{
			name:       "XML body fallback",
			body:       `<?xml version="1.0"?><qkview_upload><result>OK</result><id>54321</id></qkview_upload>`,
			wantID:     "54321",
			wantStatus: "OK",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := parseUploadResponse([]byte(tc.body), tc.location)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.ID != tc.wantID {
				t.Errorf("ID = %q, want %q", resp.ID, tc.wantID)
			}
			if resp.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", resp.Status, tc.wantStatus)
			}
		})
	}
}

func TestGetQKViewHTTP202Accepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != QKViewsPath+"/26991271" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// HTTP 202 Accepted returned while QKView processing status is RUNNING.
		w.Header().Set("Content-Type", "application/vnd.f5.ihealth.api+json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{
			"file_name": "test.qkview",
			"hostname": "test-box.local",
			"gui_uri": null,
			"f5_support_case": "01234567",
			"processing_status": "RUNNING",
			"file_size": 43027134,
			"upload": {"date": 1791245779000, "performed_by": {"name": "Test User", "email": "test@example.com"}}
		}`))
	}))
	defer server.Close()

	c := NewClient("test-tok", server.URL)
	c.HTTPClient = server.Client()

	summary, err := c.GetQKView(context.Background(), "26991271")
	if err != nil {
		t.Fatalf("GetQKView failed on HTTP 202: %v", err)
	}

	if summary.Status != "RUNNING" {
		t.Errorf("Status = %q, want RUNNING", summary.Status)
	}
	if summary.FileName != "test.qkview" {
		t.Errorf("FileName = %q, want test.qkview", summary.FileName)
	}
	if summary.CaseNumber != "01234567" {
		t.Errorf("CaseNumber = %q, want 01234567", summary.CaseNumber)
	}
	// Fallback GUI URL should be populated when gui_uri is null
	wantGUI := "https://ihealth.f5.com/qkview-analyzer/qv/26991271"
	if summary.GUIURL != wantGUI {
		t.Errorf("GUIURL = %q, want fallback %q", summary.GUIURL, wantGUI)
	}
}
