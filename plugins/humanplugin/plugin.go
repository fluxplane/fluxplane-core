package humanplugin

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name      = "human"
	ClarifyOp = "clarify"
)

const EventClarificationRequested event.Name = "human.clarification.requested"

// ClarificationRequested asks a channel/UI adapter to collect structured input.
type ClarificationRequested struct {
	Prompt string          `json:"prompt"`
	Schema json.RawMessage `json:"schema,omitempty"`
}

func (ClarificationRequested) EventName() event.Name { return EventClarificationRequested }

// Plugin contributes human-in-the-loop operations.
type Plugin struct {
	clarifier system.Clarifier
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns the human plugin.
func New(clarifier system.Clarifier) Plugin { return Plugin{clarifier: clarifier} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Human clarification operations."}
}

// Contributions returns human specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	spec := clarifySpec()
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Human input operations.", Operations: []operation.Ref{spec.Ref}}},
		Operations:    []operation.Spec{spec},
		Commands: []command.Spec{{
			Path:        command.Path{Name, ClarifyOp},
			Description: spec.Description,
			Target:      invocation.Target{Kind: invocation.TargetOperation, Operation: spec.Ref},
			Input:       spec.Input,
			Output:      spec.Output,
			Policy:      policy.InvocationPolicy{AllowedCallers: []policy.CallerKind{policy.CallerAgent}, RequiredTrust: policy.TrustVerified},
		}},
		EventTypes: []event.Event{ClarificationRequested{}, ClarificationCompleted{}},
	}, nil
}

// Operations returns executable human operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{operationruntime.NewTypedResult[clarifyInput, map[string]any](clarifySpec(), p.clarify)}, nil
}

func clarifySpec() operation.Spec {
	return operationruntime.WithTypedContract[clarifyInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: ClarifyOp},
		Description: "Ask the user for structured clarification using a prompt and optional JSON Schema.",
		Semantics:   operation.Semantics{Determinism: operation.DeterminismNonDeterministic, Effects: operation.EffectSet{operation.EffectReadExternal}, Risk: operation.RiskLow},
	})
}

type clarifyInput struct {
	Prompt   string          `json:"prompt" jsonschema:"description=Question or instruction shown to the user.,required"`
	Schema   json.RawMessage `json:"schema,omitempty" jsonschema:"description=JSON Schema describing the expected answer."`
	Defaults map[string]any  `json:"defaults,omitempty" jsonschema:"description=Optional default values for structured answers."`
}

func (p Plugin) clarify(ctx operation.Context, req clarifyInput) operation.Result {
	if strings.TrimSpace(req.Prompt) == "" {
		return operation.Failed("invalid_clarify_input", "prompt is required", nil)
	}
	ctx.Events().Emit(ClarificationRequested{Prompt: req.Prompt, Schema: req.Schema})
	if p.clarifier == nil {
		return operation.Failed("clarify_not_connected", "clarify requires a channel adapter capable of collecting user input", map[string]any{"prompt": req.Prompt})
	}
	result, err := p.clarifier.Clarify(ctx, system.ClarifyRequest{Prompt: req.Prompt, Schema: req.Schema, Defaults: req.Defaults})
	if err != nil {
		return operation.Failed("clarify_failed", err.Error(), map[string]any{"prompt": req.Prompt})
	}
	out := map[string]any{"answer": result.Answer}
	ctx.Events().Emit(ClarificationCompleted{Prompt: req.Prompt, Answer: result.Answer})
	return operation.OK(operation.Rendered{Text: renderAnswer(result.Answer), Data: out})
}

const EventClarificationCompleted event.Name = "human.clarification.completed"

// ClarificationCompleted records collected human input.
type ClarificationCompleted struct {
	Prompt string `json:"prompt"`
	Answer any    `json:"answer,omitempty"`
}

func (ClarificationCompleted) EventName() event.Name { return EventClarificationCompleted }

func renderAnswer(answer any) string {
	switch value := answer.(type) {
	case string:
		return value
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}
