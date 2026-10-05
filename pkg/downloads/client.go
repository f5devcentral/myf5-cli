package downloads

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"myf5-cli/pkg/client"
)

const (
	// DefaultDownloadsFQDN is the host this client contacts first, without a
	// `k` parameter.
	//
	// STATUS 2026-10-05 — WORKING. `myf5 downloads families` returns all 17
	// product families against this host (verified live 2026-10-05).
	//
	// HISTORY, because the earlier diagnosis here was wrong and the wrong
	// conclusion is an easy one to reach again:
	//
	// On 2026-10-02 every path against this host returned 404 with an EMPTY
	// body, including "/". That coincided with F5's cutover from ESDM
	// (Enterprise Software Download Manager) to ESDP (Enterprise Software
	// Distribution Platform) — scheduled to begin 2026-10-02 at 06:00 PT for
	// three days, with a 09:00 notification the same day confirming ESDM was
	// "now offline" — so the 404s were read as the service being down mid
	// migration, and this comment asserted the correct path was "still
	// unknown" and told the reader not to touch the client.
	//
	// That was a misdiagnosis. The 404s were OUR BUG: every path was missing
	// the gateway's leading /v1 segment. See DownloadsPrefix below for the
	// detail. The service was reachable; we were asking it for paths it does
	// not serve, and an empty-bodied 404 looks identical either way.
	//
	// The lesson worth keeping: an empty 404 from this gateway means "no such
	// route", and a plausible outage story is not evidence that the route is
	// right. Check the path before concluding the service is down.
	//
	// Access to ESDP post-go-live is requested separately per KB57950.
	//
	// CAUTION on provenance: comments here previously asserted an F5
	// "October 2, 2026 cutover" moving Downloads to this host and removing
	// `k`. No F5 bulletin supporting that has been located. The F5 articles
	// published on that date (K000163521, K15202) describe an IP ADDRESS
	// change for api.f5.com and callhome.f5.com — a firewall-allowlist
	// matter — and say nothing about base URLs or `k`. The host and the
	// absent `k` are both confirmed by observation (this host serves the
	// catalog and takes no `k`), but the bulletin said to justify them has
	// never been produced. Treat the cutover NARRATIVE as unsourced.
	DefaultDownloadsFQDN = "api.software.downloads.f5.com"

	// LegacySupportFQDN is the fallback gateway, tried when the host above
	// returns 403/404.
	//
	// The fallback has not been needed since the path fix of 2026-10-05: the
	// primary host serves the catalog directly. It is retained because it
	// costs nothing when unused, but note that it has never been observed
	// serving a successful Downloads response — every published `k` value was
	// rejected there with 400 "Missing or invalid k query parameter" during
	// 2026-10-02 probing. Do not assume it is a working safety net.
	LegacySupportFQDN = "support.apis.f5.com"

	// LegacyKParam is the `k` API usage key sent on the legacy fallback path.
	//
	// It is not a per-user secret: this value is published in F5's OpenAPI
	// specification (internal_docs/oas/openapi-outdated-5Oct2026.json) as the
	// rateLimitParam example, which
	// is also the example for the downloads metadata endpoint specifically.
	//
	// Do NOT assume keys are scoped per API family. This same value is
	// accepted by the Support Case endpoints (verified live 2026-10-02),
	// while all three published values — including this one — are currently
	// rejected by Downloads. An earlier comment claimed the reverse; it was
	// wrong.
	//
	// Override via F5_API_KEY or `api_key` in ~/.f5api_credentials if F5
	// rotates it.
	LegacyKParam = "UKKD3Vxv7NHrM3QmYk8Fk2mZnLtljAKX"
)

// Client handles interaction with the MyF5 Software Downloads API.
type Client struct {
	HTTPClient   *http.Client
	BaseFQDN     string
	Token        string
	AutoFallback bool

	// APIKey is the `k` usage key sent only on the legacy support.apis.f5.com
	// fallback path. Empty falls back to LegacyKParam — see its doc comment
	// for what that value is and is not.
	APIKey string
}

// String implements fmt.Stringer to ensure bearer tokens are never leaked in logs.
func (c *Client) String() string {
	if c == nil {
		return "<nil>"
	}
	tok := "[REDACTED]"
	if c.Token == "" {
		tok = "[EMPTY]"
	}
	return fmt.Sprintf("downloads.Client{BaseFQDN: %q, Token: %s, AutoFallback: %v}", c.BaseFQDN, tok, c.AutoFallback)
}

// GoString implements fmt.GoStringer.
func (c *Client) GoString() string {
	return c.String()
}

// NewClient initializes a MyF5 Downloads API client without an explicit API
// usage key. The key matters only on the legacy fallback path; prefer
// NewClientWithKey.
func NewClient(token string, baseFQDN string) *Client {
	return NewClientWithKey(token, baseFQDN, "")
}

// NewClientWithKey initializes a client carrying the caller's own `k` API
// usage key for the legacy fallback. An empty apiKey falls back to
// LegacyKParam.
func NewClientWithKey(token, baseFQDN, apiKey string) *Client {
	if baseFQDN == "" {
		baseFQDN = DefaultDownloadsFQDN
	}
	return &Client{
		HTTPClient:   client.NewSecureHTTPClient(30 * time.Second),
		BaseFQDN:     baseFQDN,
		Token:        token,
		AutoFallback: true,
		APIKey:       apiKey,
	}
}

// DownloadsPrefix is the path prefix for all Downloads endpoints.
//
// The doubled "v1" is NOT a typo. Verified live 2026-10-05 against
// api.software.downloads.f5.com:
//
//	/downloads-management/v1/downloads/metadata     -> 404 (empty body)
//	/v1/downloads-management/v1/downloads/metadata  -> 200
//
// The leading /v1 is the gateway's own route version and the inner /v1 is the
// API's. Omitting the leading segment 404s every Downloads call, which is what
// the pre-cutover code did. The shape matches the published OpenAPI document
// (internal_docs/oas/openapi-outdated-5Oct2026.json).
const DownloadsPrefix = "/v1/downloads-management/v1/downloads"

// GetMetadata fetches the complete product hierarchy and catalog index.
func (c *Client) GetMetadata(ctx context.Context) (*DownloadsMetadataResponse, error) {
	path := DownloadsPrefix + "/metadata"
	bodyBytes, err := c.doGetWithFallback(ctx, path)
	if err != nil {
		return nil, err
	}

	var resp DownloadsMetadataResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode metadata response: %w", err)
	}
	return &resp, nil
}

// GetProductVersions retrieves versions, containers, and file lists for a product line.
func (c *Client) GetProductVersions(ctx context.Context, family, line string) (*ProductVersionsResponse, error) {
	path := fmt.Sprintf(
		DownloadsPrefix+"/product/%s/%s",
		url.PathEscape(family),
		url.PathEscape(line),
	)
	bodyBytes, err := c.doGetWithFallback(ctx, path)
	if err != nil {
		return nil, err
	}

	var resp ProductVersionsResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode product versions response: %w", err)
	}
	return &resp, nil
}

// GetFileLinks fetches signed regional CDN download links for a specific file.
func (c *Client) GetFileLinks(ctx context.Context, family, line, version, container, fileName, language string) (*FileLinksResponse, error) {
	if language == "" {
		language = "english"
	}

	path := fmt.Sprintf(
		DownloadsPrefix+"/product/%s/%s/%s/%s/%s/%s",
		url.PathEscape(family),
		url.PathEscape(line),
		url.PathEscape(version),
		url.PathEscape(language),
		url.PathEscape(container),
		url.PathEscape(fileName),
	)
	bodyBytes, err := c.doGetWithFallback(ctx, path)
	if err != nil {
		return nil, err
	}

	var resp FileLinksResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode file links response: %w", err)
	}
	return &resp, nil
}

// doGetWithFallback executes a GET request against BaseFQDN without 'k'.
// If BaseFQDN returns 403 or 404, it automatically falls back to
// support.apis.f5.com with 'k'.
//
// Note that a 404 from the primary host is far more likely to be a wrong path
// than an unavailable service — that is what the missing leading /v1 produced
// for three days. The fallback will not rescue a malformed path: it sends the
// same path to a different host.
func (c *Client) doGetWithFallback(ctx context.Context, path string) ([]byte, error) {
	scheme := "https"
	fqdn := c.BaseFQDN
	if strings.HasPrefix(fqdn, "http://") {
		scheme = "http"
		fqdn = strings.TrimPrefix(fqdn, "http://")
	} else if strings.HasPrefix(fqdn, "https://") {
		fqdn = strings.TrimPrefix(fqdn, "https://")
	}

	primaryURL := fmt.Sprintf("%s://%s%s", scheme, fqdn, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, primaryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")

	resp, err := c.HTTPClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		// Bound JSON catalog API responses to 10 MB maximum to guard runtime heap
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		if readErr == nil && resp.StatusCode == http.StatusOK {
			return body, nil
		}

		// Check if we should fallback
		if c.AutoFallback && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound) && !strings.Contains(fqdn, LegacySupportFQDN) {
			// Try fallback
			return c.doLegacyFallback(ctx, path)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("request to %s failed with status %d: %s", primaryURL, resp.StatusCode, string(body))
		}
	}

	if c.AutoFallback && !strings.Contains(fqdn, LegacySupportFQDN) {
		return c.doLegacyFallback(ctx, path)
	}

	return nil, err
}

func (c *Client) doLegacyFallback(ctx context.Context, path string) ([]byte, error) {
	apiKey := c.APIKey
	if apiKey == "" {
		apiKey = LegacyKParam
	}
	fallbackURL := fmt.Sprintf("https://%s%s?k=%s", LegacySupportFQDN, path, apiKey)
	redactedURL := fmt.Sprintf("https://%s%s?k=[REDACTED]", LegacySupportFQDN, path)

	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fallbackURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create fallback request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "myf5-cli-go/1.0.0 (Pre-Cutover Fallback)")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fallback request to %s failed: %w", redactedURL, err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read fallback response: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			return body, nil
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			time.Sleep(time.Duration(attempt+2) * time.Second)
			continue
		}

		return nil, fmt.Errorf("fallback to %s failed with status %d: %s", redactedURL, resp.StatusCode, string(body))
	}

	return nil, fmt.Errorf("fallback request to %s failed after rate limit retries", redactedURL)
}
