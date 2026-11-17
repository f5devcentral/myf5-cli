package bigip

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestApplyGeoIPTargetValidation(t *testing.T) {
	opts := GeoIPOptions{
		TargetType: "BIG-IQ",
	}
	_, err := ApplyGeoIP(context.Background(), "192.0.2.1", "/var/tmp/geoip.zip", opts, nil)
	if err == nil {
		t.Fatalf("expected error when target is BIG-IQ, got nil")
	}
	if !strings.Contains(err.Error(), "only supported on BIG-IP") {
		t.Errorf("expected error to mention 'only supported on BIG-IP', got: %v", err)
	}
}

func TestApplyGeoIPPathValidation(t *testing.T) {
	opts := GeoIPOptions{
		TargetType: "BIG-IP",
	}

	// Injection attempt
	_, err := ApplyGeoIP(context.Background(), "192.0.2.1", "/var/tmp/geoip.zip; reboot", opts, nil)
	if err == nil {
		t.Fatalf("expected error for path with semicolon, got nil")
	}

	// Unsupported extension
	_, err = ApplyGeoIP(context.Background(), "192.0.2.1", "/var/tmp/geoip.iso", opts, nil)
	if err == nil {
		t.Fatalf("expected error for non-zip extension, got nil")
	}
	if !strings.Contains(err.Error(), "not a supported GeoIP archive") {
		t.Errorf("expected error to mention 'not a supported GeoIP archive', got: %v", err)
	}
}

func TestGenerateGeoIPScript(t *testing.T) {
	script := generateGeoIPScript("/var/tmp/geoip.zip", true, false)
	if !strings.Contains(script, `ZIP_FILE='/var/tmp/geoip.zip'`) {
		t.Errorf("expected script to contain single-quoted zip file path, got:\n%s", script)
	}
	if !strings.Contains(script, `rm -rf "$EXTRACT_DIR"`) {
		t.Errorf("expected script to clean up extract dir when keepExtracted is false")
	}
	if !strings.Contains(script, "-f") {
		t.Errorf("expected script to pass -f when force is true")
	}

	// Test single-quote escaping defense-in-depth
	scriptEscaped := generateGeoIPScript("/var/tmp/inject'test$(whoami).zip", true, false)
	if !strings.Contains(scriptEscaped, `ZIP_FILE='/var/tmp/inject'\''test$(whoami).zip'`) {
		t.Errorf("expected script to properly escape single quotes, got:\n%s", scriptEscaped)
	}

	scriptKeep := generateGeoIPScript("/var/tmp/geoip.tgz", false, true)
	if !strings.Contains(scriptKeep, "Extracted files preserved") {
		t.Errorf("expected script to preserve extract dir when keepExtracted is true")
	}
	if !strings.Contains(scriptKeep, "tar -xzf") {
		t.Errorf("expected script to use tar for .tgz archive")
	}
}

func TestParseGeoIPOutput_Success(t *testing.T) {
	rawOutput := `--- EXTRACT_DIR ---
/shared/tmp/geoip_update_1759732100_12345
Extracting /var/tmp/geoip.zip to /shared/tmp/geoip_update_1759732100_12345...
Found RPM packages:
  - geoip-data-v2-2.0.0.rpm
Installing package geoip-data-v2-2.0.0.rpm...
Reloading GeoIP in TMM...
--- INSTALLED_GEOIP_RPMS ---
geoip-data-v2-2.0.0-20260727.850.0.x86_64
geoip-17.5.1.8-0.0.10.x86_64
--- GEOIP_LOOKUP_TEST ---
Will attempt to lookup ip '8.8.8.8'
country_code = US
country_name = United States
region_name = California
continent_code = NA
--- GEOIP_COMPLETE ---
--- SCRIPT_EXIT_CODE: 0 ---
`
	res, err := parseGeoIPOutput("54.243.25.230", "/var/tmp/geoip.zip", rawOutput, 2*time.Second, MethodSCP)
	if err != nil {
		t.Fatalf("parseGeoIPOutput failed: %v", err)
	}

	if res.TargetHost != "54.243.25.230" {
		t.Errorf("expected host 54.243.25.230, got %s", res.TargetHost)
	}
	if res.ExtractedDir != "/shared/tmp/geoip_update_1759732100_12345" {
		t.Errorf("unexpected ExtractedDir: %s", res.ExtractedDir)
	}
	if len(res.InstalledRPMs) != 2 {
		t.Fatalf("expected 2 installed RPMs, got %d: %v", len(res.InstalledRPMs), res.InstalledRPMs)
	}
	if !strings.Contains(res.LookupTest, "country_code = US") {
		t.Errorf("expected LookupTest to contain country_code = US, got: %s", res.LookupTest)
	}

	summary := res.Summary()
	if !strings.Contains(summary, "GeoIP Database Update Summary") {
		t.Errorf("summary missing title: %s", summary)
	}
	if !strings.Contains(summary, "8.8.8.8 verified") {
		t.Errorf("summary missing verified test: %s", summary)
	}
}

func TestParseGeoIPOutput_Failure(t *testing.T) {
	rawOutput := `--- EXTRACT_DIR ---
/shared/tmp/geoip_update_1759732100_12345
Extracting /var/tmp/geoip.zip to /shared/tmp/geoip_update_1759732100_12345...
ERROR: Failed to extract /var/tmp/geoip.zip
--- SCRIPT_EXIT_CODE: 3 ---
`
	_, err := parseGeoIPOutput("54.243.25.230", "/var/tmp/geoip.zip", rawOutput, time.Second, MethodIControl)
	if err == nil {
		t.Fatalf("expected error for non-zero exit code, got nil")
	}
	if !strings.Contains(err.Error(), "exit code 3") {
		t.Errorf("expected error to mention exit code 3, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ERROR: Failed to extract") {
		t.Errorf("expected error to include error message, got: %v", err)
	}
}

func TestApplyGeoIPREST_MockServer(t *testing.T) {
	var receivedBody map[string]string
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mgmt/tm/util/bash" && r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &receivedBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"commandResult":"--- EXTRACT_DIR ---\n/shared/tmp/geoip_123\n--- INSTALLED_GEOIP_RPMS ---\ngeoip-data-v2\n--- GEOIP_LOOKUP_TEST ---\ncountry_code = US\n--- GEOIP_COMPLETE ---\n--- SCRIPT_EXIT_CODE: 0 ---\n"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	opts := GeoIPOptions{
		TargetType: "BIG-IP",
		Method:     MethodIControl,
		Username:   "admin",
		Password:   "secret",
		HTTPClient: server.Client(),
	}

	res, err := ApplyGeoIP(context.Background(), server.URL, "/var/tmp/update.zip", opts, nil)
	if err != nil {
		t.Fatalf("ApplyGeoIP failed via mock REST: %v", err)
	}

	if res.ExtractedDir != "/shared/tmp/geoip_123" {
		t.Errorf("expected ExtractedDir /shared/tmp/geoip_123, got: %s", res.ExtractedDir)
	}
	if res.MethodUsed != MethodIControl {
		t.Errorf("expected method icontrol, got: %s", res.MethodUsed)
	}
}

func TestApplyGeoIPREST_ApplianceModeError(t *testing.T) {
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mgmt/tm/util/bash" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"bash utility disabled"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	opts := GeoIPOptions{
		TargetType: "BIG-IP",
		Method:     MethodIControl,
		Username:   "admin",
		Password:   "secret",
		HTTPClient: server.Client(),
	}

	_, err := ApplyGeoIP(context.Background(), server.URL, "/var/tmp/update.zip", opts, nil)
	if err == nil {
		t.Fatalf("expected error on HTTP 403, got nil")
	}
	if !strings.Contains(err.Error(), "Appliance Mode") {
		t.Errorf("expected remediation hint mentioning Appliance Mode, got: %v", err)
	}
}

func TestGeoIPOptionsZero(t *testing.T) {
	var nilOpts *GeoIPOptions
	nilOpts.Zero()

	opts := &GeoIPOptions{
		Password:           "secret",
		PasswordBytes:      []byte("secret"),
		SSHPassphrase:      "passphrase",
		SSHPassphraseBytes: []byte("passphrase"),
	}
	opts.Zero()
	if opts.Password != "" || opts.SSHPassphrase != "" || opts.PasswordBytes != nil || opts.SSHPassphraseBytes != nil {
		t.Errorf("GeoIPOptions.Zero() did not clear secrets: %+v", opts)
	}
}

func TestApplyGeoIPSSHMockServer(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed generating key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed creating signer: %v", err)
	}

	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "admin" && string(pass) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
	}
	serverConfig.AddHostKey(signer)

	listener, port := newPipeSSHServer()
	defer listener.Close()

	mockOutput := "--- EXTRACT_DIR ---\n/shared/tmp/geoip_update_123\n--- INSTALLED_GEOIP_RPMS ---\ngeoip-data-v2.rpm\n--- GEOIP_LOOKUP_TEST ---\ncountry_code = US\n--- GEOIP_COMPLETE ---\n--- SCRIPT_EXIT_CODE: 0 ---\n"

	go func() {
		tcpConn, err := listener.Accept()
		if err != nil {
			return
		}
		defer tcpConn.Close()

		sshConn, chans, reqs, err := ssh.NewServerConn(tcpConn, serverConfig)
		if err != nil {
			return
		}
		defer sshConn.Close()
		go ssh.DiscardRequests(reqs)

		for newChannel := range chans {
			if newChannel.ChannelType() != "session" {
				_ = newChannel.Reject(ssh.UnknownChannelType, "unknown")
				continue
			}
			channel, requests, err := newChannel.Accept()
			if err != nil {
				return
			}

			go func(in <-chan *ssh.Request) {
				defer channel.Close()
				for req := range in {
					switch req.Type {
					case "exec":
						_ = req.Reply(true, nil)
						_, _ = channel.Write([]byte(mockOutput))
						_ = channel.CloseWrite()
						_, _ = channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
						_, _ = io.Copy(io.Discard, channel)
						return
					default:
						_ = req.Reply(false, nil)
					}
				}
			}(requests)
		}
	}()

	opts := GeoIPOptions{
		TargetType: "BIG-IP",
		Method:     MethodSCP,
		Username:   "admin",
		Password:   "secret",
		Port:       port,
		Timeout:    2 * time.Second,
	}

	out, err := applyGeoIPSSH(context.Background(), "127.0.0.1", "c2NyaXB0", opts)
	if err != nil {
		t.Fatalf("applyGeoIPSSH failed: %v", err)
	}
	if !strings.Contains(out, "GEOIP_COMPLETE") {
		t.Errorf("expected GEOIP_COMPLETE in output, got: %s", out)
	}
}

func TestApplyGeoIPIControlSuccess(t *testing.T) {
	mockOutput := "--- EXTRACT_DIR ---\n/shared/tmp/geoip_update_123\n--- INSTALLED_GEOIP_RPMS ---\ngeoip-data-v2.rpm\n--- GEOIP_LOOKUP_TEST ---\ncountry_code = US\n--- GEOIP_COMPLETE ---\n--- SCRIPT_EXIT_CODE: 0 ---\n"
	server := newMockServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mgmt/tm/util/bash" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"commandResult": mockOutput,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	opts := GeoIPOptions{
		TargetType: "BIG-IP",
		Method:     MethodIControl,
		Username:   "admin",
		Password:   "secret",
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	}

	res, err := ApplyGeoIP(context.Background(), server.URL, "/var/tmp/geoip.zip", opts, func(s string) {})
	if err != nil {
		t.Fatalf("ApplyGeoIP iControl failed: %v", err)
	}
	if res == nil || res.MethodUsed != MethodIControl {
		t.Errorf("expected MethodIControl result, got %+v", res)
	}

	// Test MethodAuto with iControl first
	optsAuto := opts
	optsAuto.Method = MethodAuto
	resAuto, err := ApplyGeoIP(context.Background(), server.URL, "/var/tmp/geoip.tgz", optsAuto, func(s string) {})
	if err != nil {
		t.Fatalf("ApplyGeoIP Auto failed: %v", err)
	}
	if resAuto == nil || resAuto.MethodUsed != MethodIControl {
		t.Errorf("expected MethodIControl result from Auto, got %+v", resAuto)
	}

	// Test MethodAuto with SSH first (IdentityFile set), SSH fails, iControl succeeds
	optsSSHFirst := opts
	optsSSHFirst.Method = MethodAuto
	optsSSHFirst.IdentityFile = "/dummy/key"
	optsSSHFirst.Port = 65530
	resFallback, err := ApplyGeoIP(context.Background(), server.URL, "/var/tmp/geoip.tar.gz", optsSSHFirst, func(s string) {})
	if err != nil {
		t.Fatalf("ApplyGeoIP fallback failed: %v", err)
	}
	if resFallback == nil || resFallback.MethodUsed != MethodIControl {
		t.Errorf("expected MethodIControl from fallback, got %+v", resFallback)
	}

	// Test both fail
	optsDead := opts
	optsDead.Port = 65530
	optsDead.HTTPClient = nil
	optsDead.Timeout = 50 * time.Millisecond
	_, err = ApplyGeoIP(context.Background(), "127.0.0.1", "/var/tmp/geoip.zip", optsDead, func(s string) {})
	if err == nil {
		t.Errorf("expected error when both fail, got nil")
	}
}
