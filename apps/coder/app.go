package coder

import (
	"context"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/distribution/authconnect"
	distcli "github.com/fluxplane/agentruntime/adapters/distribution/cli"
	distremote "github.com/fluxplane/agentruntime/adapters/distribution/remote"
	resourcediscovery "github.com/fluxplane/agentruntime/adapters/resources/discovery"
	"github.com/fluxplane/agentruntime/apps/evaluator"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/bundles/coding"
	"github.com/fluxplane/agentruntime/plugins/integrations/aws"
	"github.com/fluxplane/agentruntime/plugins/integrations/docker"
	"github.com/fluxplane/agentruntime/plugins/integrations/gitlab"
	"github.com/fluxplane/agentruntime/plugins/integrations/kubernetes"
	"github.com/fluxplane/agentruntime/plugins/integrations/loki"
	"github.com/fluxplane/agentruntime/plugins/integrations/mysql"
	"github.com/fluxplane/agentruntime/plugins/native/discovery"
	"github.com/fluxplane/agentruntime/plugins/native/identity"
	"github.com/fluxplane/agentruntime/plugins/native/image"
	"github.com/fluxplane/agentruntime/plugins/native/memory"
	"github.com/fluxplane/agentruntime/plugins/native/skills"
	"github.com/fluxplane/agentruntime/plugins/native/task"
	"github.com/fluxplane/agentruntime/plugins/native/workspace"
	"github.com/fluxplane/agentruntime/runtime/system"
	"github.com/spf13/cobra"
)

const defaultConversation = "coder"

const (
	defaultRemoteSession      = "slack-main"
	defaultRemoteConversation = "coder-remote"
	defaultRemoteSocket       = "coder-local.sock"
)

// NewCommand returns the CLI command for the coder distribution.
func NewCommand() *cobra.Command {
	return NewCommandWithOptions(CommandOptions{})
}

// CommandOptions configures coder command defaults.
type CommandOptions struct {
	WorkspaceRoots []string
	EnvFiles       []string
	Workspace      distribution.WorkspaceConfig
	Bundles        []resource.ContributionBundle
	AppRunCommand  *cobra.Command
}

// NewCommandWithOptions returns the CLI command for the coder distribution with
// configured launch defaults.
func NewCommandWithOptions(opts CommandOptions) *cobra.Command {
	startup := loadStartupResources(context.Background())
	startup.Bundles = append(startup.Bundles, cloneContributionBundles(opts.Bundles)...)
	cmd := distcli.NewCommandWithOptions(distributionFromStartup(startup), distcli.CommandOptions{
		WorkspaceRoots: opts.WorkspaceRoots,
		EnvFiles:       opts.EnvFiles,
		Workspace:      opts.Workspace,
		PromptHandler:  newRunPromptHandler(nil),
	})
	cmd.AddCommand(authconnect.NewCommand(authconnect.CommandOptions{
		NativeRegistry: launch.AuthPluginRegistry,
	}))
	cmd.AddCommand(newAppCommandWithOptions(appCommandOptions{runCommand: opts.AppRunCommand}))
	cmd.AddCommand(newBuildCommand())
	cmd.AddCommand(launch.NewDatasourceCommand())
	cmd.AddCommand(newDiscoverCommand(startup))
	cmd.AddCommand(evaluator.NewCommand())
	cmd.AddCommand(newAgentCommand())
	cmd.AddCommand(newOpCommand())
	cmd.AddCommand(distremote.NewCommand(distremote.CommandOptions{
		DefaultSession:      defaultRemoteSession,
		DefaultConversation: defaultRemoteConversation,
		DefaultSocket:       defaultRemoteSocket,
		Events:              launch.MustTerminalEventRegistry(),
	}))
	cmd.AddCommand(newServeCommandWithOptions(startup, serveCommandOptions{
		workspaceRoots: opts.WorkspaceRoots,
		envFiles:       opts.EnvFiles,
		workspace:      opts.Workspace,
	}))
	cmd.AddCommand(newShellCommandWithStartup(startup, serveCommandOptions{
		workspaceRoots: opts.WorkspaceRoots,
		envFiles:       opts.EnvFiles,
		workspace:      opts.Workspace,
	}))
	cmd.AddCommand(newWorkflowCommand())
	return cmd
}

// Distribution returns the runnable/deployable coder distribution declaration.
func Distribution() distribution.Distribution {
	return distributionFromStartup(loadStartupResources(context.Background()))
}

func distributionFromStartup(startup startupResources) distribution.Distribution {
	spec := coredistribution.Spec{
		Name:                AppName,
		Title:               "Coder",
		Description:         "Run coder in an interactive session",
		DefaultSession:      agentruntime.SessionRef{Name: SessionName},
		DefaultConversation: channel.ConversationRef{ID: defaultConversation},
		DefaultModel: coredistribution.ModelDefault{
			Provider: "openai",
			Model:    DefaultModel,
			UseCase:  "coding",
		},
		Surfaces: coredistribution.Surfaces{
			CLI:     true,
			REPL:    true,
			OneShot: true,
			Serve:   true,
		},
	}
	runtimeBundles := startup.Bundles
	describeBundles, diagnostics := launch.BundlesWithStaticPluginContributions(context.Background(), launch.StaticPluginOptions{
		Bundles: runtimeBundles,
		Plugins: localPlugins,
	})
	diagnostics = append(startup.Diagnostics, diagnostics...)
	if len(diagnostics) > 0 {
		describeBundles = append(describeBundles, agentruntime.ResourceBundle{Diagnostics: diagnostics})
	}
	return distribution.Distribution{
		Spec:    spec,
		Bundles: describeBundles,
		Runtime: launch.NewLocalRuntime(launch.LocalRuntimeConfig{
			Spec:           spec,
			Bundles:        runtimeBundles,
			Root:           startup.Root,
			Plugins:        localPlugins,
			ToolProjection: ToolProjectionConfig(),
		}),
	}
}

func newDiscoverCommand(startup startupResources) *cobra.Command {
	return resourcediscovery.NewCommand(resourcediscovery.CommandOptions{
		Use:     "discover",
		Short:   "Discover coder startup resources",
		Args:    cobra.NoArgs,
		Default: startup.Root,
		Discover: func(ctx context.Context, _ string) (resourcediscovery.Result, error) {
			view := launch.StaticPluginView(ctx, launch.StaticPluginOptions{
				Bundles: startup.Bundles,
				Plugins: localPlugins,
			})
			return resourcediscovery.Result{
				Root:            startup.Root,
				Bundles:         view.Bundles,
				Diagnostics:     append(startup.Diagnostics, view.Diagnostics...),
				ImplicitPlugins: view.ImplicitPlugins,
			}, nil
		},
	})
}

// ToolProjectionConfig returns coder's local tool projection policy.
func ToolProjectionConfig() agentruntime.ToolProjectionConfig {
	return agentruntime.ToolProjectionConfig{
		AllowSideEffects:        true,
		MaxRisk:                 operation.RiskMedium,
		IncludeBareOperations:   true,
		PreferCommandProjection: true,
	}
}

func localPlugins(hostSystem system.System) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		workspace.New(hostSystem),
		discovery.New(),
		identity.New(),
		coding.New(hostSystem),
		task.New(),
		skills.New(),
		image.New(hostSystem),
		aws.New(hostSystem),
		docker.New(hostSystem),
		gitlab.New(hostSystem),
		kubernetes.New(hostSystem),
		loki.New(hostSystem),
		mysql.New(),
		memory.New(),
	}
}

// BundleWithModel returns Bundle with a provider/model override applied.
func BundleWithModel(provider, model string) agentruntime.ResourceBundle {
	bundle := Bundle()
	if model == "" {
		return bundle
	}
	for i := range bundle.Agents {
		if bundle.Agents[i].Name == AgentName {
			bundle.Agents[i].Inference.Model = model
		}
	}
	for i := range bundle.Apps {
		if bundle.Apps[i].Name == AppName {
			bundle.Apps[i].Model.Provider = provider
			bundle.Apps[i].Model.Model = model
		}
	}
	return bundle
}
