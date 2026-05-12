package textplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
)

const Name = "text"

// Plugin contributes deterministic text transformation operations.
type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns the text plugin.
func New() Plugin { return Plugin{} }

// Config configures the text plugin contribution.
type Config struct {
	// Commands selects the operations exposed as commands. Empty means all.
	Commands []string `json:"commands,omitempty"`
}

type textOperation struct {
	key         string
	description string
	transform   func(string) string
}

var operations = []textOperation{
	{key: "upper", description: "Convert text to uppercase.", transform: strings.ToUpper},
	{key: "lower", description: "Convert text to lowercase.", transform: strings.ToLower},
	{key: "trim", description: "Trim surrounding whitespace.", transform: strings.TrimSpace},
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{
		Name:        Name,
		Description: "Pure text transformation operations and commands.",
	}
}

// Contributions returns command and operation specs.
func (Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	selected, err := selectedOperations(ctx.Ref)
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	bundle := resource.ContributionBundle{}
	for _, op := range selected {
		spec := op.spec()
		bundle.Operations = append(bundle.Operations, spec)
		bundle.Commands = append(bundle.Commands, command.Spec{
			Path:        command.Path{Name, op.key},
			Description: op.description,
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: spec.Ref,
			},
			Input:  spec.Input,
			Output: spec.Output,
			Policy: policy.InvocationPolicy{
				AllowedCallers: []policy.CallerKind{policy.CallerUser},
				RequiredTrust:  policy.TrustVerified,
			},
		})
	}
	return bundle, nil
}

// Operations returns executable operation implementations.
func (Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	selected, err := selectedOperations(ctx.Ref)
	if err != nil {
		return nil, err
	}
	out := make([]operation.Operation, 0, len(selected))
	for _, selected := range selected {
		op := selected
		out = append(out, operation.New(op.spec(), func(_ operation.Context, input operation.Value) operation.Result {
			text, ok := inputText(input)
			if !ok {
				return operation.Failed("invalid_text_input", "text operation input must be a string", nil)
			}
			return operation.OK(op.transform(text))
		}))
	}
	return out, nil
}

func selectedOperations(ref resource.PluginRef) ([]textOperation, error) {
	cfg, err := decodeConfig(ref.Config)
	if err != nil {
		return nil, err
	}
	if len(cfg.Commands) == 0 {
		return append([]textOperation(nil), operations...), nil
	}
	index := make(map[string]textOperation, len(operations)*2)
	for _, op := range operations {
		index[op.key] = op
		index[string(op.ref().Name)] = op
	}
	selected := make([]textOperation, 0, len(cfg.Commands))
	seen := map[string]struct{}{}
	for _, raw := range cfg.Commands {
		key := strings.TrimSpace(raw)
		op, ok := index[key]
		if !ok {
			return nil, fmt.Errorf("textplugin: unknown command %q", raw)
		}
		if _, exists := seen[op.key]; exists {
			continue
		}
		seen[op.key] = struct{}{}
		selected = append(selected, op)
	}
	return selected, nil
}

func decodeConfig(raw map[string]any) (Config, error) {
	if len(raw) == 0 {
		return Config{}, nil
	}
	for key := range raw {
		if key != "commands" {
			return Config{}, fmt.Errorf("textplugin: unknown config key %q", key)
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return Config{}, fmt.Errorf("textplugin: encode config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("textplugin: decode config: %w", err)
	}
	return cfg, nil
}

func (op textOperation) ref() operation.Ref {
	return operation.Ref{Name: operation.Name(Name + "." + op.key)}
}

func (op textOperation) spec() operation.Spec {
	return operation.Spec{
		Ref:         op.ref(),
		Description: op.description,
		Input: operation.Type{
			Name:        "text",
			Description: "Input text.",
		},
		Output: operation.Type{
			Name:        "text",
			Description: "Transformed text.",
		},
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	}
}

func inputText(input operation.Value) (string, bool) {
	switch v := input.(type) {
	case string:
		return v, true
	case []byte:
		return string(v), true
	case fmt.Stringer:
		return v.String(), true
	default:
		return "", false
	}
}
