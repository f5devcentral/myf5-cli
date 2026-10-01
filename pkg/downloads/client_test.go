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

func TestContainerCategories(t *testing.T) {
	rawJSON := `{
		"name": "17.5.1_Virtual-Edition",
		"type": "Release",
		"description": "Virtual Edition Images",
		"releaseDate": "2024-05-15",
		"files": [
			{"name": "BIGIP-17.5.1-0.0.8.ALL-init.ova", "fileSize": 3307290624}
		]
	}`

	var c Container
	if err := json.Unmarshal([]byte(rawJSON), &c); err != nil {
		t.Fatalf("failed to unmarshal container: %v", err)
	}

	if c.Name != "17.5.1_Virtual-Edition" {
		t.Errorf("expected container name 17.5.1_Virtual-Edition, got %s", c.Name)
	}
	if c.Type != "Release" {
		t.Errorf("expected container type Release, got %s", c.Type)
	}
	if c.Description != "Virtual Edition Images" {
		t.Errorf("expected container description Virtual Edition Images, got %s", c.Description)
	}
	if c.GetCategory() != "Virtual Edition (VE)" {
		t.Errorf("expected category Virtual Edition (VE), got %s", c.GetCategory())
	}

	// Test category classifications
	testCases := []struct {
		name     string
		cType    string
		expected string
	}{
		{"17.5.1", "Release", "Base Software & ISO Releases"},
		{"17.5.1_Virtual-Edition", "Release", "Virtual Edition (VE)"},
		{"17.5.1_Tenant_F5OS", "Release", "F5OS Tenant Images"},
		{"17.5.1.6_Tenant_F5OS_Hotfix", "Release", "Hotfixes & Patches"},
		{"GeoLocationUpdates_Pulse", "Patches", "Tools, Telemetry & Diagnostics"},
		{"AWS-Cloud", "Release", "Public Cloud Images"},
		{"Azure-Cloud", "Release", "Public Cloud Images"},
		{"Google-Cloud", "Release", "Public Cloud Images"},
		{"ASM-AttackSignaturesUpdates", "Release", "Security Signatures & Attack Updates"},
		{"BotSignaturesUpdates-Archive", "Release", "Historical Archives"},
		{"ProtocolInspection-LatestUpdate", "Upgrade", "Security Signatures & Attack Updates"},
		{"F5_System_Scanner", "Patches", "Tools, Telemetry & Diagnostics"},
		{"Open-Source", "Release", "Tools, Telemetry & Diagnostics"},
	}

	for _, tc := range testCases {
		cont := Container{Name: tc.name, Type: tc.cType}
		got := cont.GetCategory()
		if got != tc.expected {
			t.Errorf("container %s (%s): expected category %q, got %q", tc.name, tc.cType, tc.expected, got)
		}
	}
}
