// Package coder declares the first-party coding agent app resources.
package coder

import (
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/sdk"
)

const (
	AppName          = "coder"
	AgentName        = "coder"
	SessionName      = "coder"
	ShellOperation   = "shell_exec"
	HTTPRequestOp    = "http_request"
	DefaultModel     = "gpt-4.1-mini"
	DefaultNamespace = "apps/coder"
)

// Bundle returns pure app resource declarations. Runtime implementations are
// supplied by the host command.
func Bundle() resource.ContributionBundle {
	shell := ShellSpec()
	httpRequest := HTTPRequestSpec()
	agentSpec := sdk.BuildAgent(AgentName).
		WithDescription("A compact local coding assistant with shell and HTTP read tools.").
		WithSystem("You are agentsdk coder. Help with coding tasks using concise, concrete steps. "+
			"Use shell_exec for local inspection or safe commands, and http_request for HTTP GET requests. "+
			"Ask before destructive actions.").
		AsLLMAgent(DefaultModel).
		WithMaxOutputTokens(4096).
		WithMaxContinuations(6).
		WithAgency(agent.AgencyProfile{
			Autonomy: agent.AutonomyGoalDriven,
			Reactive: true,
			Social:   true,
			Stateful: true,
		}).
		WithOperations(ShellOperation, HTTPRequestOp).
		Build()

	return sdk.NewApp(AppName).
		WithSource(resource.SourceRef{
			ID:       DefaultNamespace,
			Scope:    resource.ScopeEmbedded,
			Location: DefaultNamespace,
		}).
		WithDescription("Small local coding agent app.").
		WithModel("openai", DefaultModel, "coding").
		WithDefaultAgent(agentSpec).
		WithOperation(shell).
		WithOperation(httpRequest).
		WithCommandForOperation("shell", shell).
		WithCommandForOperation("http", httpRequest).
		WithDefaultSession(coresession.Spec{
			Name:        SessionName,
			Description: "Default local coding session.",
			Agent:       agent.Ref{Name: AgentName},
			Metadata:    map[string]string{"app": AppName},
		}).
		Build()
}

// ShellSpec declares the shell execution operation.
func ShellSpec() operation.Spec {
	return sdk.BuildOperation(ShellOperation).
		WithDescription("Run one local command without invoking a shell interpreter.").
		WithInputJSONSchema("ShellExecInput", "Local command request.", `{"type":"object","properties":{"command":{"type":"string","description":"Executable name or command line."},"args":{"type":"array","items":{"type":"string"}},"timeout_ms":{"type":"integer","minimum":1,"maximum":30000}},"required":["command"],"additionalProperties":false}`).
		WithOutput("ShellExecOutput").
		WithDeterminism(operation.DeterminismNonDeterministic).
		WithEffects(operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal).
		WithIdempotency(operation.IdempotencyUnknown).
		WithRisk(operation.RiskMedium).
		Build()
}

// HTTPRequestSpec declares the HTTP request operation.
func HTTPRequestSpec() operation.Spec {
	return sdk.BuildOperation(HTTPRequestOp).
		WithDescription("Perform one HTTP GET request and return status, headers, and a bounded body.").
		WithInputJSONSchema("HTTPRequestInput", "HTTP GET request.", `{"type":"object","properties":{"url":{"type":"string","description":"http or https URL."},"max_bytes":{"type":"integer","minimum":1,"maximum":65536},"timeout_ms":{"type":"integer","minimum":1,"maximum":30000}},"required":["url"],"additionalProperties":false}`).
		WithOutput("HTTPRequestOutput").
		WithDeterminism(operation.DeterminismNonDeterministic).
		WithEffects(operation.EffectNetwork, operation.EffectReadExternal).
		WithIdempotency(operation.IdempotencyIdempotent).
		WithRisk(operation.RiskLow).
		Build()
}
