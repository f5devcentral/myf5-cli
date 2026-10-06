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
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"myf5-cli/pkg/client"
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

// String implements fmt.Stringer to ensure appliance credentials are never leaked in logs.
func (c *Client) String() string {
	if c == nil {
		return "<nil>"
	}
	pw := "[REDACTED]"
	if c.Password == "" {
		pw = "[EMPTY]"
	}
	return fmt.Sprintf("bigip.Client{Host: %q, Port: %d, Scheme: %q, Username: %q, Password: %s}", c.Host, c.Port, c.Scheme, c.Username, pw)
}

// GoString implements fmt.GoStringer.
func (c *Client) GoString() string {
	return c.String()
}

var (
	validHostnameLabelRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-_]{0,61}[a-zA-Z0-9])?$`)
)

// ValidateHost validates that host is a valid IPv4 address, IPv6 address, or RFC 1123 hostname.
// It returns the normalized host (without brackets) and whether it is IPv6.
func ValidateHost(rawHost string) (cleanHost string, isIPv6 bool, err error) {
	h := strings.TrimSpace(rawHost)
	if h == "" {
		return "", false, fmt.Errorf("host cannot be empty")
	}

	// Reject shell injection characters or path separators
	if strings.ContainsAny(h, " ;/\\`$|&\"'\t\r\n<>") {
		return "", false, fmt.Errorf("invalid host %q: prohibited characters detected", h)
	}

	// Strip surrounding brackets if IPv6
	trimmed := h
	hasBrackets := strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")
	if hasBrackets {
		trimmed = trimmed[1 : len(trimmed)-1]
	}

	// Try parsing as IP address (IPv4 or IPv6)
	if addr, parseErr := netip.ParseAddr(trimmed); parseErr == nil {
		if addr.Is4() {
			if hasBrackets {
				return "", false, fmt.Errorf("invalid host %q: IPv4 addresses must not be enclosed in brackets", h)
			}
			return addr.String(), false, nil
		}
		if addr.Is6() {
			return addr.String(), true, nil
		}
	}

	// If it contains colons or was enclosed in brackets, it was intended to be IPv6
	if strings.Contains(h, ":") || hasBrackets {
		return "", false, fmt.Errorf("invalid IPv6 address %q", h)
	}

	// Check if it looks like an attempted IPv4 address (starts with digit and contains dots)
	if isDottedDecimalCandidate(h) {
		return "", false, fmt.Errorf("invalid IPv4 address %q", h)
	}

	// Validate as RFC 1123 hostname
	if err := validateHostname(h); err != nil {
		return "", false, err
	}

	return h, false, nil
}

func isDottedDecimalCandidate(h string) bool {
	parts := strings.Split(h, ".")
	if len(parts) >= 2 {
		first := parts[0]
		if len(first) > 0 && first[0] >= '0' && first[0] <= '9' {
			return true
		}
	}
	return false
}

func validateHostname(h string) error {
	if len(h) > 253 {
		return fmt.Errorf("hostname %q exceeds maximum RFC 1123 length of 253 characters", h)
	}
	trimmed := strings.TrimSuffix(h, ".")
	labels := strings.Split(trimmed, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("invalid hostname %q: label length must be between 1 and 63 characters", h)
		}
		if !validHostnameLabelRegex.MatchString(label) {
			return fmt.Errorf("invalid hostname %q: label %q contains invalid characters or starts/ends with a hyphen", h, label)
		}
	}
	return nil
}

// ParseAndValidateApplianceHost extracts scheme, normalized host, and port, validating the host.
func ParseAndValidateApplianceHost(rawHost string, defaultPort int) (scheme, cleanHost string, port int, isIPv6 bool, err error) {
	s := strings.TrimSpace(rawHost)
	scheme = "https"
	if strings.HasPrefix(s, "http://") {
		scheme = "http"
		s = strings.TrimPrefix(s, "http://")
	} else if strings.HasPrefix(s, "https://") {
		scheme = "https"
		s = strings.TrimPrefix(s, "https://")
	} else if strings.HasPrefix(s, "ssh://") {
		scheme = "ssh"
		s = strings.TrimPrefix(s, "ssh://")
	}
	s = strings.TrimRight(s, "/")

	port = defaultPort
	// Check for embedded port: e.g. "host:8443" or "[2001:db8::1]:8443"
	if h, pStr, splitErr := net.SplitHostPort(s); splitErr == nil {
		s = h
		if p, atoiErr := strconv.Atoi(pStr); atoiErr == nil && p > 0 {
			port = p
		}
	}

	if port <= 0 {
		if scheme == "http" {
			port = 80
		} else if scheme == "ssh" {
			port = 22
		} else {
			port = 443
		}
	}

	cleanHost, isIPv6, err = ValidateHost(s)
	if err != nil {
		return "", "", 0, false, err
	}
	return scheme, cleanHost, port, isIPv6, nil
}

// ClientOptions specifies connection and authentication parameters for BIG-IP / BIG-IQ appliances.
type ClientOptions struct {
	Host           string
	Port           int
	Username       string
	Password       string
	SkipTLSVerify  bool
	ClientCertPath string
	ClientKeyPath  string
	Timeout        time.Duration
}

// NewClientWithOptions initializes a BIG-IP appliance client with validation and optional mTLS client certificates.
func NewClientWithOptions(opts ClientOptions) (*Client, error) {
	scheme, cleanHost, port, _, err := ParseAndValidateApplianceHost(opts.Host, opts.Port)
	if err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}

	var httpClient *http.Client
	if opts.ClientCertPath != "" {
		cert, err := client.LoadClientCertificate(opts.ClientCertPath, opts.ClientKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		if opts.SkipTLSVerify {
			httpClient = client.NewInsecureHTTPClientWithCert(timeout, cert)
		} else {
			httpClient = client.NewSecureHTTPClientWithCert(timeout, cert)
		}
	} else {
		if opts.SkipTLSVerify {
			httpClient = client.NewInsecureHTTPClient(timeout)
		} else {
			httpClient = client.NewSecureHTTPClient(timeout)
		}
	}

	return &Client{
		Scheme:     scheme,
		Host:       cleanHost,
		Port:       port,
		Username:   opts.Username,
		Password:   opts.Password,
		HTTPClient: httpClient,
	}, nil
}

// NewClient initializes a BIG-IP appliance client with insecure TLS bypass enabled by default for lab/self-signed appliances.
func NewClient(rawHost string, port int, username, password string) *Client {
	return NewClientWithTLS(rawHost, port, username, password, true)
}

// NewClientWithTLS initializes a BIG-IP appliance client with selectable TLS verification.
func NewClientWithTLS(rawHost string, port int, username, password string, skipTLSVerify bool) *Client {
	c, err := NewClientWithOptions(ClientOptions{
		Host:          rawHost,
		Port:          port,
		Username:      username,
		Password:      password,
		SkipTLSVerify: skipTLSVerify,
	})
	if err == nil {
		return c
	}

	// Fallback to preserve best-effort initialization if caller passed an unconventional host
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

	if h, pStr, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
		if p, splitErr := strconv.Atoi(pStr); splitErr == nil && p > 0 {
			port = p
		}
	}
	host = strings.Trim(host, "[]")

	if port <= 0 {
		if scheme == "http" {
			port = 80
		} else {
			port = 443
		}
	}

	var httpClient *http.Client
	if skipTLSVerify {
		httpClient = client.NewInsecureHTTPClient(60 * time.Minute)
	} else {
		httpClient = client.NewSecureHTTPClient(60 * time.Minute)
	}

	return &Client{
		Scheme:     scheme,
		Host:       host,
		Port:       port,
		Username:   username,
		Password:   password,
		HTTPClient: httpClient,
	}
}

// BaseURL returns the canonical base URL for the appliance, wrapping IPv6 addresses in RFC 3986 brackets.
func (c *Client) BaseURL() string {
	scheme := c.Scheme
	if scheme == "" {
		scheme = "https"
	}
	host := c.Host
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	if (scheme == "https" && c.Port == 443) || (scheme == "http" && c.Port == 80) {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, c.Port)
}

// CheckReady queries /mgmt/tm/sys/ready (or /mgmt/shared/echo for BIG-IQ) to verify appliance availability.
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

	if resp.StatusCode == http.StatusOK {
		return true, nil
	}

	// Fallback to /mgmt/shared/echo for BIG-IQ and appliances without sys ready
	if resp.StatusCode == http.StatusNotFound {
		echoURL := fmt.Sprintf("%s/mgmt/shared/echo", c.BaseURL())
		echoReq, echoErr := http.NewRequestWithContext(ctx, http.MethodGet, echoURL, nil)
		if echoErr == nil {
			c.setAuth(echoReq)
			if echoResp, doErr := c.HTTPClient.Do(echoReq); doErr == nil {
				defer echoResp.Body.Close()
				return echoResp.StatusCode == http.StatusOK, nil
			}
		}
	}

	return false, nil
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

// QKViewTask represents a TMOS autodeploy QKView generation task.
type QKViewTask struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	QKViewURI    string `json:"qkviewUri,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// QKViewTaskRequest represents the payload for creating a QKView task on TMOS autodeploy endpoint.
type QKViewTaskRequest struct {
	Name    string   `json:"name"`
	Options []string `json:"options,omitempty"`
}

// CreateQKViewTask creates an asynchronous QKView generation task on the BIG-IP appliance.
// By default, it requests unlimited snaplen ("-s0") as recommended for F5 iHealth diagnostics.
func (c *Client) CreateQKViewTask(ctx context.Context, filename string) (*QKViewTask, error) {
	return c.CreateQKViewTaskWithOptions(ctx, filename, []string{"-s0"})
}

// CreateQKViewTaskWithOptions creates an asynchronous QKView generation task with custom options.
func (c *Client) CreateQKViewTaskWithOptions(ctx context.Context, filename string, options []string) (*QKViewTask, error) {
	if err := ValidateQKViewFilename(filename); err != nil {
		return nil, err
	}
	payload := QKViewTaskRequest{
		Name:    filename,
		Options: options,
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/mgmt/cm/autodeploy/qkview", c.BaseURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, err
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create qkview task: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		return nil, fmt.Errorf("qkview task creation failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	var task QKViewTask
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("failed to decode qkview task response: %w", err)
	}
	return &task, nil
}

// QueryQKViewTask queries the status of an ongoing autodeploy QKView task.
func (c *Client) QueryQKViewTask(ctx context.Context, taskID string) (*QKViewTask, error) {
	url := fmt.Sprintf("%s/mgmt/cm/autodeploy/qkview/%s", c.BaseURL(), taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query qkview task %s: %w", taskID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		return nil, fmt.Errorf("query task failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	var task QKViewTask
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("failed to decode qkview task query: %w", err)
	}
	return &task, nil
}

// WaitForQKViewTask polls an autodeploy QKView task until completion or failure.
func (c *Client) WaitForQKViewTask(ctx context.Context, taskID string, pollInterval time.Duration, statusCb func(status string)) (*QKViewTask, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			task, err := c.QueryQKViewTask(ctx, taskID)
			if err != nil {
				return nil, err
			}
			if statusCb != nil {
				statusCb(task.Status)
			}
			if strings.EqualFold(task.Status, "SUCCEEDED") {
				return task, nil
			}
			if strings.EqualFold(task.Status, "FAILED") {
				msg := task.ErrorMessage
				if msg == "" {
					msg = "task reported FAILED"
				}
				return nil, fmt.Errorf("qkview task failed: %s", msg)
			}
		}
	}
}

// GenerateQKView initiates a QKView generation job.
func (c *Client) GenerateQKView(ctx context.Context, filename string, complete bool) (string, error) {
	return c.GenerateQKViewWithStatus(ctx, filename, complete, nil)
}

// GenerateQKViewWithStatus initiates a QKView generation job and reports progress to statusCb.
func (c *Client) GenerateQKViewWithStatus(ctx context.Context, filename string, complete bool, statusCb func(status string)) (string, error) {
	if filename == "" {
		cleanHost := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				return r
			}
			return '_'
		}, c.Host)
		filename = fmt.Sprintf("qkview_%s_%s.qkview", cleanHost, time.Now().UTC().Format("20060102T150405Z"))
	}
	if !strings.HasSuffix(filename, ".qkview") {
		filename += ".qkview"
	}

	if err := ValidateQKViewFilename(filename); err != nil {
		return "", err
	}

	if statusCb != nil {
		statusCb("Connecting to appliance")
	}

	// 1. Try autodeploy async task with options (defaults to -s0 unlimited snaplen) first
	var taskOptions []string
	if complete {
		taskOptions = []string{"-s0"}
	}
	task, err := c.CreateQKViewTaskWithOptions(ctx, filename, taskOptions)
	if err == nil && task != nil && task.ID != "" {
		if statusCb != nil {
			statusCb("Task ID: " + task.ID)
		}
		completedTask, err := c.WaitForQKViewTask(ctx, task.ID, 2*time.Second, statusCb)
		if err != nil {
			return "", err
		}
		return completedTask.Name, nil
	}

	// 2. Fallback to /mgmt/tm/util/qkview
	args := fmt.Sprintf("-f /var/tmp/qkviews/%s", filename)
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

	type result struct {
		resp *http.Response
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := c.HTTPClient.Do(req)
		resCh <- result{resp: resp, err: err}
	}()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	stages := []string{
		"Gathering MCP system configuration",
		"Collecting TMOS network interfaces & routing tables",
		"Compressing diagnostic logs & audit records",
		"Compiling system hardware metrics & health states",
		"Packaging diagnostics into archive",
	}
	stageIdx := 0

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			if statusCb != nil {
				statusCb(stages[stageIdx%len(stages)])
				stageIdx++
			}
		case res := <-resCh:
			if res.err != nil {
				return "", fmt.Errorf("generate qkview request failed: %w", res.err)
			}
			defer res.resp.Body.Close()

			if res.resp.StatusCode < 200 || res.resp.StatusCode >= 300 {
				b, _ := io.ReadAll(io.LimitReader(res.resp.Body, 1024*1024))
				return "", fmt.Errorf("qkview generation failed with HTTP %d: %s", res.resp.StatusCode, string(b))
			}

			return filename, nil
		}
	}
}

// DownloadFromURI streams a file from an iControl REST endpoint URI (e.g. /mgmt/cm/autodeploy/software-image-downloads/<filename>)
// to a local file using Content-Range chunking and reports progress via progressCb.
func (c *Client) DownloadFromURI(ctx context.Context, endpointURI, localPath string, progressCb func(transferred, total int64)) error {
	localPath = filepath.Clean(localPath)
	if dir := filepath.Dir(localPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create destination directory: %w", err)
		}
	}

	url := fmt.Sprintf("%s%s", c.BaseURL(), endpointURI)
	if !strings.HasPrefix(endpointURI, "/") {
		url = fmt.Sprintf("%s/%s", c.BaseURL(), endpointURI)
	}

	partPath := localPath + ".part"
	out, err := os.Create(partPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer func() {
		_ = out.Close()
		if _, err := os.Stat(partPath); err == nil {
			_ = os.Remove(partPath)
		}
	}()

	chunkSize := int64(1024 * 1024) // 1MB chunks
	var start int64 = 0
	end := chunkSize - 1
	var totalSize int64 = 0
	var currentBytes int64 = 0

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		c.setAuth(req)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Range", fmt.Sprintf("%d-%d/%d", start, end, totalSize))

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to download chunk from %s: %w", endpointURI, err)
		}

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
			_ = resp.Body.Close()
			return fmt.Errorf("chunk download failed with HTTP %d: %s", resp.StatusCode, string(b))
		}

		if totalSize == 0 {
			crange := resp.Header.Get("Content-Range")
			if crange != "" {
				parts := strings.Split(crange, "/")
				if len(parts) == 2 {
					if total, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
						totalSize = total
					}
				}
			}
		}

		n, err := io.Copy(out, resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed saving chunk: %w", err)
		}
		currentBytes += n
		if progressCb != nil && totalSize > 0 {
			progressCb(currentBytes, totalSize)
		}

		if totalSize > 0 && currentBytes >= totalSize {
			break
		}

		start += chunkSize
		if totalSize > 0 && (start+chunkSize) > totalSize {
			end = totalSize - 1
		} else {
			end = start + chunkSize - 1
		}
	}

	_ = out.Close()
	return os.Rename(partPath, localPath)
}

// DownloadQKView downloads a completed QKView from /mgmt/cm/autodeploy/qkview-downloads using chunked Content-Range.
func (c *Client) DownloadQKView(ctx context.Context, remoteFilename, localPath string, progressCb func(transferred, total int64)) error {
	if err := ValidateQKViewFilename(remoteFilename); err != nil {
		return err
	}
	endpoint := fmt.Sprintf("/mgmt/cm/autodeploy/qkview-downloads/%s", remoteFilename)
	return c.DownloadFromURI(ctx, endpoint, localPath, progressCb)
}

// DownloadFile downloads a file from the BIG-IP or BIG-IQ appliance via iControl REST.
// Downloads are strictly restricted to .qkview and .iso files.
// For files in /shared/images/, it queries /mgmt/cm/autodeploy/software-image-downloads/.
// For files in /var/tmp/qkviews/, it queries /mgmt/cm/autodeploy/qkview-downloads/.
func (c *Client) DownloadFile(ctx context.Context, remotePath, localPath string, progressCb func(transferred, total int64)) error {
	cleanRemote := path.Clean(remotePath)
	if strings.HasPrefix(remotePath, "/mgmt/") {
		ext := strings.ToLower(path.Ext(cleanRemote))
		if ext != ".qkview" && ext != ".iso" {
			return fmt.Errorf("prohibited download file %q: only .qkview and .iso files can be downloaded from appliances", remotePath)
		}
		return c.DownloadFromURI(ctx, remotePath, localPath, progressCb)
	}

	// Validate remote appliance path to reject path traversal and non-ISO/QKView extensions
	if err := ValidateApplianceDownloadPath(remotePath); err != nil {
		return err
	}

	baseFilename := path.Base(cleanRemote)

	if strings.HasPrefix(cleanRemote, "/shared/images/") || strings.HasSuffix(baseFilename, ".iso") {
		return c.DownloadFromURI(ctx, fmt.Sprintf("/mgmt/cm/autodeploy/software-image-downloads/%s", baseFilename), localPath, progressCb)
	}
	if strings.HasPrefix(cleanRemote, "/var/tmp/qkviews/") || strings.HasSuffix(baseFilename, ".qkview") {
		return c.DownloadFromURI(ctx, fmt.Sprintf("/mgmt/cm/autodeploy/qkview-downloads/%s", baseFilename), localPath, progressCb)
	}

	// Try software-image-downloads first
	err := c.DownloadFromURI(ctx, fmt.Sprintf("/mgmt/cm/autodeploy/software-image-downloads/%s", baseFilename), localPath, progressCb)
	if err == nil {
		return nil
	}

	// Try qkview-downloads next
	err = c.DownloadFromURI(ctx, fmt.Sprintf("/mgmt/cm/autodeploy/qkview-downloads/%s", baseFilename), localPath, progressCb)
	if err == nil {
		return nil
	}

	return fmt.Errorf("iControl REST download failed for %s: %w", remotePath, err)
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
	req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")
}
