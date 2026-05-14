package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/cmdrisk"
	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	"github.com/fluxplane/agentruntime/adapters/modelcatalog"
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

// ReasoningOverrides carries CLI-selected inference overrides. The Set fields
// distinguish real user choices from displayed flag defaults.
type ReasoningOverrides struct {
	Thinking    string
	ThinkingSet bool
	Effort      string
	EffortSet   bool
}

// ReasoningConfig is the provider-normalized reasoning request for one model.
type ReasoningConfig struct {
	Thinking string
	Effort   string
	Summary  string
}

func ResolveModelSelection(provider, model string) ModelSelection {
	registry, err := DefaultModelRegistry()
	if err != nil {
		return ModelSelection{Provider: firstNonEmptyString(provider, "openai"), Model: firstNonEmptyString(model, defaultModel)}
	}
	return registry.ResolveModelSelection(provider, model)
}

func NewModel(selection ModelSelection, debug bool) (llmagent.Model, error) {
	registry, err := DefaultModelRegistry()
	if err != nil {
		return nil, err
	}
	return registry.NewModel(selection, debug)
}

func DebugStreamPolicy(debug bool) llmagent.StreamPolicy {
	return llmagent.StreamPolicy{EmitContent: true, EmitThinking: true, EmitToolCall: debug}
}

type ModelResolver struct {
	Provider        string
	Model           string
	Thinking        string
	ThinkingSet     bool
	Effort          string
	EffortSet       bool
	DefaultProvider string
	DefaultModel    string
	Debug           bool
	ProviderSpecs   []corellm.ProviderSpec
	ModelAliases    []corellm.ModelAliasSpec
}

func (r ModelResolver) ResolveModel(_ context.Context, spec agent.Spec) (llmagent.Model, error) {
	registry, err := DefaultModelRegistryWithAliases(r.ProviderSpecs, r.ModelAliases)
	if err != nil {
		return nil, err
	}
	selection := registry.ResolveModelSelection(firstNonEmptyString(r.Provider, r.DefaultProvider, "openai"), firstNonEmptyString(r.Model, spec.Inference.Model, r.DefaultModel))
	_, modelSpec, ok := registry.ModelSpec(selection.Provider, selection.Model)
	if !ok {
		return registry.NewModel(selection, r.Debug)
	}
	reasoning, err := ResolveReasoning(selection.Provider, modelSpec, spec.Inference, ReasoningOverrides{
		Thinking:    r.Thinking,
		ThinkingSet: r.ThinkingSet,
		Effort:      r.Effort,
		EffortSet:   r.EffortSet,
	})
	if err != nil {
		return nil, err
	}
	return registry.NewModelWithOptions(selection, ModelOptions{Debug: r.Debug, Reasoning: reasoning})
}

// ValidateReasoningFlags validates user-facing CLI flag values.
func ValidateReasoningFlags(thinking string, thinkingSet bool, effort string, effortSet bool) error {
	if thinkingSet {
		switch normalizeLower(thinking) {
		case "auto", "on", "off":
		default:
			return fmt.Errorf("invalid --thinking %q; expected auto|on|off", thinking)
		}
	}
	if effortSet {
		switch normalizeLower(effort) {
		case "low", "medium", "high", "max":
		default:
			return fmt.Errorf("invalid --effort %q; expected low|medium|high|max", effort)
		}
	}
	return nil
}

// ResolveReasoning merges agent config with CLI overrides and validates the
// requested effort against modeldb-derived provider annotations.
func ResolveReasoning(provider string, modelSpec corellm.ModelSpec, inference agent.InferenceSpec, overrides ReasoningOverrides) (ReasoningConfig, error) {
	mode, err := normalizeThinkingMode(inference.Thinking, true)
	if err != nil {
		return ReasoningConfig{}, err
	}
	if mode == "" {
		mode = "auto"
	}
	if overrides.ThinkingSet {
		mode, err = normalizeThinkingMode(overrides.Thinking, false)
		if err != nil {
			return ReasoningConfig{}, err
		}
	}
	effort := normalizeLower(inference.ReasoningEffort)
	if overrides.EffortSet {
		effort = normalizeLower(overrides.Effort)
	}
	effortSet := effort != ""
	switch mode {
	case "off":
		return ReasoningConfig{Thinking: "off"}, nil
	case "on":
		if effortSet {
			if err := validateReasoningEffort(provider, modelSpec, effort); err != nil {
				return ReasoningConfig{}, err
			}
		}
		return ReasoningConfig{Thinking: "on", Effort: effort, Summary: defaultReasoningSummary(provider, modelSpec)}, nil
	case "auto":
		if effortSet {
			if err := validateReasoningEffort(provider, modelSpec, effort); err != nil {
				return ReasoningConfig{}, err
			}
			if isAnthropicMessagesProvider(provider) {
				return ReasoningConfig{Thinking: "on", Effort: effort, Summary: defaultReasoningSummary(provider, modelSpec)}, nil
			}
			return ReasoningConfig{Thinking: "auto", Effort: effort}, nil
		}
		if provider == "openrouter" {
			effort, summary := OpenRouterReasoningDefaults(modelSpec)
			return ReasoningConfig{Thinking: "auto", Effort: effort, Summary: summary}, nil
		}
		return ReasoningConfig{Thinking: "auto"}, nil
	default:
		return ReasoningConfig{}, fmt.Errorf("invalid thinking mode %q", mode)
	}
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

func debugRedactor(debug bool) adapterllm.Redactor {
	if !debug {
		return adapterllm.Redactor{ExposeThinkingSummary: true}
	}
	return adapterllm.Redactor{ExposeThinking: true, ExposeThinkingSummary: true, ExposeToolArgs: true}
}

func requireMessagesModel(provider string, modelSpec corellm.ModelSpec) error {
	if !modelcatalog.SupportsAPI(modelSpec, "anthropic-messages") {
		return fmt.Errorf("%s model %q does not expose Anthropic Messages in modeldb", provider, modelSpec.Ref.Name)
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

func validateReasoningEffort(provider string, modelSpec corellm.ModelSpec, effort string) error {
	effort = normalizeLower(effort)
	if effort == "" {
		return nil
	}
	supported := supportedReasoningEfforts(provider, modelSpec)
	if len(supported) == 0 {
		return fmt.Errorf("%s model %q does not expose reasoning efforts in modeldb", provider, modelSpec.Ref.Name)
	}
	for _, value := range supported {
		if value == effort {
			return nil
		}
	}
	return fmt.Errorf("%s model %q does not support reasoning effort %q; supported: %s", provider, modelSpec.Ref.Name, effort, strings.Join(supported, ", "))
}

func supportedReasoningEfforts(provider string, modelSpec corellm.ModelSpec) []string {
	key := "modeldb.openai_responses.reasoning_efforts"
	if isAnthropicMessagesProvider(provider) {
		key = "modeldb.anthropic_messages.reasoning_efforts"
	}
	return csvValues(modelSpec.Annotations[key])
}

func isAnthropicMessagesProvider(provider string) bool {
	switch provider {
	case "anthropic", "claudecode", "minimax":
		return true
	default:
		return false
	}
}

func defaultReasoningSummary(provider string, modelSpec corellm.ModelSpec) string {
	if provider == "openrouter" {
		_, summary := OpenRouterReasoningDefaults(modelSpec)
		return summary
	}
	return "auto"
}

func normalizeThinkingMode(value string, allowConfigAliases bool) (string, error) {
	switch normalizeLower(value) {
	case "":
		return "", nil
	case "auto", "on", "off":
		return normalizeLower(value), nil
	case "enabled":
		if allowConfigAliases {
			return "on", nil
		}
	}
	if allowConfigAliases {
		return "", fmt.Errorf("invalid thinking mode %q; expected auto|on|off|enabled", value)
	}
	return "", fmt.Errorf("invalid thinking mode %q; expected auto|on|off", value)
}

func csvValues(csv string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range strings.Split(csv, ",") {
		value = normalizeLower(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func normalizeLower(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
