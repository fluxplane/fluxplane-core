package coder

import (
	"context"

	agentruntime "github.com/fluxplane/agentruntime"
	distcli "github.com/fluxplane/agentruntime/adapters/distribution/cli"
	"github.com/fluxplane/agentruntime/adapters/resourcediscovery"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/imageplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	"github.com/fluxplane/agentruntime/plugins/taskplugin"
	"github.com/fluxplane/agentruntime/runtime/system"
	"github.com/spf13/cobra"
)

const defaultConversation = "agentsdk-coder"

// NewCommand returns the CLI command for the coder distribution.
func NewCommand() *cobra.Command {
	startup := loadStartupResources(context.Background())
	cmd := distcli.NewCommand(distributionFromStartup(startup))
	cmd.AddCommand(newDiscoverCommand(startup))
	cmd.AddCommand(newServeCommand(startup))
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
		codingplugin.New(hostSystem),
		planexecplugin.New(),
		taskplugin.New(),
		skillplugin.New(),
		imageplugin.New(hostSystem),
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
