package bigip

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"myf5-api/pkg/client"
)

// Client interacts with BIG-IP iControl REST APIs.
type Client struct {
	Scheme     string
	Host       string
	Port       int
	Username   string
	Password   string
	HTTPClient *http.Client
}

// NewClient initializes a BIG-IP appliance client.
func NewClient(rawHost string, port int, username, password string) *Client {
	scheme := "https"
	host := strings.TrimSpace(rawHost)
	if strings.HasPrefix(host, "http://") {
		scheme = "http"
		host = strings.TrimPrefix(host, "http://")
	} else if strings.HasPrefix(host, "https://") {
		scheme = "https"
		host = strings.TrimPrefix(host, "https://")
	}
	host = strings.TrimRight(host, "/")

	// If host contains embedded port (e.g. host:8443)
	if h, pStr, err := net.SplitHostPort(host); err == nil {
		host = h
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			port = p
		}
	}

	if port <= 0 {
		if scheme == "http" {
			port = 80
		} else {
			port = 443
		}
	}

	return &Client{
		Scheme:     scheme,
		Host:       host,
		Port:       port,
		Username:   username,
		Password:   password,
		HTTPClient: client.NewInsecureHTTPClient(60 * time.Minute),
	}
}

// BaseURL returns the canonical base URL for the appliance.
func (c *Client) BaseURL() string {
	scheme := c.Scheme
	if scheme == "" {
		scheme = "https"
	}
	if (scheme == "https" && c.Port == 443) || (scheme == "http" && c.Port == 80) {
		return fmt.Sprintf("%s://%s", scheme, c.Host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, c.Host, c.Port)
}

// CheckReady queries /mgmt/tm/sys/ready to verify appliance availability.
func (c *Client) CheckReady(ctx context.Context) (bool, error) {
	url := fmt.Sprintf("%s/mgmt/tm/sys/ready", c.BaseURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	c.setAuth(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("device reachability check failed: %w", err)
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

var validQKViewFilenameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+$`)

// ValidateQKViewFilename checks for illegal characters or path traversal sequences
// to prevent remote command injection and directory traversal on BIG-IP TMOS.
func ValidateQKViewFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("filename cannot be empty")
	}
	if !validQKViewFilenameRegex.MatchString(filename) {
		return fmt.Errorf("invalid qkview filename %q: contains prohibited characters (only alphanumeric, _, -, and . allowed)", filename)
	}
	if strings.Contains(filename, "..") {
		return fmt.Errorf("invalid qkview filename %q: path traversal ('..') is prohibited", filename)
	}
	return nil
}

// GenerateQKView initiates an asynchronous non-truncating QKView generation job.
func (c *Client) GenerateQKView(ctx context.Context, filename string, complete bool) (string, error) {
	if filename == "" {
		cleanHost := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				return r
			}
			return '_'
		}, c.Host)
		filename = fmt.Sprintf("qkview_%s_%d.qkview", cleanHost, time.Now().Unix())
	}
	if !strings.HasSuffix(filename, ".qkview") {
		filename += ".qkview"
	}

	if err := ValidateQKViewFilename(filename); err != nil {
		return "", err
	}

	args := fmt.Sprintf("-f /var/tmp/%s", filename)
	if complete {
		args = "-s0 " + args
	}

	payload := map[string]string{
		"command":     "run",
		"utilCmdArgs": args,
	}
	jsonBytes, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/mgmt/tm/util/qkview", c.BaseURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBytes))
	if err != nil {
		return "", err
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("generate qkview request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		return "", fmt.Errorf("qkview generation failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	return filename, nil
}

// DownloadQKView downloads a completed QKView from /mgmt/cm/autodeploy/qkview.
func (c *Client) DownloadQKView(ctx context.Context, remoteFilename, localPath string) error {
	if err := ValidateQKViewFilename(remoteFilename); err != nil {
		return err
	}
	url := fmt.Sprintf("%s/mgmt/cm/autodeploy/qkview/%s", c.BaseURL(), remoteFilename)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	c.setAuth(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download qkview: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("failed saving qkview stream: %w", err)
	}

	return nil
}

// DeleteQKView removes the temporary QKView file from the BIG-IP appliance.
func (c *Client) DeleteQKView(ctx context.Context, remoteFilename string) error {
	if err := ValidateQKViewFilename(remoteFilename); err != nil {
		return err
	}
	url := fmt.Sprintf("%s/mgmt/cm/autodeploy/qkview/%s", c.BaseURL(), remoteFilename)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	c.setAuth(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete qkview failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		return fmt.Errorf("delete failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	return nil
}

func (c *Client) setAuth(req *http.Request) {
	auth := base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + c.Password))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("User-Agent", "myf5-api-go/1.0.0")
}
