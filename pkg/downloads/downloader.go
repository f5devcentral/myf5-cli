package downloads

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"myf5-cli/pkg/client"
)

// ProgressCallback is called periodically during download with bytes transferred and total size.
type ProgressCallback func(transferred, total int64, speedBytesPerSec float64)

// DownloadFile streams a file from downloadURL directly to outputPath, calculating
// checksums in real-time and updating progress.
func DownloadFile(
	ctx context.Context,
	downloadURL string,
	outputPath string,
	expectedChecksum string,
	checksumAlgo string,
	progressCb ProgressCallback,
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("User-Agent", "myf5-cli-go-downloader/1.0.0")

	// Long timeout for large image downloads (e.g. 10 GB ISOs)
	httpClient := client.NewSecureHTTPClient(6 * time.Hour)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		bodyStr := string(bodySnippet)
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			if u, uErr := url.Parse(downloadURL); uErr == nil {
				if expStr := u.Query().Get("Expires"); expStr != "" {
					if sec, sErr := strconv.ParseInt(expStr, 10, 64); sErr == nil && sec > 0 {
						expTime := time.Unix(sec, 0)
						if time.Now().After(expTime) {
							return fmt.Errorf("download link expired at %s: AWS CloudFront presigned links are temporary (re-run command to fetch fresh link)", expTime.Local().Format("15:04:05 MST"))
						}
					}
				}
			}
			if strings.Contains(bodyStr, "Request has expired") {
				return errors.New("download link expired: AWS CloudFront presigned URL has expired (re-run command to fetch fresh link)")
			}
			if strings.Contains(bodyStr, "AccessDenied") {
				return errors.New("download access denied: signed CDN link may have expired or is invalid (re-run command to fetch fresh link)")
			}
		}
		return fmt.Errorf("server returned HTTP %d for download URL", resp.StatusCode)
	}

	totalSize := resp.ContentLength
	cleanOut := filepath.Clean(outputPath)
	if strings.HasSuffix(outputPath, "/") || strings.HasSuffix(outputPath, string(filepath.Separator)) {
		uBase := path.Base(downloadURL)
		if uBase == "" || uBase == "." || uBase == "/" {
			uBase = "download.bin"
		}
		cleanOut = filepath.Join(cleanOut, uBase)
	} else if fi, statErr := os.Stat(cleanOut); statErr == nil && fi.IsDir() {
		uBase := path.Base(downloadURL)
		if uBase == "" || uBase == "." || uBase == "/" {
			uBase = "download.bin"
		}
		cleanOut = filepath.Join(cleanOut, uBase)
	}
	outputPath = cleanOut

	if dir := filepath.Dir(outputPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create destination directory %s: %w", dir, err)
		}
	}
	partPath := outputPath + ".part"

	out, err := os.Create(partPath)
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", partPath, err)
	}
	defer func() {
		out.Close()
		// Clean up part file if download did not succeed
		if _, err := os.Stat(partPath); err == nil {
			os.Remove(partPath)
		}
	}()

	// Select hasher
	var hasher hash.Hash
	algo := strings.ToLower(strings.TrimSpace(checksumAlgo))
	switch algo {
	case "md5":
		hasher = md5.New()
	case "sha384":
		hasher = sha512.New384()
	case "sha512":
		hasher = sha512.New()
	default:
		// Default to sha256
		hasher = sha256.New()
	}

	// Stream through TeeReader: response.Body -> Hasher -> File
	teeReader := io.TeeReader(resp.Body, hasher)

	buf := make([]byte, 64*1024) // 64KB buffer
	var transferred int64
	startTime := time.Now()
	lastCallback := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, rErr := teeReader.Read(buf)
		if n > 0 {
			if _, wErr := out.Write(buf[:n]); wErr != nil {
				return fmt.Errorf("failed writing to file: %w", wErr)
			}
			transferred += int64(n)

			if progressCb != nil && time.Since(lastCallback) >= 200*time.Millisecond {
				elapsed := time.Since(startTime).Seconds()
				var speed float64
				if elapsed > 0 {
					speed = float64(transferred) / elapsed
				}
				progressCb(transferred, totalSize, speed)
				lastCallback = time.Now()
			}
		}

		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			return fmt.Errorf("error reading download stream: %w", rErr)
		}
	}

	// Final progress update
	if progressCb != nil {
		elapsed := time.Since(startTime).Seconds()
		var speed float64
		if elapsed > 0 {
			speed = float64(transferred) / elapsed
		}
		progressCb(transferred, totalSize, speed)
	}

	// Verify checksum if provided
	computedChecksum := hex.EncodeToString(hasher.Sum(nil))
	if expectedChecksum != "" {
		if !strings.EqualFold(computedChecksum, expectedChecksum) {
			return fmt.Errorf(
				"checksum verification failed!\n  Expected: %s\n  Computed: %s\nFile may be corrupt",
				expectedChecksum, computedChecksum,
			)
		}
	}

	// Close before renaming
	out.Close()

	if err := os.Rename(partPath, outputPath); err != nil {
		return fmt.Errorf("failed to finalize downloaded file: %w", err)
	}

	return nil
}

// FormatBytes formats byte counts into human-readable strings (e.g. 2.45 GB).
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
