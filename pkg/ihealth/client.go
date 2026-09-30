package ihealth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"myf5-api/pkg/client"
)

const (
	DefaultIHealthFQDN = "ihealth2-api.f5.com"
)

// Client interacts with the F5 iHealth API.
type Client struct {
	HTTPClient *http.Client
	BaseFQDN   string
	Token      string
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
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open qkview file %s: %w", filePath, err)
	}
	defer file.Close()

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
		if _, err := io.CopyBuffer(part, file, buf); err != nil {
			writeErr = err
			return
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

	uploadURL := fmt.Sprintf("%s://%s/qkview-analyzer/api/v1/qkviews", scheme, fqdn)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, pr)
	if err != nil {
		_ = pr.Close()
		return nil, fmt.Errorf("failed to create upload request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "myf5-api-go/1.0.0")

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

	listURL := fmt.Sprintf("%s://%s/qkview-analyzer/api/v1/qkviews", scheme, fqdn)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create list request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "myf5-api-go/1.0.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
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
