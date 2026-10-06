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
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"myf5-cli/pkg/client"
)

// GeoIPOptions configures connection, execution, and cleanup parameters for appliance GeoIP updates.
type GeoIPOptions struct {
	TargetType     string         // Appliance kind: must be "BIG-IP" (BIG-IQ is unsupported)
	Method         TransferMethod // "auto", "icontrol", "scp", or "ssh"
	Username       string         // Appliance username (default: "admin")
	Password       string         // Appliance password
	Port           int            // Management port override (default: 443 for REST, 22 for SSH)
	IdentityFile   string         // SSH private key path
	SSHPassphrase  string         // SSH private key passphrase
	ClientCertPath string         // mTLS client certificate path
	ClientKeyPath  string         // mTLS client private key path
	Timeout        time.Duration  // Execution timeout (default: 3 minutes)
	Force          bool           // Pass --force to rpm via geoip_update_data -f (default: true)
	KeepExtracted  bool           // Retain extracted files on appliance (default: false, cleans up temp dir)
}

// GeoIPResult captures the output, packages, and verification status of a GeoIP database update.
type GeoIPResult struct {
	TargetHost    string         `json:"targetHost"`
	ArchiveFile   string         `json:"archiveFile"`
	ExtractedDir  string         `json:"extractedDir,omitempty"`
	InstalledRPMs []string       `json:"installedRpms,omitempty"`
	LookupTest    string         `json:"lookupTest,omitempty"`
	RawOutput     string         `json:"rawOutput,omitempty"`
	Duration      time.Duration  `json:"duration"`
	MethodUsed    TransferMethod `json:"methodUsed"`
}

// Summary returns a formatted, human-readable overview of the GeoIP update operation.
func (r *GeoIPResult) Summary() string {
	if r == nil {
		return "No GeoIP result available."
	}
	var sb strings.Builder
	sb.WriteString("\n========================================================================\n")
	sb.WriteString("  GeoIP Database Update Summary\n")
	sb.WriteString("========================================================================\n")
	sb.WriteString(fmt.Sprintf("  Target Host:     %s\n", r.TargetHost))
	sb.WriteString(fmt.Sprintf("  Method:          %s\n", strings.ToUpper(string(r.MethodUsed))))
	sb.WriteString(fmt.Sprintf("  Archive File:    %s\n", r.ArchiveFile))
	if r.ExtractedDir != "" {
		status := "cleaned up"
		if strings.Contains(r.RawOutput, "Extracted files preserved") {
			status = "preserved"
		}
		sb.WriteString(fmt.Sprintf("  Extraction Dir:  %s (%s)\n", r.ExtractedDir, status))
	}
	if len(r.InstalledRPMs) > 0 {
		sb.WriteString("  Installed RPMs:  ")
		for i, rpm := range r.InstalledRPMs {
			if i > 0 {
				sb.WriteString("                   ")
			}
			sb.WriteString(rpm + "\n")
		}
	}
	sb.WriteString("  TMM Reload:      Loaded successfully (tmsh load sys geoip)\n")
	if r.LookupTest != "" {
		sb.WriteString("  Lookup Test:     8.8.8.8 verified\n")
		for _, line := range strings.Split(r.LookupTest, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "country_") || strings.HasPrefix(line, "region_") || strings.HasPrefix(line, "continent_") {
				sb.WriteString(fmt.Sprintf("                   %s\n", line))
			}
		}
	}
	if r.Duration > 0 {
		sb.WriteString(fmt.Sprintf("  Duration:        %s\n", r.Duration.Round(100*time.Millisecond)))
	}
	sb.WriteString("========================================================================\n\n")
	return sb.String()
}

// ApplyGeoIP executes post-push GeoIP automation on a BIG-IP appliance:
// 1. Validates the remote ZIP archive path.
// 2. Extracts RPM packages into a temporary directory on the appliance.
// 3. Runs /usr/local/bin/geoip_update_data for each RPM.
// 4. Reloads GeoIP tables into TMM via tmsh load sys geoip.
// 5. Cleans up temporary extraction files.
// 6. Verifies lookups via geoip_lookup and queries installed RPMs.
func ApplyGeoIP(
	ctx context.Context,
	host, remoteZipPath string,
	opts GeoIPOptions,
	statusCb func(string),
) (*GeoIPResult, error) {
	if strings.EqualFold(opts.TargetType, "BIG-IQ") {
		return nil, fmt.Errorf("GeoIP database updates are only supported on BIG-IP appliances (not BIG-IQ)")
	}

	cleanPath := path.Clean(strings.TrimSpace(remoteZipPath))
	if err := ValidateRemoteAppliancePath(cleanPath); err != nil {
		return nil, fmt.Errorf("invalid remote GeoIP archive path: %w", err)
	}

	lowerPath := strings.ToLower(cleanPath)
	if !strings.HasSuffix(lowerPath, ".zip") && !strings.HasSuffix(lowerPath, ".tgz") && !strings.HasSuffix(lowerPath, ".tar.gz") {
		return nil, fmt.Errorf("remote file %q is not a supported GeoIP archive (must be .zip, .tgz, or .tar.gz)", cleanPath)
	}

	method := opts.Method
	if method == "" {
		method = MethodAuto
	}
	if method == "ssh" {
		method = MethodSCP
	}

	if opts.Timeout <= 0 {
		opts.Timeout = 3 * time.Minute
	}

	execCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	script := generateGeoIPScript(cleanPath, opts.Force, opts.KeepExtracted)
	b64Script := base64.StdEncoding.EncodeToString([]byte(script))

	startTime := time.Now()

	switch method {
	case MethodIControl:
		if statusCb != nil {
			statusCb("Applying GeoIP update via iControl REST (/mgmt/tm/util/bash)...")
		}
		rawOut, err := applyGeoIPREST(execCtx, host, b64Script, opts)
		if err != nil {
			return nil, fmt.Errorf("iControl REST GeoIP execution failed: %w", err)
		}
		return parseGeoIPOutput(host, cleanPath, rawOut, time.Since(startTime), MethodIControl)

	case MethodSCP:
		if statusCb != nil {
			statusCb("Applying GeoIP update via SSH...")
		}
		rawOut, err := applyGeoIPSSH(execCtx, host, b64Script, opts)
		if err != nil {
			return nil, fmt.Errorf("SSH GeoIP execution failed: %w", err)
		}
		return parseGeoIPOutput(host, cleanPath, rawOut, time.Since(startTime), MethodSCP)

	case MethodAuto:
		// Determine which method to try first based on available credentials.
		trySSHFirst := opts.IdentityFile != "" || (opts.Password == "" && opts.ClientCertPath == "" && os.Getenv("BIGIP_PASSWORD") == "")

		if trySSHFirst {
			if statusCb != nil {
				statusCb("Applying GeoIP update via SSH...")
			}
			rawOut, sshErr := applyGeoIPSSH(execCtx, host, b64Script, opts)
			if sshErr == nil {
				return parseGeoIPOutput(host, cleanPath, rawOut, time.Since(startTime), MethodSCP)
			}

			if statusCb != nil {
				statusCb(fmt.Sprintf("SSH execution failed (%v). Falling back to iControl REST...", sshErr))
			}

			rawOut, restErr := applyGeoIPREST(execCtx, host, b64Script, opts)
			if restErr == nil {
				return parseGeoIPOutput(host, cleanPath, rawOut, time.Since(startTime), MethodIControl)
			}

			return nil, fmt.Errorf("both execution methods failed:\n  1. SSH error: %v\n  2. iControl REST error: %v", sshErr, restErr)
		}

		// Try iControl REST first
		if statusCb != nil {
			statusCb("Applying GeoIP update via iControl REST...")
		}
		rawOut, restErr := applyGeoIPREST(execCtx, host, b64Script, opts)
		if restErr == nil {
			return parseGeoIPOutput(host, cleanPath, rawOut, time.Since(startTime), MethodIControl)
		}

		if statusCb != nil {
			statusCb(fmt.Sprintf("iControl REST execution failed (%v). Falling back to SSH...", restErr))
		}

		rawOut, sshErr := applyGeoIPSSH(execCtx, host, b64Script, opts)
		if sshErr == nil {
			return parseGeoIPOutput(host, cleanPath, rawOut, time.Since(startTime), MethodSCP)
		}

		return nil, fmt.Errorf("both execution methods failed:\n  1. iControl REST error: %v\n  2. SSH error: %v", restErr, sshErr)

	default:
		return nil, fmt.Errorf("unsupported execution method %q (supported: auto, icontrol, scp, ssh)", method)
	}
}

// generateGeoIPScript creates the bash script executed on the appliance.
func generateGeoIPScript(remoteZipPath string, force, keepExtracted bool) string {
	cleanupAction := `rm -rf "$EXTRACT_DIR"`
	if keepExtracted {
		cleanupAction = `echo "Extracted files preserved at $EXTRACT_DIR"`
	}

	forceFlag := "-f"
	if !force {
		forceFlag = ""
	}

	extractCmd := `unzip -q -o "$ZIP_FILE" -d "$EXTRACT_DIR"`
	lowerPath := strings.ToLower(remoteZipPath)
	if strings.HasSuffix(lowerPath, ".tgz") || strings.HasSuffix(lowerPath, ".tar.gz") {
		extractCmd = `tar -xzf "$ZIP_FILE" -C "$EXTRACT_DIR"`
	}

	return fmt.Sprintf(`(
set -o pipefail
ZIP_FILE=%q

if [ ! -f "$ZIP_FILE" ]; then
  echo "ERROR: GeoIP archive file not found: $ZIP_FILE" >&2
  exit 2
fi

EXTRACT_BASE="/shared/tmp"
if [ ! -d "$EXTRACT_BASE" ] || [ ! -w "$EXTRACT_BASE" ]; then
  EXTRACT_BASE="/var/tmp"
fi

EXTRACT_DIR="${EXTRACT_BASE}/geoip_update_$(date +%%s)_$RANDOM"
mkdir -p "$EXTRACT_DIR"

echo "--- EXTRACT_DIR ---"
echo "$EXTRACT_DIR"

cleanup() {
  %s
}
trap cleanup EXIT INT TERM

echo "Extracting $ZIP_FILE to $EXTRACT_DIR..."
%s || {
  echo "ERROR: Failed to extract $ZIP_FILE" >&2
  exit 3
}

RPMS=$(find "$EXTRACT_DIR" -type f -name "*.rpm" | sort)
if [ -z "$RPMS" ]; then
  echo "ERROR: No RPM packages found in archive $ZIP_FILE" >&2
  exit 4
fi

UPDATE_BIN="/usr/local/bin/geoip_update_data"
if [ ! -x "$UPDATE_BIN" ]; then
  UPDATE_BIN=$(which geoip_update_data 2>/dev/null || true)
fi
if [ -z "$UPDATE_BIN" ] || [ ! -x "$UPDATE_BIN" ]; then
  echo "ERROR: geoip_update_data utility not found on appliance" >&2
  exit 5
fi

for rpm in $RPMS; do
  echo "Installing package $(basename "$rpm")..."
  OUT=$($UPDATE_BIN %s "$rpm" 2>&1)
  RC=$?
  if [ $RC -ne 0 ] || echo "$OUT" | grep -qi "error\|not in the expected format"; then
    echo "ERROR: geoip_update_data failed on $(basename "$rpm"): $OUT" >&2
    exit 6
  fi
  echo "$OUT"
done

echo "Reloading GeoIP in TMM..."
tmsh load sys geoip || {
  echo "ERROR: tmsh load sys geoip failed" >&2
  exit 7
}

echo "--- INSTALLED_GEOIP_RPMS ---"
if [ -x /opt/bin/rpm ]; then
  /opt/bin/rpm --dbpath /shared/lib/rpm -qa 2>/dev/null || true
fi
rpm -qa 2>/dev/null | grep -i geoip || true

echo "--- GEOIP_LOOKUP_TEST ---"
if which geoip_lookup >/dev/null 2>&1; then
  geoip_lookup 8.8.8.8 2>&1 || true
fi

echo "--- GEOIP_COMPLETE ---"
)
SCRIPT_EXIT_CODE=$?
echo "--- SCRIPT_EXIT_CODE: $SCRIPT_EXIT_CODE ---"
exit $SCRIPT_EXIT_CODE
`, remoteZipPath, cleanupAction, extractCmd, forceFlag)
}

func applyGeoIPSSH(ctx context.Context, host, scriptB64 string, opts GeoIPOptions) (string, error) {
	scpOpts := SCPOptions{
		Host:       host,
		Port:       opts.Port,
		Username:   opts.Username,
		Password:   opts.Password,
		KeyPath:    opts.IdentityFile,
		Passphrase: opts.SSHPassphrase,
		Timeout:    opts.Timeout,
	}

	clientConfig, err := BuildSSHClientConfig(scpOpts)
	if err != nil {
		return "", fmt.Errorf("failed to configure SSH client: %w", err)
	}

	h, port := CleanSSHHost(host)
	if opts.Port > 0 {
		port = opts.Port
	}
	addr := net.JoinHostPort(h, strconv.Itoa(port))

	dialTimeout := opts.Timeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultSCPTimeout
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	defer dialCancel()

	netConn, err := client.DialProxy(dialCtx, "tcp", addr, nil)
	if err != nil {
		return "", fmt.Errorf("SSH connection to %s failed: %w", addr, err)
	}
	defer netConn.Close()

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, clientConfig)
	if err != nil {
		return "", fmt.Errorf("SSH handshake failed: %w", err)
	}
	defer sshConn.Close()

	sshClient := ssh.NewClient(sshConn, chans, reqs)
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	var outBuf bytes.Buffer
	session.Stdout = &outBuf
	session.Stderr = &outBuf

	cmd := fmt.Sprintf("echo '%s' | base64 -d | bash", scriptB64)
	runErr := session.Run(cmd)
	rawOutput := outBuf.String()

	if runErr != nil && !strings.Contains(rawOutput, "--- SCRIPT_EXIT_CODE:") {
		return rawOutput, fmt.Errorf("SSH execution error: %w (output: %s)", runErr, strings.TrimSpace(rawOutput))
	}

	return rawOutput, nil
}

func applyGeoIPREST(ctx context.Context, host, scriptB64 string, opts GeoIPOptions) (string, error) {
	port := opts.Port
	if port <= 0 {
		port = 443
	}
	user := opts.Username
	if user == "" {
		user = "admin"
	}

	client, err := NewClientWithOptions(ClientOptions{
		Host:           host,
		Port:           port,
		Username:       user,
		Password:       opts.Password,
		SkipTLSVerify:  true,
		ClientCertPath: opts.ClientCertPath,
		ClientKeyPath:  opts.ClientKeyPath,
		Timeout:        opts.Timeout,
	})
	if err != nil {
		return "", fmt.Errorf("failed to initialize appliance REST client: %w", err)
	}

	payload := map[string]string{
		"command":     "run",
		"utilCmdArgs": fmt.Sprintf("-c \"echo '%s' | base64 -d | bash\"", scriptB64),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode bash request payload: %w", err)
	}

	url := fmt.Sprintf("%s/mgmt/tm/util/bash", client.BaseURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create REST bash request: %w", err)
	}
	client.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("REST bash execution request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return "", fmt.Errorf("failed reading REST bash response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("TMOS bash utility (/mgmt/tm/util/bash) returned HTTP %d (bash utility may be disabled or appliance in Appliance Mode): %s\nRemediation: Use '--method scp' or SSH key authentication to execute directly without the REST bash utility", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return "", fmt.Errorf("REST bash execution failed with HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var bashResp struct {
		CommandResult string `json:"commandResult"`
	}
	if err := json.Unmarshal(body, &bashResp); err != nil {
		return string(body), nil
	}

	return bashResp.CommandResult, nil
}

// parseGeoIPOutput parses the structured markers from the bash execution output.
func parseGeoIPOutput(
	host, archiveFile, rawOutput string,
	duration time.Duration,
	method TransferMethod,
) (*GeoIPResult, error) {
	reExitCode := regexp.MustCompile(`--- SCRIPT_EXIT_CODE:\s*(\d+)\s*---`)
	matches := reExitCode.FindStringSubmatch(rawOutput)
	if len(matches) > 1 {
		code, _ := strconv.Atoi(matches[1])
		if code != 0 {
			var errLines []string
			for _, line := range strings.Split(rawOutput, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "ERROR:") || strings.HasPrefix(trimmed, "error:") {
					errLines = append(errLines, trimmed)
				}
			}
			errMsg := strings.Join(errLines, "\n")
			if errMsg == "" {
				errMsg = strings.TrimSpace(rawOutput)
			}
			return nil, fmt.Errorf("appliance script failed (exit code %d): %s", code, errMsg)
		}
	} else if strings.Contains(rawOutput, "ERROR:") {
		return nil, fmt.Errorf("appliance script reported error: %s", strings.TrimSpace(rawOutput))
	}

	res := &GeoIPResult{
		TargetHost:  host,
		ArchiveFile: archiveFile,
		RawOutput:   rawOutput,
		Duration:    duration,
		MethodUsed:  method,
	}

	// Extract EXTRACT_DIR
	reExtract := regexp.MustCompile(`(?m)^--- EXTRACT_DIR ---\s*\n([^\n]+)`)
	if m := reExtract.FindStringSubmatch(rawOutput); len(m) > 1 {
		res.ExtractedDir = strings.TrimSpace(m[1])
	}

	// Extract INSTALLED_GEOIP_RPMS
	if idx := strings.Index(rawOutput, "--- INSTALLED_GEOIP_RPMS ---"); idx != -1 {
		section := rawOutput[idx+len("--- INSTALLED_GEOIP_RPMS ---"):]
		if endIdx := strings.Index(section, "--- GEOIP_LOOKUP_TEST ---"); endIdx != -1 {
			section = section[:endIdx]
		}
		seen := make(map[string]bool)
		for _, line := range strings.Split(section, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && strings.Contains(strings.ToLower(trimmed), "geoip") && !seen[trimmed] {
				seen[trimmed] = true
				res.InstalledRPMs = append(res.InstalledRPMs, trimmed)
			}
		}
	}

	// Extract GEOIP_LOOKUP_TEST
	if idx := strings.Index(rawOutput, "--- GEOIP_LOOKUP_TEST ---"); idx != -1 {
		section := rawOutput[idx+len("--- GEOIP_LOOKUP_TEST ---"):]
		if endIdx := strings.Index(section, "--- GEOIP_COMPLETE ---"); endIdx != -1 {
			section = section[:endIdx]
		} else if endIdx := strings.Index(section, "--- SCRIPT_EXIT_CODE:"); endIdx != -1 {
			section = section[:endIdx]
		}
		res.LookupTest = strings.TrimSpace(section)
	}

	return res, nil
}
