package bigip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var validRemotePathRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\./]+$`)

// ValidateRemoteAppliancePath ensures paths on TMOS appliances are clean, absolute,
// free of path traversal (..), and contain no shell metacharacters.
func ValidateRemoteAppliancePath(p string) error {
	if p == "" {
		return errors.New("remote appliance path cannot be empty")
	}
	if strings.Contains(p, "..") {
		return fmt.Errorf("prohibited remote appliance path %q: path traversal forbidden", p)
	}
	clean := path.Clean(p)
	if !path.IsAbs(clean) {
		return fmt.Errorf("remote appliance path %q must be an absolute path", p)
	}
	if !validRemotePathRegex.MatchString(clean) {
		return fmt.Errorf("prohibited remote appliance path %q: contains illegal characters", p)
	}
	return nil
}

// ValidateApplianceDownloadPath ensures that appliance file downloads are strictly restricted
// to diagnostic archives (.qkview) and software images (.iso).
// Arbitrary remote file retrieval (e.g. /etc/shadow, /config/bigip.conf) is forbidden.
func ValidateApplianceDownloadPath(p string) error {
	if p == "" {
		return errors.New("remote appliance download path cannot be empty")
	}
	if strings.Contains(p, "..") {
		return fmt.Errorf("prohibited remote appliance path %q: path traversal forbidden", p)
	}
	clean := path.Clean(p)
	ext := strings.ToLower(path.Ext(clean))
	if ext != ".qkview" && ext != ".iso" {
		return fmt.Errorf("prohibited download file %q: only .qkview and .iso files can be downloaded from appliances", p)
	}
	if !validRemotePathRegex.MatchString(clean) {
		return fmt.Errorf("prohibited remote appliance path %q: contains illegal characters", p)
	}
	return nil
}

// DefaultUploadChunkSize is 512KB (524,288 bytes), the standard chunk size for
// F5 BIG-IP and BIG-IQ iControl REST file transfer endpoints.
const DefaultUploadChunkSize = 512 * 1024

// UploadFile streams a local file to a BIG-IP or BIG-IQ instance via the iControl REST
// chunked file transfer endpoint (/mgmt/shared/file-transfer/uploads/<filename>).
// Chunks are transferred sequentially with Content-Range headers and progress callbacks.
// On success, the file is deposited at /var/config/rest/downloads/<filename> on the appliance.
func (c *Client) UploadFile(ctx context.Context, localPath string, remoteFilename string, progressCb func(transferred, total int64)) (string, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open local file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat local file: %w", err)
	}
	totalSize := stat.Size()

	if remoteFilename == "" {
		remoteFilename = filepath.Base(localPath)
	}

	url := fmt.Sprintf("%s/mgmt/shared/file-transfer/uploads/%s", c.BaseURL(), remoteFilename)
	chunkSize := DefaultUploadChunkSize
	buf := make([]byte, chunkSize)
	var offset int64 = 0

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		n, readErr := file.Read(buf)
		if n > 0 {
			start := offset
			end := offset + int64(n) - 1
			contentRange := fmt.Sprintf("%d-%d/%d", start, end, totalSize)

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf[:n]))
			if err != nil {
				return "", fmt.Errorf("failed to create upload request: %w", err)
			}
			c.setAuth(req)
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Content-Range", contentRange)

			resp, err := c.HTTPClient.Do(req)
			if err != nil {
				return "", fmt.Errorf("upload chunk failed at bytes %d-%d: %w", start, end, err)
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
				resp.Body.Close()
				return "", fmt.Errorf("chunk upload failed with HTTP %d: %s", resp.StatusCode, string(b))
			}
			resp.Body.Close()

			offset += int64(n)
			if progressCb != nil {
				progressCb(offset, totalSize)
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return "", fmt.Errorf("error reading local file: %w", readErr)
		}
	}

	remotePath := fmt.Sprintf("/var/config/rest/downloads/%s", remoteFilename)
	return remotePath, nil
}

// CopyRemoteFile uses the TMOS bash utility (/mgmt/tm/util/bash) as a last resort
// to move an uploaded file from /var/config/rest/downloads to another directory (e.g. /shared/images).
// Where available, SCP is preferred because it transfers directly into target directories without invoking bash.
func (c *Client) CopyRemoteFile(ctx context.Context, srcPath, destDir string) (string, error) {
	// Defensively validate source and destination paths to prevent path traversal (..) and TMOS shell command injection
	if err := ValidateRemoteAppliancePath(srcPath); err != nil {
		return "", fmt.Errorf("invalid source path: %w", err)
	}
	if err := ValidateRemoteAppliancePath(destDir); err != nil {
		return "", fmt.Errorf("invalid destination directory: %w", err)
	}

	filename := path.Base(srcPath)
	destPath := path.Join(destDir, filename)

	cmdStr := fmt.Sprintf("cp -f %s %s", srcPath, destPath)
	payload := map[string]string{
		"command":     "run",
		"utilCmdArgs": fmt.Sprintf("-c '%s'", cmdStr),
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/mgmt/tm/util/bash", c.BaseURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBytes))
	if err != nil {
		return "", err
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed executing remote copy on appliance: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("remote copy via TMOS bash utility (/mgmt/tm/util/bash) returned HTTP %d (bash utility may be disabled or appliance in Appliance Mode): %s\nRemediation: Use '--method scp' to write directly to %s without invoking bash", resp.StatusCode, strings.TrimSpace(string(b)), destDir)
		}
		return "", fmt.Errorf("remote copy failed with HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	return destPath, nil
}
