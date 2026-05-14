package agentfactory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/tool"
	appcomposition "github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/orchestration/toolprojection"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	runtimeskill "github.com/fluxplane/agentruntime/runtime/skill"
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
	Skills           appcomposition.SkillCatalog
	Resolver         *resource.Resolver
	CommandCatalog   session.CommandCatalog
	OperationCatalog session.OperationCatalog
	ToolSetCatalog   session.ToolSetCatalog
	ContextProviders []corecontext.Provider
	Model            llmagent.Model
	ModelResolver    ModelResolver
	StreamPolicy     llmagent.StreamPolicy
	Projection       toolprojection.Config
}

// Factory builds runnable agents from composed agent specs.
type Factory struct {
	agents           appcomposition.AgentCatalog
	skills           appcomposition.SkillCatalog
	resolver         *resource.Resolver
	commandCatalog   session.CommandCatalog
	operationCatalog session.OperationCatalog
	toolSetCatalog   session.ToolSetCatalog
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
		skills:           cfg.Skills,
		resolver:         cfg.Resolver,
		commandCatalog:   cfg.CommandCatalog,
		operationCatalog: cfg.OperationCatalog,
		toolSetCatalog:   cfg.ToolSetCatalog,
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
	return f.build(ctx, spec.Agent, spec)
}

// Build resolves ref and builds a runnable agent.
func (f *Factory) Build(ctx context.Context, ref agent.Ref) (agent.Agent, error) {
	return f.build(ctx, ref, coresession.Spec{})
}

func (f *Factory) build(ctx context.Context, ref agent.Ref, profile coresession.Spec) (agent.Agent, error) {
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
		return f.buildLLMAgent(ctx, applySessionProfile(binding.Spec, profile))
	default:
		return nil, fmt.Errorf("agentfactory: unsupported agent driver kind %q", binding.Spec.Driver.Kind)
	}
}

func (f *Factory) buildLLMAgent(ctx context.Context, spec agent.Spec) (agent.Agent, error) {
	model, err := f.resolveModel(ctx, spec)
	if err != nil {
		return nil, err
	}
	repo, state, err := f.skillState(spec)
	if err != nil {
		return nil, err
	}
	contextProviders := filterContextProviders(spec, f.contextProviders)
	if repo != nil && skillContextAllowed(spec) {
		contextProviders = append(contextProviders, runtimeskill.NewContextProvider(repo, state))
	}
	projection := f.projectTools()
	runtimeAgent, err := llmagent.New(
		spec,
		model,
		llmagent.WithTools(filterTools(spec, projection.Tools)...),
		llmagent.WithContextProviders(contextProviders...),
		llmagent.WithStreamPolicy(f.streamPolicy),
	)
	if err != nil {
		return nil, err
	}
	return runtimeskill.WrapAgent(runtimeAgent, state), nil
}

func skillContextAllowed(spec agent.Spec) bool {
	if spec.Context == nil {
		return true
	}
	for _, ref := range spec.Context {
		if ref.Name == runtimeskill.ContextProviderName {
			return true
		}
	}
	return false
}

func (f *Factory) skillState(spec agent.Spec) (*runtimeskill.Repository, *runtimeskill.ActivationState, error) {
	if len(f.skills) == 0 {
		if len(spec.Skills) == 0 {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("agentfactory: agent %q references skills but no skills are composed", spec.Name)
	}
	skillSpecs, err := f.resolvedSkillSpecs()
	if err != nil {
		return nil, nil, fmt.Errorf("agentfactory: skill repository: %w", err)
	}
	repo, err := runtimeskill.NewRepository(skillSpecs)
	if err != nil {
		return nil, nil, fmt.Errorf("agentfactory: skill repository: %w", err)
	}
	state, err := runtimeskill.NewActivationState(repo, spec.Skills)
	if err != nil {
		return nil, nil, fmt.Errorf("agentfactory: skills for agent %q: %w", spec.Name, err)
	}
	return repo, state, nil
}

func (f *Factory) resolvedSkillSpecs() ([]skill.Spec, error) {
	byName := map[string][]appcomposition.ResourceBinding[skill.Spec]{}
	for _, binding := range f.skills {
		name := strings.TrimSpace(string(binding.Spec.Name))
		if name == "" {
			continue
		}
		byName[name] = append(byName[name], binding)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]skill.Spec, 0, len(names))
	for _, name := range names {
		candidates := byName[name]
		if len(candidates) == 1 {
			out = append(out, candidates[0].Spec)
			continue
		}
		if f.resolver == nil {
			return nil, fmt.Errorf("duplicate skill %q and resolver is nil", name)
		}
		id, err := f.resolver.Resolve("skill", name)
		if err != nil {
			return nil, err
		}
		var found bool
		for _, binding := range candidates {
			if binding.ID.Equal(id) {
				out = append(out, binding.Spec)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("resolved skill %q to unbound resource %s", name, id.Address())
		}
	}
	return out, nil
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
	cfg.ToolSets = f.toolSetCatalog
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
	if len(spec.Tools) == 0 && spec.Commands == nil && len(spec.Operations) == 0 {
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
	if dispatchAllowedByOperations(projected.Dispatch, operations) {
		return true
	}
	for ref := range operations {
		if refMatches(string(ref), projected.Annotations["operation_id"]) {
			return true
		}
	}
	return false
}

func dispatchAllowedByOperations(dispatch *tool.Dispatch, operations map[operation.Name]struct{}) bool {
	if dispatch == nil || len(dispatch.Cases) == 0 || len(operations) == 0 {
		return false
	}
	for _, candidate := range dispatch.Cases {
		if candidate.Target.Kind != invocation.TargetOperation || candidate.Target.Operation.Name == "" {
			return false
		}
		if _, ok := operations[candidate.Target.Operation.Name]; !ok {
			return false
		}
	}
	return true
}

func filterContextProviders(spec agent.Spec, providers []corecontext.Provider) []corecontext.Provider {
	if len(providers) == 0 {
		return nil
	}
	if spec.Context == nil {
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
		providerSpec := provider.Spec()
		if providerSpec.Annotations[corecontext.AnnotationAutoContext] == "true" {
			out = append(out, provider)
			continue
		}
		if _, ok := allowed[providerSpec.Name]; ok {
			out = append(out, provider)
		}
	}
	return out
}

func applySessionProfile(spec agent.Spec, profile coresession.Spec) agent.Spec {
	if profile.Context != nil {
		spec.Context = narrowAgentContext(spec.Context, profile.Context)
	}
	if profile.Commands != nil {
		spec.Commands = narrowAgentCommands(spec.Commands, profile.Commands)
	}
	if profile.Operations != nil {
		spec.Operations = narrowAgentOperations(spec.Operations, profile.Operations)
	}
	return spec
}

func narrowAgentContext(base []corecontext.ProviderRef, caps []corecontext.ProviderRef) []corecontext.ProviderRef {
	if base == nil {
		return append([]corecontext.ProviderRef(nil), caps...)
	}
	allowed := map[corecontext.ProviderName]struct{}{}
	for _, ref := range caps {
		if ref.Name != "" {
			allowed[ref.Name] = struct{}{}
		}
	}
	out := make([]corecontext.ProviderRef, 0, len(base))
	for _, ref := range base {
		if _, ok := allowed[ref.Name]; ok {
			out = append(out, ref)
		}
	}
	return out
}

func narrowAgentCommands(base []agent.CommandRef, caps []command.Path) []agent.CommandRef {
	if base == nil {
		out := make([]agent.CommandRef, 0, len(caps))
		for _, path := range caps {
			if ref := commandPathRef(path); ref != "" {
				out = append(out, agent.CommandRef{Name: ref})
			}
		}
		return out
	}
	allowed := map[string]struct{}{}
	for _, path := range caps {
		if ref := commandPathRef(path); ref != "" {
			allowed[ref] = struct{}{}
		}
		if display := path.String(); display != "" {
			allowed[display] = struct{}{}
		}
	}
	out := make([]agent.CommandRef, 0, len(base))
	for _, ref := range base {
		if commandRefAllowed(ref.Name, allowed) {
			out = append(out, ref)
		}
	}
	return out
}

func commandRefAllowed(ref string, allowed map[string]struct{}) bool {
	if _, ok := allowed[ref]; ok {
		return true
	}
	for candidate := range allowed {
		if refMatches(candidate, ref) || refMatches(ref, candidate) {
			return true
		}
	}
	return false
}

func narrowAgentOperations(base []operation.Ref, caps []operation.Ref) []operation.Ref {
	if base == nil {
		return append([]operation.Ref(nil), caps...)
	}
	allowed := map[operation.Name]struct{}{}
	for _, ref := range caps {
		if ref.Name != "" {
			allowed[ref.Name] = struct{}{}
		}
	}
	out := make([]operation.Ref, 0, len(base))
	for _, ref := range base {
		if _, ok := allowed[ref.Name]; ok {
			out = append(out, ref)
		}
	}
	return out
}

func commandPathRef(path command.Path) string {
	if len(path) == 0 {
		return ""
	}
	parts := make([]string, 0, len(path))
	for _, part := range path {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ":") + ":" + parts[len(parts)-1]
}

func refMatches(ref, address string) bool {
	ref = strings.TrimSpace(ref)
	address = strings.TrimSpace(address)
	if ref == "" || address == "" {
		return false
	}
	return ref == address || strings.HasSuffix(address, ":"+ref)
}
