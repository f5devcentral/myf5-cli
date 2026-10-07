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

// Policy reference URLs
const (
	URLPolicyTMOS     = "https://my.f5.com/manage/s/article/K5903"
	URLPolicyHardware = "https://my.f5.com/manage/s/article/K4309"
)

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

// EvaluatePlatformLifecycle checks a hardware platform name against F5 Hardware Product Lifecycle Support Policy K4309.
func EvaluatePlatformLifecycle(platform string, asOf time.Time) HardwareLifecycle {
	cleanPlat := strings.TrimSpace(platform)
	upper := strings.ToUpper(cleanPlat)

	res := HardwareLifecycle{
		Platform:  cleanPlat,
		NoticeURL: URLPolicyHardware,
		Status:    StatusUnknown,
	}

	if cleanPlat == "" {
		res.Status = StatusUnknown
		return res
	}

	// 1. Virtual Edition (Cloud, Hypervisors)
	if isVirtualPlatform(upper) {
		res.Family = "Virtual Edition"
		res.IsVirtual = true
		res.Status = StatusSupported
		res.Recommendation = "Virtual appliance; lifecycle is governed by the TMOS software version (K5903)."
		return res
	}

	// 2. Modern Platforms: rSeries & VELOS
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

	// 3. iSeries Midrange: i2600, i2800, i4600, i4800, i5600, i5800, i7600, i7800, i10600, i10800, i11400, i11600, i11800
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

	// 4. iSeries High-End: i15600, i15800
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

	// 5. VIPRION Midrange: C2400, C2200, B2150, B2250
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

	// 6. VIPRION High-End: C4800, C4480, B4450, B4460
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

	// 7. VIPRION Legacy Blades: B2100, B4300
	if strings.Contains(upper, "B2100") || strings.Contains(upper, "B4300") {
		res.Family = "VIPRION (Legacy)"
		res.Status = StatusEOL
		res.IsEOL = true
		res.Warning = fmt.Sprintf("VIPRION blade %s reached End of Life (past End of Technical Support, K4309).", cleanPlat)
		res.Recommendation = "Replace blade with supported hardware."
		return res
	}

	// 8. Legacy Hardware (2000s, 4000s, 5000s, 7000s, 10000s, 12000s, C109, etc.)
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
	// Hardware motherboard codes: D110 (i2800), D111 (i4800), D112 (i5800), D113 (i7800), etc.
	codes := []string{"D110", "D111", "D112", "D113", "D114", "D116", "D118"}
	for _, code := range codes {
		if strings.Contains(upper, code) {
			return true
		}
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
	legacyModelRe := regexp.MustCompile(`(^|[^A-Z0-9])(2[02]00[SV]?|4[02]00[SV]?|5[02]00[SV]?|5250V?|7[02]00[SV]?|10[02]00[SV]?|10250V?|12[02]00[SV]?|12250V?)([^A-Z0-9]|$)`)
	if legacyModelRe.MatchString(upper) {
		return true
	}
	// Legacy hardware codes (C109 for 2000s, C112 for 4000, C113 for 4200v, A109 for 10200v, etc.)
	legacyCodes := []string{"C109", "C112", "C113", "C114", "C115", "A109", "A112", "A113", "D106"}
	for _, code := range legacyCodes {
		if strings.Contains(upper, code) {
			return true
		}
	}
	return false
}
