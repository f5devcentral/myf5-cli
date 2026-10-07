package bigip

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LifecycleStatus defines the support and lifecycle state of a TMOS release or hardware platform.
type LifecycleStatus string

const (
	StatusSupported       LifecycleStatus = "Supported"
	StatusApproachingEOL  LifecycleStatus = "Approaching EOL"
	StatusExtendedSupport LifecycleStatus = "Extended Support"
	StatusEOL             LifecycleStatus = "End of Life"
	StatusUnknown         LifecycleStatus = "Unknown"
)

// Policy and article reference URLs
const (
	URLPolicyTMOS     = "https://my.f5.com/manage/s/article/K5903"
	URLPolicyHardware = "https://my.f5.com/manage/s/article/K4309"
	URLArticleK13144  = "https://my.f5.com/manage/s/article/K13144"
)

// PlatformCategory groups F5 platforms into distinct lifecycle tracks under K4309.
type PlatformCategory string

const (
	CategoryVirtualEdition  PlatformCategory = "Virtual Edition"
	CategoryISeriesMidrange PlatformCategory = "iSeries Midrange"
	CategoryISeriesHighEnd  PlatformCategory = "iSeries High-End"
	CategoryModern          PlatformCategory = "Modern Architecture" // rSeries / VELOS
	CategoryVIPRIONMidrange PlatformCategory = "VIPRION Midrange"
	CategoryVIPRIONHighEnd  PlatformCategory = "VIPRION High-End"
	CategoryVIPRIONLegacy   PlatformCategory = "VIPRION Legacy"
	CategoryLegacyAppliance PlatformCategory = "Legacy Hardware"
)

// PlatformIDRecord links an internal F5 platform ID code (e.g. C119) from article K13144
// to its commercial marketing model name, product family, and hardware category.
type PlatformIDRecord struct {
	Code      string           `json:"code"`
	Model     string           `json:"model"`
	Family    string           `json:"family"`
	IsVirtual bool             `json:"isVirtual"`
	Category  PlatformCategory `json:"category"`
}

var k13144PlatformMap = map[string]PlatformIDRecord{
	// Virtual Edition (K13144 / Cloud / Hypervisors)
	"Z100":  {Code: "Z100", Model: "BIG-IP Virtual Edition", Family: "Virtual Edition", IsVirtual: true, Category: CategoryVirtualEdition},
	"Z100A": {Code: "Z100A", Model: "BIG-IP Virtual Edition (AWS)", Family: "Virtual Edition", IsVirtual: true, Category: CategoryVirtualEdition},
	"Z100H": {Code: "Z100H", Model: "BIG-IP Virtual Edition (Hyper-V)", Family: "Virtual Edition", IsVirtual: true, Category: CategoryVirtualEdition},
	"Z100K": {Code: "Z100K", Model: "BIG-IP Virtual Edition (KVM)", Family: "Virtual Edition", IsVirtual: true, Category: CategoryVirtualEdition},
	"V100":  {Code: "V100", Model: "BIG-IP Virtual Edition (ESXi)", Family: "Virtual Edition", IsVirtual: true, Category: CategoryVirtualEdition},

	// iSeries Midrange (K13144 / K4309)
	"C115": {Code: "C115", Model: "BIG-IP i4600 / i4800", Family: "BIG-IP iSeries", Category: CategoryISeriesMidrange},
	"C116": {Code: "C116", Model: "BIG-IP i10600 / i10800", Family: "BIG-IP iSeries", Category: CategoryISeriesMidrange},
	"C117": {Code: "C117", Model: "BIG-IP i850 / i2600 / i2800", Family: "BIG-IP iSeries", Category: CategoryISeriesMidrange},
	"C118": {Code: "C118", Model: "BIG-IP i7600 / i7800", Family: "BIG-IP iSeries", Category: CategoryISeriesMidrange},
	"C119": {Code: "C119", Model: "BIG-IP i5600 / i5800", Family: "BIG-IP iSeries", Category: CategoryISeriesMidrange},
	"C123": {Code: "C123", Model: "BIG-IP i11400-T / i11600-DS / i11800-DS", Family: "BIG-IP iSeries", Category: CategoryISeriesMidrange},

	// iSeries High-End (K13144 / K4309)
	"D116": {Code: "D116", Model: "BIG-IP i15600 / i15800", Family: "BIG-IP iSeries (High-End)", Category: CategoryISeriesHighEnd},

	// rSeries (F5OS)
	"C128": {Code: "C128", Model: "F5 r10600 / r10800", Family: "F5 rSeries", Category: CategoryModern},
	"C129": {Code: "C129", Model: "F5 r5600 / r5800", Family: "F5 rSeries", Category: CategoryModern},
	"C130": {Code: "C130", Model: "F5 r2600 / r2800", Family: "F5 rSeries", Category: CategoryModern},
	"C131": {Code: "C131", Model: "F5 r4600 / r4800", Family: "F5 rSeries", Category: CategoryModern},
	"C138": {Code: "C138", Model: "F5 r12600-DS / r12800-DS / r12900-DS", Family: "F5 rSeries", Category: CategoryModern},

	// VELOS (F5OS)
	"CX410": {Code: "CX410", Model: "VELOS CX410 Chassis", Family: "F5 VELOS", Category: CategoryModern},
	"BX110": {Code: "BX110", Model: "VELOS BX110 Blade", Family: "F5 VELOS", Category: CategoryModern},

	// VIPRION Chassis
	"F100": {Code: "F100", Model: "VIPRION C2400 Chassis", Family: "VIPRION", Category: CategoryVIPRIONMidrange},
	"D114": {Code: "D114", Model: "VIPRION C2200 Chassis", Family: "VIPRION", Category: CategoryVIPRIONMidrange},
	"J100": {Code: "J100", Model: "VIPRION C4400 Chassis", Family: "VIPRION (Legacy)", Category: CategoryVIPRIONLegacy},
	"J102": {Code: "J102", Model: "VIPRION C4480 Chassis", Family: "VIPRION (High-End)", Category: CategoryVIPRIONHighEnd},
	"S100": {Code: "S100", Model: "VIPRION C4800 Chassis", Family: "VIPRION (High-End)", Category: CategoryVIPRIONHighEnd},

	// VIPRION Blades
	"A100": {Code: "A100", Model: "VIPRION B4100 Blade", Family: "VIPRION (Legacy)", Category: CategoryVIPRIONLegacy},
	"A107": {Code: "A107", Model: "VIPRION B4200 Blade", Family: "VIPRION (Legacy)", Category: CategoryVIPRIONLegacy},
	"A108": {Code: "A108", Model: "VIPRION B4300 Blade", Family: "VIPRION (Legacy)", Category: CategoryVIPRIONLegacy},
	"A109": {Code: "A109", Model: "VIPRION B2100 Blade", Family: "VIPRION (Legacy)", Category: CategoryVIPRIONLegacy},
	"A110": {Code: "A110", Model: "VIPRION B4340N Blade", Family: "VIPRION (Legacy)", Category: CategoryVIPRIONLegacy},
	"A112": {Code: "A112", Model: "VIPRION B2250 Blade", Family: "VIPRION", Category: CategoryVIPRIONMidrange},
	"A113": {Code: "A113", Model: "VIPRION B2150 Blade", Family: "VIPRION", Category: CategoryVIPRIONMidrange},
	"A114": {Code: "A114", Model: "VIPRION B4450 Blade", Family: "VIPRION (High-End)", Category: CategoryVIPRIONHighEnd},

	// Legacy Hardware Appliances (K4309: EOL past End of Technical Support)
	"C102": {Code: "C102", Model: "BIG-IP 1600", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"C103": {Code: "C103", Model: "BIG-IP 3600", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"C106": {Code: "C106", Model: "BIG-IP 3900 / Enterprise Manager 4000", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"C109": {Code: "C109", Model: "BIG-IP 5000s / 5200v / 5250v", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"C112": {Code: "C112", Model: "BIG-IP 2000s / 2200s", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"C113": {Code: "C113", Model: "BIG-IP 4000s / 4200v", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"C114": {Code: "C114", Model: "BIG-IP 800", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"D104": {Code: "D104", Model: "BIG-IP 6900", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"D106": {Code: "D106", Model: "BIG-IP 8900", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"D110": {Code: "D110", Model: "BIG-IP 7000s / 7200v / BIG-IQ 7000", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"D111": {Code: "D111", Model: "BIG-IP 12250v", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"D112": {Code: "D112", Model: "BIG-IP 10350v", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"D113": {Code: "D113", Model: "BIG-IP 10000s / 10200v", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"E101": {Code: "E101", Model: "BIG-IP 11000", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
	"E102": {Code: "E102", Model: "BIG-IP 11050", Family: "BIG-IP Legacy Hardware", Category: CategoryLegacyAppliance},
}

// LookupPlatformByID returns the K13144 platform metadata for an exact F5 platform ID code
// (e.g. "C119", "C113", "Z100", "D110").
func LookupPlatformByID(code string) (PlatformIDRecord, bool) {
	clean := strings.ToUpper(strings.TrimSpace(code))
	clean = strings.TrimPrefix(clean, "PLATFORM=")
	rec, ok := k13144PlatformMap[clean]
	return rec, ok
}

var platformTokenRe = regexp.MustCompile(`\b([ACDEFSJVZ]\d{3}[A-Z]?|BX110|CX410)\b`)

// LookupPlatform searches for a known K13144 platform code in the given string,
// handling raw codes ("C119"), QKView key-values ("platform=C119"), or formatted
// strings ("BIG-IP i5800 (C119)").
func LookupPlatform(platform string) (PlatformIDRecord, bool) {
	upper := strings.ToUpper(strings.TrimSpace(platform))
	if upper == "" {
		return PlatformIDRecord{}, false
	}
	// Direct code match (e.g. "C119" or "platform=C119")
	if rec, ok := LookupPlatformByID(upper); ok {
		return rec, true
	}
	// Search for platform code token within the string
	matches := platformTokenRe.FindAllString(upper, -1)
	for _, m := range matches {
		if rec, ok := k13144PlatformMap[m]; ok {
			return rec, true
		}
	}
	return PlatformIDRecord{}, false
}

// SoftwareLifecycle captures the lifecycle milestones for a TMOS version release.
type SoftwareLifecycle struct {
	Version        string          `json:"version"`
	Branch         string          `json:"branch,omitempty"`
	Status         LifecycleStatus `json:"status"`
	IsEOL          bool            `json:"isEol"`
	EoSD           string          `json:"eosd,omitempty"` // End of Software Development (YYYY-MM-DD)
	EoTS           string          `json:"eots,omitempty"` // End of Technical Support (YYYY-MM-DD)
	NoticeURL      string          `json:"noticeUrl,omitempty"`
	Recommendation string          `json:"recommendation,omitempty"`
	Warning        string          `json:"warning,omitempty"`
}

// HardwareLifecycle captures the lifecycle milestones for a BIG-IP physical or virtual platform.
type HardwareLifecycle struct {
	Platform       string          `json:"platform"`
	Family         string          `json:"family,omitempty"`
	IsVirtual      bool            `json:"isVirtual"`
	Status         LifecycleStatus `json:"status"`
	IsEOL          bool            `json:"isEol"`
	EoS            string          `json:"eos,omitempty"`   // End of Sale (YYYY-MM-DD)
	EoNSS          string          `json:"eonss,omitempty"` // End of New Software Support (YYYY-MM-DD)
	EoSS           string          `json:"eoss,omitempty"`  // End of Software Support (YYYY-MM-DD)
	EoTS           string          `json:"eots,omitempty"`  // End of Technical Support (YYYY-MM-DD)
	NoticeURL      string          `json:"noticeUrl,omitempty"`
	Recommendation string          `json:"recommendation,omitempty"`
	Warning        string          `json:"warning,omitempty"`
}

// LifecycleInfo aggregates both software and hardware lifecycle assessments.
type LifecycleInfo struct {
	Software SoftwareLifecycle `json:"software"`
	Hardware HardwareLifecycle `json:"hardware"`
	Warnings []string          `json:"warnings,omitempty"`
}

var versionExtractorRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// ParseTMOSVersion extracts major, minor, and patch numbers from a version string.
func ParseTMOSVersion(v string) (major, minor, patch int, ok bool) {
	m := versionExtractorRe.FindStringSubmatch(v)
	if len(m) < 3 {
		return 0, 0, 0, false
	}
	var err error
	major, err = strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, 0, false
	}
	minor, err = strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, 0, false
	}
	if len(m) > 3 && m[3] != "" {
		patch, _ = strconv.Atoi(m[3])
	}
	return major, minor, patch, true
}

// EvaluateTMOSLifecycle checks a TMOS version against F5 Software Lifecycle Policy K5903.
func EvaluateTMOSLifecycle(version string, asOf time.Time) SoftwareLifecycle {
	cleanVer := strings.TrimSpace(version)
	res := SoftwareLifecycle{
		Version:   cleanVer,
		NoticeURL: URLPolicyTMOS,
		Status:    StatusUnknown,
	}

	if cleanVer == "" {
		res.Status = StatusUnknown
		return res
	}

	major, minor, _, ok := ParseTMOSVersion(cleanVer)
	if !ok {
		res.Status = StatusUnknown
		return res
	}

	res.Branch = fmt.Sprintf("%d.%d", major, minor)

	// Define milestone dates based on K5903 / K8986
	var (
		eosdDate  time.Time
		eotsDate  time.Time
		isLTS     bool
		hasRecord bool
	)

	switch {
	case major < 13:
		// TMOS 11.x and 12.x reached End of Life in 2020-2022
		eosdDate = time.Date(2022, time.December, 31, 23, 59, 59, 0, time.UTC)
		eotsDate = eosdDate
		isLTS = true
		hasRecord = true

	case major == 13:
		// TMOS 13.1.x: EoSD/EoTS Dec 31, 2023
		eosdDate = time.Date(2023, time.December, 31, 23, 59, 59, 0, time.UTC)
		eotsDate = eosdDate
		isLTS = (minor == 1)
		hasRecord = true

	case major == 14:
		// TMOS 14.1.x: EoSD/EoTS Dec 31, 2023
		eosdDate = time.Date(2023, time.December, 31, 23, 59, 59, 0, time.UTC)
		eotsDate = eosdDate
		isLTS = (minor == 1)
		hasRecord = true

	case major == 15:
		if minor == 0 {
			eosdDate = time.Date(2021, time.February, 28, 23, 59, 59, 0, time.UTC)
			eotsDate = eosdDate
			isLTS = false
		} else {
			// TMOS 15.1.x: EoSD/EoTS Dec 31, 2024
			eosdDate = time.Date(2024, time.December, 31, 23, 59, 59, 0, time.UTC)
			eotsDate = eosdDate
			isLTS = true
		}
		hasRecord = true

	case major == 16:
		if minor == 0 {
			eosdDate = time.Date(2021, time.December, 31, 23, 59, 59, 0, time.UTC)
			eotsDate = eosdDate
			isLTS = false
		} else {
			// TMOS 16.1.x: Standard EoSD/EoTS July 31, 2025 (FIPS extensions up to July 31, 2026)
			eosdDate = time.Date(2025, time.July, 31, 23, 59, 59, 0, time.UTC)
			eotsDate = eosdDate
			isLTS = true
		}
		hasRecord = true

	case major == 17:
		if minor == 0 {
			eosdDate = time.Date(2023, time.July, 31, 23, 59, 59, 0, time.UTC)
			eotsDate = eosdDate
			isLTS = false
		} else {
			// TMOS 17.1.x / 17.5.x: LTS EoSD/EoTS March 31, 2027
			eosdDate = time.Date(2027, time.March, 31, 23, 59, 59, 0, time.UTC)
			eotsDate = eosdDate
			isLTS = true
		}
		hasRecord = true

	default:
		// Future releases (e.g., BIG-IP 20.x, 21.x): actively supported
		res.Status = StatusSupported
		res.Recommendation = "Current release stream."
		return res
	}

	if hasRecord {
		res.EoSD = eosdDate.Format("2006-01-02")
		res.EoTS = eotsDate.Format("2006-01-02")

		if asOf.After(eotsDate) {
			res.Status = StatusEOL
			res.IsEOL = true
			res.Warning = fmt.Sprintf("TMOS version %s reached End of Life (EoSD/EoTS) on %s (K5903).", cleanVer, res.EoSD)
			res.Recommendation = "Upgrade to an actively supported LTS release (TMOS 17.1.x+) to receive software fixes and support."
		} else if asOf.AddDate(0, 3, 0).After(eosdDate) {
			res.Status = StatusApproachingEOL
			res.IsEOL = false
			res.Warning = fmt.Sprintf("TMOS version %s is approaching End of Life on %s (K5903).", cleanVer, res.EoSD)
			res.Recommendation = "Plan upgrade to an actively supported release (TMOS 17.1.x+)."
		} else {
			res.Status = StatusSupported
			res.IsEOL = false
			if isLTS {
				res.Recommendation = fmt.Sprintf("LTS release with standard support active until %s.", res.EoSD)
			} else {
				res.Recommendation = fmt.Sprintf("Standard support active until %s.", res.EoSD)
			}
		}
	}

	return res
}

// EvaluatePlatformLifecycle checks a hardware platform name or platform ID (K13144)
// against F5 Hardware Product Lifecycle Support Policy K4309.
func EvaluatePlatformLifecycle(platform string, asOf time.Time) HardwareLifecycle {
	cleanPlat := strings.TrimSpace(platform)
	if cleanPlat == "" {
		return HardwareLifecycle{
			NoticeURL: URLPolicyHardware,
			Status:    StatusUnknown,
		}
	}

	upper := strings.ToUpper(cleanPlat)

	res := HardwareLifecycle{
		Platform:  cleanPlat,
		NoticeURL: URLPolicyHardware,
		Status:    StatusUnknown,
	}

	// 1. Check if platform matches a known F5 Platform ID code from K13144
	if rec, ok := LookupPlatform(cleanPlat); ok {
		res.Family = rec.Family
		res.IsVirtual = rec.IsVirtual

		// If the platform input does not already mention the model, enrich display with model
		displayPlat := cleanPlat
		if !strings.Contains(upper, strings.ToUpper(rec.Model)) && !rec.IsVirtual {
			displayPlat = fmt.Sprintf("%s (%s)", cleanPlat, rec.Model)
		}

		switch rec.Category {
		case CategoryVirtualEdition:
			res.Status = StatusSupported
			res.Recommendation = "Virtual appliance; lifecycle is governed by the TMOS software version (K5903)."
			return res

		case CategoryModern:
			res.Status = StatusSupported
			res.Recommendation = "Current generation architecture running F5OS."
			return res

		case CategoryISeriesMidrange:
			res.EoS = "2024-01-01"
			res.EoNSS = "2026-01-01"
			res.EoSS = "2027-01-01"
			res.EoTS = "2031-01-01"

			eossDate := time.Date(2027, time.January, 1, 23, 59, 59, 0, time.UTC)
			eonssDate := time.Date(2026, time.January, 1, 23, 59, 59, 0, time.UTC)

			if asOf.After(eossDate) {
				res.Status = StatusExtendedSupport
				res.Warning = fmt.Sprintf("Platform %s is in Extended Support phase (EoSS reached 2027-01-01; EoTS: 2031-01-01). No software maintenance or bug fixes are developed for this platform (K4309).", displayPlat)
				res.Recommendation = "Migrate to F5 rSeries appliances."
			} else if asOf.After(eonssDate) {
				res.Status = StatusApproachingEOL
				res.Warning = fmt.Sprintf("Platform %s reached End of New Software Support (EoNSS: 2026-01-01) and reaches End of Software Support on 2027-01-01 (K4309).", displayPlat)
				res.Recommendation = "Plan migration to F5 rSeries appliances before EoSS."
			} else {
				res.Status = StatusSupported
				res.Recommendation = "Platform in Regular Support until 2027-01-01."
			}
			return res

		case CategoryISeriesHighEnd:
			res.EoS = "2027-01-01"
			res.EoNSS = "2028-01-01"
			res.EoSS = "2029-01-01"
			res.EoTS = "2030-01-01"

			eosDate := time.Date(2027, time.January, 1, 23, 59, 59, 0, time.UTC)
			if asOf.After(eosDate) {
				res.Status = StatusApproachingEOL
				res.Warning = fmt.Sprintf("Platform %s reached End of Sale on 2027-01-01 (K4309).", displayPlat)
			} else {
				res.Status = StatusSupported
				res.Recommendation = "Regular support active until 2029-01-01."
			}
			return res

		case CategoryVIPRIONMidrange:
			res.EoS = "2023-04-01"
			res.EoNSS = "2025-04-01"
			res.EoSS = "2026-04-01"
			res.EoTS = "2030-04-01"

			eossDate := time.Date(2026, time.April, 1, 23, 59, 59, 0, time.UTC)
			if asOf.After(eossDate) {
				res.Status = StatusExtendedSupport
				res.Warning = fmt.Sprintf("VIPRION platform %s is in Extended Support phase (EoSS reached 2026-04-01; EoTS: 2030-04-01). No software maintenance or bug fixes are developed for this platform (K4309).", displayPlat)
				res.Recommendation = "Migrate chassis workloads to F5 VELOS or rSeries systems."
			} else {
				res.Status = StatusApproachingEOL
				res.Warning = fmt.Sprintf("VIPRION platform %s approaches End of Software Support on 2026-04-01 (K4309).", displayPlat)
				res.Recommendation = "Plan migration to F5 VELOS."
			}
			return res

		case CategoryVIPRIONHighEnd:
			res.EoS = "2026-01-01"
			res.EoNSS = "2028-01-01"
			res.EoSS = "2029-01-01"
			res.EoTS = "2033-01-01"

			eosDate := time.Date(2026, time.January, 1, 23, 59, 59, 0, time.UTC)
			eossDate := time.Date(2029, time.January, 1, 23, 59, 59, 0, time.UTC)

			if asOf.After(eossDate) {
				res.Status = StatusExtendedSupport
				res.Warning = fmt.Sprintf("VIPRION platform %s reached EoSS on 2029-01-01 (K4309).", displayPlat)
				res.Recommendation = "Migrate to F5 VELOS."
			} else if asOf.After(eosDate) {
				res.Status = StatusSupported
				res.Warning = fmt.Sprintf("VIPRION platform %s reached End of Sale on 2026-01-01 (K4309). Regular software support active until 2029-01-01.", displayPlat)
				res.Recommendation = "Supported until 2029-01-01; evaluate migration to F5 VELOS."
			} else {
				res.Status = StatusSupported
				res.Recommendation = "Regular support active until 2029-01-01."
			}
			return res

		case CategoryVIPRIONLegacy:
			res.Status = StatusEOL
			res.IsEOL = true
			res.Warning = fmt.Sprintf("VIPRION platform %s reached End of Life (past End of Technical Support, K4309).", displayPlat)
			res.Recommendation = "Replace blade/chassis with supported hardware."
			return res

		case CategoryLegacyAppliance:
			res.Status = StatusEOL
			res.IsEOL = true
			res.Warning = fmt.Sprintf("Hardware platform %s reached End of Life (past End of Technical Support, K4309). No RMA or TAC support is available.", displayPlat)
			res.Recommendation = "Upgrade to active F5 rSeries appliances."
			return res
		}
	}

	// 2. Fallback to model name pattern matching when no platform ID was matched

	// Virtual Edition (Cloud, Hypervisors)
	if isVirtualPlatform(upper) {
		res.Family = "Virtual Edition"
		res.IsVirtual = true
		res.Status = StatusSupported
		res.Recommendation = "Virtual appliance; lifecycle is governed by the TMOS software version (K5903)."
		return res
	}

	// Modern Platforms: rSeries & VELOS
	if isModernPlatform(upper) {
		if strings.Contains(upper, "CX") || strings.Contains(upper, "BX") || strings.Contains(upper, "VELOS") {
			res.Family = "F5 VELOS"
		} else {
			res.Family = "F5 rSeries"
		}
		res.Status = StatusSupported
		res.Recommendation = "Current generation architecture running F5OS."
		return res
	}

	// iSeries Midrange: i2600, i2800, i4600, i4800, i5600, i5800, i7600, i7800, i10600, i10800, i11400, i11600, i11800
	if isISeriesMidrange(upper) {
		res.Family = "BIG-IP iSeries"
		res.EoS = "2024-01-01"
		res.EoNSS = "2026-01-01"
		res.EoSS = "2027-01-01"
		res.EoTS = "2031-01-01"

		eossDate := time.Date(2027, time.January, 1, 23, 59, 59, 0, time.UTC)
		eonssDate := time.Date(2026, time.January, 1, 23, 59, 59, 0, time.UTC)

		if asOf.After(eossDate) {
			res.Status = StatusExtendedSupport
			res.Warning = fmt.Sprintf("Platform %s is in Extended Support phase (EoSS reached 2027-01-01; EoTS: 2031-01-01). No software maintenance or bug fixes are developed for this platform (K4309).", cleanPlat)
			res.Recommendation = "Migrate to F5 rSeries appliances."
		} else if asOf.After(eonssDate) {
			res.Status = StatusApproachingEOL
			res.Warning = fmt.Sprintf("Platform %s reached End of New Software Support (EoNSS: 2026-01-01) and reaches End of Software Support on 2027-01-01 (K4309).", cleanPlat)
			res.Recommendation = "Plan migration to F5 rSeries appliances before EoSS."
		} else {
			res.Status = StatusSupported
			res.Recommendation = "Platform in Regular Support until 2027-01-01."
		}
		return res
	}

	// iSeries High-End: i15600, i15800
	if isISeriesHighEnd(upper) {
		res.Family = "BIG-IP iSeries (High-End)"
		res.EoS = "2027-01-01"
		res.EoNSS = "2028-01-01"
		res.EoSS = "2029-01-01"
		res.EoTS = "2030-01-01"

		eosDate := time.Date(2027, time.January, 1, 23, 59, 59, 0, time.UTC)
		if asOf.After(eosDate) {
			res.Status = StatusApproachingEOL
			res.Warning = fmt.Sprintf("Platform %s reached End of Sale on 2027-01-01 (K4309).", cleanPlat)
		} else {
			res.Status = StatusSupported
			res.Recommendation = "Regular support active until 2029-01-01."
		}
		return res
	}

	// VIPRION Midrange: C2400, C2200, B2150, B2250
	if isVIPRIONMidrange(upper) {
		res.Family = "VIPRION"
		res.EoS = "2023-04-01"
		res.EoNSS = "2025-04-01"
		res.EoSS = "2026-04-01"
		res.EoTS = "2030-04-01"

		eossDate := time.Date(2026, time.April, 1, 23, 59, 59, 0, time.UTC)
		if asOf.After(eossDate) {
			res.Status = StatusExtendedSupport
			res.Warning = fmt.Sprintf("VIPRION platform %s is in Extended Support phase (EoSS reached 2026-04-01; EoTS: 2030-04-01). No software maintenance or bug fixes are developed for this platform (K4309).", cleanPlat)
			res.Recommendation = "Migrate chassis workloads to F5 VELOS or rSeries systems."
		} else {
			res.Status = StatusApproachingEOL
			res.Warning = fmt.Sprintf("VIPRION platform %s approaches End of Software Support on 2026-04-01 (K4309).", cleanPlat)
			res.Recommendation = "Plan migration to F5 VELOS."
		}
		return res
	}

	// VIPRION High-End: C4800, C4480, B4450, B4460
	if isVIPRIONHighEnd(upper) {
		res.Family = "VIPRION (High-End)"
		res.EoS = "2026-01-01"
		res.EoNSS = "2028-01-01"
		res.EoSS = "2029-01-01"
		res.EoTS = "2033-01-01"

		eosDate := time.Date(2026, time.January, 1, 23, 59, 59, 0, time.UTC)
		eossDate := time.Date(2029, time.January, 1, 23, 59, 59, 0, time.UTC)

		if asOf.After(eossDate) {
			res.Status = StatusExtendedSupport
			res.Warning = fmt.Sprintf("VIPRION platform %s reached EoSS on 2029-01-01 (K4309).", cleanPlat)
			res.Recommendation = "Migrate to F5 VELOS."
		} else if asOf.After(eosDate) {
			res.Status = StatusSupported
			res.Warning = fmt.Sprintf("VIPRION platform %s reached End of Sale on 2026-01-01 (K4309). Regular software support active until 2029-01-01.", cleanPlat)
			res.Recommendation = "Supported until 2029-01-01; evaluate migration to F5 VELOS."
		} else {
			res.Status = StatusSupported
			res.Recommendation = "Regular support active until 2029-01-01."
		}
		return res
	}

	// VIPRION Legacy Blades: B2100, B4300, B4100, B4200
	if strings.Contains(upper, "B2100") || strings.Contains(upper, "B4300") || strings.Contains(upper, "B4100") || strings.Contains(upper, "B4200") {
		res.Family = "VIPRION (Legacy)"
		res.Status = StatusEOL
		res.IsEOL = true
		res.Warning = fmt.Sprintf("VIPRION blade %s reached End of Life (past End of Technical Support, K4309).", cleanPlat)
		res.Recommendation = "Replace blade with supported hardware."
		return res
	}

	// Legacy Hardware (2000s, 4000s, 5000s, 7000s, 10000s, 12000s, etc.)
	if isLegacyPlatform(upper) {
		res.Family = "BIG-IP Legacy Hardware"
		res.Status = StatusEOL
		res.IsEOL = true
		res.Warning = fmt.Sprintf("Hardware platform %s reached End of Life (past End of Technical Support, K4309). No RMA or TAC support is available.", cleanPlat)
		res.Recommendation = "Upgrade to active F5 rSeries appliances."
		return res
	}

	res.Status = StatusUnknown
	return res
}

// EvaluateLifecycle evaluates both software and hardware lifecycle statuses.
func EvaluateLifecycle(version, platform string, asOf time.Time) LifecycleInfo {
	sw := EvaluateTMOSLifecycle(version, asOf)
	hw := EvaluatePlatformLifecycle(platform, asOf)

	var warnings []string
	if sw.Warning != "" {
		warnings = append(warnings, sw.Warning)
	}
	if hw.Warning != "" {
		warnings = append(warnings, hw.Warning)
	}

	return LifecycleInfo{
		Software: sw,
		Hardware: hw,
		Warnings: warnings,
	}
}

// EvaluateLifecycle updates SystemInfo with lifecycle metadata.
func (s *SystemInfo) EvaluateLifecycle(asOf time.Time) {
	if s == nil {
		return
	}
	plat := s.Platform
	if plat == "" && s.CaseAttributes != nil && s.CaseAttributes["cloudProvider"] != "" {
		plat = s.CaseAttributes["cloudProvider"] + " Virtual Edition"
	}
	lc := EvaluateLifecycle(s.Version, plat, asOf)
	s.Lifecycle = &lc
}

func isVirtualPlatform(upper string) bool {
	return strings.Contains(upper, "Z100") ||
		strings.Contains(upper, "VIRTUAL") ||
		strings.Contains(upper, "VE-") ||
		strings.Contains(upper, "BIG-IP VE") ||
		upper == "VE" ||
		upper == "V100" ||
		strings.Contains(upper, "VMWARE") ||
		strings.Contains(upper, "KVM") ||
		strings.Contains(upper, "XEN") ||
		strings.Contains(upper, "HYPER-V")
}

func isModernPlatform(upper string) bool {
	// rSeries models: r2000, r4000, r5000, r10000, r2600, r2800, r4600, etc.
	rSeriesRe := regexp.MustCompile(`(^|[^A-Z0-9])R\d{4}`)
	if rSeriesRe.MatchString(upper) || strings.Contains(upper, "RSERIES") {
		return true
	}
	// VELOS chassis and blades
	if strings.Contains(upper, "VELOS") || strings.Contains(upper, "CX410") || strings.Contains(upper, "BX110") {
		return true
	}
	return false
}

func isISeriesMidrange(upper string) bool {
	// Matches i2600, i2800, i4600, i4800, i5600, i5800, i7600, i7800, i10600, i10800, i11400, i11600, i11800
	iMidRe := regexp.MustCompile(`(^|[^A-Z0-9])I(2[68]00|4[68]00|5[68]00|7[68]00|10[68]00|11[468]00)([^A-Z0-9]|$)`)
	if iMidRe.MatchString(upper) {
		return true
	}
	return false
}

func isISeriesHighEnd(upper string) bool {
	return strings.Contains(upper, "I15600") || strings.Contains(upper, "I15800")
}

func isVIPRIONMidrange(upper string) bool {
	return strings.Contains(upper, "C2400") || strings.Contains(upper, "C2200") ||
		strings.Contains(upper, "B2150") || strings.Contains(upper, "B2250")
}

func isVIPRIONHighEnd(upper string) bool {
	return strings.Contains(upper, "C4800") || strings.Contains(upper, "C4480") ||
		strings.Contains(upper, "B4450") || strings.Contains(upper, "B4460")
}

func isLegacyPlatform(upper string) bool {
	// Matches legacy 2000, 4000, 5000, 7000, 10000, 12000 platforms (e.g. 2000s, 2200, 4200v, 5250v, 10200v)
	legacyModelRe := regexp.MustCompile(`(^|[^A-Z0-9])(800|1600|3600|3900|6900|8900|11000|11050|2[02]00[SV]?|4[02]00[SV]?|5[02]00[SV]?|5250V?|7[02]00[SV]?|10[02]00[SV]?|10250V?|10350[VN]?|12[02]00[SV]?|12250V?)([^A-Z0-9]|$)`)
	if legacyModelRe.MatchString(upper) {
		return true
	}
	return false
}
