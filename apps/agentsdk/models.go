package agentsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/fluxplane/agentruntime/apps/launch"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
			providers, err := models(cmd.Context(), path)
			if err != nil {
				return err
			}
			switch opts.output {
			case "", "tree", "pretty":
				return renderModelsTree(cmd.OutOrStdout(), providers)
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(providers)
			case "yaml":
				enc := yaml.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent(2)
				return enc.Encode(providers)
			default:
				return fmt.Errorf("models: unsupported output %q", opts.output)
			}
		},
	}
	cmd.Flags().StringVarP(&opts.output, "output", "o", "tree", "Output format: tree|json|yaml")
	return cmd
}

func models(ctx context.Context, path string) ([]corellm.ProviderSpec, error) {
	var specs []corellm.ProviderSpec
	if strings.TrimSpace(path) != "" {
		loaded, err := distlocal.Load(ctx, path)
		if err != nil {
			return nil, err
		}
		pluginView := launch.StaticPluginView(ctx, launch.StaticPluginOptions{
			Bundles: loaded.Distribution.Bundles,
			Launch:  loaded.Launch,
		})
		for _, diag := range append(loaded.Diagnostics, pluginView.Diagnostics...) {
			if diag.Severity == resource.SeverityError {
				return nil, fmt.Errorf("models: %s", diag.Message)
			}
		}
		for _, bundle := range pluginView.Bundles {
			specs = append(specs, bundle.LLMProviders...)
		}
	}
	registry, err := distrun.DefaultModelRegistry(specs...)
	if err != nil {
		return nil, err
	}
	return registry.Providers(), nil
}

func renderModelsTree(out io.Writer, providers []corellm.ProviderSpec) error {
	_, err := fmt.Fprintln(out, "Providers:")
	if err != nil {
		return err
	}
	for _, provider := range providers {
		if _, err := fmt.Fprintf(out, "%s\n", providerLabel(provider)); err != nil {
			return err
		}
		models := append([]corellm.ModelSpec(nil), provider.Models...)
		sort.Slice(models, func(i, j int) bool { return models[i].Ref.Name < models[j].Ref.Name })
		for i, model := range models {
			connector := "├── "
			if i == len(models)-1 {
				connector = "└── "
			}
			if _, err := fmt.Fprintf(out, "%s%s%s\n", connector, model.Ref.Name, modelDetails(model)); err != nil {
				return err
			}
		}
	}
	return nil
}

func providerLabel(provider corellm.ProviderSpec) string {
	name := string(provider.Name)
	display := strings.TrimSpace(provider.DisplayName)
	if display == "" || display == name {
		return fmt.Sprintf("%s (%d models)", name, len(provider.Models))
	}
	return fmt.Sprintf("%s - %s (%d models)", name, display, len(provider.Models))
}

func modelDetails(model corellm.ModelSpec) string {
	var parts []string
	if model.ContextTokens > 0 {
		parts = append(parts, fmt.Sprintf("context %d", model.ContextTokens))
	}
	if model.MaxOutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("max %d", model.MaxOutputTokens))
	}
	if len(model.Capabilities) > 0 {
		caps := make([]string, 0, len(model.Capabilities))
		for _, capability := range model.Capabilities {
			caps = append(caps, string(capability))
		}
		sort.Strings(caps)
		parts = append(parts, "capabilities "+strings.Join(caps, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, "; ") + "]"
}
