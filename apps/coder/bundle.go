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
	PlanExecPlugin   = "planexec"
	SkillsPlugin     = "skills"
	ImagePlugin      = "image"
	DefaultModel     = "gpt-5.5"
	DefaultNamespace = "apps/coder"
)

// Bundle returns pure app resource declarations. Runtime implementations are
// supplied by the host command.
func Bundle() resource.ContributionBundle {
	agentSpec := sdk.BuildAgent(AgentName).
		WithDescription("A compact local coding assistant with filesystem, web, browser, git, shell, background process, code execution, and clarification tools.").
		WithSystem("You are agentsdk coder. Help with coding tasks using concise, concrete steps. "+
			"Prefer native project, Go language, filesystem, git, browser, web_search, web_request, and code_execute operations over shell_exec. "+
			"Use web_search for general web discovery, datasource_search with entities=[\"web.search_result\"] for configured web-search datasource queries, and web_request only for fetching known URLs. "+
			"Use project_inventory/project_docs/project_tasks for workspace structure, and go_project/go_packages/go_outline/go_symbol/go_definition/go_symbol_info/go_references/go_imports for Go code navigation. "+
			"Use markdown_outline/markdown_links/markdown_diagnostics for markdown documentation structure and local link checks. "+
			"Use file_create for new files, file_edit for edits to existing files, and file_delete for deletion. "+
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
		WithOperations(
			"project_inventory", "project_files", "project_tasks", "project_docs",
			"go_project", "go_packages", "go_outline", "go_symbol", "go_definition", "go_symbol_info", "go_references", "go_imports",
			"markdown_outline", "markdown_links", "markdown_diagnostics",
			"dir_create", "dir_list", "dir_tree",
			"file_read", "file_create", "file_edit", "file_delete", "file_stat", "file_copy", "file_move",
			"glob", "grep",
			"web_search", "web_request",
			"datasource_search", "datasource_get", "datasource_batch_get",
			"browser_open", "browser_navigate", "browser_click", "browser_type", "browser_select",
			"browser_read", "browser_screenshot", "browser_evaluate", "browser_wait", "browser_scroll",
			"browser_hover", "browser_back", "browser_forward", "browser_pdf", "browser_close",
			"git_status", "git_diff", "git_add", "git_commit", "git_tag", "git_push",
			"shell_exec", "process_start", "process_list", "process_status", "process_output", "process_kill",
			"code_execute",
			"clarify",
			"delegate", "plan", "skill",
			"image_generate", "image_understand", "image_providers",
		).
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
		WithPlugin(resource.PluginRef{Name: PlanExecPlugin}).
		WithPlugin(resource.PluginRef{Name: SkillsPlugin}).
		WithPlugin(resource.PluginRef{Name: ImagePlugin}).
		WithDefaultAgent(agentSpec).
		WithDefaultSession(coresession.Spec{
			Name:        SessionName,
			Description: "Default local coding session.",
			Agent:       agent.Ref{Name: AgentName},
			Metadata:    map[string]string{"app": AppName},
			Delegation: coresession.DelegationPolicy{
				AllowedProfiles: []coresession.Ref{{Name: "worker"}, {Name: "explorer"}},
				MaxParallel:     4,
				DefaultTimeout:  "10m",
				Operations: []operation.Ref{
					{Name: "project_inventory"}, {Name: "project_files"}, {Name: "project_tasks"}, {Name: "project_docs"},
					{Name: "go_project"}, {Name: "go_packages"}, {Name: "go_outline"}, {Name: "go_symbol"}, {Name: "go_definition"}, {Name: "go_symbol_info"}, {Name: "go_references"}, {Name: "go_imports"},
					{Name: "markdown_outline"}, {Name: "markdown_links"}, {Name: "markdown_diagnostics"},
					{Name: "dir_list"}, {Name: "dir_tree"}, {Name: "file_read"}, {Name: "file_edit"},
					{Name: "grep"}, {Name: "glob"}, {Name: "git_status"}, {Name: "git_diff"}, {Name: "git_add"}, {Name: "git_commit"},
					{Name: "shell_exec"}, {Name: "code_execute"}, {Name: "web_search"}, {Name: "web_request"},
					{Name: "datasource_search"}, {Name: "datasource_get"}, {Name: "datasource_batch_get"},
				},
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
