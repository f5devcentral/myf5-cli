package cases

import (
	"bytes"
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
	DefaultSupportFQDN = "support.apis.f5.com"
	DefaultKParam      = "UKKD3Vxv7NHrM3QmYk8Fk2mZnLtljAKX"
)

// Client interacts with MyF5 Support Case Management APIs.
type Client struct {
	HTTPClient *http.Client
	BaseFQDN   string
	Token      string
	KParam     string
}

// String implements fmt.Stringer to ensure bearer tokens and keys are never leaked in logs.
func (c *Client) String() string {
	if c == nil {
		return "<nil>"
	}
	tok := "[REDACTED]"
	if c.Token == "" {
		tok = "[EMPTY]"
	}
	return fmt.Sprintf("cases.Client{BaseFQDN: %q, Token: %s, KParam: [REDACTED]}", c.BaseFQDN, tok)
}

// GoString implements fmt.GoStringer.
func (c *Client) GoString() string {
	return c.String()
}

// NewClient initializes a new MyF5 Support Case client.
func NewClient(token string, baseFQDN string) *Client {
	if baseFQDN == "" {
		baseFQDN = DefaultSupportFQDN
	}
	return &Client{
		HTTPClient: client.NewSecureHTTPClient(30 * time.Second),
		BaseFQDN:   baseFQDN,
		Token:      token,
		KParam:     DefaultKParam,
	}
}

// GetMetadata retrieves valid case creation schema metadata (products, versions, severities).
func (c *Client) GetMetadata(ctx context.Context) (*CaseMetadataResponse, error) {
	path := "/case-management/v1/cases/metadata"
	bodyBytes, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp CaseMetadataResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode case metadata: %w", err)
	}
	return &resp, nil
}

// ListCases returns active (or all) support cases.
func (c *Client) ListCases(ctx context.Context, showClosed bool) (*ListCasesResponse, error) {
	path := "/case-management/v1/cases"
	if showClosed {
		path += "?status=all"
	}
	bodyBytes, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp ListCasesResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode cases list: %w", err)
	}
	return &resp, nil
}

// CreateCase submits a support ticket to MyF5.
func (c *Client) CreateCase(ctx context.Context, payload map[string]interface{}) (*CreateCaseResponse, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode case payload: %w", err)
	}

	path := "/case-management/v1/cases"
	bodyBytes, err := c.doRequest(ctx, http.MethodPost, path, jsonBytes)
	if err != nil {
		return nil, err
	}

	var resp CreateCaseResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode create case response: %w", err)
	}
	return &resp, nil
}

// AddComment appends notes or diagnostics to an existing case.
func (c *Client) AddComment(ctx context.Context, caseNumber, comment string) (*AddCommentResponse, error) {
	payload := map[string]string{
		"comment": comment,
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode comment payload: %w", err)
	}

	path := fmt.Sprintf("/case-management/v1/cases/%s/notes", url.PathEscape(caseNumber))
	bodyBytes, err := c.doRequest(ctx, http.MethodPost, path, jsonBytes)
	if err != nil {
		return nil, err
	}

	var resp AddCommentResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode add comment response: %w", err)
	}
	return &resp, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	scheme := "https"
	fqdn := c.BaseFQDN
	if strings.HasPrefix(fqdn, "http://") {
		scheme = "http"
		fqdn = strings.TrimPrefix(fqdn, "http://")
	} else if strings.HasPrefix(fqdn, "https://") {
		fqdn = strings.TrimPrefix(fqdn, "https://")
	}

	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	fullURL := fmt.Sprintf("%s://%s%s%sk=%s", scheme, fqdn, path, sep, c.KParam)
	redactedURL := fmt.Sprintf("%s://%s%s%sk=[REDACTED]", scheme, fqdn, path, sep)

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "myf5-api-go/1.0.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", redactedURL, err)
	}
	defer resp.Body.Close()
	// Bound Case API responses to 10 MB maximum
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request to %s failed with status %d: %s", redactedURL, resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}
