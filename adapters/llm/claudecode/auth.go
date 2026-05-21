package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/engine/runtime/httptransport"
)

const (
	credentialsFile = ".credentials.json"
	localTokenKey   = "default"
)

var (
	anthropicOAuthClient = strings.Join([]string{"9d1c250a", "e61b", "44d9", "88ed", "5944d1962f5e"}, "-")
	oauthTokenEndpoint   = "https://console.anthropic.com/v1/oauth/token"
)

type token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func (t *token) expired() bool {
	return t == nil || t.AccessToken == "" || time.Now().Add(30*time.Second).After(t.ExpiresAt)
}

type tokenProvider interface {
	Token(context.Context) (*token, error)
}

type tokenStore interface {
	Load(context.Context, string) (*token, error)
	Save(context.Context, string, *token) error
}

type managedTokenProvider struct {
	key    string
	store  tokenStore
	client *http.Client

	mu     sync.Mutex
	cached *token
}

func newManagedTokenProvider(key string, store tokenStore, client *http.Client) *managedTokenProvider {
	if client == nil {
		client = httptransport.CloneDefaultHTTPClient()
	}
	return &managedTokenProvider{key: key, store: store, client: client}
}

func (p *managedTokenProvider) Token(ctx context.Context) (*token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cached != nil && !p.cached.expired() {
		return p.cached, nil
	}
	token, err := p.store.Load(ctx, p.key)
	if err != nil {
		return nil, fmt.Errorf("load Claude Code token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("no Claude Code OAuth token found")
	}
	if token.expired() {
		if token.RefreshToken == "" {
			return nil, fmt.Errorf("claude code OAuth token is expired and has no refresh token")
		}
		token, err = p.refresh(ctx, token.RefreshToken)
		if err != nil {
			return nil, err
		}
		if err := p.store.Save(ctx, p.key, token); err != nil {
			return nil, fmt.Errorf("save refreshed Claude Code token: %w", err)
		}
	}
	p.cached = token
	return token, nil
}

func (p *managedTokenProvider) refresh(ctx context.Context, refreshToken string) (*token, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     anthropicOAuthClient,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh Claude Code OAuth token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh Claude Code OAuth token: HTTP %d", resp.StatusCode)
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode refreshed Claude Code OAuth token: %w", err)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("refreshed Claude Code OAuth token is empty")
	}
	if out.RefreshToken == "" {
		out.RefreshToken = refreshToken
	}
	return &token{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(out.ExpiresIn) * time.Second),
	}, nil
}

type localTokenStore struct {
	path string
}

func newLocalTokenStore(path string) (*localTokenStore, error) {
	if path == "" {
		dir, err := defaultClaudeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(dir, credentialsFile)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("claudecode: credentials not found at %s: %w", path, err)
	}
	return &localTokenStore{path: path}, nil
}

func defaultClaudeDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func (s *localTokenStore) Load(context.Context, string) (*token, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var creds struct {
		ClaudeAiOauth *struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	if creds.ClaudeAiOauth == nil || creds.ClaudeAiOauth.AccessToken == "" {
		return nil, nil
	}
	return &token{
		AccessToken:  creds.ClaudeAiOauth.AccessToken,
		RefreshToken: creds.ClaudeAiOauth.RefreshToken,
		ExpiresAt:    time.UnixMilli(creds.ClaudeAiOauth.ExpiresAt),
	}, nil
}

func (s *localTokenStore) Save(_ context.Context, _ string, token *token) error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	var oauth map[string]any
	if raw := root["claudeAiOauth"]; raw != nil {
		_ = json.Unmarshal(raw, &oauth)
	}
	if oauth == nil {
		oauth = map[string]any{}
	}
	oauth["accessToken"] = token.AccessToken
	oauth["refreshToken"] = token.RefreshToken
	oauth["expiresAt"] = token.ExpiresAt.UnixMilli()
	oauthBytes, err := json.Marshal(oauth)
	if err != nil {
		return err
	}
	root["claudeAiOauth"] = oauthBytes
	out, err := json.Marshal(root)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
