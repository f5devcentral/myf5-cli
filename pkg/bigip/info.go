package bigip

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// SystemInfo encapsulates appliance identification, hardware metadata, and licensing.
type SystemInfo struct {
	ChassisSerialNumber string            `json:"chassisSerialNumber,omitempty"`
	RegistrationKey     string            `json:"registrationKey,omitempty"`
	Platform            string            `json:"platform,omitempty"`
	Product             string            `json:"product,omitempty"`
	Version             string            `json:"version,omitempty"`
	Build               string            `json:"build,omitempty"`
	Edition             string            `json:"edition,omitempty"`
	Hostname            string            `json:"hostname,omitempty"`
	BaseMac             string            `json:"baseMac,omitempty"`
	ProvisionedModules  map[string]string `json:"provisionedModules,omitempty"`
	CaseAttributes      map[string]string `json:"caseAttributes,omitempty"`
	Source              string            `json:"source,omitempty"`
}

func isInternalModule(mod string) bool {
	m := strings.ToLower(strings.TrimSpace(mod))
	return m == "" || m == "mgmt" || m == "tmm" || m == "host" || m == "internal" || m == "none"
}

// PreferredSerialNumber returns the primary identifier required for support cases and entitlements.
// It prioritizes the Chassis Serial Number (e.g., hardware serial or VE UUID), falling back
// to the License Registration Key.
func (s *SystemInfo) PreferredSerialNumber() string {
	if s == nil {
		return ""
	}
	if s.ChassisSerialNumber != "" {
		return s.ChassisSerialNumber
	}
	return s.RegistrationKey
}

// PopulateCaseAttributes computes and stores standard attributes for F5 Support Case creation.
func (s *SystemInfo) PopulateCaseAttributes() {
	if s == nil {
		return
	}
	attrs := map[string]string{
		"productFamily": "BIG-IP",
		"serialNumber":  s.PreferredSerialNumber(),
		"version":       s.Version,
		"hostName":      s.Hostname,
	}

	product := "BIG-IP"
	if len(s.ProvisionedModules) > 0 {
		var active []string
		for mod, level := range s.ProvisionedModules {
			if level != "none" && level != "" && !isInternalModule(mod) {
				active = append(active, strings.ToUpper(mod))
			}
		}
		sort.Strings(active)
		if len(active) > 0 {
			product = "BIG-IP " + strings.Join(active, "/")
		}
	} else if s.Product != "" {
		product = s.Product
	}
	attrs["product"] = product

	if strings.Contains(s.Hostname, ".ec2.internal") || strings.Contains(s.Hostname, ".compute.internal") || s.Platform == "Z100" {
		attrs["cloudProvider"] = "AWS"
	} else if strings.Contains(s.Hostname, "azure") || strings.Contains(s.Hostname, "trafficmanager.net") {
		attrs["cloudProvider"] = "Azure"
	} else if strings.Contains(s.Hostname, "google") || strings.Contains(s.Hostname, "gce") {
		attrs["cloudProvider"] = "Google"
	}

	s.CaseAttributes = attrs
}

// Summary returns a human-readable multi-line representation of the appliance info.
func (s *SystemInfo) Summary() string {
	if s == nil {
		return "No system information available."
	}
	var b strings.Builder
	b.WriteString("Appliance Information:\n")
	if s.Product != "" {
		b.WriteString(fmt.Sprintf("  Product:            %s\n", s.Product))
	}
	if s.Version != "" {
		ver := s.Version
		if s.Build != "" {
			ver += fmt.Sprintf(" Build %s", s.Build)
		}
		if s.Edition != "" {
			ver += fmt.Sprintf(" (%s)", s.Edition)
		}
		b.WriteString(fmt.Sprintf("  Version:            %s\n", ver))
	}
	if s.Platform != "" {
		b.WriteString(fmt.Sprintf("  Platform:           %s\n", s.Platform))
	}
	if s.ChassisSerialNumber != "" {
		b.WriteString(fmt.Sprintf("  Chassis Serial:     %s\n", s.ChassisSerialNumber))
	}
	if s.RegistrationKey != "" {
		b.WriteString(fmt.Sprintf("  Registration Key:   %s\n", s.RegistrationKey))
	}
	if s.BaseMac != "" {
		b.WriteString(fmt.Sprintf("  Base MAC:           %s\n", s.BaseMac))
	}
	if s.Hostname != "" {
		b.WriteString(fmt.Sprintf("  Hostname:           %s\n", s.Hostname))
	}
	if len(s.ProvisionedModules) > 0 {
		var mods []string
		for k, v := range s.ProvisionedModules {
			if v != "" && v != "none" {
				mods = append(mods, fmt.Sprintf("%s (%s)", k, v))
			}
		}
		sort.Strings(mods)
		if len(mods) > 0 {
			b.WriteString(fmt.Sprintf("  Provisioned:        %s\n", strings.Join(mods, ", ")))
		}
	}
	if s.Source != "" {
		b.WriteString(fmt.Sprintf("  Source:             %s\n", s.Source))
	}
	if len(s.CaseAttributes) > 0 {
		b.WriteString("\nCase Creation Attributes (F5 Support):\n")
		var keys []string
		for k := range s.CaseAttributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("  %-18s: %s\n", k, s.CaseAttributes[k]))
		}
	}
	return b.String()
}

// InspectQKView inspects a local .qkview archive and extracts system, hardware, and licensing metadata.
func InspectQKView(filePath string) (*SystemInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open qkview file: %w", err)
	}
	defer f.Close()

	info, err := InspectQKViewReader(f)
	if err != nil {
		return nil, err
	}
	info.Source = fmt.Sprintf("qkview archive: %s", filePath)
	return info, nil
}

// InspectQKViewReader parses a gzip-compressed tar stream from a QKView archive.
func InspectQKViewReader(r io.Reader) (*SystemInfo, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	info := &SystemInfo{}

	chassisRe := regexp.MustCompile(`<bigip_chassis_serial_num>([^<]*)</bigip_chassis_serial_num>`)
	platformRe := regexp.MustCompile(`<platform>([^<]*)</platform>`)
	productRe := regexp.MustCompile(`<product>([^<]*)</product>`)
	mcpRegKeyRe := regexp.MustCompile(`<registration_key>([^<]*)</registration_key>`)
	licenseFileRegKeyRe := regexp.MustCompile(`(?i)Registration Key\s*:\s*([A-Z0-9-]+)`)
	licenseFileSerialRe := regexp.MustCompile(`(?i)Serial Number\s*:\s*([A-Za-z0-9-]+)`)
	hostnameRe := regexp.MustCompile(`(?m)^\s*hostname\s+([^\s;]+)`)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read error: %w", err)
		}

		name := strings.TrimPrefix(hdr.Name, "./")
		switch {
		case name == "VERSION.LTM" || name == "VERSION":
			scanner := bufio.NewScanner(tr)
			for scanner.Scan() {
				line := scanner.Text()
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					k := strings.TrimSpace(parts[0])
					v := strings.TrimSpace(parts[1])
					switch k {
					case "Product":
						if info.Product == "" {
							info.Product = v
						}
					case "Version":
						info.Version = v
					case "Build":
						info.Build = v
					case "Edition":
						info.Edition = v
					}
				}
			}

		case name == "PLATFORM":
			scanner := bufio.NewScanner(tr)
			for scanner.Scan() {
				line := scanner.Text()
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 && strings.TrimSpace(parts[0]) == "platform" && info.Platform == "" {
					info.Platform = strings.TrimSpace(parts[1])
				}
			}

		case name == "HWINFO":
			scanner := bufio.NewScanner(tr)
			for scanner.Scan() {
				line := scanner.Text()
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 && strings.TrimSpace(parts[0]) == "basemac" && info.BaseMac == "" {
					info.BaseMac = strings.TrimSpace(parts[1])
				}
			}

		case strings.HasSuffix(name, "config/bigip.license"):
			scanner := bufio.NewScanner(tr)
			for scanner.Scan() {
				line := scanner.Text()
				if m := licenseFileRegKeyRe.FindStringSubmatch(line); len(m) > 1 && info.RegistrationKey == "" {
					info.RegistrationKey = strings.TrimSpace(m[1])
				}
				if m := licenseFileSerialRe.FindStringSubmatch(line); len(m) > 1 && info.ChassisSerialNumber == "" {
					info.ChassisSerialNumber = strings.TrimSpace(m[1])
				}
			}

		case strings.HasSuffix(name, "config/bigip_base.conf"):
			scanner := bufio.NewScanner(tr)
			for scanner.Scan() {
				line := scanner.Text()
				if m := hostnameRe.FindStringSubmatch(line); len(m) > 1 && info.Hostname == "" {
					info.Hostname = strings.TrimSpace(m[1])
				}
			}

		case strings.HasSuffix(name, "var/prompt/hostname"):
			scanner := bufio.NewScanner(tr)
			if scanner.Scan() {
				val := strings.TrimSpace(scanner.Text())
				if val != "" && info.Hostname == "" {
					info.Hostname = val
				}
			}

		case strings.HasSuffix(name, "bigip_sys.conf"):
			scanner := bufio.NewScanner(tr)
			curMod := ""
			provisionRe := regexp.MustCompile(`(?i)(?:sys\s+)?provision\s+([a-zA-Z0-9_-]+)\s*\{`)
			levelRe := regexp.MustCompile(`(?i)level\s+([a-zA-Z0-9_-]+)`)
			for scanner.Scan() {
				line := scanner.Text()
				if curMod != "" {
					if m := levelRe.FindStringSubmatch(line); len(m) > 1 {
						if !isInternalModule(curMod) {
							if info.ProvisionedModules == nil {
								info.ProvisionedModules = make(map[string]string)
							}
							info.ProvisionedModules[curMod] = strings.ToLower(m[1])
						}
					}
					if strings.Contains(line, "}") {
						curMod = ""
					}
				} else if m := provisionRe.FindStringSubmatch(line); len(m) > 1 {
					curMod = strings.ToLower(m[1])
				}
			}

		case name == "mcp_module.xml":
			scanner := bufio.NewScanner(tr)
			inSysInfo := false
			isHostnameVar := false
			curMod := ""
			inUse := false
			for scanner.Scan() {
				line := scanner.Text()
				if strings.Contains(line, "<system_information>") {
					inSysInfo = true
					continue
				}
				if strings.Contains(line, "</system_information>") {
					inSysInfo = false
					continue
				}
				if inSysInfo {
					if m := chassisRe.FindStringSubmatch(line); len(m) > 1 && m[1] != "" {
						info.ChassisSerialNumber = strings.TrimSpace(m[1])
					}
					if m := platformRe.FindStringSubmatch(line); len(m) > 1 && m[1] != "" && info.Platform == "" {
						info.Platform = strings.TrimSpace(m[1])
					}
					if m := productRe.FindStringSubmatch(line); len(m) > 1 && m[1] != "" {
						info.Product = strings.TrimSpace(m[1])
					}
				}
				if m := mcpRegKeyRe.FindStringSubmatch(line); len(m) > 1 && m[1] != "" && info.RegistrationKey == "" {
					info.RegistrationKey = strings.TrimSpace(m[1])
				}

				// MCP Hostname DB variable
				if strings.Contains(line, "<name>hostname</name>") {
					isHostnameVar = true
					continue
				}
				if isHostnameVar {
					if strings.Contains(line, "</db_variable>") {
						isHostnameVar = false
					} else if m := regexp.MustCompile(`<value>([^<]+)</value>`).FindStringSubmatch(line); len(m) > 1 {
						val := strings.TrimSpace(m[1])
						if val != "" && (info.Hostname == "" || !strings.Contains(info.Hostname, ".")) {
							info.Hostname = val
						}
						isHostnameVar = false
					}
				}

				// MCP Provision module items
				if m := regexp.MustCompile(`<provision_name>([^<]+)</provision_name>`).FindStringSubmatch(line); len(m) > 1 {
					curMod = strings.ToLower(strings.TrimSpace(m[1]))
					inUse = false
				}
				if curMod != "" {
					if strings.Contains(line, "<in_use>1</in_use>") {
						inUse = true
					}
					if m := regexp.MustCompile(`<provision_level>([0-9]+)</provision_level>`).FindStringSubmatch(line); len(m) > 1 {
						levelNum := m[1]
						levelStr := "none"
						switch levelNum {
						case "2":
							levelStr = "minimum"
						case "3":
							levelStr = "nominal"
						case "4":
							levelStr = "dedicated"
						}
						if !isInternalModule(curMod) && (inUse || levelNum != "1") {
							if info.ProvisionedModules == nil {
								info.ProvisionedModules = make(map[string]string)
							}
							if _, exists := info.ProvisionedModules[curMod]; !exists {
								info.ProvisionedModules[curMod] = levelStr
							}
						}
						curMod = ""
						inUse = false
					}
				}
			}
		}
	}

	info.PopulateCaseAttributes()
	return info, nil
}

// GetSystemInfo queries the live BIG-IP appliance via iControl REST endpoints
// (/mgmt/tm/sys/hardware, /mgmt/tm/sys/version, /mgmt/tm/sys/license, /mgmt/tm/sys/global-settings).
func (c *Client) GetSystemInfo(ctx context.Context) (*SystemInfo, error) {
	info := &SystemInfo{
		Source: fmt.Sprintf("appliance: %s (iControl REST)", c.Host),
	}

	// 1. Hardware & Serial
	hwURL := fmt.Sprintf("%s/mgmt/tm/sys/hardware", c.BaseURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hwURL, nil)
	if err == nil {
		c.setAuth(req)
		resp, err := c.HTTPClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
				parseHardwareJSON(body, info)
			}
		}
	}

	// 2. Version & Build
	verURL := fmt.Sprintf("%s/mgmt/tm/sys/version", c.BaseURL())
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, verURL, nil)
	if err == nil {
		c.setAuth(req)
		resp, err := c.HTTPClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
				parseVersionJSON(body, info)
			}
		}
	}

	// 3. License
	licURL := fmt.Sprintf("%s/mgmt/tm/sys/license", c.BaseURL())
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, licURL, nil)
	if err == nil {
		c.setAuth(req)
		resp, err := c.HTTPClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
				parseLicenseJSON(body, info)
			}
		}
	}

	// 4. Hostname
	globalURL := fmt.Sprintf("%s/mgmt/tm/sys/global-settings", c.BaseURL())
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, globalURL, nil)
	if err == nil {
		c.setAuth(req)
		resp, err := c.HTTPClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
				var gs struct {
					Hostname string `json:"hostname"`
				}
				if json.Unmarshal(body, &gs) == nil && gs.Hostname != "" {
					info.Hostname = gs.Hostname
				}
			}
		}
	}

	// 5. Provisioned Modules
	provURL := fmt.Sprintf("%s/mgmt/tm/sys/provision", c.BaseURL())
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, provURL, nil)
	if err == nil {
		c.setAuth(req)
		resp, err := c.HTTPClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
				parseProvisionJSON(body, info)
			}
		}
	}

	if info.ChassisSerialNumber == "" && info.RegistrationKey == "" && info.Version == "" {
		return nil, fmt.Errorf("failed to retrieve system information from %s via iControl REST", c.Host)
	}

	info.PopulateCaseAttributes()
	return info, nil
}

// GetSystemInfoSSH retrieves hardware and system information via SSH executing tmsh commands.
func GetSystemInfoSSH(ctx context.Context, opts SCPOptions) (*SystemInfo, error) {
	clientConfig, err := BuildSSHClientConfig(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to configure SSH client: %w", err)
	}

	host, port := CleanSSHHost(opts.Host)
	if opts.Port > 0 {
		port = opts.Port
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultSCPTimeout
	}

	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SSH connection to %s failed: %w", addr, err)
	}
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH handshake failed: %w", err)
	}
	defer sshConn.Close()

	sshClient := ssh.NewClient(sshConn, chans, reqs)
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	cmd := `tmsh -q -c "show sys hardware; show sys version; show sys license; list sys global-settings hostname; list sys provision"`
	if err := session.Run(cmd); err != nil {
		return nil, fmt.Errorf("tmsh commands failed over SSH: %w (stderr: %s)", err, stderr.String())
	}

	info := parseTmshOutput(stdout.String())
	info.Source = fmt.Sprintf("appliance: %s (SSH)", host)
	info.PopulateCaseAttributes()
	return info, nil
}

func parseHardwareJSON(body []byte, info *SystemInfo) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return
	}

	findNestedString(raw, func(key, val string) {
		k := strings.ToLower(key)
		switch {
		case (k == "bigipchassisserialnum" || k == "chassisserialnumber" || k == "systemserialnumber") && info.ChassisSerialNumber == "":
			info.ChassisSerialNumber = val
		case k == "platform" && info.Platform == "":
			info.Platform = val
		case (k == "product" || k == "appliancetype") && info.Product == "":
			info.Product = val
		case (k == "basemac" || k == "macaddress") && info.BaseMac == "":
			info.BaseMac = val
		}
	})
}

func parseVersionJSON(body []byte, info *SystemInfo) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return
	}

	findNestedString(raw, func(key, val string) {
		k := strings.ToLower(key)
		switch k {
		case "version":
			if info.Version == "" {
				info.Version = val
			}
		case "build":
			if info.Build == "" {
				info.Build = val
			}
		case "edition":
			if info.Edition == "" {
				info.Edition = val
			}
		case "product":
			if info.Product == "" {
				info.Product = val
			}
		}
	})
}

func parseLicenseJSON(body []byte, info *SystemInfo) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return
	}

	findNestedString(raw, func(key, val string) {
		k := strings.ToLower(key)
		if (k == "registrationkey" || k == "baseregkey") && info.RegistrationKey == "" {
			info.RegistrationKey = val
		}
	})
}

func parseProvisionJSON(body []byte, info *SystemInfo) {
	var resp struct {
		Items []struct {
			Name  string `json:"name"`
			Level string `json:"level"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err == nil && len(resp.Items) > 0 {
		for _, item := range resp.Items {
			lvl := strings.ToLower(strings.TrimSpace(item.Level))
			name := strings.ToLower(strings.TrimSpace(item.Name))
			if name != "" && lvl != "" && lvl != "none" && !isInternalModule(name) {
				if info.ProvisionedModules == nil {
					info.ProvisionedModules = make(map[string]string)
				}
				info.ProvisionedModules[name] = lvl
			}
		}
	}
}

func findNestedString(node any, cb func(key, val string)) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if str, ok := child.(string); ok && str != "" {
				cb(k, str)
			} else if childMap, ok := child.(map[string]any); ok {
				// Handle TMOS description / value nested stat structure: {"description": "..."}
				if desc, ok := childMap["description"].(string); ok && desc != "" {
					cb(k, desc)
				}
				if val, ok := childMap["value"].(string); ok && val != "" {
					cb(k, val)
				}
				findNestedString(childMap, cb)
			} else {
				findNestedString(child, cb)
			}
		}
	case []any:
		for _, item := range v {
			findNestedString(item, cb)
		}
	}
}

func parseTmshOutput(output string) *SystemInfo {
	info := &SystemInfo{}
	scanner := bufio.NewScanner(strings.NewReader(output))

	chassisRe := regexp.MustCompile(`(?i)(?:Chassis Serial|Serial Number)\s+([^\s]+)`)
	regKeyRe := regexp.MustCompile(`(?i)Registration Key\s+([A-Z0-9-]+)`)
	platformRe := regexp.MustCompile(`(?i)(?:Platform\s+Name|Platform)\s+([^\s]+)`)
	macRe := regexp.MustCompile(`(?i)Base MAC\s+([0-9a-fA-F:]+)`)
	versionRe := regexp.MustCompile(`(?i)Version\s+([0-9.]+)`)
	buildRe := regexp.MustCompile(`(?i)Build\s+([0-9.]+)`)
	editionRe := regexp.MustCompile(`(?i)Edition\s+(.+)`)
	productRe := regexp.MustCompile(`(?i)Product\s+(.+)`)
	hostnameRe := regexp.MustCompile(`(?i)hostname\s+([^\s;]+)`)
	provisionRe := regexp.MustCompile(`(?i)(?:sys\s+)?provision\s+([a-zA-Z0-9_-]+)\s*\{`)
	levelRe := regexp.MustCompile(`(?i)level\s+([a-zA-Z0-9_-]+)`)

	curMod := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if curMod != "" {
			if m := levelRe.FindStringSubmatch(line); len(m) > 1 {
				lvl := strings.ToLower(m[1])
				if !isInternalModule(curMod) && lvl != "none" {
					if info.ProvisionedModules == nil {
						info.ProvisionedModules = make(map[string]string)
					}
					info.ProvisionedModules[curMod] = lvl
				}
			}
			if strings.Contains(line, "}") {
				curMod = ""
			}
			continue
		} else if m := provisionRe.FindStringSubmatch(line); len(m) > 1 {
			curMod = strings.ToLower(m[1])
			continue
		}

		if m := chassisRe.FindStringSubmatch(line); len(m) > 1 && info.ChassisSerialNumber == "" {
			info.ChassisSerialNumber = m[1]
		}
		if m := regKeyRe.FindStringSubmatch(line); len(m) > 1 && info.RegistrationKey == "" {
			info.RegistrationKey = m[1]
		}
		if m := macRe.FindStringSubmatch(line); len(m) > 1 && info.BaseMac == "" {
			info.BaseMac = m[1]
		}
		if m := platformRe.FindStringSubmatch(line); len(m) > 1 && info.Platform == "" {
			val := strings.TrimSpace(m[1])
			if !strings.EqualFold(val, "information") && !strings.EqualFold(val, "status") {
				info.Platform = val
			}
		}
		if m := versionRe.FindStringSubmatch(line); len(m) > 1 && info.Version == "" {
			info.Version = m[1]
		}
		if m := buildRe.FindStringSubmatch(line); len(m) > 1 && info.Build == "" {
			info.Build = m[1]
		}
		if m := editionRe.FindStringSubmatch(line); len(m) > 1 && info.Edition == "" {
			info.Edition = m[1]
		}
		if m := productRe.FindStringSubmatch(line); len(m) > 1 && info.Product == "" {
			info.Product = m[1]
		}
		if m := hostnameRe.FindStringSubmatch(line); len(m) > 1 && info.Hostname == "" {
			info.Hostname = m[1]
		}
	}

	return info
}
