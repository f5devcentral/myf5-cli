package bigip

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func createTestQKViewArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, content := range files {
		data := []byte(content)
		hdr := &tar.Header{
			Name: name,
			Mode: 0600,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader failed: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("Write data failed: %v", err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("Close tar failed: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("Close gzip failed: %v", err)
	}

	return buf.Bytes()
}

func TestInspectQKViewReader_Success(t *testing.T) {
	files := map[string]string{
		"VERSION.LTM": `Product: BIG-IP
Version: 17.5.1
Build: 0.32.12
Edition: Engineering Hotfix`,
		"PLATFORM": `platform=Z100
family=2000`,
		"HWINFO": `basemac=02:ff:cd:2c:41:07
cores=8`,
		"config/bigip.license": `Registration Key : ABCDE-FGHIJ-KLMNO-PQRST-UVWXYZA
Serial Number : f5-license-serial-1234`,
		"config/bigip_base.conf": `sys global-settings {
    hostname bigip1.corp.internal;
}`,
		"mcp_module.xml": `<system_information>
    <bigip_chassis_serial_num>f5-chassis-9999</bigip_chassis_serial_num>
    <platform>Z100</platform>
    <product>BIG-IP</product>
    <registration_key>ABCDE-FGHIJ-KLMNO-PQRST-UVWXYZA</registration_key>
</system_information>`,
	}

	archive := createTestQKViewArchive(t, files)
	info, err := InspectQKViewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("InspectQKViewReader failed: %v", err)
	}

	if info.Product != "BIG-IP" {
		t.Errorf("Product = %q; want BIG-IP", info.Product)
	}
	if info.Version != "17.5.1" {
		t.Errorf("Version = %q; want 17.5.1", info.Version)
	}
	if info.Build != "0.32.12" {
		t.Errorf("Build = %q; want 0.32.12", info.Build)
	}
	if info.Edition != "Engineering Hotfix" {
		t.Errorf("Edition = %q; want Engineering Hotfix", info.Edition)
	}
	if info.Platform != "Z100" {
		t.Errorf("Platform = %q; want Z100", info.Platform)
	}
	if info.BaseMac != "02:ff:cd:2c:41:07" {
		t.Errorf("BaseMac = %q; want 02:ff:cd:2c:41:07", info.BaseMac)
	}
	if info.Hostname != "bigip1.corp.internal" {
		t.Errorf("Hostname = %q; want bigip1.corp.internal", info.Hostname)
	}
	if info.RegistrationKey != "ABCDE-FGHIJ-KLMNO-PQRST-UVWXYZA" {
		t.Errorf("RegistrationKey = %q; want ABCDE-FGHIJ-KLMNO-PQRST-UVWXYZA", info.RegistrationKey)
	}
	if info.ChassisSerialNumber != "f5-chassis-9999" {
		t.Errorf("ChassisSerialNumber = %q; want f5-chassis-9999", info.ChassisSerialNumber)
	}
	if got := info.PreferredSerialNumber(); got != "f5-chassis-9999" {
		t.Errorf("PreferredSerialNumber = %q; want f5-chassis-9999", got)
	}

	summary := info.Summary()
	if !strings.Contains(summary, "BIG-IP") || !strings.Contains(summary, "17.5.1") || !strings.Contains(summary, "f5-chassis-9999") {
		t.Errorf("Summary() did not contain expected fields:\n%s", summary)
	}
}

func TestInspectQKViewReader_McpFallback(t *testing.T) {
	files := map[string]string{
		"VERSION": `Product: BIG-IP
Version: 16.1.4`,
		"mcp_module.xml": `<system_information>
    <bigip_chassis_serial_num>f5-chassis-fallback-555</bigip_chassis_serial_num>
    <platform>Z100</platform>
</system_information>
<registration_key>REG-FALLBACK-123</registration_key>`,
	}

	archive := createTestQKViewArchive(t, files)
	info, err := InspectQKViewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("InspectQKViewReader failed: %v", err)
	}

	if info.ChassisSerialNumber != "f5-chassis-fallback-555" {
		t.Errorf("ChassisSerialNumber = %q; want f5-chassis-fallback-555", info.ChassisSerialNumber)
	}
	if info.RegistrationKey != "REG-FALLBACK-123" {
		t.Errorf("RegistrationKey = %q; want REG-FALLBACK-123", info.RegistrationKey)
	}
	if got := info.PreferredSerialNumber(); got != "f5-chassis-fallback-555" {
		t.Errorf("PreferredSerialNumber = %q; want f5-chassis-fallback-555", got)
	}
}

func TestInspectQKViewReader_InvalidData(t *testing.T) {
	_, err := InspectQKViewReader(bytes.NewReader([]byte("not a gzip")))
	if err == nil {
		t.Fatal("expected error on non-gzip stream, got nil")
	}

	// Gzip with invalid tar data
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte("plain garbage inside gzip"))
	gz.Close()

	_, err = InspectQKViewReader(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error on corrupted tar stream, got nil")
	}
}

func TestInspectQKView_File(t *testing.T) {
	files := map[string]string{
		"VERSION": "Product: BIG-IP\nVersion: 15.1.0\n",
	}
	data := createTestQKViewArchive(t, files)
	tmpFile := t.TempDir() + "/test.qkview"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	info, err := InspectQKView(tmpFile)
	if err != nil {
		t.Fatalf("InspectQKView failed: %v", err)
	}
	if info.Product != "BIG-IP" || info.Version != "15.1.0" {
		t.Errorf("Unexpected info: %+v", info)
	}
	if !strings.Contains(info.Source, tmpFile) {
		t.Errorf("Source = %q; want containing %q", info.Source, tmpFile)
	}

	// Non-existent file
	_, err = InspectQKView(tmpFile + ".nonexistent")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestPreferredSerialNumber_Fallback(t *testing.T) {
	var info *SystemInfo
	if got := info.PreferredSerialNumber(); got != "" {
		t.Errorf("nil info PreferredSerialNumber() = %q; want empty", got)
	}

	info = &SystemInfo{RegistrationKey: "MY-REG-KEY"}
	if got := info.PreferredSerialNumber(); got != "MY-REG-KEY" {
		t.Errorf("info PreferredSerialNumber() = %q; want MY-REG-KEY", got)
	}
}

func TestParseHardwareJSON_NestedStats(t *testing.T) {
	payload := `{
		"kind": "tm:sys:hardware:hardwarestats",
		"entries": {
			"https://localhost/mgmt/tm/sys/hardware/system-info": {
				"nestedStats": {
					"entries": {
						"bigipChassisSerialNum": { "description": "4a719c23-01b2-4d8e-8a03-75b28d7b328a" },
						"platform": { "description": "BIG-IP Virtual Edition" },
						"baseMac": { "description": "00:50:56:9c:12:34" },
						"product": { "description": "BIG-IP" }
					}
				}
			}
		}
	}`

	info := &SystemInfo{}
	parseHardwareJSON([]byte(payload), info)

	if info.ChassisSerialNumber != "4a719c23-01b2-4d8e-8a03-75b28d7b328a" {
		t.Errorf("ChassisSerialNumber = %q; want 4a719c23-01b2-4d8e-8a03-75b28d7b328a", info.ChassisSerialNumber)
	}
	if info.Platform != "BIG-IP Virtual Edition" {
		t.Errorf("Platform = %q; want BIG-IP Virtual Edition", info.Platform)
	}
	if info.BaseMac != "00:50:56:9c:12:34" {
		t.Errorf("BaseMac = %q; want 00:50:56:9c:12:34", info.BaseMac)
	}
	if info.Product != "BIG-IP" {
		t.Errorf("Product = %q; want BIG-IP", info.Product)
	}
}

func TestParseVersionJSON(t *testing.T) {
	payload := `{
		"entries": {
			"https://localhost/mgmt/tm/sys/version": {
				"nestedStats": {
					"entries": {
						"Version": { "description": "17.5.1" },
						"Build": { "description": "0.0.12" },
						"Edition": { "description": "Final" }
					}
				}
			}
		}
	}`

	info := &SystemInfo{}
	parseVersionJSON([]byte(payload), info)

	if info.Version != "17.5.1" {
		t.Errorf("Version = %q; want 17.5.1", info.Version)
	}
	if info.Build != "0.0.12" {
		t.Errorf("Build = %q; want 0.0.12", info.Build)
	}
	if info.Edition != "Final" {
		t.Errorf("Edition = %q; want Final", info.Edition)
	}
}

func TestParseLicenseJSON(t *testing.T) {
	payload := `{
		"entries": {
			"https://localhost/mgmt/tm/sys/license": {
				"nestedStats": {
					"entries": {
						"registrationKey": { "description": "ABCDE-12345-67890-ABCDE-1234567" }
					}
				}
			}
		}
	}`

	info := &SystemInfo{}
	parseLicenseJSON([]byte(payload), info)

	if info.RegistrationKey != "ABCDE-12345-67890-ABCDE-1234567" {
		t.Errorf("RegistrationKey = %q; want ABCDE-12345-67890-ABCDE-1234567", info.RegistrationKey)
	}
}

func TestParseTmshOutput(t *testing.T) {
	output := `
Sys::Hardware
Chassis Information
  Chassis Serial        f5-tmsh-serial-888
Platform Information
  Platform Name         i5800
  Base MAC              00:98:76:54:32:10

Sys::Version
Main Package
  Product               BIG-IP
  Version               16.1.4
  Build                 0.0.12
  Edition               Final

Sys::License
Active License
  Registration Key      REG-TMSH-KEY-777

Sys::Global Settings
sys global-settings {
    hostname bigip-tmsh.local;
}

Sys::Provision
sys provision ltm {
    level nominal
}
sys provision mgmt {
    level dedicated
}
sys provision asm {
    level none
}
`
	info := parseTmshOutput(output)
	info.PopulateCaseAttributes()

	if info.ChassisSerialNumber != "f5-tmsh-serial-888" {
		t.Errorf("ChassisSerialNumber = %q; want f5-tmsh-serial-888", info.ChassisSerialNumber)
	}
	if info.Platform != "i5800" {
		t.Errorf("Platform = %q; want i5800", info.Platform)
	}
	if info.BaseMac != "00:98:76:54:32:10" {
		t.Errorf("BaseMac = %q; want 00:98:76:54:32:10", info.BaseMac)
	}
	if info.Product != "BIG-IP" {
		t.Errorf("Product = %q; want BIG-IP", info.Product)
	}
	if info.Version != "16.1.4" {
		t.Errorf("Version = %q; want 16.1.4", info.Version)
	}
	if info.Build != "0.0.12" {
		t.Errorf("Build = %q; want 0.0.12", info.Build)
	}
	if info.Edition != "Final" {
		t.Errorf("Edition = %q; want Final", info.Edition)
	}
	if info.RegistrationKey != "REG-TMSH-KEY-777" {
		t.Errorf("RegistrationKey = %q; want REG-TMSH-KEY-777", info.RegistrationKey)
	}
	if info.Hostname != "bigip-tmsh.local" {
		t.Errorf("Hostname = %q; want bigip-tmsh.local", info.Hostname)
	}
	if info.ProvisionedModules["ltm"] != "nominal" {
		t.Errorf("ProvisionedModules[ltm] = %q; want nominal", info.ProvisionedModules["ltm"])
	}
	if _, ok := info.ProvisionedModules["mgmt"]; ok {
		t.Errorf("expected mgmt to be excluded as internal module")
	}
	if _, ok := info.ProvisionedModules["asm"]; ok {
		t.Errorf("expected asm to be excluded due to level none")
	}
	if info.CaseAttributes["product"] != "BIG-IP LTM" {
		t.Errorf("CaseAttributes[product] = %q; want BIG-IP LTM", info.CaseAttributes["product"])
	}
	if info.CaseAttributes["serialNumber"] != "f5-tmsh-serial-888" {
		t.Errorf("CaseAttributes[serialNumber] = %q; want f5-tmsh-serial-888", info.CaseAttributes["serialNumber"])
	}
}

func TestGetSystemInfo_RestMock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mgmt/tm/sys/hardware":
			json.NewEncoder(w).Encode(map[string]any{
				"chassisSerialNumber": "chs-rest-test-1",
				"platform":            "Z100",
			})
		case "/mgmt/tm/sys/version":
			json.NewEncoder(w).Encode(map[string]any{
				"version": "17.5.1",
				"build":   "0.0.1",
				"edition": "Final",
				"product": "BIG-IP",
			})
		case "/mgmt/tm/sys/license":
			json.NewEncoder(w).Encode(map[string]any{
				"registrationKey": "REG-REST-TEST",
			})
		case "/mgmt/tm/sys/global-settings":
			json.NewEncoder(w).Encode(map[string]any{
				"hostname": "bigip-rest.corp.test",
			})
		case "/mgmt/tm/sys/provision":
			json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]string{
					{"name": "ltm", "level": "nominal"},
					{"name": "mgmt", "level": "nominal"},
					{"name": "asm", "level": "none"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, 0, "admin", "admin")
	client.HTTPClient = server.Client()

	info, err := client.GetSystemInfo(context.Background())
	if err != nil {
		t.Fatalf("GetSystemInfo failed: %v", err)
	}

	if info.ChassisSerialNumber != "chs-rest-test-1" {
		t.Errorf("ChassisSerialNumber = %q; want chs-rest-test-1", info.ChassisSerialNumber)
	}
	if info.RegistrationKey != "REG-REST-TEST" {
		t.Errorf("RegistrationKey = %q; want REG-REST-TEST", info.RegistrationKey)
	}
	if info.Platform != "Z100" {
		t.Errorf("Platform = %q; want Z100", info.Platform)
	}
	if info.Product != "BIG-IP" {
		t.Errorf("Product = %q; want BIG-IP", info.Product)
	}
	if info.Version != "17.5.1" {
		t.Errorf("Version = %q; want 17.5.1", info.Version)
	}
	if info.Build != "0.0.1" {
		t.Errorf("Build = %q; want 0.0.1", info.Build)
	}
	if info.Hostname != "bigip-rest.corp.test" {
		t.Errorf("Hostname = %q; want bigip-rest.corp.test", info.Hostname)
	}
	if info.ProvisionedModules["ltm"] != "nominal" {
		t.Errorf("ProvisionedModules[ltm] = %q; want nominal", info.ProvisionedModules["ltm"])
	}
	if _, ok := info.ProvisionedModules["mgmt"]; ok {
		t.Errorf("expected mgmt to be excluded as internal module")
	}
	if _, ok := info.ProvisionedModules["asm"]; ok {
		t.Errorf("expected asm to be excluded due to level none")
	}
	if info.CaseAttributes["product"] != "BIG-IP LTM" {
		t.Errorf("CaseAttributes[product] = %q; want BIG-IP LTM", info.CaseAttributes["product"])
	}
	if info.CaseAttributes["serialNumber"] != "chs-rest-test-1" {
		t.Errorf("CaseAttributes[serialNumber] = %q; want chs-rest-test-1", info.CaseAttributes["serialNumber"])
	}
}

func TestSystemInfo_SummaryLifecycle(t *testing.T) {
	// Case 1: EOL Software and EOL Hardware
	eolInfo := &SystemInfo{
		Product:  "BIG-IP",
		Version:  "15.1.8",
		Platform: "2000s",
	}
	summary := eolInfo.Summary()
	if !strings.Contains(summary, "Lifecycle & Support Warnings:") {
		t.Errorf("expected Lifecycle & Support Warnings in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "TMOS version 15.1.8 reached End of Life") {
		t.Errorf("expected TMOS EOL warning in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Hardware platform 2000s reached End of Life") {
		t.Errorf("expected hardware EOL warning in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Software Rec:") {
		t.Errorf("expected Software Rec in summary, got:\n%s", summary)
	}
	if strings.Contains(summary, "Hardware Rec:") {
		t.Errorf("expected no Hardware Rec in summary, got:\n%s", summary)
	}

	// Case 2: Supported Software and Virtual Edition Hardware
	suppInfo := &SystemInfo{
		Product:  "BIG-IP",
		Version:  "17.5.1",
		Platform: "Z100",
	}
	suppSummary := suppInfo.Summary()
	if !strings.Contains(suppSummary, "Lifecycle & Support Status:") {
		t.Errorf("expected Lifecycle & Support Status in summary, got:\n%s", suppSummary)
	}
	if !strings.Contains(suppSummary, "Software (17.5.1") || !strings.Contains(suppSummary, "Supported") {
		t.Errorf("expected Software Supported in summary, got:\n%s", suppSummary)
	}
	if !strings.Contains(suppSummary, "Hardware (Virtual Edition): Supported") {
		t.Errorf("expected Hardware Supported in summary, got:\n%s", suppSummary)
	}
	if strings.Contains(suppSummary, "[WARNING]") {
		t.Errorf("did not expect warnings in supported summary, got:\n%s", suppSummary)
	}
}

func TestParseTmshOutput_WithHardwareType(t *testing.T) {
	output := `
Sys::Hardware
Chassis Information
  Chassis Serial        chs-i5800-999
Platform Information
  Platform Name         BIG-IP i5800
  Base MAC              00:01:d7:aa:bb:cc
System Information
  Type                  C119
  Appliance Series      i5000

Sys::Version
Main Package
  Product               BIG-IP
  Version               17.1.1
  Build                 0.0.4
  Edition               Final

Sys::License
Active License
  Registration Key      REG-TMSH-KEY-999
`
	info := parseTmshOutput(output)
	info.PopulateCaseAttributes()

	if info.Platform != "C119" {
		t.Errorf("Platform = %q; want C119", info.Platform)
	}
	if info.ChassisSerialNumber != "chs-i5800-999" {
		t.Errorf("ChassisSerialNumber = %q; want chs-i5800-999", info.ChassisSerialNumber)
	}
	summary := info.Summary()
	if !strings.Contains(summary, "Platform:           C119 (BIG-IP i5600 / i5800)") {
		t.Errorf("Summary() did not contain enriched platform:\n%s", summary)
	}
}

func TestSystemInfo_SummaryPlatformEnrichment(t *testing.T) {
	// Case 1: Raw platform code C119
	info1 := &SystemInfo{Platform: "C119"}
	sum1 := info1.Summary()
	if !strings.Contains(sum1, "Platform:           C119 (BIG-IP i5600 / i5800)") {
		t.Errorf("Summary() did not enrich C119:\n%s", sum1)
	}

	// Case 2: Raw platform code Z100
	info2 := &SystemInfo{Platform: "Z100"}
	sum2 := info2.Summary()
	if !strings.Contains(sum2, "Platform:           Z100 (BIG-IP Virtual Edition)") {
		t.Errorf("Summary() did not enrich Z100:\n%s", sum2)
	}

	// Case 3: Already fully named platform
	info3 := &SystemInfo{Platform: "BIG-IP Virtual Edition"}
	sum3 := info3.Summary()
	if !strings.Contains(sum3, "Platform:           BIG-IP Virtual Edition") || strings.Contains(sum3, "BIG-IP Virtual Edition (BIG-IP Virtual Edition)") {
		t.Errorf("Summary() duplicate enrichment for BIG-IP Virtual Edition:\n%s", sum3)
	}

	// Case 4: BIG-IQ 8.4.2.3 on Z100 Virtual Edition
	info4 := &SystemInfo{
		Product:             "BIG-IQ",
		Version:             "8.4.2.3",
		Build:               "0.0.8",
		Edition:             "Final",
		Platform:            "Z100",
		ChassisSerialNumber: "test-bigiq-serial-001",
		Hostname:            "bigiq-ve.ec2.internal",
		ProvisionedModules:  map[string]string{"biq": "nominal"},
	}
	info4.PopulateCaseAttributes()
	sum4 := info4.Summary()
	if info4.CaseAttributes["productFamily"] != "BIG-IQ" {
		t.Errorf("CaseAttributes[productFamily] = %q; want BIG-IQ", info4.CaseAttributes["productFamily"])
	}
	if info4.CaseAttributes["product"] != "BIG-IQ Centralized Management" {
		t.Errorf("CaseAttributes[product] = %q; want BIG-IQ Centralized Management", info4.CaseAttributes["product"])
	}
	if !strings.Contains(sum4, "Platform:           Z100 (BIG-IQ Virtual Edition)") {
		t.Errorf("Summary() did not enrich Z100 as BIG-IQ Virtual Edition:\n%s", sum4)
	}
	if !strings.Contains(sum4, "Software (8.4.2.3  ): Supported") || strings.Contains(sum4, "[WARNING]") {
		t.Errorf("Summary() did not mark BIG-IQ 8.4.2.3 as Supported:\n%s", sum4)
	}
}
