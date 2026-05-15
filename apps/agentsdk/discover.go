package agentsdk

import (
	"context"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	"github.com/fluxplane/agentruntime/adapters/resourcediscovery"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/spf13/cobra"
)

func newDiscoverCommand() *cobra.Command {
	return resourcediscovery.NewCommand(resourcediscovery.CommandOptions{
		Use:      "discover [path]",
		Short:    "Discover configured resources",
		Discover: discoverLocalResources,
	})
}

func discoverLocalResources(ctx context.Context, root string) (resourcediscovery.Result, error) {
	loaded, err := distlocal.Load(ctx, root)
	if err != nil {
		return resourcediscovery.Result{}, err
	}
	pluginView := launch.StaticPluginView(ctx, launch.StaticPluginOptions{
		Bundles: loaded.Distribution.Bundles,
		Launch:  loaded.Launch,
	})
	return resourcediscovery.Result{
		Root:            loaded.Root,
		Bundles:         pluginView.Bundles,
		Diagnostics:     append(loaded.Diagnostics, pluginView.Diagnostics...),
		ImplicitPlugins: pluginView.ImplicitPlugins,
	}, nil
}
