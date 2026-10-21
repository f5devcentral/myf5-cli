package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("unexpected content type: %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") == "" {
			t.Errorf("missing Authorization header")
		}

		resp := TokenResponse{
			AccessToken: "test-bearer-token-12345",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			Scope:       "myf5_scope",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	token, err := AuthenticateWithClient(context.Background(), server.Client(), server.URL, "app123", "client_id", "client_secret", "myf5_scope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "test-bearer-token-12345" {
		t.Errorf("got token %s, want test-bearer-token-12345", token)
	}
}

// TestAuthenticateAudienceSelection verifies that the token request form payload
// transmits the expected audience and scope for both MyF5 and iHealth clients.
// Per F5 article K000130498, an iHealth token request carries
// audience=api://ciam-api-ihealth and omits scope.
func TestAuthenticateAudienceSelection(t *testing.T) {
	cases := []struct {
		name         string
		scope        string
		wantAudience string
		wantScope    string
	}{
		{"myf5 scope keeps myf5 audience", "myf5_scope", DefaultAudience, "myf5_scope"},
		{"ihealth scope switches audience and drops scope", "ihealth", IHealthAudience, ""},
		{"empty scope defaults to myf5", "", DefaultAudience, DefaultScope},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotAudience, gotScope string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Fatalf("parse form: %v", err)
				}
				gotAudience = r.PostForm.Get("audience")
				gotScope = r.PostForm.Get("scope")
				json.NewEncoder(w).Encode(TokenResponse{AccessToken: "tok"})
			}))
			defer server.Close()

			if _, err := AuthenticateWithClient(context.Background(), server.Client(), server.URL, "", "cid", "csec", tc.scope); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotAudience != tc.wantAudience {
				t.Errorf("audience = %q, want %q", gotAudience, tc.wantAudience)
			}
			if gotScope != tc.wantScope {
				t.Errorf("scope = %q, want %q", gotScope, tc.wantScope)
			}
		})
	}
}

func TestAuthenticateWithSecretBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Basic Y2xpZW50X2lkOnNlY3JldF9ieXRlcw==" { // base64 of "client_id:secret_bytes"
			t.Errorf("unexpected Authorization header: %s", authHeader)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TokenResponse{
			AccessToken: "bytes-token-xyz",
			TokenType:   "Bearer",
		})
	}))
	defer server.Close()

	sec := []byte("secret_bytes")
	tok, err := AuthenticateWithSecretBytes(context.Background(), server.Client(), server.URL, "app", "client_id", sec, "myf5_scope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "bytes-token-xyz" {
		t.Errorf("got token %s, want bytes-token-xyz", tok)
	}
}
