// Package anthropic adapts Anthropic's Messages API to AgentRuntime.
package anthropic

import (
	"fmt"
	"os"
	"strings"

	adapterllm "github.com/fluxplane/engine/adapters/llm"
	"github.com/fluxplane/engine/adapters/llm/anthropicmessages"
	corellm "github.com/fluxplane/engine/core/llm"
)

const DefaultBaseURL = "https://api.anthropic.com"

// Config configures an Anthropic Messages model.
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

// New returns an Anthropic-backed Messages model.
func New(cfg Config) (*anthropicmessages.Model, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	}
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic: ANTHROPIC_API_KEY is not set")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return anthropicmessages.New(anthropicmessages.Config{
		Model:           cfg.Model,
		APIKey:          apiKey,
		BaseURL:         baseURL,
		ProviderName:    "anthropic",
		APIName:         "anthropic.messages",
		AuthHeader:      "x-api-key",
		MaxOutputTokens: cfg.MaxOutputTokens,
		PromptCache:     cfg.PromptCache,
		Pricing:         cfg.Pricing,
		Thinking:        cfg.Thinking,
		ReasoningEffort: cfg.ReasoningEffort,
		Redactor:        cfg.Redactor,
	})
}
