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
	// DefaultDownloadsFQDN is the primary F5 Software Downloads API gateway hostname.
	// Requests against this host authenticate via OAuth2 Bearer token without a `k` query parameter.
	DefaultDownloadsFQDN = "api.software.downloads.f5.com"

	// LegacySupportFQDN is the fallback API gateway hostname tried when the primary
	// host returns HTTP 403 or 404 and AutoFallback is enabled.
	LegacySupportFQDN = "support.apis.f5.com"

	// LegacyKParam is the default `k` API usage key sent on the legacy fallback path.
	// This public rate-limit identifier is defined in F5's OpenAPI specification and
	// can be overridden via F5_API_KEY or `api_key` in ~/.f5api_credentials.
	LegacyKParam = "UKKD3Vxv7NHrM3QmYk8Fk2mZnLtljAKX"
)

// Client handles interaction with the MyF5 Software Downloads API.
type Client struct {
	HTTPClient   *http.Client
	BaseFQDN     string
	Token        string
	AutoFallback bool

	// APIKey is the `k` usage key sent only on the legacy support.apis.f5.com
	// fallback path. Empty falls back to LegacyKParam.
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

// Zero clears bearer tokens and API keys from memory.
func (c *Client) Zero() {
	if c == nil {
		return
	}
	c.Token = ""
	c.APIKey = ""
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

// DownloadsPrefix is the gateway path prefix for all Software Downloads API endpoints.
// The leading /v1 is the API gateway route version and the inner /v1 is the service version.
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
		// If cold 403, ESDP API gateway often requires an active product line session
		// before issuing signed download links. Warm the session and retry once.
		if strings.Contains(err.Error(), "403") {
			_, _ = c.GetProductVersions(ctx, family, line)
			bodyBytes, err = c.doGetWithFallback(ctx, path)
		}
		if err != nil {
			return nil, err
		}
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
// Note that a 404 from the primary host may indicate an invalid request path
// rather than an unavailable service; the fallback sends the same path to the
// legacy host.
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
		req.Header.Set("User-Agent", "myf5-cli-go/1.0.0 (Legacy Fallback)")

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
			backoff := time.Duration(attempt+2) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}

		return nil, fmt.Errorf("fallback to %s failed with status %d: %s", redactedURL, resp.StatusCode, string(body))
	}

	return nil, fmt.Errorf("fallback request to %s failed after rate limit retries", redactedURL)
}
