// Package sdk contains IO-free convenience builders for app resource specs.
//
// Builders in this package only produce core specs and contribution bundles.
// Runtime implementations, transports, stores, and provider clients are still
// supplied by runtime, orchestration, adapters, or the host application.
package sdk

import (
	"encoding/json"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-policy"
)

// NewApp starts a pure app contribution builder.
func NewApp(name string) *AppBuilder {
	return &AppBuilder{
		source: resource.SourceRef{ID: name, Scope: resource.ScopeEmbedded, Location: name},
		app: coreapp.Spec{
			Name: coreapp.Name(name),
		},
	}
}

// AppBuilder builds one contribution bundle centered on one app spec.
type AppBuilder struct {
	source     resource.SourceRef
	app        coreapp.Spec
	agents     []agent.Spec
	opSets     []operation.Set
	operations []operation.Spec
	commands   []command.Spec
	sessions   []coresession.Spec
	plugins    []resource.PluginRef
}

// WithSource sets the contribution source used for resource IDs.
func (b *AppBuilder) WithSource(source resource.SourceRef) *AppBuilder {
	if b == nil {
		return b
	}
	b.source = source
	return b
}

// WithDescription sets the app description.
func (b *AppBuilder) WithDescription(description string) *AppBuilder {
	if b == nil {
		return b
	}
	b.app.Description = description
	return b
}

// WithModel sets the app-level model policy.
func (b *AppBuilder) WithModel(provider, model, useCase string) *AppBuilder {
	if b == nil {
		return b
	}
	b.app.Model = coreapp.ModelPolicy{
		Provider: provider,
		Model:    model,
		UseCase:  useCase,
	}
	return b
}

// WithDefaultAgent adds an agent spec and marks it as the app default.
func (b *AppBuilder) WithDefaultAgent(spec agent.Spec) *AppBuilder {
	if b == nil {
		return b
	}
	b.agents = append(b.agents, spec)
	b.app.DefaultAgent = agent.Ref{Name: spec.Name}
	return b
}

// WithAgent adds an agent spec.
func (b *AppBuilder) WithAgent(spec agent.Spec) *AppBuilder {
	if b == nil {
		return b
	}
	b.agents = append(b.agents, spec)
	return b
}

// WithOperationSet adds an operation capability set.
func (b *AppBuilder) WithOperationSet(set operation.Set) *AppBuilder {
	if b == nil {
		return b
	}
	b.opSets = append(b.opSets, set)
	return b
}

// WithOperation adds an operation spec.
func (b *AppBuilder) WithOperation(spec operation.Spec) *AppBuilder {
	if b == nil {
		return b
	}
	b.operations = append(b.operations, spec)
	return b
}

// WithPlugin adds a plugin dependency to the app bundle.
func (b *AppBuilder) WithPlugin(ref resource.PluginRef) *AppBuilder {
	if b == nil {
		return b
	}
	b.plugins = append(b.plugins, ref)
	return b
}

// WithCommand adds a command spec.
func (b *AppBuilder) WithCommand(spec command.Spec) *AppBuilder {
	if b == nil {
		return b
	}
	b.commands = append(b.commands, spec)
	return b
}

// WithCommandForOperation adds a command that invokes an operation.
func (b *AppBuilder) WithCommandForOperation(path string, spec operation.Spec, opts ...CommandOption) *AppBuilder {
	if b == nil {
		return b
	}
	b.commands = append(b.commands, CommandForOperation(path, spec, opts...))
	return b
}

// WithDefaultSession adds a session spec and marks it as the app default.
func (b *AppBuilder) WithDefaultSession(spec coresession.Spec) *AppBuilder {
	if b == nil {
		return b
	}
	b.sessions = append(b.sessions, spec)
	b.app.DefaultSession = coresession.Ref{Name: spec.Name}
	return b
}

// WithSession adds a session spec.
func (b *AppBuilder) WithSession(spec coresession.Spec) *AppBuilder {
	if b == nil {
		return b
	}
	b.sessions = append(b.sessions, spec)
	return b
}

// Build returns the normalized contribution bundle.
func (b *AppBuilder) Build() resource.ContributionBundle {
	if b == nil {
		return resource.ContributionBundle{}
	}
	return resource.ContributionBundle{
		Source:        b.source,
		Apps:          []coreapp.Spec{b.app},
		Agents:        append([]agent.Spec(nil), b.agents...),
		OperationSets: append([]operation.Set(nil), b.opSets...),
		Operations:    append([]operation.Spec(nil), b.operations...),
		Commands:      append([]command.Spec(nil), b.commands...),
		Sessions:      append([]coresession.Spec(nil), b.sessions...),
		Plugins:       append([]resource.PluginRef(nil), b.plugins...),
	}
}

// BuildAgent starts an agent spec builder.
func BuildAgent(name string) *AgentBuilder {
	return &AgentBuilder{spec: agent.Spec{Name: agent.Name(name)}}
}

// AgentBuilder builds an inert agent spec.
type AgentBuilder struct {
	spec agent.Spec
}

// WithDescription sets the agent description.
func (b *AgentBuilder) WithDescription(description string) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Description = description
	return b
}

// WithSystem sets the agent system instruction.
func (b *AgentBuilder) WithSystem(system string) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.System = system
	return b
}

// AsLLMAgent configures the generic driver and model hints for an LLM-backed
// runtime agent. It does not bind a provider client.
func (b *AgentBuilder) AsLLMAgent(model string) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Driver = agent.DriverSpec{Kind: "llmagent"}
	b.spec.Inference.Model = model
	return b
}

// WithMaxOutputTokens sets the inert model output budget hint.
func (b *AgentBuilder) WithMaxOutputTokens(tokens int) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Inference.MaxOutputTokens = tokens
	return b
}

// WithMaxSteps sets the inner turn budget for agent/model decision calls. Tool
// executions do not count directly; the decision that requests them does.
func (b *AgentBuilder) WithMaxSteps(max int) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Turns.MaxSteps = max
	return b
}

// WithMaxContinuations sets the outer follow-up budget used after a terminal
// turn when a stop condition asks the runtime to continue.
func (b *AgentBuilder) WithMaxContinuations(max int) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Turns.Continuation.MaxContinuations = max
	if max > 0 && strings.TrimSpace(b.spec.Turns.Continuation.StopCondition.Type) == "" {
		b.spec.Turns.Continuation.StopCondition = agent.StopConditionSpec{Type: "max-continuations", Max: max}
	}
	return b
}

// WithContinuationContextPolicy sets how continuation evaluators receive
// context.
func (b *AgentBuilder) WithContinuationContextPolicy(policy string) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Turns.Continuation.ContextPolicy = policy
	return b
}

// WithStopCondition sets the outer continuation stop condition.
func (b *AgentBuilder) WithStopCondition(condition agent.StopConditionSpec) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Turns.Continuation.StopCondition = condition
	return b
}

// WithPromptStopCondition sets a prompt-based outer continuation stop
// condition.
func (b *AgentBuilder) WithPromptStopCondition(prompt string) *AgentBuilder {
	return b.WithStopCondition(agent.StopConditionSpec{Type: "prompt", Prompt: prompt})
}

// WithMaxContinuationStopCondition sets a deterministic stop condition that
// continues until the given continuation count is reached.
func (b *AgentBuilder) WithMaxContinuationStopCondition(max int) *AgentBuilder {
	return b.WithStopCondition(agent.StopConditionSpec{Type: "max-continuations", Max: max})
}

// WithAgency sets the declarative agency profile.
func (b *AgentBuilder) WithAgency(profile agent.AgencyProfile) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Agency = profile
	return b
}

// WithOperation exposes an operation to the agent by name.
func (b *AgentBuilder) WithOperation(name string) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Operations = append(b.spec.Operations, operation.Ref{Name: operation.Name(name)})
	return b
}

// WithOperations exposes operations to the agent by name.
func (b *AgentBuilder) WithOperations(names ...string) *AgentBuilder {
	for _, name := range names {
		b = b.WithOperation(name)
	}
	return b
}

// WithDatasource grants the agent access to a datasource by name.
func (b *AgentBuilder) WithDatasource(name string) *AgentBuilder {
	if b == nil {
		return b
	}
	b.spec.Datasources = append(b.spec.Datasources, coredatasource.Ref{Name: coredatasource.Name(name)})
	return b
}

// WithDatasources grants the agent access to datasources by name.
func (b *AgentBuilder) WithDatasources(names ...string) *AgentBuilder {
	for _, name := range names {
		b = b.WithDatasource(name)
	}
	return b
}

// Build returns the agent spec.
func (b *AgentBuilder) Build() agent.Spec {
	if b == nil {
		return agent.Spec{}
	}
	return b.spec
}

// BuildOperation starts an operation spec builder.
func BuildOperation(name string) *OperationBuilder {
	return &OperationBuilder{spec: operation.Spec{Ref: operation.Ref{Name: operation.Name(name)}}}
}

// OperationBuilder builds an inert operation spec.
type OperationBuilder struct {
	spec operation.Spec
}

// WithDescription sets the operation description.
func (b *OperationBuilder) WithDescription(description string) *OperationBuilder {
	if b == nil {
		return b
	}
	b.spec.Description = description
	return b
}

// WithInputJSONSchema sets the JSON schema for the operation input.
func (b *OperationBuilder) WithInputJSONSchema(name, description, schema string) *OperationBuilder {
	if b == nil {
		return b
	}
	b.spec.Input = operation.Type{
		Name:        name,
		Description: description,
		Schema: operation.Schema{
			Format: "json-schema",
			Data:   json.RawMessage(schema),
		},
	}
	return b
}

// WithOutput sets the output type metadata.
func (b *OperationBuilder) WithOutput(name string) *OperationBuilder {
	if b == nil {
		return b
	}
	b.spec.Output = operation.Type{Name: name}
	return b
}

// WithSemantics sets the full semantics declaration.
func (b *OperationBuilder) WithSemantics(semantics operation.Semantics) *OperationBuilder {
	if b == nil {
		return b
	}
	b.spec.Semantics = semantics
	return b
}

// WithEffects sets the declared side-effect categories.
func (b *OperationBuilder) WithEffects(effects ...operation.Effect) *OperationBuilder {
	if b == nil {
		return b
	}
	b.spec.Semantics.Effects = operation.EffectSet{}
	for _, effect := range effects {
		b.spec.Semantics.Effects = append(b.spec.Semantics.Effects, effect)
	}
	return b
}

// WithRisk sets the declared operation risk.
func (b *OperationBuilder) WithRisk(risk operation.RiskLevel) *OperationBuilder {
	if b == nil {
		return b
	}
	b.spec.Semantics.Risk = risk
	return b
}

// WithDeterminism sets the declared determinism.
func (b *OperationBuilder) WithDeterminism(determinism operation.Determinism) *OperationBuilder {
	if b == nil {
		return b
	}
	b.spec.Semantics.Determinism = determinism
	return b
}

// WithIdempotency sets the declared idempotency.
func (b *OperationBuilder) WithIdempotency(idempotency operation.Idempotency) *OperationBuilder {
	if b == nil {
		return b
	}
	b.spec.Semantics.Idempotency = idempotency
	return b
}

// Build returns the operation spec.
func (b *OperationBuilder) Build() operation.Spec {
	if b == nil {
		return operation.Spec{}
	}
	return b.spec
}

// CommandOption customizes a generated command spec.
type CommandOption func(*command.Spec)

// WithCommandPolicy sets the generated command invocation policy.
func WithCommandPolicy(policy policy.InvocationPolicy) CommandOption {
	return func(spec *command.Spec) {
		spec.Policy = policy
	}
}

// CommandForOperation returns a command spec that invokes the given operation.
func CommandForOperation(path string, spec operation.Spec, opts ...CommandOption) command.Spec {
	cmd := command.Spec{
		Path:        commandPath(path),
		Description: spec.Description,
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: spec.Ref,
		},
		Input:  spec.Input,
		Output: spec.Output,
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser, policy.CallerAgent},
			RequiredTrust:  policy.TrustVerified,
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cmd)
		}
	}
	return cmd
}

func commandPath(path string) command.Path {
	path = strings.Trim(path, "/ ")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	out := make(command.Path, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
