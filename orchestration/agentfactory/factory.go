package agentfactory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/agent"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/orchestration/agentconfig"
	"github.com/fluxplane/fluxplane-core/orchestration/resourcecatalog"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-core/orchestration/toolprojection"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	runtimeskill "github.com/fluxplane/fluxplane-core/runtime/skill"
	"github.com/fluxplane/fluxplane-skill"
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

// ModelResolution describes a resolved runtime model and its catalog metadata.
type ModelResolution struct {
	Model llmagent.Model
	Spec  corellm.ModelSpec
}

// ModelResolverWithSpec resolves the runtime model together with the inert
// model catalog entry selected for the session.
type ModelResolverWithSpec interface {
	ResolveModelWithSpec(context.Context, agent.Spec) (ModelResolution, error)
}

// Config configures a composed-agent factory.
type Config struct {
	Agents           resourcecatalog.AgentCatalog
	Skills           resourcecatalog.SkillCatalog
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
	agents           resourcecatalog.AgentCatalog
	skills           resourcecatalog.SkillCatalog
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
		return f.buildLLMAgent(ctx, agentconfig.ApplySessionProfile(binding.Spec, profile))
	default:
		return nil, fmt.Errorf("agentfactory: unsupported agent driver kind %q", binding.Spec.Driver.Kind)
	}
}

func (f *Factory) buildLLMAgent(ctx context.Context, spec agent.Spec) (agent.Agent, error) {
	model, modelSpec, hasModelSpec, err := f.resolveModel(ctx, spec)
	if err != nil {
		return nil, err
	}
	if hasModelSpec {
		spec = annotateResolvedModelSpec(spec, modelSpec)
	}
	repo, state, err := f.skillState(spec)
	if err != nil {
		return nil, err
	}
	contextProviders := agentconfig.FilterContextProviders(spec, f.contextProviders)
	if repo != nil && skillContextAllowed(spec) {
		contextProviders = append(contextProviders, runtimeskill.NewContextProvider(repo, state))
	}
	projection := f.projectTools()
	runtimeAgent, err := llmagent.New(
		spec,
		model,
		llmagent.WithTools(agentconfig.FilterTools(spec, projection.Tools)...),
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
	byName := map[string][]resourcecatalog.Binding[skill.Spec]{}
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

func (f *Factory) resolveModel(ctx context.Context, spec agent.Spec) (llmagent.Model, corellm.ModelSpec, bool, error) {
	if f.modelResolver != nil {
		if resolver, ok := f.modelResolver.(ModelResolverWithSpec); ok {
			resolved, err := resolver.ResolveModelWithSpec(ctx, spec)
			if err != nil {
				return nil, corellm.ModelSpec{}, false, fmt.Errorf("agentfactory: resolve model for agent %q: %w", spec.Name, err)
			}
			if resolved.Model == nil {
				return nil, corellm.ModelSpec{}, false, fmt.Errorf("agentfactory: resolve model for agent %q: %w", spec.Name, llmagent.ErrModelMissing)
			}
			return resolved.Model, resolved.Spec, true, nil
		}
		model, err := f.modelResolver.ResolveModel(ctx, spec)
		if err != nil {
			return nil, corellm.ModelSpec{}, false, fmt.Errorf("agentfactory: resolve model for agent %q: %w", spec.Name, err)
		}
		if model == nil {
			return nil, corellm.ModelSpec{}, false, fmt.Errorf("agentfactory: resolve model for agent %q: %w", spec.Name, llmagent.ErrModelMissing)
		}
		return model, corellm.ModelSpec{}, false, nil
	}
	if f.model == nil {
		return nil, corellm.ModelSpec{}, false, fmt.Errorf("agentfactory: resolve model for agent %q: %w", spec.Name, llmagent.ErrModelMissing)
	}
	return f.model, corellm.ModelSpec{}, false, nil
}

func annotateResolvedModelSpec(spec agent.Spec, model corellm.ModelSpec) agent.Spec {
	if spec.Inference.Annotations == nil {
		spec.Inference.Annotations = map[string]string{}
	} else {
		spec.Inference.Annotations = cloneStringMap(spec.Inference.Annotations)
	}
	if model.ContextTokens > 0 {
		spec.Inference.Annotations["llm.context_tokens"] = strconv.FormatInt(model.ContextTokens, 10)
	}
	if model.MaxOutputTokens > 0 {
		spec.Inference.Annotations["llm.max_output_tokens"] = strconv.FormatInt(model.MaxOutputTokens, 10)
	}
	if spec.Inference.Model == "" && model.Ref.Name != "" {
		spec.Inference.Model = string(model.Ref.Name)
	}
	return spec
}

func cloneStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (f *Factory) projectTools() toolprojection.Result {
	cfg := f.projection
	cfg.Commands = f.commandCatalog
	cfg.Operations = f.operationCatalog
	cfg.ToolSets = f.toolSetCatalog
	return toolprojection.ProjectForAgent(cfg)
}
