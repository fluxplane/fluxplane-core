// Package coder declares the first-party coding agent app resources.
package coder

import (
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/sdk"
)

const (
	AppName          = "coder"
	AgentName        = "coder"
	SessionName      = "coder"
	CodingPlugin     = "coding"
	DefaultModel     = "gpt-4.1-mini"
	DefaultNamespace = "apps/coder"
)

// Bundle returns pure app resource declarations. Runtime implementations are
// supplied by the host command.
func Bundle() resource.ContributionBundle {
	agentSpec := sdk.BuildAgent(AgentName).
		WithDescription("A compact local coding assistant with filesystem, web, browser, git, shell, background process, code execution, and clarification tools.").
		WithSystem("You are agentsdk coder. Help with coding tasks using concise, concrete steps. "+
			"Prefer native filesystem, git, browser, web_request, and code_execute operations over shell_exec. "+
			"Use shell_exec only when no native operation fits. Ask before destructive actions.").
		AsLLMAgent(DefaultModel).
		WithMaxOutputTokens(4096).
		WithMaxContinuations(150).
		WithAgency(agent.AgencyProfile{
			Autonomy: agent.AutonomyGoalDriven,
			Reactive: true,
			Social:   true,
			Stateful: true,
		}).
		WithOperations(
			"dir_create", "dir_list", "dir_tree",
			"file_read", "file_create", "file_patch", "file_delete", "file_stat", "file_copy", "file_move",
			"glob", "grep",
			"web_request",
			"browser_open", "browser_navigate", "browser_click", "browser_type", "browser_select",
			"browser_read", "browser_screenshot", "browser_evaluate", "browser_wait", "browser_scroll",
			"browser_hover", "browser_back", "browser_forward", "browser_pdf", "browser_close",
			"git_status", "git_diff",
			"shell_exec", "process_start", "process_list", "process_status", "process_output", "process_kill",
			"code_execute",
			"clarify",
		).
		Build()

	return sdk.NewApp(AppName).
		WithSource(resource.SourceRef{
			ID:       DefaultNamespace,
			Scope:    resource.ScopeEmbedded,
			Location: DefaultNamespace,
		}).
		WithDescription("Small local coding agent app.").
		WithModel("openai", DefaultModel, "coding").
		WithPlugin(resource.PluginRef{Name: CodingPlugin}).
		WithDefaultAgent(agentSpec).
		WithDefaultSession(coresession.Spec{
			Name:        SessionName,
			Description: "Default local coding session.",
			Agent:       agent.Ref{Name: AgentName},
			Metadata:    map[string]string{"app": AppName},
		}).
		Build()
}
