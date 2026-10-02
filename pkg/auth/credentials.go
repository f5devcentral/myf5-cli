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

// ZeroBytes securely overwrites a byte slice with zeros to clear secrets from RAM.
// runtime.KeepAlive ensures the compiler does not optimize away the loop as a dead store.
func ZeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

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

	// APIKey is the "API usage key" sent as the required `k` query parameter
	// on support.apis.f5.com endpoints. It is a SECOND credential, distinct
	// from the OAuth bearer token built from ClientID/ClientSecret: the F5
	// OpenAPI spec marks `k` required on every Support Case operation and
	// defines bearerAuth separately. A bad token yields 401; a `k` that is not
	// associated with the caller's API entitlement yields 403 with
	// "The API Key to API association not exist."
	//
	// It is issued per entitlement from the MyF5 API Catalog, so it cannot be
	// a single shared constant. Empty means "fall back to the package default".
	APIKey string
}

// String implements fmt.Stringer to ensure secrets are never leaked into logs or stdout.
func (c Credentials) String() string {
	// APIKey is a credential in its own right and must be redacted alongside
	// ClientSecret, never printed even in part.
	apiKey := "[EMPTY]"
	if c.APIKey != "" {
		apiKey = "[REDACTED]"
	}
	if c.ClientSecret != "" {
		return fmt.Sprintf("Credentials{ClientID: %q, ClientSecret: [REDACTED], APIKey: %s}", c.ClientID, apiKey)
	}
	return fmt.Sprintf("Credentials{ClientID: %q, APIKey: %s}", c.ClientID, apiKey)
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

	// The API usage key resolves independently of the OAuth pair: a user may
	// supply it by environment variable while keeping client credentials in a
	// file, or vice versa. It is therefore read here and also looked up in the
	// credentials file below, with the environment taking precedence.
	apiKey := os.Getenv("F5_API_KEY")

	if clientID != "" && clientSecret != "" {
		return &Credentials{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			APIKey:       apiKey,
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
				// Environment wins over the file, matching the precedence used
				// for the OAuth pair above.
				if apiKey != "" {
					creds.APIKey = apiKey
				}
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
			// World-writable files are dangerous and must be rejected
			if perm&0002 != 0 {
				return nil, fmt.Errorf(
					"insecure permissions on credential file %s (mode %04o): world-writable files are prohibited.\n"+
						"To secure your credentials, run: chmod 600 %s",
					filePath, perm, filePath,
				)
			}
			if perm&0004 != 0 {
				fmt.Fprintf(os.Stderr, "Warning: credential file %s is world-readable (mode %04o). Recommended: chmod 600 %s (or chmod 640 %s)\n", filePath, perm, filePath, filePath)
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
			// A profile may carry its own api_key; fall back to any top-level
			// one so a single key can serve every profile.
			ak := lookupKey(section, "api_key", "apikey", "k")
			if ak == "" {
				ak = lookupKey(kvStore, "api_key", "apikey", "k")
			}
			return &Credentials{ClientID: cid, ClientSecret: csec, APIKey: ak}, nil
		}
	}

	// Fallback to top-level key=value format (e.g. standard ~/.f5api_credentials)
	cid := lookupKey(kvStore, "client_id", "clientid", "f5_client_id")
	csec := lookupKey(kvStore, "client_secret", "clientsecret", "f5_client_secret")
	if cid != "" && csec != "" {
		ak := lookupKey(kvStore, "api_key", "apikey", "k", "f5_api_key")
		return &Credentials{ClientID: cid, ClientSecret: csec, APIKey: ak}, nil
	}

	return nil, fmt.Errorf("no credentials found in %s for profile %s", filePath, profile)
}
