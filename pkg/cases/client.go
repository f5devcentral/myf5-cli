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

	// DefaultKParam is the default `k` API usage key sent to Support Case endpoints.
	// This public rate-limit identifier is defined in F5's OpenAPI specification and
	// can be overridden via F5_API_KEY or `api_key` in ~/.f5api_credentials.
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

// Zero clears bearer tokens and API keys from memory.
func (c *Client) Zero() {
	if c == nil {
		return
	}
	c.Token = ""
	c.KParam = ""
}

// NewClient initializes a new MyF5 Support Case client using DefaultKParam.
// Prefer NewClientWithKey when an explicit API usage key is configured.
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

// apiError converts a body-reported failure into an error.
//
// The Case API signals failure in the response body via isError as well as by
// HTTP status, and the two do not always agree: a 2xx can carry isError true
// with a null data field. Decoding only `data` in that case yields an empty
// result that reads as success, which is how a failure becomes a silent
// "0 cases found".
func apiError(action string, isError bool, message string) error {
	if !isError {
		return nil
	}
	if message == "" {
		message = "the API reported an error without a message"
	}
	return fmt.Errorf("failed to %s: %s", action, message)
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
	if err := apiError("retrieve case metadata", resp.IsError, resp.Message); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Case list `type` query parameter values for GET /case-management/v1/cases.
// The `type` parameter is required by the endpoint.
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
	if err := apiError("list cases", resp.IsError, resp.Message); err != nil {
		return nil, err
	}
	return &resp, nil
}

// requiredCreateFields lists the base fields required for every product family
// on CreateCase. The family-specific entitlement identifier (`serialNumber` or
// `subscriptionId`) is resolved separately by EntitlementFieldFor.
var requiredCreateFields = []string{
	"productFamily",
	"product",
	"version",
	"subject",
	"description",
	"priority",
	"reasonForContact",
	"caseOwnerEmail",
	"preferredContactMethod",
	"timezone",
}

// EntitlementFieldFor returns the entitlement identifier field required by the
// MyF5 Support Case API for a given product family (`subscriptionId` for NGINX
// and Aspen Mesh; `serialNumber` for BIG-IP, F5OS, and all other families).
func EntitlementFieldFor(productFamily string) string {
	switch productFamily {
	case "NGINX", "Aspen Mesh":
		return "subscriptionId"
	default:
		return "serialNumber"
	}
}

// RequiredCreateFields returns the fields CreateCase requires, so callers can
// validate or describe a payload without duplicating the list.
func RequiredCreateFields() []string {
	out := make([]string, len(requiredCreateFields))
	copy(out, requiredCreateFields)
	return out
}

// ErrMissingCreateFields reports a payload rejected before it was sent.
type ErrMissingCreateFields struct {
	Missing []string
}

func (e *ErrMissingCreateFields) Error() string {
	msg := fmt.Sprintf(
		"cannot create case: missing required field(s): %s",
		strings.Join(e.Missing, ", "),
	)
	// The entitlement identifier is required by the public API, not by support
	// cases in general — the web UI creates them without one. Say so, rather
	// than leaving the user to conclude their case cannot be opened at all.
	for _, f := range e.Missing {
		if f == "serialNumber" || f == "subscriptionId" {
			msg += "\n\nThe MyF5 Support Case API requires an entitlement identifier on create," +
				"\neven though the MyF5 web UI does not: the web UI writes to Salesforce" +
				"\ndirectly and applies different validation. If you have no serial number or" +
				"\nsubscription to hand, open the case at https://my.f5.com and use" +
				"\n`myf5 cases comment` or `myf5 ihealth upload` with the case number after."
			break
		}
	}
	return msg
}

// GetCase fetches a single case by number.
//
// The response reuses the list envelope: `data` is an ARRAY even for a single
// case, so this returns the first element. It carries considerably more than
// the list view — account, contacts, and the full email thread.
func (c *Client) GetCase(ctx context.Context, caseNumber string) (*CaseDetail, error) {
	path := fmt.Sprintf("/case-management/v1/cases/%s", url.PathEscape(caseNumber))
	bodyBytes, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp GetCaseResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode case detail: %w", err)
	}
	if err := apiError("retrieve case "+caseNumber, resp.IsError, resp.Message); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("case %s not found", caseNumber)
	}
	return &resp.Data[0], nil
}

// CreateCase submits a support ticket to MyF5.
//
// Required fields are checked locally first. The API's own message for a
// missing field names three fields whatever the actual omission is, so
// catching it here gives the caller something accurate to act on and costs
// nothing against a 10-requests-per-minute rate limit.
func (c *Client) CreateCase(ctx context.Context, payload map[string]interface{}) (*CreateCaseResponse, error) {
	present := func(f string) bool {
		v, ok := payload[f]
		if !ok {
			return false
		}
		s, isStr := v.(string)
		return !isStr || strings.TrimSpace(s) != ""
	}

	var missing []string
	for _, f := range requiredCreateFields {
		if !present(f) {
			missing = append(missing, f)
		}
	}
	// The entitlement identifier depends on the family, so it is checked
	// separately rather than living in the fixed list.
	family, _ := payload["productFamily"].(string)
	if ent := EntitlementFieldFor(family); !present(ent) {
		missing = append(missing, ent)
	}
	if len(missing) > 0 {
		return nil, &ErrMissingCreateFields{Missing: missing}
	}
	return c.createCase(ctx, payload)
}

// CreateCaseUnchecked submits a payload without the local required-field
// check.
//
// The required fields differ by product family — BIG-IP is identified by
// serialNumber, NGINX and Aspen Mesh by subscriptionId — and the API's own
// error message is phrased for BIG-IP whatever family was sent. This exists so
// those per-family rules can be probed against the live API rather than
// guessed at. Prefer CreateCase everywhere else.
func (c *Client) CreateCaseUnchecked(ctx context.Context, payload map[string]interface{}) (*CreateCaseResponse, error) {
	return c.createCase(ctx, payload)
}

func (c *Client) createCase(ctx context.Context, payload map[string]interface{}) (*CreateCaseResponse, error) {
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

// AddComment appends a comment to an existing support case via
// PATCH /case-management/v1/cases/{caseNumber} with a {"comments": "..."} payload.
func (c *Client) AddComment(ctx context.Context, caseNumber, comment string) (*AddCommentResponse, error) {
	payload := map[string]string{
		"comments": comment,
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode comment payload: %w", err)
	}

	path := fmt.Sprintf("/case-management/v1/cases/%s", url.PathEscape(caseNumber))
	bodyBytes, err := c.doRequest(ctx, http.MethodPatch, path, jsonBytes)
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
		// Distinguish between a rejected `k` API usage key and an invalid or
		// out-of-support entitlement identifier, both of which return HTTP 403.
		if resp.StatusCode == http.StatusForbidden {
			body := string(respBytes)
			if strings.Contains(body, "out of Support") ||
				strings.Contains(body, "value entered is invalid") ||
				strings.Contains(body, "Unable to verify") {
				return nil, fmt.Errorf(
					"request to %s failed with status 403: %s\n"+
						"The API accepted the request but could not verify the entitlement it names —\n"+
						"a serial number, registration key or subscription number it does not\n"+
						"recognise, or one not covered by active support. Which identifier is needed\n"+
						"depends on the product family: BIG-IP uses a serial number, NGINX and Aspen\n"+
						"Mesh use a subscription number.\n"+
						"This is a data problem, not a credentials problem: your OAuth token and `k`\n"+
						"key both worked.",
					redactedURL, body,
				)
			}
			return nil, fmt.Errorf(
				"request to %s failed with status 403: %s\n"+
					"The `k` API usage key was rejected for this API. These keys are per-API-family\n"+
					"constants from F5's OpenAPI specification, not per-user secrets, so this usually\n"+
					"means the wrong family's key was sent (Case, Downloads and Knowledge each have\n"+
					"their own) rather than anything being wrong with your account.\n"+
					"If F5 has rotated the key, override it with F5_API_KEY or `api_key` in ~/.f5api_credentials.\n"+
					"Note that a bad OAuth token returns 401, not 403.",
				redactedURL, body,
			)
		}
		return nil, fmt.Errorf("request to %s failed with status %d: %s", redactedURL, resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}
