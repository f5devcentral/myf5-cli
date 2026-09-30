package bigip

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
func NewClient(host string, port int, username, password string) *Client {
	if port <= 0 {
		port = 443
	}
	return &Client{
		Scheme:     "https",
		Host:       host,
		Port:       port,
		Username:   username,
		Password:   password,
		HTTPClient: client.NewInsecureHTTPClient(60 * time.Minute),
	}
}

func (c *Client) getScheme() string {
	if c.Scheme != "" {
		return c.Scheme
	}
	return "https"
}

// CheckReady queries /mgmt/tm/sys/ready to verify appliance availability.
func (c *Client) CheckReady(ctx context.Context) (bool, error) {
	url := fmt.Sprintf("%s://%s:%d/mgmt/tm/sys/ready", c.getScheme(), c.Host, c.Port)
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

// GenerateQKView initiates an asynchronous non-truncating QKView generation job.
func (c *Client) GenerateQKView(ctx context.Context, filename string, complete bool) (string, error) {
	if filename == "" {
		filename = fmt.Sprintf("qkview_%s_%d.qkview", c.Host, time.Now().Unix())
	}
	if !strings.HasSuffix(filename, ".qkview") {
		filename += ".qkview"
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

	url := fmt.Sprintf("%s://%s:%d/mgmt/tm/util/qkview", c.getScheme(), c.Host, c.Port)
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
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("qkview generation failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	return filename, nil
}

// DownloadQKView downloads a completed QKView from /mgmt/cm/autodeploy/qkview.
func (c *Client) DownloadQKView(ctx context.Context, remoteFilename, localPath string) error {
	url := fmt.Sprintf("%s://%s:%d/mgmt/cm/autodeploy/qkview/%s", c.getScheme(), c.Host, c.Port, remoteFilename)
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
	url := fmt.Sprintf("%s://%s:%d/mgmt/cm/autodeploy/qkview/%s", c.getScheme(), c.Host, c.Port, remoteFilename)
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
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed with HTTP %d: %s", resp.StatusCode, string(b))
	}

	return nil
}

func (c *Client) setAuth(req *http.Request) {
	auth := base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + c.Password))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("User-Agent", "myf5-api-go/1.0.0")
}
