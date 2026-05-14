package agentsdk

import (
	"context"
	"fmt"
	"strings"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/fluxplane/agentruntime/adapters/modelview"
	"github.com/fluxplane/agentruntime/apps/launch"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/spf13/cobra"
)

type modelsOptions struct {
	output string
}

func newModelsCommand() *cobra.Command {
	var opts modelsOptions
	cmd := &cobra.Command{
		Use:   "models [path]",
		Short: "List available model providers and models",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) > 0 {
				path = args[0]
			}
			providers, aliases, err := models(cmd.Context(), path)
			if err != nil {
				return err
			}
			switch opts.output {
			case "", "tree", "pretty":
				return modelview.RenderTree(cmd.OutOrStdout(), providers, aliases...)
			case "json":
				return modelview.RenderJSON(cmd.OutOrStdout(), providers)
			case "yaml":
				return modelview.RenderYAML(cmd.OutOrStdout(), providers)
			default:
				return fmt.Errorf("models: unsupported output %q", opts.output)
			}
		},
	}
	cmd.Flags().StringVarP(&opts.output, "output", "o", "tree", "Output format: tree|json|yaml")
	return cmd
}

func models(ctx context.Context, path string) ([]corellm.ProviderSpec, []corellm.ModelAliasSpec, error) {
	var specs []corellm.ProviderSpec
	var aliases []corellm.ModelAliasSpec
	if strings.TrimSpace(path) != "" {
		loaded, err := distlocal.Load(ctx, path)
		if err != nil {
			return nil, nil, err
		}
		pluginView := launch.StaticPluginView(ctx, launch.StaticPluginOptions{
			Bundles: loaded.Distribution.Bundles,
			Launch:  loaded.Launch,
		})
		for _, diag := range append(loaded.Diagnostics, pluginView.Diagnostics...) {
			if diag.Severity == resource.SeverityError {
				return nil, nil, fmt.Errorf("models: %s", diag.Message)
			}
		}
		for _, bundle := range pluginView.Bundles {
			specs = append(specs, bundle.LLMProviders...)
			aliases = append(aliases, bundle.LLMModelAliases...)
		}
	}
	registry, err := distrun.DefaultModelRegistryWithAliases(specs, aliases)
	if err != nil {
		return nil, nil, err
	}
	return registry.Providers(), registry.Aliases(), nil
}
