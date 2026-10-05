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

	// DefaultKParam is the `k` API usage key sent to Support Case endpoints.
	//
	// It is not a per-user secret: this value is published in F5's OpenAPI
	// specification (docs/openapi-outdated-5Oct2026.json) as the
	// rateLimitParam example.
	//
	// VERIFIED 2026-10-02: this value is accepted by GET
	// /case-management/v1/cases and returns real data. The spec's
	// rateLimitParam_Cases example (X4Y6fGWJdC1qLhTsCSDW4lPcqKvF5g5r) is
	// REJECTED there with 400 "Missing or invalid k query parameter", so the
	// spec's per-endpoint key examples do not reflect the live API. Prefer
	// what is observed over what is specified.
	//
	// UNRESOLVED: the same three published keys are all rejected by the
	// Downloads endpoints on this host. Whether that is a genuine key
	// mismatch or rate limiting returning a misleading message is not yet
	// established — the API is rate limited and the probing that produced
	// those 400s was itself heavy. Re-test singly, not in a loop.
	//
	// Override via F5_API_KEY or `api_key` in ~/.f5api_credentials if F5
	// rotates it.
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

// Case list `type` values, per the caseTypeParam enum in the F5 OpenAPI
// specification (docs/openapi-outdated-5Oct2026.json). `type` is REQUIRED on
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
	if err := apiError("list cases", resp.IsError, resp.Message); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Field requirements for CreateCase, as the LIVE API enforces them.
//
// These differ from the published OpenAPI document, which is wrong on two
// counts (verified live 2026-10-05):
//
//   - serialNumber is documented as an optional field "applicable for only
//     BIG-IP products". The API requires it. Omitting it returns
//     400 "Serial Number,Product Family and Product are required to create
//     Case." even for a Pre Sales case, and even though existing cases are
//     readable with serialNumber: "".
//   - The schema's required list names `version`, while every example in the
//     same document sends `productVersion`. `version` is what the API takes.
//
// A serial that is present but unknown returns 403 "The value entered is
// invalid or out of Support", which is how we know the rest of the payload
// validates: the API only reaches the lookup once everything else passes.
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

// EntitlementFieldFor names the identifier a product family is verified by.
//
// IMPORTANT — this is a constraint of the PUBLIC API, not of a support case.
// A HAR capture of the MyF5 web UI creating case 01292481 on 2026-10-05 shows
// a BIG-IP Pre Sales case created with its serial/registration-key field
// EMPTY. The UI does not call support.apis.f5.com at all: it writes to
// Salesforce directly through an Aura endpoint
// (CaseManagementCreateCaseCtllerWOSharing.upsertCase), and that path applies
// different validation.
//
// So a case without an entitlement identifier is perfectly legitimate; it is
// the public API that refuses to create one. We cannot relax the requirement
// — the API enforces it and we cannot submit through Salesforce — but the
// error message must not tell the user their case is impossible when the web
// UI would accept it.
//
// Every family requires one, but not the same one, and the API's messages make
// this clear only after the fact (verified live 2026-10-05):
//
//	BIG-IP, no serialNumber    -> 400 "Serial Number,Product Family and Product
//	                                   are required to create Case."
//	NGINX,  no subscriptionId  -> 403 "Unable to verify Subscription. Please make
//	                                   sure Subscription Number and Product
//	                                   Family vaules are provided." [sic]
//
// Note the BIG-IP message names three fields whatever is actually missing, and
// is phrased for BIG-IP even when another family was sent — so it cannot be
// used to work out which identifier a family wants. That is why this mapping
// exists rather than relying on the server to say.
//
// Families not listed here fall back to serialNumber, which is the BIG-IP
// behaviour and the most common case. A family whose requirement we have not
// confirmed is better served by a local error naming the likely field than by
// a server round trip returning a message about a different one.
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

// AddComment appends a comment to an existing case.
//
// This is a PATCH to the case itself, not a POST to a sub-resource. An
// earlier implementation sent
//
//	POST /case-management/v1/cases/{n}/notes  {"comment": "..."}
//
// which diverges from the specification in method, path and field name:
// there is no /notes endpoint in the API at all, and the field is plural.
// The spec's CASE_PATCH_REQUEST is described as "Case Add Comment Request"
// with the example {"comments": "Adding test comments"}.
//
// NOT YET VERIFIED LIVE — exercising it writes a visible comment to a real
// support case, so this is reasoned from the specification alone. Given that
// the spec was wrong about three other things on 2026-10-02, treat the first
// live run as the real test.
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
		// A 403 here is nearly always the `k` API usage key rather than the
		// bearer token (a bad token returns 401). Say so, because the raw API
		// message — "The API Key to API association not exist" — does not make
		// it obvious which of the two credentials is at fault, nor that the
		// built-in fallback key cannot work for anyone.
		if resp.StatusCode == http.StatusForbidden {
			// A 403 here is NOT always the `k` key. The Case API also returns
			// 403 for a well-formed request whose data it rejected — notably
			// an unrecognised or out-of-support serial number on create,
			// which answers with "The value entered is invalid or out of
			// Support." (verified live 2026-10-05). Blaming the API key for
			// that sends the reader after entirely the wrong credential, so
			// only offer the key explanation when the body does not already
			// carry a specific grievance.
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
