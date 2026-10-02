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

	"myf5-cli/pkg/client"
)

const (
	DefaultSupportFQDN = "support.apis.f5.com"

	// DefaultKParam is a legacy fallback for the required `k` API usage key.
	//
	// It is NOT a usable default: the key is issued per entitlement from the
	// MyF5 API Catalog, so a single shared constant cannot be valid for every
	// caller. Requests carrying it are rejected with HTTP 403 and
	// "The API Key to API association not exist."
	//
	// It is retained only so existing setups fail the same way they did before
	// the key became configurable, rather than changing error shape. Supply a
	// real key via F5_API_KEY or `api_key` in ~/.f5api_credentials.
	//
	// This value is present in the repository's git history and in every
	// released binary; treat it as compromised and do not reuse it.
	DefaultKParam = "UKKD3Vxv7NHrM3QmYk8Fk2mZnLtljAKX"
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

// NewClient initializes a new MyF5 Support Case client using the legacy
// fallback API usage key. Prefer NewClientWithKey, which accepts the caller's
// own key; see DefaultKParam for why the fallback cannot work.
func NewClient(token string, baseFQDN string) *Client {
	return NewClientWithKey(token, baseFQDN, "")
}

// NewClientWithKey initializes a client with an explicit `k` API usage key.
// An empty apiKey falls back to DefaultKParam.
func NewClientWithKey(token, baseFQDN, apiKey string) *Client {
	if baseFQDN == "" {
		baseFQDN = DefaultSupportFQDN
	}
	if apiKey == "" {
		apiKey = DefaultKParam
	}
	return &Client{
		HTTPClient: client.NewSecureHTTPClient(30 * time.Second),
		BaseFQDN:   baseFQDN,
		Token:      token,
		KParam:     apiKey,
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

// Case list `type` values, per the caseTypeParam enum in the F5 OpenAPI
// specification (docs/openapi.json). `type` is REQUIRED on
// GET /case-management/v1/cases; omitting it returns
// 400 "Missing type in the query params".
const (
	TypeAllCases       = "ALL_CASES"
	TypeAllOpenCases   = "ALL_OPEN_CASES"
	TypeAllClosedCases = "ALL_CLOSED_CASES"
	TypeMyOpenCases    = "MY_OPEN_CASES"
	TypeMyClosedCases  = "MY_CLOSED_CASES"
	TypeMyAllCases     = "MY_ALL_CASES"
)

// ListCases returns support cases. showClosed selects ALL_CASES (open and
// closed) rather than ALL_OPEN_CASES.
//
// Note that `status` is NOT the way to widen the result set: per the spec it
// takes comma-separated metadata values such as "New,Solving". An earlier
// implementation sent `status=all`, which is not a valid status.
func (c *Client) ListCases(ctx context.Context, showClosed bool) (*ListCasesResponse, error) {
	caseType := TypeAllOpenCases
	if showClosed {
		caseType = TypeAllCases
	}
	path := "/case-management/v1/cases?type=" + caseType
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
	req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")

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
		// A 403 here is nearly always the `k` API usage key rather than the
		// bearer token (a bad token returns 401). Say so, because the raw API
		// message — "The API Key to API association not exist" — does not make
		// it obvious which of the two credentials is at fault, nor that the
		// built-in fallback key cannot work for anyone.
		if resp.StatusCode == http.StatusForbidden {
			hint := "the API usage key (`k`) was rejected for this API"
			if c.KParam == DefaultKParam {
				hint = "no API usage key is configured, so the non-functional built-in fallback was sent"
			}
			return nil, fmt.Errorf(
				"request to %s failed with status 403: %s\n"+
					"%s.\n"+
					"This key is separate from your OAuth client credentials and is issued per entitlement:\n"+
					"  1. Log in to https://my.f5.com\n"+
					"  2. Request API Catalog access from F5 Support\n"+
					"  3. Create an API key, then set F5_API_KEY or add `api_key = <key>` to ~/.f5api_credentials",
				redactedURL, string(respBytes), hint,
			)
		}
		return nil, fmt.Errorf("request to %s failed with status %d: %s", redactedURL, resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}
