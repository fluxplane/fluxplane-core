package codex

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAuthUsesExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"test-token"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	auth, err := loadAuth(path, nil)
	if err != nil {
		t.Fatalf("loadAuth: %v", err)
	}
	token, err := auth.token(t.Context())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if token != "test-token" {
		t.Fatalf("token = %q, want auth file token", token)
	}
}

func TestRefreshPreservesUnknownAuthFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	original := `{
		"auth_mode":"chatgpt",
		"tokens":{
			"access_token":"old-access",
			"refresh_token":"old-refresh",
			"id_token":"keep-id-token",
			"account_id":"account-1"
		},
		"last_refresh":"2024-01-02T03:04:05Z",
		"custom_top_level":{"keep":true}
	}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	auth, err := loadAuth(path, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != tokenEndpoint {
			t.Fatalf("refresh URL = %q, want %q", req.URL.String(), tokenEndpoint)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"new-access","expires_in":3600}`)),
		}, nil
	})})
	if err != nil {
		t.Fatalf("loadAuth: %v", err)
	}

	gotToken, err := auth.token(t.Context())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if gotToken != "new-access" {
		t.Fatalf("token = %q, want refreshed token", gotToken)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var saved struct {
		Tokens map[string]any  `json:"tokens"`
		Custom map[string]bool `json:"custom_top_level"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Unmarshal saved auth: %v\n%s", err, data)
	}
	if saved.Tokens["access_token"] != "new-access" {
		t.Fatalf("access_token = %v, want new-access", saved.Tokens["access_token"])
	}
	if saved.Tokens["refresh_token"] != "old-refresh" {
		t.Fatalf("refresh_token = %v, want old-refresh", saved.Tokens["refresh_token"])
	}
	if saved.Tokens["id_token"] != "keep-id-token" {
		t.Fatalf("id_token = %v, want preserved keep-id-token\nsaved: %s", saved.Tokens["id_token"], data)
	}
	if !saved.Custom["keep"] {
		t.Fatalf("custom_top_level not preserved in saved auth: %s", data)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
