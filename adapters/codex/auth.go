// Package codex adapts the Codex Responses backend to the agentsdk LLM model
// port. It deliberately uses the local Codex OAuth file.
package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/agentruntime/runtime/httptransport"
)

const (
	DefaultBaseURL = "https://chatgpt.com/backend-api/codex"
	AuthFilePath   = ".codex/auth.json"
	EnvAuthPath    = "CODEX_AUTH_PATH"

	tokenEndpoint     = "https://auth.openai.com/oauth/token"
	clientID          = "app_EMoamEEZ73f0CkXaXp7hrann"
	tokenExpiryBuffer = 5 * time.Minute
	authModeChatGPT   = "chatgpt"
)

type authFile struct {
	AuthMode string `json:"auth_mode"`
	Tokens   struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh time.Time `json:"last_refresh"`
}

type auth struct {
	mu     sync.Mutex
	file   authFile
	path   string
	expiry time.Time
	client *http.Client
}

func loadAuth(path string, client *http.Client) (*auth, error) {
	if path == "" {
		path = os.Getenv(EnvAuthPath)
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("codex: get home dir: %w", err)
		}
		path = filepath.Join(home, AuthFilePath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("codex: read %s: %w", path, err)
	}
	var file authFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("codex: parse auth file: %w", err)
	}
	if file.AuthMode != "" && file.AuthMode != authModeChatGPT {
		return nil, fmt.Errorf("codex: unsupported auth mode %q", file.AuthMode)
	}
	if file.Tokens.AccessToken == "" && file.Tokens.RefreshToken == "" {
		return nil, fmt.Errorf("codex: no tokens in %s", path)
	}
	a := &auth{file: file, path: path, client: client}
	if exp, err := jwtExpiry(file.Tokens.AccessToken); err == nil {
		a.expiry = exp
	}
	return a, nil
}

func (a *auth) setHeaders(ctx context.Context, h http.Header) error {
	token, err := a.token(ctx)
	if err != nil {
		return err
	}
	h.Set("Authorization", "Bearer "+token)
	if a.file.Tokens.AccountID != "" {
		h.Set("ChatGPT-Account-ID", a.file.Tokens.AccountID)
	}
	return nil
}

func (a *auth) token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.expiry.IsZero() && time.Now().Add(tokenExpiryBuffer).Before(a.expiry) {
		return a.file.Tokens.AccessToken, nil
	}
	if a.file.Tokens.RefreshToken == "" {
		if a.file.Tokens.AccessToken != "" {
			return a.file.Tokens.AccessToken, nil
		}
		return "", fmt.Errorf("codex: no access token")
	}
	return a.refreshLocked(ctx)
}

func (a *auth) refreshLocked(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {a.file.Tokens.RefreshToken},
		"client_id":     {clientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("codex: build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := a.client
	if client == nil {
		client = httptransport.CloneDefaultHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("codex: token refresh: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("codex: decode refresh response (status %d): %w", resp.StatusCode, err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("codex: token refresh failed: %s: %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("codex: empty access token in refresh response (status %d)", resp.StatusCode)
	}
	a.file.Tokens.AccessToken = result.AccessToken
	if result.RefreshToken != "" {
		a.file.Tokens.RefreshToken = result.RefreshToken
	}
	if result.ExpiresIn > 0 {
		a.expiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	} else if exp, err := jwtExpiry(result.AccessToken); err == nil {
		a.expiry = exp
	}
	a.saveLocked()
	return result.AccessToken, nil
}

func (a *auth) saveLocked() {
	if a.path == "" {
		return
	}
	a.file.LastRefresh = time.Now().UTC()
	if data, err := json.MarshalIndent(a.file, "", "  "); err == nil {
		_ = os.WriteFile(a.path, data, 0o600)
	}
}

func jwtExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.NewDecoder(bytes.NewReader(payload)).Decode(&claims); err != nil {
		return time.Time{}, fmt.Errorf("decode JWT claims: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("JWT has no exp claim")
	}
	return time.Unix(claims.Exp, 0), nil
}
