package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/core/workflow"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/session"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

// Config describes app composition input.
type Config struct {
	Context           context.Context
	Agent             agent.Agent
	Operations        []operation.Operation
	ContextProviders  []corecontext.Provider
	EventTypes        []event.Event
	Plugins           []pluginhost.Plugin
	Bundles           []resource.ContributionBundle
	OperationExecutor operationruntime.Executor
}

// Composition is executable runtime configuration assembled from resources and
// provided implementations.
type Composition struct {
	Agent                agent.Agent
	Commands             *command.Registry
	Operations           *operation.Registry
	ResourceIndex        *resource.ResourceIndex
	Resolver             *resource.Resolver
	AppCatalog           AppCatalog
	AgentCatalog         AgentCatalog
	SkillCatalog         SkillCatalog
	ContextProviders     ContextProviderCatalog
	ContextProviderImpls []corecontext.Provider
	DatasourceProviders  []coredatasource.Provider
	DatasourceCatalog    DatasourceCatalog
	LLMProviderCatalog   LLMProviderCatalog
	LLMModelAliasCatalog LLMModelAliasCatalog
	WorkflowCatalog      WorkflowCatalog
	OperationSetCatalog  OperationSetCatalog
	ToolSetCatalog       session.ToolSetCatalog
	CommandCatalog       session.CommandCatalog
	OperationCatalog     session.OperationCatalog
	SessionCatalog       session.SessionCatalog
	AppSpecs             []coreapp.Spec
	AgentSpecs           []agent.Spec
	SkillSpecs           []skill.Spec
	ContextSpecs         []corecontext.ProviderSpec
	DatasourceSpecs      []coredatasource.Spec
	LLMProviderSpecs     []corellm.ProviderSpec
	LLMModelAliases      []corellm.ModelAliasSpec
	WorkflowSpecs        []workflow.Spec
	OperationSets        []operation.Set
	ToolSets             []tool.Set
	OperationSpecs       []operation.Spec
	SessionSpecs         []coresession.Spec
	OperationExecutor    operationruntime.Executor
	EventRegistry        *event.Registry
	Bundles              []resource.ContributionBundle
	Diagnostics          []resource.Diagnostic
}

// Compose validates and registers resource contributions with supplied and
// plugin-contributed runtime implementations. Resource operation specs are
// declarations; executable operation implementations come from host or plugin
// code.
func Compose(cfg Config) (Composition, error) {
	bundles, pluginOperations, pluginContextProviders, pluginDatasourceProviders, diagnostics, err := resolvePluginContributions(cfg.Context, cfg.Bundles, cfg.Plugins)
	if err != nil {
		return Composition{Diagnostics: diagnostics}, err
	}
	for _, bundle := range bundles {
		diagnostics = append(diagnostics, bundle.Diagnostics...)
	}
	eventRegistry, err := NewEventRegistry(EventRegistryConfig{Bundles: bundles, EventTypes: cfg.EventTypes})
	if err != nil {
		diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
		return Composition{Diagnostics: diagnostics}, err
	}

	index := resource.NewResourceIndex()
	resolver := resource.NewResolver(resource.ResolverConfig{Index: index})

	appCatalog, appSpecs, appDiagnostic, err := collectApps(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, appDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	agentCatalog, agentSpecs, agentDiagnostic, err := collectAgents(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, agentDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	skillCatalog, skillSpecs, skillDiagnostic, err := collectSkills(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, skillDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	contextCatalog, contextSpecs, contextDiagnostic, err := collectContextProviders(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, contextDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	datasourceCatalog, datasourceSpecs, datasourceDiagnostic, err := collectDatasources(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, datasourceDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	llmProviderCatalog, llmProviderSpecs, llmProviderDiagnostic, err := collectLLMProviders(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, llmProviderDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	llmModelAliasCatalog, llmModelAliases, llmModelAliasDiagnostic, err := collectLLMModelAliases(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, llmModelAliasDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	workflowCatalog, workflowSpecs, workflowDiagnostic, err := collectWorkflows(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, workflowDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	operationSetCatalog, operationSets, operationSetDiagnostic, err := collectOperationSets(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, operationSetDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	toolSetCatalog, toolSets, toolSetDiagnostic, err := collectToolSets(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, toolSetDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}

	operationSpecContributions, opSpecDiagnostic, err := collectOperationSpecs(bundles)
	if err != nil {
		diagnostics = append(diagnostics, opSpecDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	operationSpecs := make([]operation.Spec, 0, len(operationSpecContributions))
	for _, contribution := range operationSpecContributions {
		index.Add(contribution.ID)
		operationSpecs = append(operationSpecs, contribution.Spec)
	}

	operationCatalog := session.OperationCatalog{}
	operationByName := map[operation.Name][]operation.Operation{}
	for _, op := range cfg.Operations {
		id := resource.DeriveResourceID(resource.SourceRef{Scope: resource.ScopeExplicit}, "operation", string(op.Spec().Ref.Name))
		if err := addOperation(operationCatalog, index, id, op); err != nil {
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
		operationByName[op.Spec().Ref.Name] = append(operationByName[op.Spec().Ref.Name], op)
	}
	for _, op := range pluginOperations {
		id := resource.DeriveResourceID(op.Source, "operation", string(op.Operation.Spec().Ref.Name))
		if err := addOperation(operationCatalog, index, id, op.Operation); err != nil {
			diagnostics = append(diagnostics, diagnostic(op.Source, err))
			return Composition{Diagnostics: diagnostics}, err
		}
		operationByName[op.Operation.Spec().Ref.Name] = append(operationByName[op.Operation.Spec().Ref.Name], op.Operation)
	}

	operations := operation.NewRegistry()
	for name, candidates := range operationByName {
		if len(candidates) != 1 {
			continue
		}
		if err := operations.Register(candidates[0]); err != nil {
			err := fmt.Errorf("app: register operation %q: %w", name, err)
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
	}

	sessionCatalog, sessionSpecs, sessionDiagnostic, err := collectSessions(bundles, appCatalog, resolver, index)
	if err != nil {
		diagnostics = append(diagnostics, sessionDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}

	commands := command.NewRegistry()
	commandCatalog := session.CommandCatalog{}
	pathCounts := map[string]int{}
	for _, bundle := range bundles {
		for _, spec := range bundle.Commands {
			id := resource.DeriveResourceID(bundle.Source, "command", commandName(spec))
			index.Add(id)
			targetID, operationID, err := resolveCommandTarget(resolver, operationCatalog, id, spec)
			if err != nil {
				diagnostics = append(diagnostics, diagnostic(bundle.Source, err))
				return Composition{Diagnostics: diagnostics}, err
			}
			if err := addCommand(commandCatalog, id, session.CommandBinding{
				ID:          id,
				Spec:        spec,
				TargetID:    targetID,
				OperationID: operationID,
			}); err != nil {
				diagnostics = append(diagnostics, diagnostic(bundle.Source, err))
				return Composition{Diagnostics: diagnostics}, err
			}
			pathCounts[spec.Path.String()]++
		}
	}
	for _, binding := range commandCatalog {
		if pathCounts[binding.Spec.Path.String()] != 1 {
			continue
		}
		if err := commands.Register(binding.Spec); err != nil {
			err := fmt.Errorf("app: register command %s: %w", binding.Spec.Path.String(), err)
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{ID: binding.ID.Address()}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
	}

	return Composition{
		Agent:                cfg.Agent,
		Commands:             commands,
		Operations:           operations,
		ResourceIndex:        index,
		Resolver:             resolver,
		AppCatalog:           appCatalog,
		AgentCatalog:         agentCatalog,
		SkillCatalog:         skillCatalog,
		ContextProviders:     contextCatalog,
		ContextProviderImpls: append(append([]corecontext.Provider(nil), cfg.ContextProviders...), pluginContextProviders...),
		DatasourceProviders:  pluginDatasourceProviders,
		DatasourceCatalog:    datasourceCatalog,
		LLMProviderCatalog:   llmProviderCatalog,
		LLMModelAliasCatalog: llmModelAliasCatalog,
		WorkflowCatalog:      workflowCatalog,
		OperationSetCatalog:  operationSetCatalog,
		ToolSetCatalog:       toolSetCatalog,
		CommandCatalog:       commandCatalog,
		OperationCatalog:     operationCatalog,
		SessionCatalog:       sessionCatalog,
		AppSpecs:             appSpecs,
		AgentSpecs:           agentSpecs,
		SkillSpecs:           skillSpecs,
		ContextSpecs:         contextSpecs,
		DatasourceSpecs:      datasourceSpecs,
		LLMProviderSpecs:     llmProviderSpecs,
		LLMModelAliases:      llmModelAliases,
		WorkflowSpecs:        workflowSpecs,
		OperationSets:        operationSets,
		ToolSets:             toolSets,
		OperationSpecs:       operationSpecs,
		SessionSpecs:         sessionSpecs,
		OperationExecutor:    cfg.OperationExecutor,
		EventRegistry:        eventRegistry,
		Bundles:              bundles,
		Diagnostics:          diagnostics,
	}, nil
}

type resourceSelector[T any] func(resource.ContributionBundle) []T
type resourceNameFunc[T any] func(T, resource.SourceRef) string
type resourceValidateFunc[T any] func(T) error

func collectResourceSpecs[T any](
	bundles []resource.ContributionBundle,
	index *resource.ResourceIndex,
	kind string,
	selectSpecs resourceSelector[T],
	nameOf resourceNameFunc[T],
	validate resourceValidateFunc[T],
) (map[string]ResourceBinding[T], []T, resource.Diagnostic, error) {
	catalog := map[string]ResourceBinding[T]{}
	var specs []T
	for _, bundle := range bundles {
		for _, spec := range selectSpecs(bundle) {
			if validate != nil {
				if err := validate(spec); err != nil {
					err := fmt.Errorf("app: %s spec: %w", kind, err)
					return nil, nil, diagnostic(bundle.Source, err), err
				}
			}
			id := resource.DeriveResourceID(bundle.Source, kind, nameOf(spec, bundle.Source))
			if id.Name == "" {
				err := fmt.Errorf("app: %s resource id name is empty", kind)
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			if previous, exists := catalog[id.Address()]; exists {
				err := fmt.Errorf("app: duplicate %s resource %q from %s and %s", kind, id.Address(), sourceLabel(previous.Source), sourceLabel(bundle.Source))
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			catalog[id.Address()] = ResourceBinding[T]{ID: id, Source: bundle.Source, Spec: spec}
			index.Add(id)
			specs = append(specs, spec)
		}
	}
	return catalog, specs, resource.Diagnostic{}, nil
}

func collectApps(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (AppCatalog, []coreapp.Spec, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"app",
		func(bundle resource.ContributionBundle) []coreapp.Spec { return bundle.Apps },
		func(spec coreapp.Spec, source resource.SourceRef) string { return appResourceName(spec, source) },
		func(spec coreapp.Spec) error { return spec.Validate() },
	)
	return AppCatalog(catalog), specs, diag, err
}

func collectAgents(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (AgentCatalog, []agent.Spec, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"agent",
		func(bundle resource.ContributionBundle) []agent.Spec { return bundle.Agents },
		func(spec agent.Spec, _ resource.SourceRef) string { return string(spec.Name) },
		func(spec agent.Spec) error { return spec.Validate() },
	)
	return AgentCatalog(catalog), specs, diag, err
}

func collectSkills(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (SkillCatalog, []skill.Spec, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"skill",
		func(bundle resource.ContributionBundle) []skill.Spec { return bundle.Skills },
		func(spec skill.Spec, _ resource.SourceRef) string { return string(spec.Name) },
		func(spec skill.Spec) error { return spec.Validate() },
	)
	return SkillCatalog(catalog), specs, diag, err
}

func collectContextProviders(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (ContextProviderCatalog, []corecontext.ProviderSpec, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"context_provider",
		func(bundle resource.ContributionBundle) []corecontext.ProviderSpec { return bundle.ContextProviders },
		func(spec corecontext.ProviderSpec, _ resource.SourceRef) string { return string(spec.Name) },
		func(spec corecontext.ProviderSpec) error { return spec.Validate() },
	)
	return ContextProviderCatalog(catalog), specs, diag, err
}

func collectDatasources(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (DatasourceCatalog, []coredatasource.Spec, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"datasource",
		func(bundle resource.ContributionBundle) []coredatasource.Spec { return bundle.Datasources },
		func(spec coredatasource.Spec, _ resource.SourceRef) string { return string(spec.Name) },
		func(spec coredatasource.Spec) error { return spec.Validate() },
	)
	return DatasourceCatalog(catalog), specs, diag, err
}

func collectLLMProviders(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (LLMProviderCatalog, []corellm.ProviderSpec, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"llm_provider",
		func(bundle resource.ContributionBundle) []corellm.ProviderSpec { return bundle.LLMProviders },
		func(spec corellm.ProviderSpec, _ resource.SourceRef) string { return string(spec.Name) },
		func(spec corellm.ProviderSpec) error { return spec.Validate() },
	)
	return LLMProviderCatalog(catalog), specs, diag, err
}

func collectLLMModelAliases(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (LLMModelAliasCatalog, []corellm.ModelAliasSpec, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"llm_model_alias",
		func(bundle resource.ContributionBundle) []corellm.ModelAliasSpec { return bundle.LLMModelAliases },
		func(spec corellm.ModelAliasSpec, _ resource.SourceRef) string { return spec.Name },
		func(spec corellm.ModelAliasSpec) error { return spec.Validate() },
	)
	return LLMModelAliasCatalog(catalog), specs, diag, err
}

func collectWorkflows(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (WorkflowCatalog, []workflow.Spec, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"workflow",
		func(bundle resource.ContributionBundle) []workflow.Spec { return bundle.Workflows },
		func(spec workflow.Spec, _ resource.SourceRef) string { return string(spec.Name) },
		func(spec workflow.Spec) error { return spec.Validate() },
	)
	return WorkflowCatalog(catalog), specs, diag, err
}

func collectOperationSets(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (OperationSetCatalog, []operation.Set, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"operation_set",
		func(bundle resource.ContributionBundle) []operation.Set { return bundle.OperationSets },
		func(spec operation.Set, _ resource.SourceRef) string { return spec.Name },
		func(spec operation.Set) error { return spec.Validate() },
	)
	return OperationSetCatalog(catalog), specs, diag, err
}

func collectToolSets(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (session.ToolSetCatalog, []tool.Set, resource.Diagnostic, error) {
	catalog := session.ToolSetCatalog{}
	var specs []tool.Set
	for _, bundle := range bundles {
		for _, spec := range bundle.ToolSets {
			if err := spec.Validate(); err != nil {
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			id := resource.DeriveResourceID(bundle.Source, "tool_set", spec.Name)
			index.Add(id)
			if _, exists := catalog[id.Address()]; exists {
				err := fmt.Errorf("duplicate tool_set resource %q", id.Address())
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			catalog[id.Address()] = session.ToolSetBinding{ID: id, Spec: spec}
			specs = append(specs, spec)
		}
	}
	return catalog, specs, resource.Diagnostic{}, nil
}

func collectSessions(
	bundles []resource.ContributionBundle,
	apps AppCatalog,
	resolver *resource.Resolver,
	index *resource.ResourceIndex,
) (session.SessionCatalog, []coresession.Spec, resource.Diagnostic, error) {
	catalog := session.SessionCatalog{}
	var specs []coresession.Spec
	for _, bundle := range bundles {
		for _, spec := range bundle.Sessions {
			id := resource.DeriveResourceID(bundle.Source, "session", string(spec.Name))
			if err := addSession(catalog, index, id, spec); err != nil {
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			specs = append(specs, spec)
		}
	}
	for _, appBinding := range apps {
		spec, ok, err := defaultSessionSpec(appBinding, resolver)
		if err != nil {
			return nil, nil, diagnostic(appBinding.Source, err), err
		}
		if !ok {
			continue
		}
		id := resource.ResourceID{
			Kind:      "session",
			Origin:    appBinding.ID.Origin,
			Namespace: appBinding.ID.Namespace,
			Name:      string(spec.Name),
		}
		if _, exists := catalog[id.Address()]; exists {
			continue
		}
		if err := addSession(catalog, index, id, spec); err != nil {
			return nil, nil, diagnostic(appBinding.Source, err), err
		}
		specs = append(specs, spec)
	}
	for _, appBinding := range apps {
		if appBinding.Spec.DefaultSession.Name == "" {
			continue
		}
		if _, err := resolver.ResolveInScope("session", string(appBinding.Spec.DefaultSession.Name), appBinding.ID); err != nil {
			err := fmt.Errorf("app: default session %q for app %s: %w", appBinding.Spec.DefaultSession.Name, appBinding.ID.Address(), err)
			return nil, nil, diagnostic(appBinding.Source, err), err
		}
	}
	return catalog, specs, resource.Diagnostic{}, nil
}

func addSession(catalog session.SessionCatalog, index *resource.ResourceIndex, id resource.ResourceID, spec coresession.Spec) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("app: session spec: %w", err)
	}
	if id.Name == "" {
		return fmt.Errorf("app: session resource id name is empty")
	}
	if _, exists := catalog[id.Address()]; exists {
		return fmt.Errorf("app: duplicate session resource %q", id.Address())
	}
	catalog[id.Address()] = session.SessionBinding{ID: id, Spec: spec}
	index.Add(id)
	return nil
}

func appResourceName(spec coreapp.Spec, source resource.SourceRef) string {
	if name := strings.TrimSpace(string(spec.Name)); name != "" {
		return name
	}
	if namespace := resource.DeriveNamespace(source); namespace.Last() != "" {
		return namespace.Last()
	}
	if source.ID != "" {
		return source.ID
	}
	if source.Ref != "" {
		return source.Ref
	}
	return "app"
}

func defaultSessionSpec(appBinding ResourceBinding[coreapp.Spec], resolver *resource.Resolver) (coresession.Spec, bool, error) {
	appSpec := appBinding.Spec
	if appSpec.DefaultAgent.Name == "" {
		return coresession.Spec{}, false, nil
	}
	if _, err := resolver.ResolveInScope("agent", string(appSpec.DefaultAgent.Name), appBinding.ID); err != nil {
		return coresession.Spec{}, false, fmt.Errorf("app: default agent %q for app %s: %w", appSpec.DefaultAgent.Name, appBinding.ID.Address(), err)
	}
	name := strings.TrimSpace(string(appSpec.DefaultSession.Name))
	if name == "" {
		name = "default"
	}
	description := "Default session for " + string(appSpec.DefaultAgent.Name)
	if appSpec.Description != "" {
		description = appSpec.Description
	}
	return coresession.Spec{
		Name:        coresession.Name(name),
		Description: description,
		Agent:       appSpec.DefaultAgent,
		Metadata: map[string]string{
			"app": appBinding.ID.Address(),
		},
	}, true, nil
}

func resolvePluginContributions(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin) ([]resource.ContributionBundle, []pluginhost.OperationContribution, []corecontext.Provider, []coredatasource.Provider, []resource.Diagnostic, error) {
	out := append([]resource.ContributionBundle(nil), bundles...)
	var operations []pluginhost.OperationContribution
	var contextProviders []corecontext.Provider
	var datasourceProviders []coredatasource.Provider
	var diagnostics []resource.Diagnostic
	host, err := pluginhost.New(plugins...)
	if err != nil {
		diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
		return out, operations, contextProviders, datasourceProviders, diagnostics, err
	}
	for _, bundle := range bundles {
		if len(bundle.Plugins) == 0 {
			continue
		}
		contributed, err := host.Resolve(ctx, bundle.Plugins...)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(bundle.Source, err))
			return out, operations, contextProviders, datasourceProviders, diagnostics, err
		}
		out = append(out, contributed.Bundles...)
		operations = append(operations, contributed.Operations...)
		for _, provider := range contributed.ContextProviders {
			contextProviders = append(contextProviders, provider.Provider)
		}
		for _, provider := range contributed.DatasourceProviders {
			datasourceProviders = append(datasourceProviders, provider.Provider)
		}
	}
	return out, operations, contextProviders, datasourceProviders, diagnostics, nil
}

type operationSpecContribution struct {
	ID   resource.ResourceID
	Spec operation.Spec
}

func collectOperationSpecs(bundles []resource.ContributionBundle) ([]operationSpecContribution, resource.Diagnostic, error) {
	seen := map[string]resource.SourceRef{}
	var specs []operationSpecContribution
	for _, bundle := range bundles {
		for _, spec := range bundle.Operations {
			name := spec.Ref.Name
			if name == "" {
				err := fmt.Errorf("app: operation spec name is empty")
				return nil, diagnostic(bundle.Source, err), err
			}
			id := resource.DeriveResourceID(bundle.Source, "operation", string(name))
			if previous, exists := seen[id.Address()]; exists {
				err := fmt.Errorf("app: duplicate operation spec %q from %s and %s", id.Address(), sourceLabel(previous), sourceLabel(bundle.Source))
				return nil, diagnostic(bundle.Source, err), err
			}
			seen[id.Address()] = bundle.Source
			specs = append(specs, operationSpecContribution{ID: id, Spec: spec})
		}
	}
	return specs, resource.Diagnostic{}, nil
}

func addOperation(catalog session.OperationCatalog, index *resource.ResourceIndex, id resource.ResourceID, op operation.Operation) error {
	if op == nil {
		return fmt.Errorf("app: operation %s is nil", id.Address())
	}
	if id.Name == "" {
		return fmt.Errorf("app: operation resource id name is empty")
	}
	if _, exists := catalog[id.Address()]; exists {
		return fmt.Errorf("app: duplicate operation resource %q", id.Address())
	}
	catalog[id.Address()] = session.OperationBinding{ID: id, Operation: op}
	index.Add(id)
	return nil
}

func addCommand(catalog session.CommandCatalog, id resource.ResourceID, binding session.CommandBinding) error {
	if id.Name == "" {
		return fmt.Errorf("app: command resource id name is empty")
	}
	if _, exists := catalog[id.Address()]; exists {
		return fmt.Errorf("app: duplicate command resource %q", id.Address())
	}
	catalog[id.Address()] = binding
	return nil
}

func resolveCommandTarget(
	resolver *resource.Resolver,
	operations session.OperationCatalog,
	commandID resource.ResourceID,
	spec command.Spec,
) (resource.ResourceID, resource.ResourceID, error) {
	switch spec.Target.Kind {
	case invocation.TargetOperation:
		if spec.Target.Operation.Name == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s targets an empty operation", spec.Path.String())
		}
		binding, err := operations.Resolve(spec.Target.Operation.String(), commandID)
		if err != nil {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target operation: %w", spec.Path.String(), err)
		}
		return binding.ID, binding.ID, nil
	case invocation.TargetWorkflow:
		if strings.TrimSpace(string(spec.Target.Workflow)) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s targets an empty workflow", spec.Path.String())
		}
		id, err := resolver.ResolveInScope("workflow", string(spec.Target.Workflow), commandID)
		if err != nil {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target workflow: %w", spec.Path.String(), err)
		}
		return id, resource.ResourceID{}, nil
	case invocation.TargetAgent:
		if strings.TrimSpace(string(spec.Target.Agent.Name)) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s targets an empty agent", spec.Path.String())
		}
		id, err := resolver.ResolveInScope("agent", string(spec.Target.Agent.Name), commandID)
		if err != nil {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target agent: %w", spec.Path.String(), err)
		}
		return id, resource.ResourceID{}, nil
	case invocation.TargetSession:
		if strings.TrimSpace(spec.Target.Session) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s targets an empty session", spec.Path.String())
		}
		id, err := resolver.ResolveInScope("session", spec.Target.Session, commandID)
		if err != nil {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target session: %w", spec.Path.String(), err)
		}
		return id, resource.ResourceID{}, nil
	case invocation.TargetPrompt:
		if strings.TrimSpace(spec.Target.Prompt) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target prompt is empty", spec.Path.String())
		}
		return resource.ResourceID{}, resource.ResourceID{}, nil
	case invocation.TargetMessage:
		if strings.TrimSpace(spec.Target.Message) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target message is empty", spec.Path.String())
		}
		return resource.ResourceID{}, resource.ResourceID{}, nil
	case "":
		return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target kind is empty", spec.Path.String())
	default:
		return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target kind %q is unsupported", spec.Path.String(), spec.Target.Kind)
	}
}

func commandName(spec command.Spec) string {
	if name := strings.TrimSpace(spec.Annotations["name"]); name != "" {
		return name
	}
	for i := len(spec.Path) - 1; i >= 0; i-- {
		if part := strings.TrimSpace(spec.Path[i]); part != "" {
			return part
		}
	}
	return ""
}

func sourceLabel(source resource.SourceRef) string {
	if source.ID != "" {
		return source.ID
	}
	if source.Location != "" {
		return source.Location
	}
	if source.Ref != "" {
		return source.Ref
	}
	return "unknown source"
}

func diagnostic(source resource.SourceRef, err error) resource.Diagnostic {
	return resource.Diagnostic{
		Severity: resource.SeverityError,
		Source:   source,
		Message:  err.Error(),
	}
}
