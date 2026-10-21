package bigip

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	if !strings.Contains(script, `ZIP_FILE="/var/tmp/geoip.zip"`) {
		t.Errorf("expected script to contain zip file path, got:\n%s", script)
	}
	if !strings.Contains(script, `rm -rf "$EXTRACT_DIR"`) {
		t.Errorf("expected script to clean up extract dir when keepExtracted is false")
	}
	if !strings.Contains(script, "-f") {
		t.Errorf("expected script to pass -f when force is true")
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}

	_, err := ApplyGeoIP(context.Background(), server.URL, "/var/tmp/update.zip", opts, nil)
	if err == nil {
		t.Fatalf("expected error on HTTP 403, got nil")
	}
	if !strings.Contains(err.Error(), "Appliance Mode") {
		t.Errorf("expected remediation hint mentioning Appliance Mode, got: %v", err)
	}
}
