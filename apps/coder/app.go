package coder

import (
	"context"

	fluxplane "github.com/fluxplane/engine"
	"github.com/fluxplane/engine/adapters/distribution/authconnect"
	distcli "github.com/fluxplane/engine/adapters/distribution/cli"
	distremote "github.com/fluxplane/engine/adapters/distribution/remote"
	resourcediscovery "github.com/fluxplane/engine/adapters/resources/discovery"
	"github.com/fluxplane/engine/apps/evaluator"
	"github.com/fluxplane/engine/apps/launch"
	"github.com/fluxplane/engine/core/channel"
	coredistribution "github.com/fluxplane/engine/core/distribution"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/orchestration/distribution"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/plugins/bundles/coding"
	"github.com/fluxplane/engine/plugins/integrations/aws"
	"github.com/fluxplane/engine/plugins/integrations/confluence"
	"github.com/fluxplane/engine/plugins/integrations/docker"
	"github.com/fluxplane/engine/plugins/integrations/gitlab"
	"github.com/fluxplane/engine/plugins/integrations/jira"
	"github.com/fluxplane/engine/plugins/integrations/kubernetes"
	"github.com/fluxplane/engine/plugins/integrations/loki"
	"github.com/fluxplane/engine/plugins/integrations/mysql"
	"github.com/fluxplane/engine/plugins/integrations/slack"
	"github.com/fluxplane/engine/plugins/native/discovery"
	"github.com/fluxplane/engine/plugins/native/identity"
	"github.com/fluxplane/engine/plugins/native/image"
	"github.com/fluxplane/engine/plugins/native/memory"
	"github.com/fluxplane/engine/plugins/native/skills"
	"github.com/fluxplane/engine/plugins/native/task"
	"github.com/fluxplane/engine/plugins/native/workspace"
	"github.com/fluxplane/engine/runtime/system"
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
}

// NewCommandWithOptions returns the CLI command for the coder distribution with
// configured launch defaults.
func NewCommandWithOptions(opts CommandOptions) *cobra.Command {
	startup := loadStartupResources(context.Background())
	startup.Bundles = append(startup.Bundles, cloneContributionBundles(opts.Bundles)...)
	cmd := distcli.NewCommandWithOptions(distributionFromStartup(startup), distcli.CommandOptions{
		WorkspaceRoots:           opts.WorkspaceRoots,
		EnvFiles:                 opts.EnvFiles,
		Workspace:                opts.Workspace,
		PromptHandler:            newRunPromptHandler(nil),
		EnablePluginAuthEnvFlag:  true,
		EnablePrivateNetworkFlag: true,
	})
	cmd.AddCommand(authconnect.NewCommand(authconnect.CommandOptions{
		TargetRegistry: coderAuthTargetRegistry(startup),
	}))
	cmd.AddCommand(newBuildCommand())
	cmd.AddCommand(launch.NewDatasourceCommandWithOptions(launch.DatasourceCommandOptions{
		PluginFactory: localPluginsWithAuth,
	}))
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
		DefaultSession:      fluxplane.SessionRef{Name: SessionName},
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
		describeBundles = append(describeBundles, fluxplane.ResourceBundle{Diagnostics: diagnostics})
	}
	return distribution.Distribution{
		Spec:    spec,
		Bundles: describeBundles,
		Runtime: launch.NewLocalRuntime(launch.LocalRuntimeConfig{
			Spec:           spec,
			Bundles:        runtimeBundles,
			Root:           startup.Root,
			PluginFactory:  localPluginsWithAuth,
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
func ToolProjectionConfig() fluxplane.ToolProjectionConfig {
	return fluxplane.ToolProjectionConfig{
		AllowSideEffects:        true,
		AllowApprovalRequired:   true,
		IncludeBareOperations:   true,
		PreferCommandProjection: true,
	}
}

func mergeCoderToolProjection(cfg fluxplane.ToolProjectionConfig, maxRisk operation.RiskLevel) fluxplane.ToolProjectionConfig {
	if maxRisk != "" {
		cfg.MaxRisk = maxRisk
	}
	return cfg
}

func localPlugins(hostSystem system.System) []pluginhost.Plugin {
	return localPluginsWithAuth(launch.PluginFactoryContext{System: hostSystem})
}

func localPluginsWithAuth(ctx launch.PluginFactoryContext) []pluginhost.Plugin {
	hostSystem := ctx.System
	nativeStore := ctx.NativeAuthStore
	nativeResolver := ctx.NativeAuthResolver
	return []pluginhost.Plugin{
		workspace.New(hostSystem),
		discovery.New(),
		identity.New(),
		coding.New(hostSystem),
		task.New(),
		skills.New(),
		image.New(hostSystem),
		aws.New(hostSystem),
		slack.NewWithResolver(hostSystem, ctx.Dispatcher, nativeResolver, nativeStore),
		docker.New(hostSystem),
		gitlab.NewWithResolver(hostSystem, nativeResolver),
		jira.NewWithResolver(hostSystem, nativeStore, nativeResolver),
		confluence.NewWithResolver(hostSystem, nativeStore, nativeResolver),
		kubernetes.New(hostSystem),
		loki.New(hostSystem),
		mysql.New(),
		memory.New(),
	}
}

func coderAuthTargetRegistry(startup startupResources) authconnect.TargetRegistry {
	bundles := cloneContributionBundles(startup.Bundles)
	return func(ctx context.Context) ([]pluginhost.AuthTarget, error) {
		hostSystem, err := system.NewHost(system.Config{AllowPrivateNetwork: true})
		if err != nil {
			return nil, err
		}
		return pluginhost.ResolveAuthTargets(ctx, declaredPluginRefs(bundles), localPlugins(hostSystem))
	}
}

func declaredPluginRefs(bundles []resource.ContributionBundle) []resource.PluginRef {
	seen := map[string]bool{}
	var out []resource.PluginRef
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			if ref.Name == "" || seen[ref.Key()] {
				continue
			}
			seen[ref.Key()] = true
			out = append(out, ref)
		}
	}
	return out
}

// BundleWithModel returns Bundle with a provider/model override applied.
func BundleWithModel(provider, model string) fluxplane.ResourceBundle {
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
