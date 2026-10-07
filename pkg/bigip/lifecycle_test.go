package bigip

import (
	"testing"
	"time"
)

func TestParseTMOSVersion(t *testing.T) {
	tests := []struct {
		input   string
		wantMaj int
		wantMin int
		wantPat int
		wantOK  bool
	}{
		{"17.5.1", 17, 5, 1, true},
		{"17.5.1.9", 17, 5, 1, true},
		{"v16.1.4.1", 16, 1, 4, true},
		{"BIG-IP 15.1.10", 15, 1, 10, true},
		{"14.1.5", 14, 1, 5, true},
		{"13.1.3", 13, 1, 3, true},
		{"12.1.6", 12, 1, 6, true},
		{"11.6.5", 11, 6, 5, true},
		{"21.0.0", 21, 0, 0, true},
		{"invalid", 0, 0, 0, false},
		{"", 0, 0, 0, false},
	}

	for _, tt := range tests {
		maj, min, pat, ok := ParseTMOSVersion(tt.input)
		if ok != tt.wantOK {
			t.Errorf("ParseTMOSVersion(%q) ok = %v; want %v", tt.input, ok, tt.wantOK)
			continue
		}
		if ok {
			if maj != tt.wantMaj || min != tt.wantMin || pat != tt.wantPat {
				t.Errorf("ParseTMOSVersion(%q) = (%d, %d, %d); want (%d, %d, %d)", tt.input, maj, min, pat, tt.wantMaj, tt.wantMin, tt.wantPat)
			}
		}
	}
}

func TestEvaluateTMOSLifecycle(t *testing.T) {
	now2026 := time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		version    string
		asOf       time.Time
		wantStatus LifecycleStatus
		wantEOL    bool
	}{
		{
			name:       "TMOS 11.6.5 is EOL",
			version:    "11.6.5",
			asOf:       now2026,
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 12.1.6 is EOL",
			version:    "12.1.6",
			asOf:       now2026,
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 13.1.5 is EOL",
			version:    "13.1.5",
			asOf:       now2026,
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 14.1.5 is EOL",
			version:    "14.1.5",
			asOf:       now2026,
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 15.0.0 is EOL",
			version:    "15.0.0",
			asOf:       now2026,
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 15.1.10 is EOL",
			version:    "15.1.10.2",
			asOf:       now2026,
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 16.0.0 is EOL",
			version:    "16.0.0",
			asOf:       now2026,
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 16.1.4 is EOL in late 2026",
			version:    "16.1.4.1",
			asOf:       now2026,
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 17.0.0 is EOL",
			version:    "17.0.0",
			asOf:       now2026,
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 17.1.1 is supported in 2026",
			version:    "17.1.1",
			asOf:       now2026,
			wantStatus: StatusSupported,
			wantEOL:    false,
		},
		{
			name:       "TMOS 17.5.1.9 is supported in 2026",
			version:    "17.5.1.9",
			asOf:       now2026,
			wantStatus: StatusSupported,
			wantEOL:    false,
		},
		{
			name:       "TMOS 17.1.1 approaches EOL in Feb 2027",
			version:    "17.1.1",
			asOf:       time.Date(2027, time.February, 1, 0, 0, 0, 0, time.UTC),
			wantStatus: StatusApproachingEOL,
			wantEOL:    false,
		},
		{
			name:       "TMOS 17.1.1 is EOL in April 2027",
			version:    "17.1.1",
			asOf:       time.Date(2027, time.April, 15, 0, 0, 0, 0, time.UTC),
			wantStatus: StatusEOL,
			wantEOL:    true,
		},
		{
			name:       "TMOS 21.0.0 is supported",
			version:    "21.0.0",
			asOf:       now2026,
			wantStatus: StatusSupported,
			wantEOL:    false,
		},
		{
			name:       "Unknown empty version",
			version:    "",
			asOf:       now2026,
			wantStatus: StatusUnknown,
			wantEOL:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := EvaluateTMOSLifecycle(tt.version, tt.asOf)
			if res.Status != tt.wantStatus {
				t.Errorf("EvaluateTMOSLifecycle(%q) status = %v; want %v", tt.version, res.Status, tt.wantStatus)
			}
			if res.IsEOL != tt.wantEOL {
				t.Errorf("EvaluateTMOSLifecycle(%q) isEOL = %v; want %v", tt.version, res.IsEOL, tt.wantEOL)
			}
			if tt.wantEOL && res.Warning == "" {
				t.Errorf("expected warning for EOL version %q", tt.version)
			}
		})
	}
}

func TestEvaluatePlatformLifecycle(t *testing.T) {
	now2026 := time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		platform    string
		asOf        time.Time
		wantStatus  LifecycleStatus
		wantEOL     bool
		wantVirtual bool
		wantFamily  string
	}{
		{
			name:        "AWS Virtual Edition Z100",
			platform:    "Z100",
			asOf:        now2026,
			wantStatus:  StatusSupported,
			wantEOL:     false,
			wantVirtual: true,
			wantFamily:  "Virtual Edition",
		},
		{
			name:        "Generic Virtual Edition",
			platform:    "BIG-IP Virtual Edition",
			asOf:        now2026,
			wantStatus:  StatusSupported,
			wantEOL:     false,
			wantVirtual: true,
			wantFamily:  "Virtual Edition",
		},
		{
			name:        "rSeries r5800 appliance",
			platform:    "r5800",
			asOf:        now2026,
			wantStatus:  StatusSupported,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "F5 rSeries",
		},
		{
			name:        "VELOS CX410 chassis",
			platform:    "CX410",
			asOf:        now2026,
			wantStatus:  StatusSupported,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "F5 VELOS",
		},
		{
			name:        "iSeries i2800 in late 2026 (approaching EoSS)",
			platform:    "i2800",
			asOf:        now2026,
			wantStatus:  StatusApproachingEOL,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "BIG-IP iSeries",
		},
		{
			name:        "iSeries i2800 in 2027 (Extended Support)",
			platform:    "i2800",
			asOf:        time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC),
			wantStatus:  StatusExtendedSupport,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "BIG-IP iSeries",
		},
		{
			name:        "iSeries C117 code (i850/i2600/i2800)",
			platform:    "BIG-IP C117",
			asOf:        now2026,
			wantStatus:  StatusApproachingEOL,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "BIG-IP iSeries",
		},
		{
			name:        "iSeries C119 code (i5600/i5800)",
			platform:    "C119",
			asOf:        now2026,
			wantStatus:  StatusApproachingEOL,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "BIG-IP iSeries",
		},
		{
			name:        "Legacy D110 code (7000s/7200v/BIG-IQ 7000)",
			platform:    "BIG-IP D110",
			asOf:        now2026,
			wantStatus:  StatusEOL,
			wantEOL:     true,
			wantVirtual: false,
			wantFamily:  "BIG-IP Legacy Hardware",
		},
		{
			name:        "iSeries i15800 High-End",
			platform:    "i15800",
			asOf:        now2026,
			wantStatus:  StatusSupported,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "BIG-IP iSeries (High-End)",
		},
		{
			name:        "VIPRION C2400 chassis in late 2026 (Extended Support)",
			platform:    "C2400",
			asOf:        now2026,
			wantStatus:  StatusExtendedSupport,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "VIPRION",
		},
		{
			name:        "VIPRION B2250 blade in late 2026 (Extended Support)",
			platform:    "B2250",
			asOf:        now2026,
			wantStatus:  StatusExtendedSupport,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "VIPRION",
		},
		{
			name:        "VIPRION C4800 chassis in late 2026",
			platform:    "C4800",
			asOf:        now2026,
			wantStatus:  StatusSupported,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "VIPRION (High-End)",
		},
		{
			name:        "VIPRION legacy blade B2100 is EOL",
			platform:    "B2100",
			asOf:        now2026,
			wantStatus:  StatusEOL,
			wantEOL:     true,
			wantVirtual: false,
			wantFamily:  "VIPRION (Legacy)",
		},
		{
			name:        "Legacy 4200v appliance is EOL",
			platform:    "4200v",
			asOf:        now2026,
			wantStatus:  StatusEOL,
			wantEOL:     true,
			wantVirtual: false,
			wantFamily:  "BIG-IP Legacy Hardware",
		},
		{
			name:        "Legacy 2000s hardware code C109 is EOL",
			platform:    "C109",
			asOf:        now2026,
			wantStatus:  StatusEOL,
			wantEOL:     true,
			wantVirtual: false,
			wantFamily:  "BIG-IP Legacy Hardware",
		},
		{
			name:        "Unknown platform",
			platform:    "CustomUnknownBox",
			asOf:        now2026,
			wantStatus:  StatusUnknown,
			wantEOL:     false,
			wantVirtual: false,
			wantFamily:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := EvaluatePlatformLifecycle(tt.platform, tt.asOf)
			if res.Status != tt.wantStatus {
				t.Errorf("EvaluatePlatformLifecycle(%q) status = %v; want %v", tt.platform, res.Status, tt.wantStatus)
			}
			if res.IsEOL != tt.wantEOL {
				t.Errorf("EvaluatePlatformLifecycle(%q) isEOL = %v; want %v", tt.platform, res.IsEOL, tt.wantEOL)
			}
			if res.IsVirtual != tt.wantVirtual {
				t.Errorf("EvaluatePlatformLifecycle(%q) isVirtual = %v; want %v", tt.platform, res.IsVirtual, tt.wantVirtual)
			}
			if res.Family != tt.wantFamily {
				t.Errorf("EvaluatePlatformLifecycle(%q) family = %q; want %q", tt.platform, res.Family, tt.wantFamily)
			}
		})
	}
}

func TestEvaluateLifecycle_Combined(t *testing.T) {
	now2026 := time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)

	// Case 1: EOL Software + Approaching EOL Hardware
	info := &SystemInfo{
		Version:  "15.1.8",
		Platform: "i2800",
	}
	info.EvaluateLifecycle(now2026)

	if info.Lifecycle == nil {
		t.Fatal("expected Lifecycle to be populated")
	}
	if !info.Lifecycle.Software.IsEOL {
		t.Errorf("expected software to be EOL, got %v", info.Lifecycle.Software.Status)
	}
	if info.Lifecycle.Hardware.Status != StatusApproachingEOL {
		t.Errorf("expected hardware to be Approaching EOL, got %v", info.Lifecycle.Hardware.Status)
	}
	if len(info.Lifecycle.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d: %v", len(info.Lifecycle.Warnings), info.Lifecycle.Warnings)
	}

	// Case 2: Supported Software on Virtual Edition
	veInfo := &SystemInfo{
		Version:  "17.5.1",
		Platform: "Z100",
	}
	veInfo.EvaluateLifecycle(now2026)
	if len(veInfo.Lifecycle.Warnings) != 0 {
		t.Errorf("expected 0 warnings for 17.5.1 on Z100, got: %v", veInfo.Lifecycle.Warnings)
	}
}

func TestLookupPlatform(t *testing.T) {
	tests := []struct {
		input       string
		wantCode    string
		wantModel   string
		wantFamily  string
		wantVirtual bool
		wantFound   bool
	}{
		{"C119", "C119", "BIG-IP i5600 / i5800", "BIG-IP iSeries", false, true},
		{"c119", "C119", "BIG-IP i5600 / i5800", "BIG-IP iSeries", false, true},
		{"C117", "C117", "BIG-IP i850 / i2600 / i2800", "BIG-IP iSeries", false, true},
		{"C115", "C115", "BIG-IP i4600 / i4800", "BIG-IP iSeries", false, true},
		{"C113", "C113", "BIG-IP 4000s / 4200v", "BIG-IP Legacy Hardware", false, true},
		{"D110", "D110", "BIG-IP 7000s / 7200v / BIG-IQ 7000", "BIG-IP Legacy Hardware", false, true},
		{"Z100", "Z100", "BIG-IP Virtual Edition", "Virtual Edition", true, true},
		{"Z100A", "Z100A", "BIG-IP Virtual Edition (AWS)", "Virtual Edition", true, true},
		{"F100", "F100", "VIPRION C2400 Chassis", "VIPRION", false, true},
		{"A108", "A108", "VIPRION B4300 Blade", "VIPRION (Legacy)", false, true},
		{"CX410", "CX410", "VELOS CX410 Chassis", "F5 VELOS", false, true},
		{"BX110", "BX110", "VELOS BX110 Blade", "F5 VELOS", false, true},
		{"platform=C119", "C119", "BIG-IP i5600 / i5800", "BIG-IP iSeries", false, true},
		{"BIG-IP i5800 (C119)", "C119", "BIG-IP i5600 / i5800", "BIG-IP iSeries", false, true},
		{"NonExistentPlatform", "", "", "", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			rec, found := LookupPlatform(tt.input)
			if found != tt.wantFound {
				t.Fatalf("LookupPlatform(%q) found = %v; want %v", tt.input, found, tt.wantFound)
			}
			if found {
				if rec.Code != tt.wantCode {
					t.Errorf("LookupPlatform(%q).Code = %q; want %q", tt.input, rec.Code, tt.wantCode)
				}
				if rec.Model != tt.wantModel {
					t.Errorf("LookupPlatform(%q).Model = %q; want %q", tt.input, rec.Model, tt.wantModel)
				}
				if rec.Family != tt.wantFamily {
					t.Errorf("LookupPlatform(%q).Family = %q; want %q", tt.input, rec.Family, tt.wantFamily)
				}
				if rec.IsVirtual != tt.wantVirtual {
					t.Errorf("LookupPlatform(%q).IsVirtual = %v; want %v", tt.input, rec.IsVirtual, tt.wantVirtual)
				}
			}
		})
	}
}
