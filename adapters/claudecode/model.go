// Package claudecode adapts Claude Code-compatible Anthropic Messages access
// through local Claude OAuth credentials.
package claudecode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/anthropicmessages"
	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/runtime/httptransport"
)

const (
	DefaultBaseURL       = "https://api.anthropic.com"
	claudeUserAgent      = "claude-cli/2.1.112 (external, sdk-cli)"
	claudeBillingHeader  = "x-anthropic-billing-header: cc_version=2.1.112.43b; cc_entrypoint=sdk-cli; cch=1757e;"
	claudeSystemCore     = "You are a Claude agent, built on Anthropic's Claude Agent SDK."
	stainlessPackage     = "0.81.0"
	stainlessNodeVersion = "v24.3.0"
	systemCacheTTL       = "1h"
)

// Config configures a Claude Code-compatible Messages model.
type Config struct {
	Model           string
	AuthPath        string
	BaseURL         string
	MaxOutputTokens int
	PromptCache     bool
	Pricing         []corellm.PricingSpec
	Thinking        string
	ReasoningEffort string
	Redactor        adapterllm.Redactor
	HTTPClient      *http.Client
}

// New returns a Claude Code-compatible Anthropic Messages model.
func New(cfg Config) (*anthropicmessages.Model, error) {
	client := cfg.HTTPClient
	if client == nil {
		client = httptransport.CloneDefaultHTTPClient()
	}
	store, err := newLocalTokenStore(strings.TrimSpace(cfg.AuthPath))
	if err != nil {
		return nil, err
	}
	provider := newManagedTokenProvider(localTokenKey, store, client)
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	preflight := &preflightProcessor{sessionID: randomUUID()}
	return anthropicmessages.New(anthropicmessages.Config{
		Model:           cfg.Model,
		BaseURL:         baseURL,
		ProviderName:    "claudecode",
		APIName:         "claudecode.messages",
		AuthHeader:      "",
		MaxOutputTokens: cfg.MaxOutputTokens,
		PromptCache:     cfg.PromptCache,
		Pricing:         cfg.Pricing,
		Thinking:        cfg.Thinking,
		ReasoningEffort: cfg.ReasoningEffort,
		Redactor:        cfg.Redactor,
		HTTPClient:      client,
		Query:           map[string]string{"beta": "true"},
		RequestProcessors: []anthropicmessages.RequestProcessor{
			preflight.Process,
		},
		HeaderFuncs: []anthropicmessages.HeaderFunc{
			bearerHeader(provider),
			claudeHeaders,
		},
	})
}

func bearerHeader(provider tokenProvider) anthropicmessages.HeaderFunc {
	return func(ctx context.Context, req *http.Request, _ anthropicmessages.MessageRequest) error {
		token, err := provider.Token(ctx)
		if err != nil {
			return err
		}
		req.Header.Del("x-api-key")
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		return nil
	}
}

func claudeHeaders(_ context.Context, req *http.Request, wire anthropicmessages.MessageRequest) error {
	betas := []string{
		"claude-code-20250219",
		"interleaved-thinking-2025-05-14",
		"context-management-2025-06-27",
		"prompt-caching-scope-2026-01-05",
		"advisor-tool-2026-03-01",
	}
	if req.Header.Get("Authorization") != "" {
		betas = append(betas, "oauth-2025-04-20")
	}
	if wire.OutputConfig != nil && wire.OutputConfig.Effort != "" {
		betas = append(betas, "effort-2025-11-24")
	}
	for _, beta := range betas {
		addAnthropicBeta(req.Header, beta)
	}
	req.Header.Set("User-Agent", claudeUserAgent)
	req.Header.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")
	req.Header.Set("X-App", "cli")
	if wire.ClaudeSessionID != "" {
		req.Header.Set("X-Claude-Code-Session-Id", wire.ClaudeSessionID)
	}
	req.Header.Set("X-Stainless-Lang", "js")
	req.Header.Set("X-Stainless-Os", stainlessOS())
	req.Header.Set("X-Stainless-Arch", stainlessArch())
	req.Header.Set("X-Stainless-Package-Version", stainlessPackage)
	req.Header.Set("X-Stainless-Retry-Count", "0")
	req.Header.Set("X-Stainless-Runtime", "node")
	req.Header.Set("X-Stainless-Runtime-Version", stainlessNodeVersion)
	req.Header.Set("X-Stainless-Timeout", "600")
	req.Header.Set("Accept-Encoding", httptransport.ExtendedAcceptEncoding)
	req.Header.Set("Connection", "keep-alive")
	return nil
}

type preflightProcessor struct {
	sessionID string
}

func (p *preflightProcessor) Process(_ context.Context, req *anthropicmessages.MessageRequest) error {
	req.ClaudeSessionID = p.sessionID
	req.System = append([]anthropicmessages.ContentBlock{
		{Type: "text", Text: claudeBillingHeader},
		{Type: "text", Text: claudeSystemCore},
	}, req.System...)
	if userID := buildClaudeUserID(p.sessionID); userID != "" {
		if req.Metadata == nil {
			req.Metadata = map[string]any{}
		}
		req.Metadata["user_id"] = userID
	}
	moveReasoningEffort(req)
	if req.Thinking != nil && len(req.ContextManagement) == 0 {
		req.ContextManagement = json.RawMessage(`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`)
	}
	applySystemCacheControl(req)
	return nil
}

func moveReasoningEffort(req *anthropicmessages.MessageRequest) {
	effort := strings.TrimSpace(req.Effort)
	if effort == "" {
		return
	}
	req.Effort = ""
	if req.OutputConfig == nil {
		req.OutputConfig = &anthropicmessages.OutputConfig{}
	}
	req.OutputConfig.Effort = claudeWireEffort(effort)
	if req.Thinking != nil && req.Thinking.BudgetTokens == 1024 {
		req.Thinking.Type = "adaptive"
		req.Thinking.BudgetTokens = 0
	}
}

func claudeWireEffort(effort string) string {
	if effort == "max" {
		return "xhigh"
	}
	return effort
}

func applySystemCacheControl(req *anthropicmessages.MessageRequest) {
	cache := &anthropicmessages.CacheControl{Type: "ephemeral", TTL: systemCacheTTL}
	for i := len(req.System) - 1; i >= 0; i-- {
		if req.System[i].Type == "text" && req.System[i].Text != "" {
			req.System[i].CacheControl = cache
			return
		}
	}
}

func buildClaudeUserID(sessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		UserID       string `json:"userID"`
		OAuthAccount struct {
			AccountUUID string `json:"accountUuid"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.UserID == "" {
		return ""
	}
	meta := map[string]string{
		"device_id":  cfg.UserID,
		"session_id": sessionID,
	}
	if cfg.OAuthAccount.AccountUUID != "" {
		meta["account_uuid"] = cfg.OAuthAccount.AccountUUID
	}
	out, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(out)
}

func addAnthropicBeta(headers http.Header, beta string) {
	beta = strings.TrimSpace(beta)
	if beta == "" {
		return
	}
	for _, current := range strings.Split(headers.Get("Anthropic-Beta"), ",") {
		if strings.TrimSpace(current) == beta {
			return
		}
	}
	if current := strings.TrimSpace(headers.Get("Anthropic-Beta")); current != "" {
		headers.Set("Anthropic-Beta", current+","+beta)
		return
	}
	headers.Set("Anthropic-Beta", beta)
}

func stainlessOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacOS"
	case "windows":
		return "Windows"
	default:
		return "Linux"
	}
}

func stainlessArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	default:
		return "x64"
	}
}

func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}
