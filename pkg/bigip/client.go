package bigip

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"myf5-cli/pkg/auth"
	"myf5-cli/pkg/client"
)

// Client interacts with BIG-IP and BIG-IQ iControl REST APIs.
type Client struct {
	Scheme         string
	Host           string
	Port           int
	Username       string
	Password       string
	PasswordBytes  []byte
	TargetType     string
	authMu         sync.Mutex
	authToken      []byte
	refreshToken   []byte
	loginFailedErr error
	HTTPClient     *http.Client
}

// Zero securely scrubs appliance password bytes and REST tokens from RAM and clears secret fields.
func (c *Client) Zero() {
	if c == nil {
		return
	}
	c.authMu.Lock()
	if len(c.authToken) > 0 {
		auth.ZeroBytes(c.authToken)
		c.authToken = nil
	}
	if len(c.refreshToken) > 0 {
		auth.ZeroBytes(c.refreshToken)
		c.refreshToken = nil
	}
	c.loginFailedErr = nil
	c.authMu.Unlock()

	if len(c.PasswordBytes) > 0 {
		auth.ZeroBytes(c.PasswordBytes)
		c.PasswordBytes = nil
	}
	c.Password = ""
}

// String implements fmt.Stringer to ensure appliance credentials are never leaked in logs.
func (c *Client) String() string {
	if c == nil {
		return "<nil>"
	}
	pw := "[REDACTED]"
	if c.Password == "" && len(c.PasswordBytes) == 0 {
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
	if strings.Contains(rawHost, "://") {
		u, err := url.Parse(rawHost)
		if err == nil {
			if u.User != nil {
				return "", "", 0, false, fmt.Errorf("embedded credentials in host URL %q are prohibited", rawHost)
			}
			if u.RawQuery != "" || u.Fragment != "" {
				return "", "", 0, false, fmt.Errorf("query parameters or fragments in host URL %q are prohibited", rawHost)
			}
		}
	}

	scheme = "https"
	s := strings.TrimSpace(rawHost)
	if strings.HasPrefix(s, "http://") {
		trimmed := strings.TrimPrefix(s, "http://")
		hostPart := trimmed
		if h, _, splitErr := net.SplitHostPort(trimmed); splitErr == nil {
			hostPart = h
		}
		hostPart = strings.Trim(hostPart, "[]")
		if hostPart != "127.0.0.1" && hostPart != "::1" && hostPart != "localhost" {
			return "", "", 0, false, fmt.Errorf("unencrypted HTTP is forbidden: appliance communication requires HTTPS (use https://%s or omit the scheme)", trimmed)
		}
		scheme = "http"
		s = trimmed
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
	PasswordBytes  []byte
	TargetType     string
	SkipTLSVerify  bool
	ClientCertPath string
	ClientKeyPath  string
	Timeout        time.Duration
	HTTPClient     *http.Client
}

func isTruthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// NewClientWithOptions initializes a BIG-IP / BIG-IQ appliance client with validation and optional mTLS client certificates.
func NewClientWithOptions(opts ClientOptions) (*Client, error) {
	if opts.Username != "" {
		if err := ValidateApplianceUsername(opts.Username); err != nil {
			return nil, err
		}
	}
	if opts.Password != "" {
		if err := ValidateAppliancePassword(opts.Password); err != nil {
			return nil, err
		}
	} else if len(opts.PasswordBytes) > 0 {
		if err := ValidateAppliancePassword(string(opts.PasswordBytes)); err != nil {
			return nil, err
		}
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("invalid port %d: must be between 1 and 65535", opts.Port)
	}
	if opts.ClientCertPath != "" {
		if _, err := ValidateCredentialFilePath(opts.ClientCertPath, "Client certificate"); err != nil {
			return nil, err
		}
	}
	if opts.ClientKeyPath != "" {
		if _, err := ValidateCredentialFilePath(opts.ClientKeyPath, "Client key"); err != nil {
			return nil, err
		}
	}

	scheme, cleanHost, port, _, err := ParseAndValidateApplianceHost(opts.Host, opts.Port)
	if err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}

	skipTLS := opts.SkipTLSVerify
	if isTruthyEnv("MYF5_STRICT_APPLIANCE_TLS") {
		skipTLS = false
	}

	var httpClient *http.Client
	if opts.HTTPClient != nil {
		httpClient = opts.HTTPClient
	} else if opts.ClientCertPath != "" {
		cert, err := client.LoadClientCertificate(opts.ClientCertPath, opts.ClientKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		if skipTLS {
			httpClient = client.NewInsecureHTTPClientWithCert(timeout, cert)
		} else {
			httpClient = client.NewSecureHTTPClientWithCert(timeout, cert)
		}
	} else {
		if skipTLS {
			httpClient = client.NewInsecureHTTPClient(timeout)
		} else {
			httpClient = client.NewSecureHTTPClient(timeout)
		}
	}

	pwBytes := opts.PasswordBytes
	if len(pwBytes) == 0 && opts.Password != "" {
		pwBytes = []byte(opts.Password)
	}

	return &Client{
		Scheme:        scheme,
		Host:          cleanHost,
		Port:          port,
		Username:      opts.Username,
		Password:      opts.Password,
		PasswordBytes: pwBytes,
		TargetType:    normalizeTargetType(opts.TargetType),
		HTTPClient:    httpClient,
	}, nil
}

// isBigIQTarget reports whether targetType refers to a BIG-IQ appliance (accepting both "BIG-IQ" and "bigiq").
func isBigIQTarget(targetType string) bool {
	t := strings.TrimSpace(targetType)
	return strings.EqualFold(t, "BIG-IQ") || strings.EqualFold(t, "bigiq")
}

func normalizeTargetType(targetType string) string {
	t := strings.TrimSpace(targetType)
	if isBigIQTarget(t) {
		return "BIG-IQ"
	}
	if strings.EqualFold(t, "BIG-IP") || strings.EqualFold(t, "bigip") {
		return "BIG-IP"
	}
	return targetType
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

// CheckReadyWithKind queries /mgmt/tm/sys/ready (with BIG-IQ fallbacks to /mgmt/shared/echo,
// /mgmt/cm/system/machineid-resolver, and /mgmt/shared/failover-state) to verify appliance
// availability and returns the detected appliance kind ("BIG-IP", "BIG-IQ", or "" if not ready).
func (c *Client) CheckReadyWithKind(ctx context.Context) (string, error) {
	probeOK := func(endpoint string) (int, error) {
		u := fmt.Sprintf("%s%s", c.BaseURL(), endpoint)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return 0, err
		}
		resp, err := c.doWithAuth(req)
		if err != nil {
			return 0, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}

	url := fmt.Sprintf("%s/mgmt/tm/sys/ready", c.BaseURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.doWithAuth(req)
	if err != nil {
		return "", fmt.Errorf("device reachability check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("authentication failed on %s (HTTP 401): verify appliance username and password", c.Host)
	}

	if resp.StatusCode == http.StatusOK {
		verURL := fmt.Sprintf("%s/mgmt/tm/sys/version", c.BaseURL())
		verReq, verErr := http.NewRequestWithContext(ctx, http.MethodGet, verURL, nil)
		if verErr == nil {
			if verResp, verDoErr := c.doWithAuth(verReq); verDoErr == nil {
				defer verResp.Body.Close()
				if verResp.StatusCode == http.StatusOK {
					verBody, _ := io.ReadAll(io.LimitReader(verResp.Body, 512*1024))
					var vInfo SystemInfo
					parseVersionJSON(verBody, &vInfo)
					if vInfo.ApplianceKind() == "BIG-IQ" {
						return "BIG-IQ", nil
					}
				}
			}
		}
		return "BIG-IP", nil
	}

	// Fallback to /mgmt/shared/echo for BIG-IQ and appliances without /mgmt/tm/sys/ready
	// (BIG-IQ webd / restjavad returns 404 or 403 on /mgmt/tm/sys/ready).
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadRequest {
		if code, echoErr := probeOK("/mgmt/shared/echo"); echoErr == nil {
			if code == http.StatusOK {
				return "BIG-IQ", nil
			}
			if code == http.StatusUnauthorized {
				return "", fmt.Errorf("authentication failed on %s (HTTP 401): verify appliance username and password", c.Host)
			}
		}
		// When explicitly targeting BIG-IQ, also probe documented public BIG-IQ API endpoints
		// in case webd blocks internal /mgmt/shared/echo on hardened BIG-IQ 8.x builds.
		if isBigIQTarget(c.TargetType) {
			for _, pubEndpoint := range []string{"/mgmt/cm/system/machineid-resolver", "/mgmt/shared/failover-state"} {
				if code, pubErr := probeOK(pubEndpoint); pubErr == nil {
					if code == http.StatusOK {
						return "BIG-IQ", nil
					}
					if code == http.StatusUnauthorized {
						return "", fmt.Errorf("authentication failed on %s (HTTP 401): verify appliance username and password", c.Host)
					}
				}
			}
		}
	}

	return "", nil
}

// CheckReady queries /mgmt/tm/sys/ready (or /mgmt/shared/echo for BIG-IQ) to verify appliance availability.
func (c *Client) CheckReady(ctx context.Context) (bool, error) {
	kind, err := c.CheckReadyWithKind(ctx)
	if err != nil {
		return false, err
	}
	return kind != "", nil
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
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doWithAuth(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create qkview task: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		return nil, fmt.Errorf("qkview task creation failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	var task QKViewTask
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&task); err != nil {
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

	resp, err := c.doWithAuth(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query qkview task %s: %w", taskID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		return nil, fmt.Errorf("query task failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	var task QKViewTask
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&task); err != nil {
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
	req.Header.Set("Content-Type", "application/json")

	type result struct {
		resp *http.Response
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := c.doWithAuth(req)
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

			if res.resp.StatusCode == http.StatusNotFound {
				// Fallback to bash for BIG-IQ where /mgmt/tm/util/qkview may return 404
				if err := ValidateQKViewFilename(filename); err != nil {
					return "", err
				}
				bashArgs := fmt.Sprintf("-c \"mkdir -p /var/tmp/qkviews && /usr/bin/qkview %s-f /var/tmp/qkviews/'%s' && cp /var/tmp/qkviews/'%s' /var/config/rest/downloads/\"", map[bool]string{true: "-s0 ", false: ""}[complete], filename, filename)
				bashPayload := map[string]string{
					"command":     "run",
					"utilCmdArgs": bashArgs,
				}
				bashBytes, _ := json.Marshal(bashPayload)
				bashURL := fmt.Sprintf("%s/mgmt/tm/util/bash", c.BaseURL())
				bashReq, err := http.NewRequestWithContext(ctx, http.MethodPost, bashURL, bytes.NewReader(bashBytes))
				if err != nil {
					return "", err
				}
				bashReq.Header.Set("Content-Type", "application/json")
				bashResp, err := c.doWithAuth(bashReq)
				if err != nil {
					return "", fmt.Errorf("bash fallback for qkview failed: %w", err)
				}
				defer bashResp.Body.Close()
				bashBody, _ := io.ReadAll(io.LimitReader(bashResp.Body, 1024*1024))
				if bashResp.StatusCode < 200 || bashResp.StatusCode >= 300 {
					summary := summarizeHTTPErrorBody(bashBody)
					if bashResp.StatusCode == http.StatusForbidden || bashResp.StatusCode == http.StatusNotFound {
						return "", fmt.Errorf("bash qkview generation failed with HTTP %d (%s): /mgmt/tm/util/bash is blocked or unavailable on this appliance (use --method scp or SSH)", bashResp.StatusCode, summary)
					}
					return "", fmt.Errorf("bash qkview generation failed with HTTP %d: %s", bashResp.StatusCode, summary)
				}
				if strings.Contains(string(bashBody), "command not found") || strings.Contains(string(bashBody), "syntax error") {
					return "", fmt.Errorf("bash qkview generation command failed: %s", summarizeHTTPErrorBody(bashBody))
				}
				return filename, nil
			}

			if res.resp.StatusCode < 200 || res.resp.StatusCode >= 300 {
				b, _ := io.ReadAll(io.LimitReader(res.resp.Body, 1024*1024))
				return "", fmt.Errorf("qkview generation failed with HTTP %d: %s", res.resp.StatusCode, summarizeHTTPErrorBody(b))
			}

			return filename, nil
		}
	}
}

// DownloadFromURI streams a file from an iControl REST endpoint URI (e.g. /mgmt/cm/autodeploy/software-image-downloads/<filename>)
// to a local file using Content-Range chunking (or single-response 200 OK for BIG-IQ /mgmt/shared/file-transfer/downloads)
// and reports progress via progressCb.
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
	out, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
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
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Range", fmt.Sprintf("%d-%d/%d", start, end, totalSize))

		resp, err := c.doWithAuth(req)
		if err != nil {
			return fmt.Errorf("failed to download chunk from %s: %w", endpointURI, err)
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
			_ = resp.Body.Close()
			return fmt.Errorf("chunk download failed with HTTP %d: %s", resp.StatusCode, summarizeHTTPErrorBody(b))
		}

		crange := resp.Header.Get("Content-Range")
		if totalSize == 0 && crange != "" {
			parts := strings.Split(crange, "/")
			if len(parts) == 2 {
				if total, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					totalSize = total
				}
			}
		}

		n, err := io.Copy(out, resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed saving chunk: %w", err)
		}
		currentBytes += n

		// Endpoints such as BIG-IQ GET /mgmt/shared/file-transfer/downloads/<file-name>
		// return the full file in a single 200 OK body without a Content-Range header.
		if crange == "" && resp.StatusCode == http.StatusOK {
			totalSize = currentBytes
			if progressCb != nil && totalSize > 0 {
				progressCb(currentBytes, totalSize)
			}
			break
		}

		if progressCb != nil && totalSize > 0 {
			progressCb(currentBytes, totalSize)
		}

		if (totalSize > 0 && currentBytes >= totalSize) || n == 0 {
			break
		}

		start += chunkSize
		if totalSize > 0 && (start+chunkSize) > totalSize {
			end = totalSize - 1
		} else {
			end = start + chunkSize - 1
		}
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("failed closing downloaded file: %w", err)
	}
	return os.Rename(partPath, localPath)
}

// DownloadQKView downloads a completed QKView from /mgmt/cm/autodeploy/qkview-downloads using chunked Content-Range.
func (c *Client) DownloadQKView(ctx context.Context, remoteFilename, localPath string, progressCb func(transferred, total int64)) error {
	if err := ValidateQKViewFilename(remoteFilename); err != nil {
		return err
	}
	if isBigIQTarget(c.TargetType) {
		return fmt.Errorf("BIG-IQ does not expose /mgmt/cm/autodeploy/qkview-downloads over iControl REST; use --method scp to download /var/tmp/qkviews/%s", remoteFilename)
	}
	endpoint := fmt.Sprintf("/mgmt/cm/autodeploy/qkview-downloads/%s", remoteFilename)
	return c.DownloadFromURI(ctx, endpoint, localPath, progressCb)
}

// DownloadFile downloads a file from the BIG-IP or BIG-IQ appliance via iControl REST.
// Downloads are strictly restricted to .qkview and .iso files.
// For files in /shared/images/, it queries /mgmt/cm/autodeploy/software-image-downloads/.
// For files in /var/config/rest/downloads/, it queries /mgmt/shared/file-transfer/downloads/.
// For files in /var/tmp/qkviews/ (BIG-IP only), it queries /mgmt/cm/autodeploy/qkview-downloads/.
// Other absolute paths are rejected because iControl REST endpoints only serve those specific directories.
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
	ext := strings.ToLower(path.Ext(baseFilename))

	if strings.HasPrefix(cleanRemote, "/") {
		if path.Dir(cleanRemote) == "/shared/images" && ext == ".iso" {
			return c.DownloadFromURI(ctx, fmt.Sprintf("/mgmt/cm/autodeploy/software-image-downloads/%s", baseFilename), localPath, progressCb)
		}
		if path.Dir(cleanRemote) == "/var/config/rest/downloads" && (ext == ".iso" || ext == ".qkview") {
			return c.DownloadFromURI(ctx, fmt.Sprintf("/mgmt/shared/file-transfer/downloads/%s", baseFilename), localPath, progressCb)
		}
		if path.Dir(cleanRemote) == "/var/tmp/qkviews" && ext == ".qkview" {
			if isBigIQTarget(c.TargetType) {
				return fmt.Errorf("BIG-IQ does not expose /mgmt/cm/autodeploy/qkview-downloads for /var/tmp/qkviews/ over iControl REST (supported BIG-IQ REST download paths: /shared/images/*.iso and /var/config/rest/downloads/*.{iso,qkview}); use --method scp for %q", remotePath)
			}
			return c.DownloadFromURI(ctx, fmt.Sprintf("/mgmt/cm/autodeploy/qkview-downloads/%s", baseFilename), localPath, progressCb)
		}
		return fmt.Errorf("iControl REST download only supports .qkview files in /var/tmp/qkviews/, .iso files in /shared/images/, and .iso/.qkview files in /var/config/rest/downloads/ (got %q); use --method scp for other paths", remotePath)
	}

	if ext == ".iso" {
		return c.DownloadFromURI(ctx, fmt.Sprintf("/mgmt/cm/autodeploy/software-image-downloads/%s", baseFilename), localPath, progressCb)
	}
	if ext == ".qkview" {
		if isBigIQTarget(c.TargetType) {
			return fmt.Errorf("BIG-IQ does not expose /mgmt/cm/autodeploy/qkview-downloads over iControl REST; specify /var/config/rest/downloads/%s or use --method scp", baseFilename)
		}
		return c.DownloadFromURI(ctx, fmt.Sprintf("/mgmt/cm/autodeploy/qkview-downloads/%s", baseFilename), localPath, progressCb)
	}

	return fmt.Errorf("iControl REST download failed for %s: unsupported extension %q", remotePath, ext)
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

	resp, err := c.doWithAuth(req)
	if err != nil {
		return fmt.Errorf("delete qkview failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		return fmt.Errorf("delete failed with HTTP %d: %s", resp.StatusCode, summarizeHTTPErrorBody(b))
	}

	return nil
}

// GetEndpointExample queries the BIG-IP iControl REST `/example` endpoint for a given URI.
// On BIG-IP, appending `/example` to any REST worker endpoint returns the JSON template / schema
// describing the expected request body, parameters, and response attributes.
func (c *Client) GetEndpointExample(ctx context.Context, endpoint string) ([]byte, error) {
	p := strings.TrimSpace(endpoint)
	if p == "" {
		return nil, errors.New("endpoint path cannot be empty")
	}

	// Clean leading and trailing slashes for consistent parsing
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")

	// If it doesn't start with mgmt/, prepend mgmt/
	if !strings.HasPrefix(p, "mgmt/") {
		p = "mgmt/" + p
	}

	// If it doesn't already end with /example, append /example
	if !strings.HasSuffix(p, "/example") {
		p = p + "/example"
	}

	fullURL := fmt.Sprintf("%s/%s", c.BaseURL(), p)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for example endpoint: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.doWithAuth(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch example for %s: %w", p, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response from %s: %w", p, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("example request to %s returned HTTP %d: %s", p, resp.StatusCode, summarizeHTTPErrorBody(body))
	}

	// Indent JSON if valid
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err == nil {
		return pretty.Bytes(), nil
	}

	return body, nil
}

var (
	htmlTagRe        = regexp.MustCompile(`<[^>]+>`)
	htmlTitleRe      = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlWebdFooterRe = regexp.MustCompile(`(?is)<center>\s*webd\s*</center>`)
	multiSpaceRe     = regexp.MustCompile(`\s+`)
)

// summarizeHTTPErrorBody converts multi-line HTML error pages (e.g. BIG-IQ webd 403 or
// WSGI/Apache 404 responses) into compact single-line summaries while preserving JSON
// and plain-text error payloads intact.
func summarizeHTTPErrorBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	if !strings.Contains(lower, "<html") && !strings.Contains(lower, "<!doctype html") && !strings.Contains(lower, "<title>") && !strings.Contains(lower, "<h1>") {
		return s
	}

	isWebd := htmlWebdFooterRe.MatchString(s)
	title := ""
	if m := htmlTitleRe.FindStringSubmatch(s); len(m) > 1 {
		title = multiSpaceRe.ReplaceAllString(strings.TrimSpace(htmlTagRe.ReplaceAllString(m[1], " ")), " ")
	}

	stripped := htmlTagRe.ReplaceAllString(s, " ")
	stripped = multiSpaceRe.ReplaceAllString(strings.TrimSpace(stripped), " ")
	if title != "" && isWebd && (stripped == title+" "+title+" webd" || strings.EqualFold(stripped, title+" webd")) {
		return title + " (webd)"
	}
	if title != "" && strings.HasPrefix(stripped, title+" ") {
		rest := strings.TrimSpace(strings.TrimPrefix(stripped, title))
		// Avoid repeating identical heading after <title> (e.g. "404 Not Found" + "Not Found")
		for _, dup := range []string{title, strings.TrimSpace(strings.TrimLeft(title, "0123456789 "))} {
			if dup != "" && strings.HasPrefix(rest, dup) {
				rest = strings.TrimSpace(strings.TrimPrefix(rest, dup))
			}
		}
		if isWebd && strings.HasSuffix(rest, "webd") {
			rest = strings.TrimSpace(strings.TrimSuffix(rest, "webd"))
			if rest == "" {
				return title + " (webd)"
			}
			return fmt.Sprintf("%s: %s (webd)", title, rest)
		}
		if rest == "" {
			return title
		}
		return fmt.Sprintf("%s: %s", title, rest)
	}
	return stripped
}

type authHTTPStatusError struct {
	statusCode int
	message    string
}

func (e *authHTTPStatusError) Error() string {
	return fmt.Sprintf("login failed with HTTP %d: %s", e.statusCode, e.message)
}

func (c *Client) hasClientCert() bool {
	if c == nil || c.HTTPClient == nil {
		return false
	}
	if tr, ok := c.HTTPClient.Transport.(*http.Transport); ok && tr != nil && tr.TLSClientConfig != nil {
		return len(tr.TLSClientConfig.Certificates) > 0
	}
	return false
}

func (c *Client) setAuth(req *http.Request) {
	c.authMu.Lock()
	tokenCopy := string(c.authToken)
	c.authMu.Unlock()

	if tokenCopy != "" {
		req.Header.Set("X-F5-Auth-Token", tokenCopy)
		req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")
		return
	}

	pwBytes := c.PasswordBytes
	if len(pwBytes) == 0 && c.Password != "" {
		pwBytes = []byte(c.Password)
	}
	if len(pwBytes) == 0 && c.hasClientCert() {
		req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")
		return
	}
	buf := make([]byte, len(c.Username)+1+len(pwBytes))
	copy(buf, c.Username)
	buf[len(c.Username)] = ':'
	copy(buf[len(c.Username)+1:], pwBytes)
	authHeader := base64.StdEncoding.EncodeToString(buf)
	auth.ZeroBytes(buf)

	req.Header.Set("Authorization", "Basic "+authHeader)
	req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")
}

func (c *Client) storeTokensLocked(accessToken, refresh string) {
	if len(c.authToken) > 0 {
		auth.ZeroBytes(c.authToken)
	}
	c.authToken = []byte(accessToken)
	if refresh != "" {
		if len(c.refreshToken) > 0 {
			auth.ZeroBytes(c.refreshToken)
		}
		c.refreshToken = []byte(refresh)
	}
	c.loginFailedErr = nil
}

func (c *Client) postAuthTokenPayload(ctx context.Context, endpointURL string, payload any) (string, string, int, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to marshal login payload: %w", err)
	}
	defer auth.ZeroBytes(bodyBytes)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", "", 0, fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		return "", "", resp.StatusCode, &authHTTPStatusError{
			statusCode: resp.StatusCode,
			message:    summarizeHTTPErrorBody(b),
		}
	}

	var parsed struct {
		Token struct {
			Token string `json:"token"`
		} `json:"token"`
		RefreshToken struct {
			Token string `json:"token"`
		} `json:"refreshToken"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&parsed); err != nil {
		return "", "", resp.StatusCode, fmt.Errorf("failed to decode login token response: %w", err)
	}
	if parsed.Token.Token == "" {
		return "", "", resp.StatusCode, errors.New("empty token received from login response")
	}
	return parsed.Token.Token, parsed.RefreshToken.Token, resp.StatusCode, nil
}

// acquireAuthToken obtains an iControl REST X-F5-Auth-Token via POST /mgmt/shared/authn/login.
// Per F5 BIG-IQ API Reference (r_auth_login.html), BIG-IQ local authentication omits
// loginProviderName (resolving to provider "local"), whereas BIG-IP TMOS uses
// "loginProviderName": "tmos". Both forms are supported cleanly without redundant lockouts.
func (c *Client) acquireAuthToken(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	return c.acquireAuthTokenLocked(ctx)
}

func (c *Client) acquireAuthTokenLocked(ctx context.Context) error {
	loginURL := fmt.Sprintf("%s/mgmt/shared/authn/login", c.BaseURL())
	pw := string(c.PasswordBytes)
	if pw == "" && c.Password != "" {
		pw = c.Password
	}

	if pw == "" && c.hasClientCert() {
		tok, refTok, status, err := c.postAuthTokenPayload(ctx, loginURL, map[string]bool{"needsToken": true})
		if err != nil {
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				c.loginFailedErr = err
			}
			return err
		}
		c.storeTokensLocked(tok, refTok)
		return nil
	}

	bigiqPayload := map[string]string{
		"username": c.Username,
		"password": pw,
	}
	tmosPayload := map[string]string{
		"username":          c.Username,
		"password":          pw,
		"loginProviderName": "tmos",
	}

	if isBigIQTarget(c.TargetType) {
		tok, refTok, status, err := c.postAuthTokenPayload(ctx, loginURL, bigiqPayload)
		if err == nil {
			c.storeTokensLocked(tok, refTok)
			return nil
		}
		// Only retry with "tmos" if the endpoint rejected the schema (HTTP 400) or mentioned loginProviderName,
		// avoiding duplicate failed login attempts on BIG-IQ when a password is wrong.
		if status == http.StatusBadRequest || (status == http.StatusUnauthorized && strings.Contains(strings.ToLower(err.Error()), "provider")) {
			if tok2, refTok2, status2, err2 := c.postAuthTokenPayload(ctx, loginURL, tmosPayload); err2 == nil {
				c.storeTokensLocked(tok2, refTok2)
				return nil
			} else if status2 == http.StatusUnauthorized || status2 == http.StatusForbidden {
				c.loginFailedErr = err2
				return err2
			}
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			c.loginFailedErr = err
		}
		return err
	}

	// Default (BIG-IP or auto): try standard TMOS provider first; if rejected with 400/401
	// (e.g. auto-detecting a BIG-IQ whose local provider is "local", not "tmos"), retry without loginProviderName.
	tok, refTok, status, err := c.postAuthTokenPayload(ctx, loginURL, tmosPayload)
	if err == nil {
		c.storeTokensLocked(tok, refTok)
		return nil
	}
	if status == http.StatusBadRequest || status == http.StatusUnauthorized {
		if tok2, refTok2, _, err2 := c.postAuthTokenPayload(ctx, loginURL, bigiqPayload); err2 == nil {
			c.storeTokensLocked(tok2, refTok2)
			return nil
		}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		c.loginFailedErr = err
	}
	return err
}

// refreshAuthToken exchanges an unexpired BIG-IQ refreshToken via POST /mgmt/shared/authn/exchange
// (F5 BIG-IQ API Reference r_auth_exchange.html) when the 5-minute access token expires mid-session,
// falling back to acquireAuthTokenLocked if no refresh token is available or exchange fails.
func (c *Client) refreshAuthTokenLocked(ctx context.Context) error {
	if len(c.authToken) > 0 {
		auth.ZeroBytes(c.authToken)
		c.authToken = nil
	}
	if len(c.refreshToken) > 0 {
		exchangeURL := fmt.Sprintf("%s/mgmt/shared/authn/exchange", c.BaseURL())
		payload := map[string]any{
			"refreshToken": map[string]string{
				"token": string(c.refreshToken),
			},
		}
		if tok, refTok, _, err := c.postAuthTokenPayload(ctx, exchangeURL, payload); err == nil {
			c.storeTokensLocked(tok, refTok)
			return nil
		}
		auth.ZeroBytes(c.refreshToken)
		c.refreshToken = nil
	}
	if len(c.PasswordBytes) > 0 || c.Password != "" || c.hasClientCert() {
		return c.acquireAuthTokenLocked(ctx)
	}
	return errors.New("cannot refresh expired auth token without credentials")
}

func (c *Client) doWithAuth(req *http.Request) (*http.Response, error) {
	hasCreds := len(c.PasswordBytes) > 0 || c.Password != "" || c.hasClientCert()

	c.authMu.Lock()
	hasToken := len(c.authToken) > 0
	priorLoginErr := c.loginFailedErr
	isBigIQ := isBigIQTarget(c.TargetType)
	if isBigIQ && !hasToken && hasCreds && priorLoginErr == nil {
		// BIG-IQ disables HTTP Basic authentication by default (r_auth_login.html) and its
		// WSGI file-transfer handlers require X-F5-Auth-Token on the initial request.
		tokenErr := c.acquireAuthTokenLocked(req.Context())
		if tokenErr == nil {
			hasToken = true
		} else {
			var statusErr *authHTTPStatusError
			if errors.As(tokenErr, &statusErr) && (statusErr.statusCode == http.StatusUnauthorized || statusErr.statusCode == http.StatusForbidden) {
				c.authMu.Unlock()
				return nil, fmt.Errorf("BIG-IQ authentication failed on %s: %w", c.Host, tokenErr)
			}
			// If /mgmt/shared/authn/login returned 404 (e.g. a simplified mock server in tests),
			// fall through and send the request normally.
		}
	} else if isBigIQ && !hasToken && priorLoginErr != nil {
		c.authMu.Unlock()
		return nil, fmt.Errorf("BIG-IQ authentication failed on %s: %w", c.Host, priorLoginErr)
	}
	c.authMu.Unlock()

	c.setAuth(req)
	resp, err := c.HTTPClient.Do(req)
	if err == nil && resp.StatusCode == http.StatusUnauthorized && hasCreds && (req.Body == nil || req.GetBody != nil) {
		origBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		_ = resp.Body.Close()

		c.authMu.Lock()
		var tokenErr error
		if !hasToken && len(c.authToken) > 0 {
			// Another concurrent caller already acquired a token.
			tokenErr = nil
		} else if !hasToken && c.loginFailedErr != nil {
			tokenErr = c.loginFailedErr
		} else if hasToken {
			// Existing token expired (e.g. BIG-IQ 5-minute access token timeout); refresh once.
			tokenErr = c.refreshAuthTokenLocked(req.Context())
		} else {
			tokenErr = c.acquireAuthTokenLocked(req.Context())
		}
		c.authMu.Unlock()

		if tokenErr == nil {
			req2 := req.Clone(req.Context())
			if req.GetBody != nil {
				b, getErr := req.GetBody()
				if getErr != nil {
					resp.Body = io.NopCloser(bytes.NewReader(origBody))
					return resp, getErr
				}
				req2.Body = b
			}
			req2.Header.Del("Authorization")
			c.setAuth(req2)
			return c.HTTPClient.Do(req2)
		}

		resp.Body = io.NopCloser(bytes.NewReader(origBody))
	}
	return resp, err
}

var validUsernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.\-@]*$`)

// ValidateApplianceUsername validates a username for BIG-IP / BIG-IQ appliance access.
func ValidateApplianceUsername(username string) error {
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if len(username) > 64 {
		return fmt.Errorf("username exceeds maximum length of 64 characters")
	}
	if strings.Contains(username, ":") {
		return fmt.Errorf("username cannot contain a colon")
	}
	for _, r := range username {
		if r < 32 || r == 127 || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return fmt.Errorf("username cannot contain control characters or whitespace")
		}
	}
	if !validUsernameRegex.MatchString(username) {
		return fmt.Errorf("username contains invalid characters")
	}
	return nil
}

// K2873SpecialCharacters contains the 32 allowed special characters defined in F5 K2873.
const K2873SpecialCharacters = "!@#$%^&*()`~-+=[]{};’:\"'./<>|\\?_"

// ValidateAppliancePassword validates an appliance password against F5 K2873 policy.
// It allows alphanumeric characters and the 32 special characters specified in K2873.
// To prevent secret disclosure in logs or stderr, error messages never include the password or rejected character.
func ValidateAppliancePassword(password string) error {
	if password == "" {
		return fmt.Errorf("appliance password cannot be empty")
	}
	if len(password) > 255 {
		return fmt.Errorf("appliance password exceeds maximum length of 255 characters")
	}
	for i, r := range password {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(K2873SpecialCharacters, r) {
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return fmt.Errorf("invalid appliance password: whitespace is not permitted (refer to F5 K2873 for allowed password characters)")
		}
		if r < 32 || r == 127 {
			return fmt.Errorf("invalid appliance password: control characters are not permitted (refer to F5 K2873)")
		}
		return fmt.Errorf("invalid appliance password: contains unsupported character at byte offset %d (refer to F5 K2873 for allowed special characters)", i)
	}
	return nil
}

// ValidatePort validates a port string and returns the integer value.
func ValidatePort(rawPort string) (int, error) {
	portStr := strings.TrimSpace(rawPort)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: must be a numeric value", rawPort)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}
	return port, nil
}

// ValidateCredentialFilePath validates a file path used for credentials (keys/certs).
func ValidateCredentialFilePath(filePath, label string) (string, error) {
	for _, r := range filePath {
		if r < 32 || r == 127 {
			return "", fmt.Errorf("%s path cannot contain control characters", label)
		}
	}
	cleanPath := filepath.Clean(strings.TrimSpace(filePath))
	fi, err := os.Stat(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to access %s file %s: %w", label, cleanPath, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s must be a regular file: %s", label, cleanPath)
	}
	if fi.Size() == 0 {
		return "", fmt.Errorf("%s file is empty: %s", label, cleanPath)
	}
	if fi.Size() > 1024*1024 {
		return "", fmt.Errorf("%s file exceeds maximum allowed size of 1MB: %s", label, cleanPath)
	}
	return cleanPath, nil
}
