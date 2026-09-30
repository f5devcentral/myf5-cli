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

		resp := ListCasesResponse{
			Data: []CaseSummary{
				{CaseNumber: "C10001", Subject: "SSL handshake failure", Status: "Open", Severity: "Sev 2"},
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
