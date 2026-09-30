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

	"myf5-api/pkg/client"
)

const (
	// DefaultDownloadsFQDN is the authoritative endpoint effective October 2, 2026.
	// Does NOT accept the legacy 'k' parameter.
	DefaultDownloadsFQDN = "api.software.downloads.f5.com"

	// LegacySupportFQDN is the fallback gateway used prior to October 2.
	LegacySupportFQDN = "support.apis.f5.com"

	// LegacyKParam is the query rate-limiting parameter required ONLY on support.apis.f5.com.
	LegacyKParam = "UKKD3Vxv7NHrM3QmYk8Fk2mZnLtljAKX"
)

// Client handles interaction with the MyF5 Software Downloads API.
type Client struct {
	HTTPClient  *http.Client
	BaseFQDN    string
	Token       string
	AutoFallback bool
}

// NewClient initializes a MyF5 Downloads API client.
func NewClient(token string, baseFQDN string) *Client {
	if baseFQDN == "" {
		baseFQDN = DefaultDownloadsFQDN
	}
	return &Client{
		HTTPClient:  client.NewSecureHTTPClient(30 * time.Second),
		BaseFQDN:    baseFQDN,
		Token:       token,
		AutoFallback: true,
	}
}

// GetMetadata fetches the complete product hierarchy and catalog index.
func (c *Client) GetMetadata(ctx context.Context) (*DownloadsMetadataResponse, error) {
	path := "/downloads-management/v1/downloads/metadata"
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
		"/downloads-management/v1/downloads/product/%s/%s",
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
		"/downloads-management/v1/downloads/product/%s/%s/%s/%s/%s/%s",
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
// If BaseFQDN returns CloudFront 403 or 404 (common before the Oct 2 cutover),
// it automatically falls back to support.apis.f5.com with 'k'.
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
	req.Header.Set("User-Agent", "myf5-api-go/1.0.0")

	resp, err := c.HTTPClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
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
	fallbackURL := fmt.Sprintf("https://%s%s?k=%s", LegacySupportFQDN, path, LegacyKParam)

	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fallbackURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create fallback request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "myf5-api-go/1.0.0 (Pre-Cutover Fallback)")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fallback request to %s failed: %w", fallbackURL, err)
		}

		body, err := io.ReadAll(resp.Body)
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

		return nil, fmt.Errorf("fallback to %s failed with status %d: %s", fallbackURL, resp.StatusCode, string(body))
	}

	return nil, fmt.Errorf("fallback request to %s failed after rate limit retries", fallbackURL)
}
