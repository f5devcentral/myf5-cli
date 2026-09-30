package downloads

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/downloads-management/v1/downloads/metadata" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-tok" {
			t.Errorf("missing or bad auth header")
		}
		// Confirm NO 'k' query param sent
		if r.URL.Query().Get("k") != "" {
			t.Errorf("k query param must not be sent to downloads api")
		}

		resp := DownloadsMetadataResponse{}
		resp.Data.ProductFamilies = []ProductFamily{
			{Name: "BIG-IP", DisplayName: "BIG-IP"},
			{Name: "NGINX", DisplayName: "NGINX"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient("test-tok", server.URL)
	c.HTTPClient = server.Client()

	meta, err := c.GetMetadata(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta.Data.ProductFamilies) != 2 {
		t.Errorf("expected 2 product families, got %d", len(meta.Data.ProductFamilies))
	}
}

func TestGetProductVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/BIG-IP/big-ip_v17.x") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		resp := ProductVersionsResponse{}
		resp.Data.ProductVersions = []ProductVersion{
			{Name: "17.1.1", ReleaseDate: "2024-05-15"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient("test-tok", server.URL)
	c.HTTPClient = server.Client()

	ver, err := c.GetProductVersions(context.Background(), "BIG-IP", "big-ip_v17.x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ver.Data.ProductVersions) != 1 || ver.Data.ProductVersions[0].Name != "17.1.1" {
		t.Errorf("unexpected versions response: %+v", ver)
	}
}
