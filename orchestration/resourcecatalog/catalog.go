// Package resourcecatalog indexes inert resource specs contributed by bundles.
package resourcecatalog

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/language"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/workflow"
)

// Binding binds an inert resource spec to its canonical resource identity.
type Binding[T any] struct {
	ID     resource.ResourceID `json:"id"`
	Source resource.SourceRef  `json:"source,omitempty"`
	Spec   T                   `json:"spec"`
}

// AppCatalog indexes application manifests by canonical resource ID address.
type AppCatalog map[string]Binding[coreapp.Spec]

// AgentCatalog indexes agent specs by canonical resource ID address.
type AgentCatalog map[string]Binding[agent.Spec]

// SkillCatalog indexes skill specs by canonical resource ID address.
type SkillCatalog map[string]Binding[skill.Spec]

// ContextProviderCatalog indexes context provider specs by canonical resource
// ID address.
type ContextProviderCatalog map[string]Binding[corecontext.ProviderSpec]

// DatasourceCatalog indexes datasource specs by canonical resource ID address.
type DatasourceCatalog map[string]Binding[coredatasource.Spec]

// LLMProviderCatalog indexes LLM provider specs by canonical resource ID
// address.
type LLMProviderCatalog map[string]Binding[corellm.ProviderSpec]

// LLMModelAliasCatalog indexes model alias specs by canonical resource ID
// address.
type LLMModelAliasCatalog map[string]Binding[corellm.ModelAliasSpec]

// WorkflowCatalog indexes workflow specs by canonical resource ID address.
type WorkflowCatalog map[string]Binding[workflow.Spec]

// OperationSetCatalog indexes named operation capability sets.
type OperationSetCatalog map[string]Binding[operation.Set]

// ToolchainCatalog indexes toolchain specs by canonical resource ID address.
type ToolchainCatalog map[string]Binding[language.ToolchainSpec]

// Catalogs groups all inert resource spec catalogs.
type Catalogs struct {
	AppCatalog           AppCatalog
	AgentCatalog         AgentCatalog
	SkillCatalog         SkillCatalog
	ContextProviders     ContextProviderCatalog
	DatasourceCatalog    DatasourceCatalog
	LLMProviderCatalog   LLMProviderCatalog
	LLMModelAliasCatalog LLMModelAliasCatalog
	WorkflowCatalog      WorkflowCatalog
	OperationSetCatalog  OperationSetCatalog
	ToolchainCatalog     ToolchainCatalog
}

// Specs groups the ordered inert specs collected from resource bundles.
type Specs struct {
	AppSpecs         []coreapp.Spec
	AgentSpecs       []agent.Spec
	SkillSpecs       []skill.Spec
	ContextSpecs     []corecontext.ProviderSpec
	DatasourceSpecs  []coredatasource.Spec
	LLMProviderSpecs []corellm.ProviderSpec
	LLMModelAliases  []corellm.ModelAliasSpec
	WorkflowSpecs    []workflow.Spec
	OperationSets    []operation.Set
	Toolchains       []language.ToolchainSpec
}

// Collect validates, indexes, and returns inert resource catalogs and specs.
func Collect(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (Catalogs, Specs, resource.Diagnostic, error) {
	appCatalog, appSpecs, diag, err := collectApps(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	agentCatalog, agentSpecs, diag, err := collectAgents(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	skillCatalog, skillSpecs, diag, err := collectSkills(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	contextCatalog, contextSpecs, diag, err := collectContextProviders(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	datasourceCatalog, datasourceSpecs, diag, err := collectDatasources(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	llmProviderCatalog, llmProviderSpecs, diag, err := collectLLMProviders(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	llmModelAliasCatalog, llmModelAliases, diag, err := collectLLMModelAliases(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	workflowCatalog, workflowSpecs, diag, err := collectWorkflows(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	operationSetCatalog, operationSets, diag, err := collectOperationSets(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	toolchainCatalog, toolchains, diag, err := collectToolchains(bundles, index)
	if err != nil {
		return Catalogs{}, Specs{}, diag, err
	}
	return Catalogs{
			AppCatalog:           appCatalog,
			AgentCatalog:         agentCatalog,
			SkillCatalog:         skillCatalog,
			ContextProviders:     contextCatalog,
			DatasourceCatalog:    datasourceCatalog,
			LLMProviderCatalog:   llmProviderCatalog,
			LLMModelAliasCatalog: llmModelAliasCatalog,
			WorkflowCatalog:      workflowCatalog,
			OperationSetCatalog:  operationSetCatalog,
			ToolchainCatalog:     toolchainCatalog,
		}, Specs{
			AppSpecs:         appSpecs,
			AgentSpecs:       agentSpecs,
			SkillSpecs:       skillSpecs,
			ContextSpecs:     contextSpecs,
			DatasourceSpecs:  datasourceSpecs,
			LLMProviderSpecs: llmProviderSpecs,
			LLMModelAliases:  llmModelAliases,
			WorkflowSpecs:    workflowSpecs,
			OperationSets:    operationSets,
			Toolchains:       toolchains,
		}, resource.Diagnostic{}, nil
}

type selector[T any] func(resource.ContributionBundle) []T
type nameFunc[T any] func(T, resource.SourceRef) string
type validateFunc[T any] func(T) error

func collectResourceSpecs[T any](
	bundles []resource.ContributionBundle,
	index *resource.ResourceIndex,
	kind string,
	selectSpecs selector[T],
	nameOf nameFunc[T],
	validate validateFunc[T],
) (map[string]Binding[T], []T, resource.Diagnostic, error) {
	catalog := map[string]Binding[T]{}
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
			catalog[id.Address()] = Binding[T]{ID: id, Source: bundle.Source, Spec: spec}
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

func collectToolchains(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (ToolchainCatalog, []language.ToolchainSpec, resource.Diagnostic, error) {
	catalog, specs, diag, err := collectResourceSpecs(
		bundles,
		index,
		"toolchain",
		func(bundle resource.ContributionBundle) []language.ToolchainSpec { return bundle.Toolchains },
		func(spec language.ToolchainSpec, _ resource.SourceRef) string { return spec.ID },
		func(spec language.ToolchainSpec) error { return spec.Validate() },
	)
	return ToolchainCatalog(catalog), specs, diag, err
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

// DefaultSessionSpec derives an implicit session from an app's default agent,
// when one is configured.
func DefaultSessionSpec(appBinding Binding[coreapp.Spec], resolver *resource.Resolver) (coresession.Spec, bool, error) {
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
