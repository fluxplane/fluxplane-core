// Package coder declares the first-party coding agent app resources.
package coder

import (
	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	"github.com/fluxplane/agentruntime/sdk"
)

const (
	AppName          = "coder"
	AgentName        = "coder"
	SessionName      = "coder"
	CodingPlugin     = "coding"
	TaskPlugin       = "task"
	SkillsPlugin     = "skills"
	ImagePlugin      = "image"
	DefaultModel     = "gpt-5.5"
	DefaultNamespace = "apps/coder"
)

// Bundle returns pure app resource declarations. Runtime implementations are
// supplied by the host command.
func Bundle() resource.ContributionBundle {
	operations := fullCapabilityOperationNames()
	delegationOperations := defaultDelegationOperationNames()
	agentSpec := sdk.BuildAgent(AgentName).
		WithDescription("A compact local coding assistant with filesystem, web, browser, git, shell, background process, code execution, and clarification tools.").
		WithSystem("You are agentsdk coder. Help with coding tasks using concise, concrete steps. " +
			"Prefer native project, Go language, filesystem, git, browser, web_search, web_request, and code_execute operations over shell_exec. " +
			"Use web_search for general web discovery, datasource_search with entities=[\"web.search_result\"] for configured web_search datasource queries, and web_request only for fetching known URLs. " +
			"Use project_inventory/project_docs/project_tasks/project_task_run for workspace structure and discovered project tasks, go_info/go_env/go_version/go_doc/go_list/go_test/go_fmt/go_vet/go_build/go_install for Go toolchain work, and go_project/go_packages/go_outline/go_symbol/go_definition/go_symbol_info/go_references/go_imports/go_implementations/go_callers/go_callees for Go code navigation. " +
			"Use markdown_outline/markdown_links/markdown_diagnostics for markdown documentation structure and local link checks. " +
			"Use file_create for new files, file_edit for edits to existing files, and file_delete for deletion. " +
			"Use shell_exec only when no native operation fits. Ask before destructive actions.").
		AsLLMAgent(DefaultModel).
		WithMaxOutputTokens(4096).
		WithMaxSteps(50).
		WithAgency(agent.AgencyProfile{
			Autonomy: agent.AutonomyGoalDriven,
			Reactive: true,
			Social:   true,
			Stateful: true,
		}).
		WithOperations(operations...).
		WithDatasource("web_search").
		Build()

	bundle := sdk.NewApp(AppName).
		WithSource(resource.SourceRef{
			ID:       DefaultNamespace,
			Scope:    resource.ScopeEmbedded,
			Location: DefaultNamespace,
		}).
		WithDescription("Small local coding agent app.").
		WithModel("openai", DefaultModel, "coding").
		WithPlugin(resource.PluginRef{Name: CodingPlugin}).
		WithPlugin(resource.PluginRef{Name: TaskPlugin}).
		WithPlugin(resource.PluginRef{Name: SkillsPlugin}).
		WithPlugin(resource.PluginRef{Name: ImagePlugin}).
		WithDefaultAgent(agentSpec).
		WithDefaultSession(coresession.Spec{
			Name:        SessionName,
			Description: "Default local coding session.",
			Agent:       agent.Ref{Name: AgentName},
			Metadata:    map[string]string{"app": AppName},
			Delegation: coresession.DelegationPolicy{
				AllowedProfiles: []coresession.Ref{{Name: "worker"}, {Name: "explorer"}, {Name: "reviewer"}, {Name: "task"}, {Name: "task-planner"}},
				MaxParallel:     4,
				DefaultTimeout:  "10m",
				Operations:      operationRefs(delegationOperations),
			},
		}).
		Build()
	bundle.Datasources = append(bundle.Datasources, coredatasource.Spec{
		Name:        "web_search",
		Description: "Default public web search datasource.",
		Kind:        "web_search",
		Entities:    []coredatasource.EntityType{webplugin.SearchResultEntity},
	})
	if len(bundle.Apps) > 0 {
		bundle.Apps[0].Sources = append(bundle.Apps[0].Sources, coreapp.SourceSpec{
			Location:  ".agents",
			Scope:     string(resource.ScopeProject),
			Ecosystem: "agentdir",
		})
		bundle.Apps[0].Discovery.IncludeGlobalUserResources = true
	}
	return bundle
}

func operationRefs(names []string) []operation.Ref {
	out := make([]operation.Ref, 0, len(names))
	for _, name := range names {
		out = append(out, operation.Ref{Name: operation.Name(name)})
	}
	return out
}
