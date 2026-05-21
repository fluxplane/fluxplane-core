package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/engine/adapters/llm/modelcatalog"
	"github.com/fluxplane/engine/core/agent"
	corellm "github.com/fluxplane/engine/core/llm"
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
	got = ResolveModelSelection("openai", "claudecode/claude-sonnet-4-6")
	if got.Provider != "claudecode" || got.Model != "claude-sonnet-4-6" {
		t.Fatalf("selection = %#v, want claudecode/claude-sonnet-4-6", got)
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

func TestResolveModelSelectionUsesBuiltInAliases(t *testing.T) {
	registry, err := DefaultModelRegistry()
	if err != nil {
		t.Fatalf("DefaultModelRegistry: %v", err)
	}
	got := registry.ResolveModelSelection("openai", "codex")
	if got.Provider != "codex" || got.Model != "gpt-5.5" {
		t.Fatalf("codex selection = %#v, want codex/gpt-5.5", got)
	}
	got = registry.ResolveModelSelection("openai", "claude/sonnet")
	if got.Provider != "anthropic" || !strings.HasPrefix(got.Model, "claude-sonnet-") {
		t.Fatalf("claude/sonnet selection = %#v, want anthropic claude sonnet", got)
	}
	got = registry.ResolveModelSelection("anthropic", "sonnet")
	if got.Provider != "anthropic" || !strings.HasPrefix(got.Model, "claude-sonnet-") {
		t.Fatalf("anthropic sonnet selection = %#v, want anthropic claude sonnet", got)
	}
	got = registry.ResolveModelSelection("openai", "minimax")
	if got.Provider != "minimax" || got.Model == "" {
		t.Fatalf("minimax selection = %#v, want minimax default model", got)
	}
}

func TestResolveModelSelectionUsesModelAliasesFromProviders(t *testing.T) {
	registry, err := NewModelRegistryWithAliases([]ModelProvider{{
		Spec: corellm.ProviderSpec{
			Name: "test",
			Models: []corellm.ModelSpec{{
				Ref:     corellm.ModelRef{Name: "test-model-v2"},
				Aliases: []corellm.ModelName{"latest"},
			}},
		},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("NewModelRegistryWithAliases: %v", err)
	}
	got := registry.ResolveModelSelection("openai", "test/latest")
	if got.Provider != "test" || got.Model != "test-model-v2" {
		t.Fatalf("selection = %#v, want test/test-model-v2", got)
	}
}

func TestResolveModelSelectionAppAliasesOverrideBuiltIns(t *testing.T) {
	registry, err := DefaultModelRegistryWithAliases(nil, []corellm.ModelAliasSpec{{
		Name:   "codex",
		Target: corellm.ModelRef{Provider: "openai", Name: "gpt-4.1-mini"},
	}})
	if err != nil {
		t.Fatalf("DefaultModelRegistryWithAliases: %v", err)
	}
	got := registry.ResolveModelSelection("openai", "codex")
	if got.Provider != "openai" || got.Model != "gpt-4.1-mini" {
		t.Fatalf("selection = %#v, want app alias override to openai/gpt-4.1-mini", got)
	}
}

func TestContributedModelParamsMergeWithBuiltInProvider(t *testing.T) {
	registry, err := DefaultModelRegistryWithAliases([]corellm.ProviderSpec{{
		Name: "openrouter",
		Models: []corellm.ModelSpec{{
			Ref: corellm.ModelRef{Provider: "openrouter", Name: "openai/gpt-5.5"},
			Params: corellm.ModelParams{
				ReasoningEffort: "medium",
			},
		}},
	}}, []corellm.ModelAliasSpec{{
		Name:   "smart_model",
		Target: corellm.ModelRef{Provider: "openrouter", Name: "openai/gpt-5.5"},
	}})
	if err != nil {
		t.Fatalf("DefaultModelRegistryWithAliases: %v", err)
	}
	got := registry.ResolveModelSelection("openai", "smart_model")
	if got.Provider != "openrouter" || got.Model != "openai/gpt-5.5" {
		t.Fatalf("selection = %#v, want openrouter/openai/gpt-5.5", got)
	}
	_, modelSpec, ok := registry.ModelSpec(got.Provider, got.Model)
	if !ok {
		t.Fatalf("ModelSpec(%s, %s) not found", got.Provider, got.Model)
	}
	if modelSpec.Params.ReasoningEffort != "medium" {
		t.Fatalf("params = %#v, want medium effort", modelSpec.Params)
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
	claudeDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	writeClaudeCodeCredentials(t, filepath.Join(claudeDir, ".credentials.json"))
	model, err = NewModel(ModelSelection{Provider: "claudecode", Model: "claude-haiku-4-5-20251001"}, false)
	if err != nil {
		t.Fatalf("NewModel claudecode: %v", err)
	}
	if model == nil {
		t.Fatal("claudecode model is nil")
	}
}

func TestDefaultModelRegistryIncludesClaudeCode(t *testing.T) {
	registry, err := DefaultModelRegistry()
	if err != nil {
		t.Fatalf("DefaultModelRegistry: %v", err)
	}
	provider, model, ok := registry.ModelSpec("claudecode", "claude-haiku-4-5-20251001")
	if !ok {
		t.Fatal("claudecode claude-haiku-4-5-20251001 missing")
	}
	if provider.DisplayName != "Claude Code" || model.Ref.Provider != "claudecode" {
		t.Fatalf("provider/model = %#v %#v, want claudecode rebinding", provider, model.Ref)
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

func TestResolveReasoningPrefersModelParamsOverAgentInference(t *testing.T) {
	model := reasoningModel("gpt-test", "low,medium,high,max")
	model.Params.ReasoningEffort = "medium"
	got, err := ResolveReasoning("openai", model, agent.InferenceSpec{
		ReasoningEffort: "high",
	}, ReasoningOverrides{})
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if got.Effort != "medium" {
		t.Fatalf("reasoning = %#v, want model param effort", got)
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
	got, err = ResolveReasoning("claudecode", anthropicReasoningModel("claude-test", "low,high", "enabled"), agent.InferenceSpec{}, ReasoningOverrides{
		Effort:    "high",
		EffortSet: true,
	})
	if err != nil {
		t.Fatalf("ResolveReasoning claudecode: %v", err)
	}
	if got.Thinking != "on" || got.Effort != "high" {
		t.Fatalf("reasoning = %#v, want on/high for claudecode", got)
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

func writeClaudeCodeCredentials(t *testing.T, path string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  "access",
			"refreshToken": "refresh",
			"expiresAt":    time.Now().Add(time.Hour).UnixMilli(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
