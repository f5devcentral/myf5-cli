package auth

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ForbiddenSecretFlags lists CLI flags that cannot be passed to prevent secrets
// from being leaked into process tables (ps aux), shell history, or system logs.
var ForbiddenSecretFlags = []string{
	"--client-secret",
	"-client-secret",
	"--clientsecret",
	"-clientsecret",
	"--password",
	"-password",
	"--pw",
	"-pw",
	"--secret",
	"-secret",
	"--token",
	"-token",
	"--access-token",
	"-access-token",
	"--api-key",
	"-api-key",
	"--apikey",
	"-apikey",
	"--auth-token",
	"-auth-token",
	"--key",
	"-key",
}

// CheckNoCLISecrets validates that no forbidden secret arguments exist in argv.
func CheckNoCLISecrets(args []string) error {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		for _, forbidden := range ForbiddenSecretFlags {
			if lower == forbidden || strings.HasPrefix(lower, forbidden+"=") {
				return fmt.Errorf(
					"passing secrets via CLI argument '%s' is strictly forbidden\n"+
						"CLI arguments expose secrets in process listings (ps aux), system logs, and shell history.\n"+
						"Please store API credentials in environment variables (F5_CLIENT_ID, F5_CLIENT_SECRET) or ~/.f5api_credentials.\n"+
						"For BIG-IP appliances, use BIGIP_PASSWORD environment variable or interactive masked prompt",
					arg,
				)
			}
		}
	}
	return nil
}

// Credentials holds resolved API credentials.
type Credentials struct {
	ClientID     string
	ClientSecret string
}

// String implements fmt.Stringer to ensure secrets are never leaked into logs or stdout.
func (c Credentials) String() string {
	if c.ClientSecret != "" {
		return fmt.Sprintf("Credentials{ClientID: %q, ClientSecret: [REDACTED]}", c.ClientID)
	}
	return fmt.Sprintf("Credentials{ClientID: %q}", c.ClientID)
}

// GoString implements fmt.GoStringer.
func (c Credentials) GoString() string {
	return c.String()
}

// ResolveCredentials resolves API credentials following the Zero-Secret policy:
// 1. Environment variables (F5_CLIENT_ID, F5_CLIENT_SECRET, IHEALTH_*)
// 2. ~/.f5api_credentials (primary configuration file)
// 3. ~/.ihealth_credentials (legacy fallback file)
func ResolveCredentials(profile string) (*Credentials, error) {
	if profile == "" {
		profile = "default"
	}

	// 1. Check environment variables
	clientID := os.Getenv("F5_CLIENT_ID")
	if clientID == "" {
		clientID = os.Getenv("IHEALTH_CLIENT_ID")
	}

	clientSecret := os.Getenv("F5_CLIENT_SECRET")
	if clientSecret == "" {
		clientSecret = os.Getenv("IHEALTH_CLIENT_SECRET")
	}

	if clientID != "" && clientSecret != "" {
		return &Credentials{
			ClientID:     clientID,
			ClientSecret: clientSecret,
		}, nil
	}

	// 2. Check credentials files
	homeDir, err := os.UserHomeDir()
	if err == nil {
		paths := []string{
			filepath.Join(homeDir, ".f5api_credentials"),
			filepath.Join(homeDir, ".ihealth_credentials"),
		}

		for _, path := range paths {
			if creds, err := readCredentialsFile(path, profile); err == nil && creds != nil {
				return creds, nil
			}
		}
	}

	return nil, errors.New(
		"F5 API credentials not found.\n" +
			"Credentials must be stored in environment variables (F5_CLIENT_ID, F5_CLIENT_SECRET) " +
			"or ~/.f5api_credentials (mode 0600).\n" +
			"CLI arguments for secrets are strictly prohibited.",
	)
}

// readCredentialsFile parses KEY=VALUE or INI format credentials files.
func readCredentialsFile(filePath, profile string) (*Credentials, error) {
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filePath)
		if err == nil {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("credential file %s is not a regular file", filePath)
			}
			perm := info.Mode().Perm()
			if perm&0077 != 0 {
				return nil, fmt.Errorf(
					"insecure permissions on credential file %s (mode %04o): must be 0600 (owner read/write only).\n"+
						"To secure your credentials, run: chmod 600 %s",
					filePath, perm, filePath,
				)
			}
		}
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	currentSection := ""
	kvStore := make(map[string]string)
	profileStore := make(map[string]map[string]string)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// INI section header [profile]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if profileStore[currentSection] == nil {
				profileStore[currentSection] = make(map[string]string)
			}
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)

		if currentSection == "" {
			kvStore[key] = val
		} else {
			profileStore[currentSection][key] = val
		}
	}

	lookupKey := func(m map[string]string, keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok && v != "" {
				return v
			}
		}
		return ""
	}

	targetProfile := strings.ToLower(profile)
	if section, exists := profileStore[targetProfile]; exists {
		cid := lookupKey(section, "client_id", "clientid", "id")
		csec := lookupKey(section, "client_secret", "clientsecret", "secret")
		if cid != "" && csec != "" {
			return &Credentials{ClientID: cid, ClientSecret: csec}, nil
		}
	}

	// Fallback to top-level key=value format (e.g. standard ~/.f5api_credentials)
	cid := lookupKey(kvStore, "client_id", "clientid", "f5_client_id")
	csec := lookupKey(kvStore, "client_secret", "clientsecret", "f5_client_secret")
	if cid != "" && csec != "" {
		return &Credentials{ClientID: cid, ClientSecret: csec}, nil
	}

	return nil, fmt.Errorf("no credentials found in %s for profile %s", filePath, profile)
}
