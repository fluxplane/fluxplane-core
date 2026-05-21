// Package fluxplaneapp assembles the generic Fluxplane app-manifest CLI.
package fluxplaneapp

import (
	"context"

	"github.com/fluxplane/engine/adapters/distribution/authconnect"
	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
	resourcediscovery "github.com/fluxplane/engine/adapters/resources/discovery"
	"github.com/fluxplane/engine/apps/launch"
	"github.com/spf13/cobra"
)

// NewCommand returns the generic Fluxplane app-manifest CLI.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fluxplane",
		Short: "Run and manage Fluxplane apps",
	}
	cmd.AddCommand(launch.NewInitCommand())
	cmd.AddCommand(launch.NewRunCommand())
	cmd.AddCommand(launch.NewServeCommand())
	cmd.AddCommand(launch.NewAppBuildCommand())
	cmd.AddCommand(launch.NewAppDeployCommand())
	cmd.AddCommand(launch.NewAppUndeployCommand())
	cmd.AddCommand(launch.NewAppConfigCommand(distlocal.Load, nil))
	cmd.AddCommand(launch.NewAppDescribeCommand(distlocal.Load))
	cmd.AddCommand(launch.NewAppHealthcheckCommand())
	cmd.AddCommand(authconnect.NewCommand(authconnect.CommandOptions{
		TargetRegistry: launch.ManifestAuthTargetRegistry(distlocal.Load),
	}))
	cmd.AddCommand(launch.NewDatasourceCommandWithOptions(launch.DatasourceCommandOptions{RequireManifest: true}))
	cmd.AddCommand(newDiscoverCommand())
	return cmd
}

func newDiscoverCommand() *cobra.Command {
	return resourcediscovery.NewCommand(resourcediscovery.CommandOptions{
		Use:   "discover [path]",
		Short: "Discover Fluxplane app resources",
		Discover: func(ctx context.Context, path string) (resourcediscovery.Result, error) {
			loaded, err := distlocal.Load(ctx, path)
			if err != nil {
				return resourcediscovery.Result{}, err
			}
			view := launch.StaticPluginView(ctx, launch.StaticPluginOptions{
				Bundles: loaded.Distribution.Bundles,
				Launch:  loaded.Launch,
			})
			return resourcediscovery.Result{
				Root:            loaded.Root,
				Bundles:         view.Bundles,
				Diagnostics:     append(loaded.Diagnostics, view.Diagnostics...),
				ImplicitPlugins: view.ImplicitPlugins,
			}, nil
		},
	})
}
