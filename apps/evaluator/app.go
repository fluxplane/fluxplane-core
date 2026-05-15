// Package evaluator assembles the first-party app evaluation agent.
package evaluator

import (
	"os"

	agentruntime "github.com/fluxplane/agentruntime"
	distcli "github.com/fluxplane/agentruntime/adapters/distribution/cli"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/spf13/cobra"
)

const (
	AppName             = "evaluator"
	AgentName           = "evaluator"
	SessionName         = "evaluator"
	DefaultModel        = "codex"
	DefaultNamespace    = "apps/evaluator"
	defaultConversation = "agentsdk-evaluator"
)

// NewCommand returns the CLI command for the evaluator distribution.
func NewCommand() *cobra.Command {
	dist := Distribution()
	cmd := distcli.NewCommand(dist)
	cmd.AddCommand(newTargetCommand(dist))
	return cmd
}

// Distribution returns the runnable/deployable evaluator distribution declaration.
func Distribution() distribution.Distribution {
	spec := coredistribution.Spec{
		Name:                AppName,
		Title:               "Evaluator",
		Description:         "Evaluate AgentRuntime apps over the public channel protocol.",
		DefaultSession:      agentruntime.SessionRef{Name: SessionName},
		DefaultConversation: channel.ConversationRef{ID: defaultConversation},
		DefaultModel: coredistribution.ModelDefault{
			Provider: "codex",
			Model:    DefaultModel,
			UseCase:  "evaluation",
		},
		Surfaces: coredistribution.Surfaces{
			CLI:     true,
			REPL:    true,
			OneShot: true,
			Serve:   true,
		},
	}
	bundle := Bundle()
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	bundles := []resource.ContributionBundle{bundle}
	return distribution.Distribution{
		Spec:    spec,
		Bundles: bundles,
		Runtime: launch.NewLocalRuntime(launch.LocalRuntimeConfig{
			Root:                root,
			Spec:                spec,
			Bundles:             bundles,
			Plugins:             evaluatorPlugins,
			AllowPrivateNetwork: true,
		}),
	}
}

// Bundle returns pure app resource declarations. Runtime implementations are
// supplied by the host command.
func Bundle() resource.ContributionBundle {
	agentSpec := agent.Spec{
		Name:        AgentName,
		Description: "Autonomous app evaluator for AgentRuntime channel applications.",
		System: "You are agentsdk evaluator. Evaluate target AgentRuntime apps by interacting through the public channel protocol. " +
			"When the user gives a socket or URL plus an app description, you MUST use the target_submit tool to probe the target. " +
			"Unix sockets are supported: pass base_url=http://unix and unix_socket exactly as provided. Do not claim that the socket is inaccessible before attempting target_submit. " +
			"If the user does not provide a specific probe prompt, choose a small concrete prompt appropriate for the target app, call target_submit, then report the returned thread_id, run_id, event count, outbound_text, and error field. " +
			"For coding assistants, a suitable default probe is a small implementation request whose correctness you can assess from the returned text. " +
			"Use deterministic metrics when available: runtime, token use, model calls, tool calls, operation failures, retries, latency, event counts, and completion status. " +
			"Distinguish observed evidence from speculation. Do not modify code or configuration unless the user explicitly starts a separate implementation phase.",
		Driver: agent.DriverSpec{Kind: "llmagent"},
		Inference: agent.InferenceSpec{
			Model:           DefaultModel,
			MaxOutputTokens: 4096,
		},
		Agency: agent.AgencyProfile{
			Autonomy: agent.AutonomyAutonomous,
			Reactive: true,
			Social:   true,
			Stateful: true,
		},
		Operations: []operation.Ref{
			{Name: TargetSubmitOperation},
		},
	}

	bundle := resource.ContributionBundle{
		Apps: []coreapp.Spec{{
			Name:        AppName,
			Description: "Evaluate AgentRuntime apps and produce evidence-backed reports.",
			Sources: []coreapp.SourceSpec{{
				Location:  DefaultNamespace,
				Scope:     string(resource.ScopeEmbedded),
				Ecosystem: "go",
			}},
			Model:        coreapp.ModelPolicy{Provider: "codex", Model: DefaultModel, UseCase: "evaluation"},
			DefaultAgent: agent.Ref{Name: AgentName},
			Plugins: []coreapp.PluginRef{
				{Name: AppName},
			},
		}},
		Agents: []agent.Spec{agentSpec},
		Sessions: []coresession.Spec{{
			Name:        SessionName,
			Description: "Default app evaluation session.",
			Agent:       agent.Ref{Name: AgentName},
			Metadata:    map[string]string{"app": AppName},
		}},
		Operations: []operation.Spec{targetSubmitSpec()},
		Plugins: []resource.PluginRef{
			{Name: AppName},
		},
	}
	return bundle
}
