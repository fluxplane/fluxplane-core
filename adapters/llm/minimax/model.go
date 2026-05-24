// Package minimax adapts MiniMax's Anthropic-compatible Messages API.
package minimax

import (
	"fmt"
	"os"
	"strings"

	adapterllm "github.com/fluxplane/fluxplane-core/adapters/llm"
	"github.com/fluxplane/fluxplane-core/adapters/llm/anthropicmessages"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
)

const DefaultBaseURL = "https://api.minimax.io/anthropic"

// Config configures a MiniMax Messages model.
type Config struct {
	Model           string
	APIKey          string
	BaseURL         string
	MaxOutputTokens int
	PromptCache     bool
	Pricing         []corellm.PricingSpec
	Thinking        string
	ReasoningEffort string
	Redactor        adapterllm.Redactor
}

// New returns a MiniMax-backed Anthropic-compatible Messages model.
func New(cfg Config) (*anthropicmessages.Model, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("MINIMAX_KEY"))
	}
	if apiKey == "" {
		return nil, fmt.Errorf("minimax: MINIMAX_API_KEY is not set")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return anthropicmessages.New(anthropicmessages.Config{
		Model:           cfg.Model,
		APIKey:          apiKey,
		BaseURL:         baseURL,
		ProviderName:    "minimax",
		APIName:         "minimax.anthropic_messages",
		AuthHeader:      "Authorization",
		AuthScheme:      "Bearer",
		MaxOutputTokens: cfg.MaxOutputTokens,
		PromptCache:     cfg.PromptCache,
		Pricing:         cfg.Pricing,
		Thinking:        cfg.Thinking,
		ReasoningEffort: cfg.ReasoningEffort,
		Redactor:        cfg.Redactor,
	})
}
