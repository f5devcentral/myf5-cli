package bigip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
)

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

// CopyRemoteFile uses the TMOS bash utility (/mgmt/tm/util/bash) to copy a file
// from one path to another on the appliance (e.g., from /var/config/rest/downloads to /shared/images).
func (c *Client) CopyRemoteFile(ctx context.Context, srcPath, destDir string) (string, error) {
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
		return "", fmt.Errorf("remote copy failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	return destPath, nil
}
