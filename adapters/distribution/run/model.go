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
	DefaultProvider string
	DefaultModel    string
	Debug           bool
	ProviderSpecs   []corellm.ProviderSpec
}

func (r ModelResolver) ResolveModel(_ context.Context, spec agent.Spec) (llmagent.Model, error) {
	registry, err := DefaultModelRegistry(r.ProviderSpecs...)
	if err != nil {
		return nil, err
	}
	selection := registry.ResolveModelSelection(firstNonEmptyString(r.Provider, r.DefaultProvider, "openai"), firstNonEmptyString(r.Model, spec.Inference.Model, r.DefaultModel))
	return registry.NewModel(selection, r.Debug)
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
