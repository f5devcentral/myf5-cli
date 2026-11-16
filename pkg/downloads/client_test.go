package downloads

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockServer struct {
	URL    string
	client *http.Client
}

func (m *mockServer) Close()               {}
func (m *mockServer) Client() *http.Client { return m.client }

func newMockServer(handler http.Handler) *mockServer {
	return &mockServer{
		URL: "http://mock.downloads.f5.invalid",
		client: &http.Client{
			Transport: &mockRoundTripper{
				fn: func(req *http.Request) (*http.Response, error) {
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					return rec.Result(), nil
				},
			},
		},
	}
}

func TestGetMetadata(t *testing.T) {
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The leading /v1 is required by the gateway. Serving the path without
		// it returns 404 live, so this assertion pins the doubled-v1 shape
		// deliberately; see DownloadsPrefix.
		if r.URL.Path != "/v1/downloads-management/v1/downloads/metadata" {
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
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

type mockRoundTripper struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.fn(req)
}

func TestDownloadsClientMethods(t *testing.T) {
	var nilClient *Client
	if nilClient.String() != "<nil>" {
		t.Errorf("nil Client.String() = %q, want <nil>", nilClient.String())
	}
	nilClient.Zero()

	c := NewClientWithKey("tok123", "downloads.example.com", "key123")
	if !strings.Contains(c.String(), "downloads.example.com") {
		t.Errorf("String() missing BaseFQDN: %s", c.String())
	}
	if strings.Contains(c.String(), "tok123") {
		t.Errorf("String() leaked token: %s", c.String())
	}
	if c.GoString() != c.String() {
		t.Errorf("GoString() != String()")
	}

	cEmpty := NewClient("", "")
	if !strings.Contains(cEmpty.String(), "[EMPTY]") {
		t.Errorf("String() should show [EMPTY] for empty token")
	}
	if cEmpty.BaseFQDN != DefaultDownloadsFQDN {
		t.Errorf("BaseFQDN = %s, want %s", cEmpty.BaseFQDN, DefaultDownloadsFQDN)
	}

	c.Zero()
	if c.Token != "" || c.APIKey != "" {
		t.Errorf("Zero() did not clear tokens")
	}
}

func TestGetFileLinks(t *testing.T) {
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "BIG-IP") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		resp := FileLinksResponse{
			Filename: "BIGIP.iso",
			DownloadLinks: []DownloadLink{
				{Href: "https://cdn.example.com/BIGIP.iso", Region: "us-east"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient("tok", server.URL)
	c.HTTPClient = server.Client()

	resp, err := c.GetFileLinks(context.Background(), "BIG-IP", "v17", "17.1.0", "Release", "BIGIP.iso", "")
	if err != nil {
		t.Fatalf("GetFileLinks failed: %v", err)
	}
	if len(resp.DownloadLinks) != 1 || resp.DownloadLinks[0].Region != "us-east" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestDoLegacyFallback(t *testing.T) {
	c := NewClientWithKey("tok", "http://primary.invalid", "my-k-param")
	c.HTTPClient = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(req *http.Request) (*http.Response, error) {
				if !strings.Contains(req.URL.String(), LegacySupportFQDN) {
					t.Errorf("expected legacy URL %s, got %s", LegacySupportFQDN, req.URL.String())
				}
				if req.URL.Query().Get("k") != "my-k-param" {
					t.Errorf("expected k=my-k-param, got %s", req.URL.Query().Get("k"))
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	body, err := c.doLegacyFallback(context.Background(), "/test/path")
	if err != nil {
		t.Fatalf("doLegacyFallback failed: %v", err)
	}
	if string(body) != `{"status":"ok"}` {
		t.Errorf("got %s, want {\"status\":\"ok\"}", string(body))
	}

	c2 := NewClient("tok", "http://primary.invalid")
	c2.HTTPClient = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(req *http.Request) (*http.Response, error) {
				if req.URL.Query().Get("k") != LegacyKParam {
					t.Errorf("expected default LegacyKParam, got %s", req.URL.Query().Get("k"))
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"status":"default"}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}
	body2, err := c2.doLegacyFallback(context.Background(), "/test/path")
	if err != nil {
		t.Fatalf("doLegacyFallback failed: %v", err)
	}
	if string(body2) != `{"status":"default"}` {
		t.Errorf("got %s, want {\"status\":\"default\"}", string(body2))
	}
}

func TestDownloadsModels(t *testing.T) {
	pl := ProductLine{Name: "v17", DisplayName: "Version 17.x"}
	if pl.GetTitle() != "Version 17.x" {
		t.Errorf("ProductLine.GetTitle() = %s, want Version 17.x", pl.GetTitle())
	}
	plEmpty := ProductLine{Name: "v17"}
	if plEmpty.GetTitle() != "v17" {
		t.Errorf("ProductLine.GetTitle() = %s, want v17", plEmpty.GetTitle())
	}

	pf := ProductFamily{Name: "BIG-IP", DisplayName: "F5 BIG-IP"}
	if pf.GetTitle() != "F5 BIG-IP" {
		t.Errorf("ProductFamily.GetTitle() = %s, want F5 BIG-IP", pf.GetTitle())
	}
	pfEmpty := ProductFamily{Name: "BIG-IP"}
	if pfEmpty.GetTitle() != "BIG-IP" {
		t.Errorf("ProductFamily.GetTitle() = %s, want BIG-IP", pfEmpty.GetTitle())
	}

	pvr1 := ProductVersionsResponse{Versions: []ProductVersion{{Name: "17.1.0"}}}
	if len(pvr1.GetVersions()) != 1 {
		t.Errorf("GetVersions() len = %d, want 1", len(pvr1.GetVersions()))
	}
	pvr2 := ProductVersionsResponse{}
	pvr2.Data.ProductVersions = []ProductVersion{{Name: "16.1.0"}}
	if len(pvr2.GetVersions()) != 1 || pvr2.GetVersions()[0].Name != "16.1.0" {
		t.Errorf("GetVersions() from Data failed")
	}

	dl1 := DownloadLink{Region: "us-west", Location: "Oregon"}
	if dl1.GetRegion() != "us-west" {
		t.Errorf("GetRegion() = %s, want us-west", dl1.GetRegion())
	}
	dl2 := DownloadLink{Location: "Frankfurt"}
	if dl2.GetRegion() != "Frankfurt" {
		t.Errorf("GetRegion() = %s, want Frankfurt", dl2.GetRegion())
	}

	var nilDl *DownloadLink
	if _, ok := nilDl.ExpirationTime(); ok {
		t.Errorf("nilDl.ExpirationTime() ok = true, want false")
	}
	dlRFC := DownloadLink{ExpiresAt: "2030-01-01T00:00:00Z"}
	if _, ok := dlRFC.ExpirationTime(); !ok {
		t.Errorf("RFC3339 ExpirationTime failed")
	}
	dlNoTZ := DownloadLink{ExpiresAt: "2030-01-01T00:00:00"}
	if _, ok := dlNoTZ.ExpirationTime(); !ok {
		t.Errorf("NoTZ ExpirationTime failed")
	}
	dlUnix := DownloadLink{ExpiresAt: "1893456000"}
	if _, ok := dlUnix.ExpirationTime(); !ok {
		t.Errorf("Unix ExpirationTime failed")
	}
	dlAWS := DownloadLink{Href: "https://cdn.example.com/file.iso?Expires=1893456000"}
	if _, ok := dlAWS.ExpirationTime(); !ok {
		t.Errorf("AWS Query ExpirationTime failed")
	}
	dlAmz := DownloadLink{Href: "https://cdn.example.com/file.iso?X-Amz-Date=20300101T000000Z&X-Amz-Expires=3600"}
	if _, ok := dlAmz.ExpirationTime(); !ok {
		t.Errorf("AWS X-Amz ExpirationTime failed")
	}

	flr1 := FileLinksResponse{DownloadLinks: []DownloadLink{{Region: "eu-central"}}}
	if len(flr1.GetLinks()) != 1 {
		t.Errorf("GetLinks() from root failed")
	}
	flr2 := FileLinksResponse{}
	flr2.Data.DownloadLinks = []DownloadLink{{Region: "ap-southeast"}}
	if len(flr2.GetLinks()) != 1 {
		t.Errorf("GetLinks() from Data failed")
	}

	flrHashes := FileLinksResponse{
		MD5: "d41d8cd98f00b204e9800998ecf8427e",
		Meta: map[string]any{
			"sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}
	flrHashes.Data.Hashes = []HashItem{{Algorithm: "sha512", Value: "cf83e135"}}
	hashes := flrHashes.GetAllHashes()
	if len(hashes) != 3 {
		t.Errorf("GetAllHashes() count = %d, want 3", len(hashes))
	}
}
