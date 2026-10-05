package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"myf5-cli/pkg/client"
)

const (
	DefaultAuthFQDN  = "idp.identity.f5.com"
	DefaultAppID     = "aus19gt5bu0jGw9Fi358"
	OktaAuthServerID = "ausp95yxb4ShxMGvW357" // Authoritative Okta CIAM Authorization Server ID for identity.account.f5.com
	DefaultAudience  = "api://ciam-api-myf5"
	DefaultScope     = "myf5_scope"

	// IHealthAudience is the resource server for the iHealth APIs, per F5
	// article K000130498. A client is only authorized for audiences it holds a
	// grant on, so requesting the MyF5 audience with iHealth credentials fails
	// with 403 "... is not authorized to access resource server ...".
	IHealthAudience = "api://ciam-api-ihealth"

	// IHealthScope is empty deliberately. K000130498's token request sends
	// only client_id, client_secret, audience and grant_type — no scope. The
	// iHealth client is not granted "ihealth" as a scope; access is conveyed
	// by the audience.
	IHealthScope = ""
)

// AudienceForScope maps a requested scope to the OAuth audience that issues
// tokens for it. Unknown scopes fall back to the MyF5 audience.
func AudienceForScope(scope string) string {
	if scope == "ihealth" || scope == IHealthAudience {
		return IHealthAudience
	}
	return DefaultAudience
}

// TokenResponse represents the OAuth2 token endpoint JSON response.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// Authenticate obtains an OAuth2 Bearer token from F5 Identity Services.
func Authenticate(ctx context.Context, authFQDN, appID, clientID, clientSecret, scope string) (string, error) {
	return AuthenticateWithClient(ctx, nil, authFQDN, appID, clientID, clientSecret, scope)
}

// AuthenticateWithClient allows passing a custom HTTP client (useful for tests or custom proxy).
func AuthenticateWithClient(ctx context.Context, httpClient *http.Client, authFQDN, appID, clientID, clientSecret, scope string) (string, error) {
	if authFQDN == "" {
		authFQDN = DefaultAuthFQDN
	}
	if appID == "" {
		appID = DefaultAppID
	}
	if scope == "" {
		scope = DefaultScope
	}

	scheme := "https"
	if strings.HasPrefix(authFQDN, "http://") {
		scheme = "http"
		authFQDN = strings.TrimPrefix(authFQDN, "http://")
	} else if strings.HasPrefix(authFQDN, "https://") {
		authFQDN = strings.TrimPrefix(authFQDN, "https://")
	}

	var tokenURL string
	if strings.Contains(authFQDN, "account.f5.com") {
		// Route to Okta CIAM authorization server endpoint on identity.account.f5.com.
		// If caller provided generic default appID, use the authoritative Okta authorization server ID ausp95yxb4ShxMGvW357.
		authServerID := appID
		if authServerID == "" || authServerID == DefaultAppID {
			authServerID = OktaAuthServerID
		}
		tokenURL = fmt.Sprintf("%s://%s/oauth2/%s/v1/token", scheme, authFQDN, authServerID)
	} else {
		// Route to modern Auth0 endpoint on idp.identity.f5.com
		tokenURL = fmt.Sprintf("%s://%s/oauth/token", scheme, authFQDN)
	}

	// The audience names the resource server the token is for, and a client is
	// only authorized for audiences it holds a grant on. Requesting the MyF5
	// audience with an iHealth client fails with
	//   403 "Client ... is not authorized to access resource server
	//        api://ciam-api-myf5. You need to create a client-grant ..."
	// so the audience is derived from the requested scope. F5_OAUTH_AUDIENCE
	// overrides it if F5 introduces another resource server.
	audience := os.Getenv("F5_OAUTH_AUDIENCE")
	if audience == "" {
		audience = AudienceForScope(scope)
	}

	// iHealth tokens are requested WITHOUT a scope parameter (K000130498);
	// access is conveyed by the audience alone. Sending scope=ihealth yields
	// 403 "Client has not been granted scopes: ihealth".
	if audience == IHealthAudience {
		scope = IHealthScope
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("audience", audience)
	if scope != "" {
		data.Set("scope", scope)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create auth request: %w", err)
	}

	// Basic Auth credentials
	rawCreds := []byte(clientID + ":" + clientSecret)
	authHeader := base64.StdEncoding.EncodeToString(rawCreds)
	// Zero plaintext credential byte buffer immediately after base64 encoding
	ZeroBytes(rawCreds)

	req.Header.Set("Authorization", "Basic "+authHeader)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "myf5-cli-go/1.0.0")

	if httpClient == nil {
		httpClient = client.NewSecureHTTPClient(15 * time.Second)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("authentication request failed: %w", err)
	}
	defer resp.Body.Close()

	// Enforce 1 MB bound on auth response to prevent memory exhaustion
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read auth response body: %w", err)
	}
	defer ZeroBytes(bodyBytes)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("authentication failed with HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse auth response json: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("token endpoint returned empty access_token: %s", string(bodyBytes))
	}

	return tokenResp.AccessToken, nil
}
