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
