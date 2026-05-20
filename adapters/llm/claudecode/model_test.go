package claudecode

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/adapters/llm/anthropicmessages"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/fluxplane/agentruntime/runtime/httptransport"
)

func TestNewMissingCredentials(t *testing.T) {
	_, err := New(Config{Model: "claude-test", AuthPath: filepath.Join(t.TempDir(), ".credentials.json")})
	if err == nil || !strings.Contains(err.Error(), "claudecode: credentials not found") {
		t.Fatalf("err = %v, want missing credentials", err)
	}
}

func TestStreamUsesClaudeCodeHeadersAndPreflight(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"userID":"device-1","oauthAccount":{"accountUuid":"acct-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	authPath := writeCredentials(t, home, "oauth-token", "refresh-token", time.Now().Add(time.Hour))

	var seen *http.Request
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(context.Background())
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","model":"claude-test","content":[]}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")))
	}))
	defer server.Close()

	model, err := New(Config{
		Model:           "claude-test",
		AuthPath:        authPath,
		BaseURL:         server.URL,
		Thinking:        "on",
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = model.Stream(context.Background(), llmagent.Request{Goal: "hello"}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if seen == nil {
		t.Fatal("request not sent")
	}
	if seen.URL.Path != "/v1/messages" || seen.URL.Query().Get("beta") != "true" {
		t.Fatalf("url = %s, want /v1/messages?beta=true", seen.URL.String())
	}
	if seen.Header.Get("Authorization") != "Bearer oauth-token" || seen.Header.Get("x-api-key") != "" {
		t.Fatalf("auth headers = %v", seen.Header)
	}
	if seen.Header.Get("User-Agent") != claudeUserAgent ||
		seen.Header.Get("X-App") != "cli" ||
		seen.Header.Get("X-Claude-Code-Session-Id") == "" ||
		seen.Header.Get("Accept-Encoding") != httptransport.ExtendedAcceptEncoding {
		t.Fatalf("Claude Code headers = %v", seen.Header)
	}
	for _, want := range []string{
		"claude-code-20250219",
		"interleaved-thinking-2025-05-14",
		"context-management-2025-06-27",
		"prompt-caching-scope-2026-01-05",
		"advisor-tool-2026-03-01",
		"oauth-2025-04-20",
		"effort-2025-11-24",
	} {
		if !strings.Contains(seen.Header.Get("Anthropic-Beta"), want) {
			t.Fatalf("Anthropic-Beta = %q, missing %q", seen.Header.Get("Anthropic-Beta"), want)
		}
	}
	var wire anthropicmessages.MessageRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.System) < 2 || wire.System[0].Text != claudeBillingHeader || wire.System[1].Text != claudeSystemCore {
		t.Fatalf("system = %#v, want Claude Code preflight", wire.System)
	}
	if got := wire.System[len(wire.System)-1].CacheControl; got == nil || got.Type != "ephemeral" || got.TTL != "1h" {
		t.Fatalf("system cache = %#v", wire.System)
	}
	if wire.Thinking == nil || wire.Thinking.Type != "adaptive" || wire.Thinking.BudgetTokens != 0 {
		t.Fatalf("thinking = %#v, want adaptive", wire.Thinking)
	}
	if wire.OutputConfig == nil || wire.OutputConfig.Effort != "high" {
		t.Fatalf("output_config = %#v, want high effort", wire.OutputConfig)
	}
	if string(wire.ContextManagement) != `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}` {
		t.Fatalf("context_management = %s", wire.ContextManagement)
	}
	rawUserID, ok := wire.Metadata["user_id"].(string)
	if !ok {
		t.Fatalf("metadata = %#v, want user_id string", wire.Metadata)
	}
	var userID map[string]string
	if err := json.Unmarshal([]byte(rawUserID), &userID); err != nil {
		t.Fatal(err)
	}
	if userID["device_id"] != "device-1" || userID["account_uuid"] != "acct-1" || userID["session_id"] != seen.Header.Get("X-Claude-Code-Session-Id") {
		t.Fatalf("user_id = %#v, header session = %q", userID, seen.Header.Get("X-Claude-Code-Session-Id"))
	}
}

func TestStreamDecodesCompressedClaudeCodeSSE(t *testing.T) {
	authPath := writeCredentials(t, t.TempDir(), "oauth-token", "refresh-token", time.Now().Add(time.Hour))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != httptransport.ExtendedAcceptEncoding {
			t.Fatalf("Accept-Encoding = %q, want extended encodings", r.Header.Get("Accept-Encoding"))
		}
		body := strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","model":"claude-test","content":[]}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gzipBytes(t, []byte(body)))
	}))
	defer server.Close()

	model, err := New(Config{Model: "claude-test", AuthPath: authPath, BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{Goal: "hello"}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message == nil || resp.Message.Content != "ok" {
		t.Fatalf("message = %#v, want ok", resp.Message)
	}
}

func TestExpiredCredentialRefreshesAndPersists(t *testing.T) {
	dir := t.TempDir()
	authPath := writeCredentials(t, dir, "old-access", "old-refresh", time.Now().Add(-time.Hour))
	oldEndpoint := oauthTokenEndpoint
	defer func() { oauthTokenEndpoint = oldEndpoint }()

	var refreshSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshSeen = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
		case "/v1/messages":
			if r.Header.Get("Authorization") != "Bearer new-access" {
				t.Fatalf("Authorization = %q, want refreshed token", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	oauthTokenEndpoint = server.URL + "/oauth/token"

	model, err := New(Config{Model: "claude-test", AuthPath: authPath, BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = model.Stream(context.Background(), llmagent.Request{Goal: "hello"}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !refreshSeen {
		t.Fatal("refresh endpoint was not called")
	}
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new-access") || !strings.Contains(string(data), "new-refresh") {
		t.Fatalf("credentials not updated: %s", string(data))
	}
}

func TestClaudeConfigDirOverridesHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := writeCredentials(t, dir, "access", "refresh", time.Now().Add(time.Hour))
	store, err := newLocalTokenStore("")
	if err != nil {
		t.Fatalf("newLocalTokenStore: %v", err)
	}
	if store.path != path {
		t.Fatalf("store path = %q, want %q", store.path, path)
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeCredentials(t *testing.T, dir, access, refresh string, expires time.Time) string {
	t.Helper()
	path := filepath.Join(dir, credentialsFile)
	data := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  access,
			"refreshToken": refresh,
			"expiresAt":    expires.UnixMilli(),
		},
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
