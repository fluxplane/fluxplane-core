package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/anthropic"
	"github.com/fluxplane/agentruntime/adapters/cmdrisk"
	"github.com/fluxplane/agentruntime/adapters/codex"
	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	"github.com/fluxplane/agentruntime/adapters/minimax"
	"github.com/fluxplane/agentruntime/adapters/modelcatalog"
	"github.com/fluxplane/agentruntime/adapters/openai"
	"github.com/fluxplane/agentruntime/adapters/openrouter"
	"github.com/fluxplane/agentruntime/core/agent"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const defaultModel = "gpt-5.5"

type ModelSelection struct {
	Provider string
	Model    string
}

func ResolveModelSelection(provider, model string) ModelSelection {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "openai"
	}
	model = strings.TrimSpace(model)
	if before, after, ok := strings.Cut(model, "/"); ok && before != "" && after != "" {
		if knownCLIProvider(before) && provider == "openai" {
			provider = before
			model = after
		}
	}
	if model == "" {
		model = defaultModel
	}
	return ModelSelection{Provider: provider, Model: model}
}

func NewModel(selection ModelSelection, debug bool) (llmagent.Model, error) {
	_, modelSpec, found := modelcatalog.Find(selection.Provider, selection.Model)
	pricing := modelSpec.Pricing
	runtime := openaiadapter.DefaultResponsesRuntimeConfig()
	switch selection.Provider {
	case "openai":
		return openaiadapter.New(openaiadapter.Config{
			Model:             selection.Model,
			Runtime:           runtime,
			Pricing:           pricing,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(debug),
		})
	case "codex":
		return codex.New(codex.Config{
			Model:             selection.Model,
			Runtime:           runtime,
			Pricing:           pricing,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(debug),
		})
	case "openrouter":
		if !found {
			return nil, fmt.Errorf("openrouter model %q was not found in modeldb; use an exact OpenRouter model id, for example --model openrouter/anthropic/claude-sonnet-4.6", selection.Model)
		}
		if !modelcatalog.SupportsAPI(modelSpec, "openai-responses") {
			return nil, fmt.Errorf("openrouter model %q does not expose OpenAI Responses in modeldb", selection.Model)
		}
		reasoningEffort, reasoningSummary := OpenRouterReasoningDefaults(modelSpec)
		return openrouter.New(openrouter.Config{
			Model:             selection.Model,
			Pricing:           pricing,
			ReasoningEffort:   reasoningEffort,
			ReasoningSummary:  reasoningSummary,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(debug),
		})
	case "anthropic":
		if err := requireMessagesModel(selection.Provider, selection.Model, modelSpec, found); err != nil {
			return nil, err
		}
		return anthropic.New(anthropic.Config{
			Model:           selection.Model,
			Pricing:         pricing,
			MaxOutputTokens: maxOutputTokens(modelSpec),
			PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
			Redactor:        debugRedactor(debug),
		})
	case "minimax":
		if err := requireMessagesModel(selection.Provider, selection.Model, modelSpec, found); err != nil {
			return nil, err
		}
		return minimax.New(minimax.Config{
			Model:           selection.Model,
			Pricing:         pricing,
			MaxOutputTokens: maxOutputTokens(modelSpec),
			PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
			Redactor:        debugRedactor(debug),
		})
	default:
		return nil, fmt.Errorf("unknown provider %q", selection.Provider)
	}
}

func DebugStreamPolicy(debug bool) llmagent.StreamPolicy {
	return llmagent.StreamPolicy{EmitContent: true, EmitThinking: true, EmitToolCall: debug}
}

type ModelResolver struct {
	Provider     string
	Model        string
	DefaultModel string
	Debug        bool
}

func (r ModelResolver) ResolveModel(_ context.Context, spec agent.Spec) (llmagent.Model, error) {
	selection := ResolveModelSelection(firstNonEmptyString(r.Provider, "openai"), firstNonEmptyString(r.Model, spec.Inference.Model, r.DefaultModel))
	return NewModel(selection, r.Debug)
}

func OpenRouterReasoningDefaults(modelSpec corellm.ModelSpec) (string, string) {
	effort := firstSupportedCSV(modelSpec.Annotations["modeldb.openai_responses.reasoning_efforts"], "minimal", "low", "medium", "high")
	summary := firstSupportedCSV(modelSpec.Annotations["modeldb.openai_responses.reasoning_summaries"], "auto", "concise", "detailed")
	return effort, summary
}

func CommandRisk(root string) operationruntime.CommandRiskClassifier {
	secretPrefixes := []string{
		filepath.Join(root, ".env"),
		filepath.Join(root, ".git", "config"),
		filepath.Join(root, ".git", "credentials"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		secretPrefixes = append(secretPrefixes,
			filepath.Join(home, ".ssh"),
			filepath.Join(home, ".aws"),
			filepath.Join(home, ".config", "gh"),
		)
	}
	return cmdrisk.New(cmdrisk.Config{
		WorkingDirectory:        root,
		WorkspacePathPrefixes:   []string{root},
		SecretPathPrefixes:      secretPrefixes,
		SensitivePathPrefixes:   []string{filepath.Join(root, ".git")},
		Sandboxed:               false,
		Disposable:              false,
		Interactive:             false,
		NetworkApprovalAsMedium: true,
	})
}

func knownCLIProvider(provider string) bool {
	switch provider {
	case "openai", "codex", "openrouter", "anthropic", "minimax":
		return true
	default:
		return false
	}
}

func debugRedactor(debug bool) adapterllm.Redactor {
	if !debug {
		return adapterllm.Redactor{ExposeThinkingSummary: true}
	}
	return adapterllm.Redactor{ExposeThinking: true, ExposeThinkingSummary: true, ExposeToolArgs: true}
}

func requireMessagesModel(provider, model string, modelSpec corellm.ModelSpec, found bool) error {
	if !found {
		return fmt.Errorf("%s model %q was not found in modeldb", provider, model)
	}
	if !modelcatalog.SupportsAPI(modelSpec, "anthropic-messages") {
		return fmt.Errorf("%s model %q does not expose Anthropic Messages in modeldb", provider, model)
	}
	return nil
}

func maxOutputTokens(modelSpec corellm.ModelSpec) int {
	if modelSpec.MaxOutputTokens > 0 && modelSpec.MaxOutputTokens < int64(^uint(0)>>1) {
		return int(modelSpec.MaxOutputTokens)
	}
	return 0
}

func firstSupportedCSV(csv string, preferred ...string) string {
	values := map[string]bool{}
	for _, value := range strings.Split(csv, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			values[value] = true
		}
	}
	for _, value := range preferred {
		if values[value] {
			return value
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
