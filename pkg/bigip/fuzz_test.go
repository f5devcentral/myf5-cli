package bigip

import (
	"bytes"
	"path"
	"strings"
	"testing"
)

func FuzzValidateRemoteAppliancePath(f *testing.F) {
	seeds := []string{
		"/var/tmp/test.iso",
		"/shared/images/BIGIP-17.1.0.iso",
		"/shared/images",
		"/var/tmp/qkviews/test.qkview",
		"../../../etc/passwd",
		"/var/tmp/../etc/passwd",
		"/var/tmp/malicious.sh",
		"/var/tmp/test.iso\x00.sh",
		"/shared/images/../../root/.ssh/authorized_keys",
		"",
		"/",
		"//var//tmp//valid.iso",
		"/var/tmp/test..iso",
		"/var/tmp/file;reboot",
		"/var/tmp/$(whoami)",
		"/var/tmp/`id`",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, p string) {
		err := ValidateRemoteAppliancePath(p)
		if err == nil {
			// Invariants that MUST hold if path validation passes:
			if p == "" {
				t.Fatalf("ValidateRemoteAppliancePath accepted empty path")
			}
			if strings.Contains(p, "..") {
				t.Fatalf("ValidateRemoteAppliancePath accepted path traversal: %q", p)
			}
			cleaned := path.Clean(p)
			if !path.IsAbs(cleaned) {
				t.Fatalf("ValidateRemoteAppliancePath accepted non-absolute path: %q", p)
			}
			if !validRemotePathRegex.MatchString(cleaned) {
				t.Fatalf("ValidateRemoteAppliancePath accepted invalid characters: %q", p)
			}
			if strings.ContainsAny(p, ";|&$><`*?\n\r\t") {
				t.Fatalf("ValidateRemoteAppliancePath accepted shell metacharacters: %q", p)
			}
		} else {
			// If p contains traversal, it must be rejected
			if strings.Contains(p, "..") {
				// expected rejection
			}
		}
	})
}

func FuzzValidateApplianceDownloadPath(f *testing.F) {
	seeds := []string{
		"/var/tmp/test.iso",
		"/var/tmp/my.qkview",
		"/shared/images/BIGIP-17.1.0.iso",
		"/etc/shadow",
		"/root/.ssh/id_rsa",
		"/var/tmp/script.sh",
		"../../../../etc/passwd",
		"",
		"/var/tmp/something.iso.exe",
		"/var/tmp/qkview.ISO",
		"/var/tmp/archive.QKVIEW",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, p string) {
		err := ValidateApplianceDownloadPath(p)
		if err == nil {
			// Invariants for download paths:
			if strings.Contains(p, "..") {
				t.Fatalf("ValidateApplianceDownloadPath accepted path traversal: %q", p)
			}
			cleaned := path.Clean(p)
			ext := strings.ToLower(path.Ext(cleaned))
			if ext != ".iso" && ext != ".qkview" {
				t.Fatalf("ValidateApplianceDownloadPath accepted forbidden extension: %q", p)
			}
			if !validRemotePathRegex.MatchString(cleaned) {
				t.Fatalf("ValidateApplianceDownloadPath accepted invalid characters: %q", p)
			}
		}
	})
}

func FuzzValidateHost(f *testing.F) {
	seeds := []string{
		"192.168.1.1",
		"10.0.0.1",
		"2001:db8::1",
		"[2001:db8::1]",
		"bigip.internal.local",
		"bigip-dut_1",
		"bigip;rm -rf /",
		"`id`",
		"$(whoami)",
		"192.168.1.1:8443",
		"-leading-dash",
		"trailing-dash-",
		"",
		"   ",
		"256.256.256.256",
		"::1",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, host string) {
		// ValidateHost must never panic on arbitrary input
		_, _, _ = ValidateHost(host)
	})
}

func FuzzCleanSSHHost(f *testing.F) {
	seeds := []string{
		"10.0.0.1",
		"[2001:db8::1]",
		"2001:db8::1",
		"[[::1]]",
		"[]",
		"[",
		"]",
		"[10.0.0.1]",
		"[[[::1]]]",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, host string) {
		res, _ := CleanSSHHost(host)
		if strings.HasPrefix(res, "[") && strings.HasSuffix(res, "]") {
			t.Fatalf("CleanSSHHost returned bracketed host: %q", res)
		}
	})
}

func FuzzInspectQKViewReader(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("not a tar or gzip file at all"))
	f.Add([]byte("\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\xfffake compressed content"))
	f.Add([]byte("\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\x03\x03\x00\x00\x00\x00\x00\x00\x00\x00\x00"))

	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		// Must handle completely arbitrary or corrupted byte sequences gracefully without crashing/panicking
		_, _ = InspectQKViewReader(r)
	})
}

func FuzzParseHardwareJSON(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"items":[{"systemInformation":[{"platform":"Z100","systemId":"b7972418-4a6c-48be-a6ca-3870817349ab"}]}]}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"entries": {"https://localhost/mgmt/tm/sys/hardware/system-info": {"nestedStats": {"entries": {"platform": {"description": "Z100"}}}}}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		info := &SystemInfo{}
		parseHardwareJSON(data, info)
	})
}

func FuzzParseTmshOutput(f *testing.F) {
	f.Add("Platform Name: BIG-IP 10000\nChassis Serial: f5-abcd-1234\n")
	f.Add("Sys::Version\nMain Package\n  Product  BIG-IP\n  Version  17.1.0\n  Build    0.0.4\n  Edition  Final\n")
	f.Add("sys provision afm { level nominal }\nsys provision ltm { level nominal }\n")

	f.Fuzz(func(t *testing.T, text string) {
		_ = parseTmshOutput(text)
	})
}
