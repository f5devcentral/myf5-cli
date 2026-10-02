package cases

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListCases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/case-management/v1/cases" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("k") != DefaultKParam {
			t.Errorf("expected k param %s, got %s", DefaultKParam, r.URL.Query().Get("k"))
		}
		if r.Header.Get("Authorization") != "Bearer case-tok" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		// `type` is REQUIRED by the spec. Omitting it returns
		// 400 "Missing type in the query params" from the real API, but this
		// test server answered 200 regardless, so the bug shipped. Assert the
		// request the API actually demands, not merely that one was made.
		if got := r.URL.Query().Get("type"); got != TypeAllOpenCases {
			t.Errorf("type = %q, want %q", got, TypeAllOpenCases)
		}

		resp := ListCasesResponse{
			Data: []CaseSummary{
				{CaseNumber: "C10001", Subject: "SSL handshake failure", Status: "Open", Priority: "2 - Site At Risk"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient("case-tok", server.URL)
	c.HTTPClient = server.Client()

	list, err := c.ListCases(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0].CaseNumber != "C10001" {
		t.Errorf("unexpected cases returned: %+v", list)
	}
}

// TestListCasesDecodesLiveFieldNames guards against struct tags that match
// nothing in the real payload. The round-trip test above encodes the response
// from the same Go struct it decodes into, so a wrong tag is symmetric and
// invisible — `severity` and `createdDate` both passed that test for the
// project's whole history while always decoding as empty. Decoding a literal
// payload shaped like GetCaseApiResponse is what catches it.
func TestListCasesDecodesLiveFieldNames(t *testing.T) {
	payload := `{"data":[{"caseNumber":"01287164","subject":"RFE: tmipsecd","status":"Resolution Provided","priority":"3 - Site Not At Risk","dateOpened":"2025-09-12"}]}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(payload)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}))
	defer server.Close()

	c := NewClient("tok", server.URL)
	c.HTTPClient = server.Client()

	list, err := c.ListCases(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list.Data) != 1 {
		t.Fatalf("expected 1 case, got %d", len(list.Data))
	}
	got := list.Data[0]
	if got.Priority != "3 - Site Not At Risk" {
		t.Errorf("Priority = %q; a tag mismatch decodes as empty", got.Priority)
	}
	if got.DateOpened != "2025-09-12" {
		t.Errorf("DateOpened = %q; a tag mismatch decodes as empty", got.DateOpened)
	}
	if got.Status != "Resolution Provided" {
		t.Errorf("Status = %q", got.Status)
	}
}
