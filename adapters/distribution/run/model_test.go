package run

import (
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/adapters/modelcatalog"
	"github.com/fluxplane/agentruntime/core/agent"
	corellm "github.com/fluxplane/agentruntime/core/llm"
)

func TestResolveModelSelectionParsesProviderPrefix(t *testing.T) {
	got := ResolveModelSelection("openai", "codex/gpt-5.5")
	if got.Provider != "codex" || got.Model != "gpt-5.5" {
		t.Fatalf("selection = %#v, want codex/gpt-5.5", got)
	}
	got = ResolveModelSelection("openai", "anthropic/claude-haiku-4-5-20251001")
	if got.Provider != "anthropic" || got.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("selection = %#v, want anthropic/claude-haiku-4-5-20251001", got)
	}
	got = ResolveModelSelection("openai", "minimax/MiniMax-M2.7")
	if got.Provider != "minimax" || got.Model != "MiniMax-M2.7" {
		t.Fatalf("selection = %#v, want minimax/MiniMax-M2.7", got)
	}
}

func TestResolveModelSelectionKeepsOpenRouterSlashModel(t *testing.T) {
	got := ResolveModelSelection("openai", "openrouter/anthropic/claude-sonnet-4.6")
	if got.Provider != "openrouter" || got.Model != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("selection = %#v, want openrouter/anthropic/claude-sonnet-4.6", got)
	}
	got = ResolveModelSelection("openrouter", "anthropic/claude-sonnet-4.6")
	if got.Provider != "openrouter" || got.Model != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("selection = %#v, want explicit openrouter provider", got)
	}
}

func TestNewModelRejectsUnknownOpenRouterModel(t *testing.T) {
	_, err := NewModel(ModelSelection{Provider: "openrouter", Model: "gpt-5.5"}, false)
	if err == nil || !strings.Contains(err.Error(), "exact OpenRouter model id") {
		t.Fatalf("error = %v, want exact OpenRouter model id", err)
	}
}

func TestNewModelSupportsOpenRouterResponsesModel(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	model, err := NewModel(ModelSelection{Provider: "openrouter", Model: "anthropic/claude-sonnet-4.6"}, false)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	if model == nil {
		t.Fatalf("model is nil")
	}
}

func TestNewModelSupportsAnthropicMessagesModels(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	model, err := NewModel(ModelSelection{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"}, false)
	if err != nil {
		t.Fatalf("NewModel anthropic: %v", err)
	}
	if model == nil {
		t.Fatal("anthropic model is nil")
	}
	t.Setenv("MINIMAX_API_KEY", "test-key")
	model, err = NewModel(ModelSelection{Provider: "minimax", Model: "MiniMax-M2.7"}, false)
	if err != nil {
		t.Fatalf("NewModel minimax: %v", err)
	}
	if model == nil {
		t.Fatal("minimax model is nil")
	}
}

func TestOpenRouterReasoningDefaultsPreferMinimalAndAuto(t *testing.T) {
	_, modelSpec, ok := modelcatalog.Find("openrouter", "moonshotai/kimi-k2-thinking")
	if !ok {
		t.Fatal("openrouter moonshotai/kimi-k2-thinking missing from modeldb")
	}
	effort, summary := OpenRouterReasoningDefaults(modelSpec)
	if effort != "minimal" {
		t.Fatalf("effort = %q, want minimal", effort)
	}
	if summary != "auto" {
		t.Fatalf("summary = %q, want auto", summary)
	}
}

func TestResolveReasoningMapsEnabledConfigToOn(t *testing.T) {
	got, err := ResolveReasoning("openai", reasoningModel("gpt-test", "low,medium,high,max"), agent.InferenceSpec{
		Thinking:        "enabled",
		ReasoningEffort: "high",
	}, ReasoningOverrides{})
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if got.Thinking != "on" || got.Effort != "high" {
		t.Fatalf("reasoning = %#v, want on/high", got)
	}
}

func TestResolveReasoningAllowsThinkingOnlyAnthropic(t *testing.T) {
	got, err := ResolveReasoning("anthropic", anthropicReasoningModel("claude-test", "", "enabled"), agent.InferenceSpec{
		Thinking: "enabled",
	}, ReasoningOverrides{})
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if got.Thinking != "on" || got.Effort != "" {
		t.Fatalf("reasoning = %#v, want thinking on without effort", got)
	}
}

func TestResolveReasoningPropagatesExplicitEffortInAutoMode(t *testing.T) {
	got, err := ResolveReasoning("openai", reasoningModel("gpt-test", "low,medium,high,max"), agent.InferenceSpec{}, ReasoningOverrides{
		Effort:    "high",
		EffortSet: true,
	})
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if got.Thinking != "auto" || got.Effort != "high" {
		t.Fatalf("reasoning = %#v, want auto/high", got)
	}
}

func TestResolveReasoningAutoEffortEnablesAnthropicThinking(t *testing.T) {
	got, err := ResolveReasoning("anthropic", anthropicReasoningModel("claude-test", "low,high", "enabled"), agent.InferenceSpec{}, ReasoningOverrides{
		Effort:    "high",
		EffortSet: true,
	})
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if got.Thinking != "on" || got.Effort != "high" {
		t.Fatalf("reasoning = %#v, want on/high", got)
	}
}

func TestResolveReasoningOffSuppressesOpenRouterDefaults(t *testing.T) {
	got, err := ResolveReasoning("openrouter", reasoningModel("moonshotai/kimi-k2-thinking", "minimal,low,medium,high"), agent.InferenceSpec{}, ReasoningOverrides{
		Thinking:    "off",
		ThinkingSet: true,
		Effort:      "high",
		EffortSet:   true,
	})
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if got.Thinking != "off" || got.Effort != "" || got.Summary != "" {
		t.Fatalf("reasoning = %#v, want off without defaults", got)
	}
}

func TestResolveReasoningRejectsUnsupportedMax(t *testing.T) {
	_, err := ResolveReasoning("openai", reasoningModel("gpt-test", "low,medium,high"), agent.InferenceSpec{}, ReasoningOverrides{
		Thinking:    "on",
		ThinkingSet: true,
		Effort:      "max",
		EffortSet:   true,
	})
	if err == nil || !strings.Contains(err.Error(), `does not support reasoning effort "max"`) {
		t.Fatalf("ResolveReasoning error = %v, want unsupported max", err)
	}
}

func TestValidateReasoningFlagsRejectsAuthThinking(t *testing.T) {
	err := ValidateReasoningFlags("auth", true, "medium", false)
	if err == nil || !strings.Contains(err.Error(), `invalid --thinking "auth"`) {
		t.Fatalf("ValidateReasoningFlags error = %v, want invalid thinking", err)
	}
}

func reasoningModel(name, efforts string) corellm.ModelSpec {
	return corellm.ModelSpec{
		Ref: corellm.ModelRef{Name: corellm.ModelName(name)},
		Annotations: map[string]string{
			"modeldb.openai_responses.reasoning_efforts": efforts,
		},
	}
}

func anthropicReasoningModel(name, efforts, thinkingModes string) corellm.ModelSpec {
	return corellm.ModelSpec{
		Ref: corellm.ModelRef{Name: corellm.ModelName(name)},
		Annotations: map[string]string{
			"modeldb.anthropic_messages.reasoning_efforts": efforts,
			"modeldb.anthropic_messages.thinking_modes":    thinkingModes,
		},
	}
}
