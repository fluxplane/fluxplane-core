package agentfactory

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/tool"
	appcomposition "github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/orchestration/toolprojection"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

// ModelResolver resolves the provider-neutral model implementation for one
// agent spec. Real provider adapters should implement this boundary later.
type ModelResolver interface {
	ResolveModel(context.Context, agent.Spec) (llmagent.Model, error)
}

// ModelResolverFunc adapts a function into a model resolver.
type ModelResolverFunc func(context.Context, agent.Spec) (llmagent.Model, error)

// ResolveModel calls f.
func (f ModelResolverFunc) ResolveModel(ctx context.Context, spec agent.Spec) (llmagent.Model, error) {
	if f == nil {
		return nil, llmagent.ErrModelMissing
	}
	return f(ctx, spec)
}

// Config configures a composed-agent factory.
type Config struct {
	Agents           appcomposition.AgentCatalog
	Resolver         *resource.Resolver
	CommandCatalog   session.CommandCatalog
	OperationCatalog session.OperationCatalog
	ContextProviders []corecontext.Provider
	Model            llmagent.Model
	ModelResolver    ModelResolver
	StreamPolicy     llmagent.StreamPolicy
	Projection       toolprojection.Config
}

// Factory builds runnable agents from composed agent specs.
type Factory struct {
	agents           appcomposition.AgentCatalog
	resolver         *resource.Resolver
	commandCatalog   session.CommandCatalog
	operationCatalog session.OperationCatalog
	contextProviders []corecontext.Provider
	model            llmagent.Model
	modelResolver    ModelResolver
	streamPolicy     llmagent.StreamPolicy
	projection       toolprojection.Config
}

// New returns an agent factory.
func New(cfg Config) *Factory {
	return &Factory{
		agents:           cfg.Agents,
		resolver:         cfg.Resolver,
		commandCatalog:   cfg.CommandCatalog,
		operationCatalog: cfg.OperationCatalog,
		contextProviders: append([]corecontext.Provider(nil), cfg.ContextProviders...),
		model:            cfg.Model,
		modelResolver:    cfg.ModelResolver,
		streamPolicy:     cfg.StreamPolicy,
		projection:       cfg.Projection,
	}
}

// AgentForSession resolves the session's configured agent and builds a
// runnable implementation.
func (f *Factory) AgentForSession(ctx context.Context, spec coresession.Spec) (agent.Agent, error) {
	if spec.Agent.Name == "" {
		return nil, fmt.Errorf("agentfactory: session %q agent ref is empty", spec.Name)
	}
	return f.Build(ctx, spec.Agent)
}

// Build resolves ref and builds a runnable agent.
func (f *Factory) Build(ctx context.Context, ref agent.Ref) (agent.Agent, error) {
	if f == nil {
		return nil, fmt.Errorf("agentfactory: factory is nil")
	}
	if ref.Name == "" {
		return nil, fmt.Errorf("agentfactory: agent ref is empty")
	}
	if f.resolver == nil {
		return nil, fmt.Errorf("agentfactory: resolver is nil")
	}
	id, err := f.resolver.Resolve("agent", string(ref.Name))
	if err != nil {
		return nil, fmt.Errorf("agentfactory: resolve agent %q: %w", ref.Name, err)
	}
	binding, ok := f.agents[id.Address()]
	if !ok {
		return nil, fmt.Errorf("agentfactory: resolved agent %q is not bound", id.Address())
	}
	switch binding.Spec.Driver.Kind {
	case "", llmagent.DriverKind:
		return f.buildLLMAgent(ctx, binding.Spec)
	default:
		return nil, fmt.Errorf("agentfactory: unsupported agent driver kind %q", binding.Spec.Driver.Kind)
	}
}

func (f *Factory) buildLLMAgent(ctx context.Context, spec agent.Spec) (agent.Agent, error) {
	model, err := f.resolveModel(ctx, spec)
	if err != nil {
		return nil, err
	}
	projection := f.projectTools()
	return llmagent.New(
		spec,
		model,
		llmagent.WithTools(filterTools(spec, projection.Tools)...),
		llmagent.WithContextProviders(filterContextProviders(spec, f.contextProviders)...),
		llmagent.WithStreamPolicy(f.streamPolicy),
	)
}

func (f *Factory) resolveModel(ctx context.Context, spec agent.Spec) (llmagent.Model, error) {
	if f.modelResolver != nil {
		model, err := f.modelResolver.ResolveModel(ctx, spec)
		if err != nil {
			return nil, fmt.Errorf("agentfactory: resolve model for agent %q: %w", spec.Name, err)
		}
		if model == nil {
			return nil, fmt.Errorf("agentfactory: resolve model for agent %q: %w", spec.Name, llmagent.ErrModelMissing)
		}
		return model, nil
	}
	if f.model == nil {
		return nil, fmt.Errorf("agentfactory: resolve model for agent %q: %w", spec.Name, llmagent.ErrModelMissing)
	}
	return f.model, nil
}

func (f *Factory) projectTools() toolprojection.Result {
	cfg := f.projection
	cfg.Commands = f.commandCatalog
	cfg.Operations = f.operationCatalog
	if cfg.Caller.Kind == "" {
		cfg.Caller = policy.Caller{Kind: policy.CallerAgent}
	}
	if cfg.Trust.Kind == "" {
		cfg.Trust.Kind = policy.TrustInvocation
	}
	if cfg.Trust.Level == "" {
		cfg.Trust.Level = policy.TrustVerified
	}
	return toolprojection.Project(cfg)
}

func filterTools(spec agent.Spec, tools []tool.Spec) []tool.Spec {
	if len(spec.Tools) == 0 && len(spec.Commands) == 0 && len(spec.Operations) == 0 {
		return tools
	}
	allowedTools := map[string]struct{}{}
	for _, ref := range spec.Tools {
		if ref.Name != "" {
			allowedTools[ref.Name] = struct{}{}
		}
	}
	allowedCommands := map[string]struct{}{}
	for _, ref := range spec.Commands {
		if ref.Name != "" {
			allowedCommands[ref.Name] = struct{}{}
		}
	}
	allowedOperations := map[operation.Name]struct{}{}
	for _, ref := range spec.Operations {
		if ref.Name != "" {
			allowedOperations[ref.Name] = struct{}{}
		}
	}
	out := make([]tool.Spec, 0, len(tools))
	for _, projected := range tools {
		if toolAllowed(projected, allowedTools, allowedCommands, allowedOperations) {
			out = append(out, projected)
		}
	}
	return out
}

func toolAllowed(projected tool.Spec, tools map[string]struct{}, commands map[string]struct{}, operations map[operation.Name]struct{}) bool {
	if _, ok := tools[string(projected.Name)]; ok {
		return true
	}
	for ref := range commands {
		if refMatches(ref, projected.Annotations["command_id"]) || ref == string(projected.Name) {
			return true
		}
	}
	if _, ok := operations[projected.Target.Operation.Name]; ok {
		return true
	}
	for ref := range operations {
		if refMatches(string(ref), projected.Annotations["operation_id"]) {
			return true
		}
	}
	return false
}

func filterContextProviders(spec agent.Spec, providers []corecontext.Provider) []corecontext.Provider {
	if len(providers) == 0 {
		return nil
	}
	if len(spec.Context) == 0 {
		return append([]corecontext.Provider(nil), providers...)
	}
	allowed := map[corecontext.ProviderName]struct{}{}
	for _, ref := range spec.Context {
		if ref.Name != "" {
			allowed[ref.Name] = struct{}{}
		}
	}
	out := make([]corecontext.Provider, 0, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		if _, ok := allowed[provider.Spec().Name]; ok {
			out = append(out, provider)
		}
	}
	return out
}

func refMatches(ref, address string) bool {
	ref = strings.TrimSpace(ref)
	address = strings.TrimSpace(address)
	if ref == "" || address == "" {
		return false
	}
	return ref == address || strings.HasSuffix(address, ":"+ref)
}
