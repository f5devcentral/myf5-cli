package ihealth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"myf5-cli/pkg/client"
)

const (
	// DefaultIHealthFQDN represents the standard iHealth API gateway hostname.
	// F5 article K000130498 uses this host in its worked example.
	DefaultIHealthFQDN = "ihealth2-api.f5.com"
	// PrimaryIHealthFQDN represents the primary authoritative iHealth API gateway hostname.
	PrimaryIHealthFQDN = "ihealth-api.f5.com"

	// QKViewsPath is the QKView collection path.
	//
	// There is NO version segment. An earlier value of
	// "/qkview-analyzer/api/v1/qkviews" returned
	//   404 "The requested resource is not available"
	// because the API versions by Accept header, not by URL path
	// (K000130498).
	QKViewsPath = "/qkview-analyzer/api/qkviews"

	// IHealthAccept is the vendor media type the iHealth API requires, with
	// the +json suffix.
	//
	// The suffix is NOT optional. Verified live 2026-10-02: the bare
	// "application/vnd.f5.ihealth.api" returns 200 with an XML body
	//   <?xml version="1.0"...?><qkview_ids><id>26894783</id></qkview_ids>
	// which fails to decode with
	//   invalid character '<' looking for beginning of value
	// The API selects its representation from this header, so the suffix is
	// what asks for JSON.
	IHealthAccept = "application/vnd.f5.ihealth.api+json"
)

// UploadProgressCallback reports transferred bytes and total bytes during file upload.
type UploadProgressCallback func(transferred, total int64)

// Client interacts with the F5 iHealth API.
type Client struct {
	HTTPClient *http.Client
	BaseFQDN   string
	Token      string
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
	return fmt.Sprintf("ihealth.Client{BaseFQDN: %q, Token: %s}", c.BaseFQDN, tok)
}

// GoString implements fmt.GoStringer.
func (c *Client) GoString() string {
	return c.String()
}

// NewClient initializes a new iHealth API client.
func NewClient(token string, baseFQDN string) *Client {
	if baseFQDN == "" {
		baseFQDN = DefaultIHealthFQDN
	}
	return &Client{
		HTTPClient: client.NewSecureHTTPClient(60 * time.Minute),
		BaseFQDN:   baseFQDN,
		Token:      token,
	}
}

// UploadQKView streams a local QKView file to iHealth using multipart/form-data via io.Pipe
// to ensure constant O(1) memory consumption regardless of archive size.
func (c *Client) UploadQKView(ctx context.Context, filePath, caseNumber, description string) (*UploadQKViewResponse, error) {
	return c.UploadQKViewWithProgress(ctx, filePath, caseNumber, description, nil)
}

// UploadQKViewWithProgress streams a local QKView file to iHealth with real-time progress callbacks.
func (c *Client) UploadQKViewWithProgress(ctx context.Context, filePath, caseNumber, description string, progressCb UploadProgressCallback) (*UploadQKViewResponse, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open qkview file %s: %w", filePath, err)
	}
	defer file.Close()

	var totalSize int64
	if stat, err := file.Stat(); err == nil {
		totalSize = stat.Size()
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		var writeErr error
		defer func() {
			if writeErr != nil {
				_ = pw.CloseWithError(writeErr)
			} else {
				_ = pw.Close()
			}
		}()

		// visible_in_gui must be True for uploads to appear in the web GUI (K000130498).
		if err := writer.WriteField("visible_in_gui", "True"); err != nil {
			writeErr = err
			return
		}
		if caseNumber != "" {
			// K000130498 specifies f5_support_case; we write both f5_support_case
			// and case_number for gateway compatibility.
			if err := writer.WriteField("f5_support_case", caseNumber); err != nil {
				writeErr = err
				return
			}
			if err := writer.WriteField("case_number", caseNumber); err != nil {
				writeErr = err
				return
			}
		}
		if description != "" {
			if err := writer.WriteField("description", description); err != nil {
				writeErr = err
				return
			}
		}

		part, err := writer.CreateFormFile("qkview", filepath.Base(filePath))
		if err != nil {
			writeErr = err
			return
		}

		buf := make([]byte, 64*1024)
		var transferred int64
		for {
			n, rErr := file.Read(buf)
			if n > 0 {
				if _, wErr := part.Write(buf[:n]); wErr != nil {
					writeErr = wErr
					return
				}
				transferred += int64(n)
				if progressCb != nil {
					progressCb(transferred, totalSize)
				}
			}
			if rErr != nil {
				if errors.Is(rErr, io.EOF) {
					break
				}
				writeErr = rErr
				return
			}
		}

		writeErr = writer.Close()
	}()

	scheme := "https"
	fqdn := c.BaseFQDN
	if strings.HasPrefix(fqdn, "http://") {
		scheme = "http"
		fqdn = strings.TrimPrefix(fqdn, "http://")
	} else if strings.HasPrefix(fqdn, "https://") {
		fqdn = strings.TrimPrefix(fqdn, "https://")
	}

	uploadURL := fmt.Sprintf("%s://%s%s", scheme, fqdn, QKViewsPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, pr)
	if err != nil {
		_ = pr.Close()
		return nil, fmt.Errorf("failed to create upload request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", IHealthAccept)
	req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// Close the read half so the writer goroutine's next Write fails
		// instead of blocking forever on a pipe nobody is draining. Without
		// this, a mid-upload network failure leaks the goroutine and the open
		// file along with it — which is exactly when it is most likely to
		// happen, since QKViews are large and uploads are long.
		_ = pr.CloseWithError(err)
		return nil, fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read upload response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upload failed with HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	uploadResp, err := parseUploadResponse(respBytes, resp.Header.Get("Location"))
	if err != nil {
		return nil, fmt.Errorf("failed to decode upload response: %w", err)
	}
	return uploadResp, nil
}

// parseUploadResponse extracts the uploaded QKView ID, status, and message from
// the API response body or headers.
//
// iHealth responds to uploads with either an XML-derived JSON wrapper:
//
//	{"qkview_upload": {"result": "OK", "id": 26991271, "location": "..."}}
//
// or a flat object:
//
//	{"id": "...", "status": "..."}
//
// or returns the ID in a Location response header (or XML body fallback).
func parseUploadResponse(respBytes []byte, locationHeader string) (*UploadQKViewResponse, error) {
	var raw struct {
		ID           any    `json:"id"`
		Status       string `json:"status"`
		Result       string `json:"result"`
		Location     string `json:"location"`
		Message      string `json:"message"`
		QKViewUpload *struct {
			ID       any    `json:"id"`
			Status   string `json:"status"`
			Result   string `json:"result"`
			Location string `json:"location"`
			Message  string `json:"message"`
		} `json:"qkview_upload"`
	}

	var id, status, msg, loc string

	if len(respBytes) > 0 {
		dec := json.NewDecoder(bytes.NewReader(respBytes))
		dec.UseNumber()
		if err := dec.Decode(&raw); err == nil {
			if raw.QKViewUpload != nil {
				id = stringifyUploadID(raw.QKViewUpload.ID)
				status = raw.QKViewUpload.Status
				if status == "" {
					status = raw.QKViewUpload.Result
				}
				msg = raw.QKViewUpload.Message
				loc = raw.QKViewUpload.Location
			}
			if id == "" {
				id = stringifyUploadID(raw.ID)
			}
			if status == "" {
				status = raw.Status
				if status == "" {
					status = raw.Result
				}
			}
			if msg == "" {
				msg = raw.Message
			}
			if loc == "" {
				loc = raw.Location
			}
		} else {
			// If JSON decode failed (e.g. XML response), attempt regex fallback
			if m := regexp.MustCompile(`<id>([^<]+)</id>`).FindSubmatch(respBytes); len(m) > 1 {
				id = string(m[1])
			}
			if m := regexp.MustCompile(`<result>([^<]+)</result>`).FindSubmatch(respBytes); len(m) > 1 {
				status = string(m[1])
			}
			if m := regexp.MustCompile(`<location>([^<]+)</location>`).FindSubmatch(respBytes); len(m) > 1 {
				loc = string(m[1])
			}
		}
	}

	if id == "" && loc != "" {
		id = idFromURL(loc)
	}
	if id == "" && locationHeader != "" {
		id = idFromURL(locationHeader)
	}
	if status == "" && id != "" {
		status = "OK"
	}

	if id == "" && status == "" && msg == "" && len(respBytes) > 0 {
		msg = strings.TrimSpace(string(respBytes))
	}

	return &UploadQKViewResponse{
		ID:      id,
		Status:  status,
		Message: msg,
	}, nil
}

func stringifyUploadID(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case float64:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case int:
		return strconv.Itoa(val)
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}

func idFromURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
		return ""
	}
	base := path.Base(u.Path)
	if base == "/" || base == "." {
		return ""
	}
	return base
}

// ListQKViews retrieves uploaded QKViews from iHealth.
func (c *Client) ListQKViews(ctx context.Context) ([]QKViewSummary, error) {
	scheme := "https"
	fqdn := c.BaseFQDN
	if strings.HasPrefix(fqdn, "http://") {
		scheme = "http"
		fqdn = strings.TrimPrefix(fqdn, "http://")
	} else if strings.HasPrefix(fqdn, "https://") {
		fqdn = strings.TrimPrefix(fqdn, "https://")
	}

	listURL := fmt.Sprintf("%s://%s%s", scheme, fqdn, QKViewsPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create list request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", IHealthAccept)
	req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list request failed: %w", err)
	}
	defer resp.Body.Close()

	// Bound diagnostic QKView list JSON responses to 10 MB maximum to guard runtime heap
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed with HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	// The collection endpoint returns ONLY ids — {"id":["26894783", ...]} —
	// not an array of objects. Metadata lives on the per-qkview detail
	// resource, so each id is fetched individually to populate the summary.
	var idList struct {
		ID []string `json:"id"`
	}
	if err := json.Unmarshal(respBytes, &idList); err != nil {
		return nil, fmt.Errorf("failed to decode qkview id list: %w", err)
	}

	summaries := make([]QKViewSummary, 0, len(idList.ID))
	for _, id := range idList.ID {
		s, err := c.GetQKView(ctx, id)
		if err != nil {
			// One unreadable record should not hide the rest of the list.
			summaries = append(summaries, QKViewSummary{ID: id, Status: "unavailable: " + err.Error()})
			continue
		}
		summaries = append(summaries, *s)
	}
	return summaries, nil
}

// GetQKView fetches the metadata for a single QKView.
func (c *Client) GetQKView(ctx context.Context, id string) (*QKViewSummary, error) {
	scheme := "https"
	fqdn := c.BaseFQDN
	if strings.HasPrefix(fqdn, "http://") {
		scheme = "http"
		fqdn = strings.TrimPrefix(fqdn, "http://")
	} else if strings.HasPrefix(fqdn, "https://") {
		fqdn = strings.TrimPrefix(fqdn, "https://")
	}

	detailURL := fmt.Sprintf("%s://%s%s/%s", scheme, fqdn, QKViewsPath, url.PathEscape(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create detail request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", IHealthAccept)
	req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("detail request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	// The iHealth API returns HTTP 200 when processing is complete, and
	// HTTP 202 (Accepted) while diagnostic analysis is RUNNING. Both carry
	// the qkviewDetail JSON body.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("failed with HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var detail qkviewDetail
	if err := json.Unmarshal(respBytes, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode qkview detail: %w", err)
	}
	return detail.toSummary(id), nil
}
