package ihealth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
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

	// IHealthAccept is the vendor media type the iHealth API requires.
	// Requesting "application/json" is what the version segment above was
	// compensating for; the API selects its representation from this header.
	IHealthAccept = "application/vnd.f5.ihealth.api"
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

		if caseNumber != "" {
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

	var uploadResp UploadQKViewResponse
	if err := json.Unmarshal(respBytes, &uploadResp); err != nil {
		return nil, fmt.Errorf("failed to decode upload response: %w", err)
	}
	return &uploadResp, nil
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

	var summaries []QKViewSummary
	if err := json.Unmarshal(respBytes, &summaries); err != nil {
		return nil, fmt.Errorf("failed to decode qkview summaries: %w", err)
	}
	return summaries, nil
}
