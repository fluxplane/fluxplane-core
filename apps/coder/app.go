// Package coder declares the first-party coding agent app resources.
package coder

import (
	"encoding/json"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
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
	return resource.ContributionBundle{
		Source: resource.SourceRef{
			ID:       DefaultNamespace,
			Scope:    resource.ScopeEmbedded,
			Location: DefaultNamespace,
		},
		Apps: []coreapp.Spec{{
			Name:           AppName,
			Description:    "Small local coding agent app.",
			DefaultAgent:   agent.Ref{Name: AgentName},
			DefaultSession: coresession.Ref{Name: SessionName},
			Model: coreapp.ModelPolicy{
				Provider: "openai",
				Model:    DefaultModel,
				UseCase:  "coding",
			},
		}},
		Agents: []agent.Spec{{
			Name:        AgentName,
			Description: "A compact local coding assistant with shell and HTTP read tools.",
			System: "You are agentsdk coder. Help with coding tasks using concise, concrete steps. " +
				"Use shell_exec for local inspection or safe commands, and http_request for HTTP GET requests. " +
				"Ask before destructive actions.",
			Driver: agent.DriverSpec{Kind: "llmagent"},
			Inference: agent.InferenceSpec{
				Model:           DefaultModel,
				MaxOutputTokens: 4096,
			},
			Policy: agent.Policy{MaxContinuations: 6},
			Agency: agent.AgencyProfile{
				Autonomy: agent.AutonomyGoalDriven,
				Reactive: true,
				Social:   true,
				Stateful: true,
			},
			Operations: []operation.Ref{
				{Name: ShellOperation},
				{Name: HTTPRequestOp},
			},
		}},
		Operations: []operation.Spec{
			ShellSpec(),
			HTTPRequestSpec(),
		},
		Commands: []command.Spec{
			commandForOperation(command.Path{"shell"}, ShellSpec()),
			commandForOperation(command.Path{"http"}, HTTPRequestSpec()),
		},
		Sessions: []coresession.Spec{{
			Name:        SessionName,
			Description: "Default local coding session.",
			Agent:       agent.Ref{Name: AgentName},
			Metadata:    map[string]string{"app": AppName},
		}},
	}
}

// ShellSpec declares the shell execution operation.
func ShellSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: ShellOperation},
		Description: "Run one local command without invoking a shell interpreter.",
		Input: operation.Type{
			Name:        "ShellExecInput",
			Description: "Local command request.",
			Schema: operation.Schema{
				Format: "json-schema",
				Data:   json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Executable name or command line."},"args":{"type":"array","items":{"type":"string"}},"timeout_ms":{"type":"integer","minimum":1,"maximum":30000}},"required":["command"],"additionalProperties":false}`),
			},
		},
		Output: operation.Type{Name: "ShellExecOutput"},
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyUnknown,
			Risk:        operation.RiskMedium,
		},
	}
}

// HTTPRequestSpec declares the HTTP request operation.
func HTTPRequestSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: HTTPRequestOp},
		Description: "Perform one HTTP GET request and return status, headers, and a bounded body.",
		Input: operation.Type{
			Name:        "HTTPRequestInput",
			Description: "HTTP GET request.",
			Schema: operation.Schema{
				Format: "json-schema",
				Data:   json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"http or https URL."},"max_bytes":{"type":"integer","minimum":1,"maximum":65536},"timeout_ms":{"type":"integer","minimum":1,"maximum":30000}},"required":["url"],"additionalProperties":false}`),
			},
		},
		Output: operation.Type{Name: "HTTPRequestOutput"},
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	}
}

func commandForOperation(path command.Path, spec operation.Spec) command.Spec {
	return command.Spec{
		Path:        path,
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
}
