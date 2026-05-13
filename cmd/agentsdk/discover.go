package main

import (
	"fmt"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	"github.com/fluxplane/agentruntime/adapters/resourcediscovery"
	"github.com/spf13/cobra"
)

type discoverOptions struct {
	output string
}

func newDiscoverCommand() *cobra.Command {
	var opts discoverOptions
	cmd := &cobra.Command{
		Use:   "discover [path]",
		Short: "Discover configured resources",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) > 0 {
				root = args[0]
			}
			loaded, err := distlocal.Load(cmd.Context(), root)
			if err != nil {
				return err
			}
			result := resourcediscovery.Result{
				Root:        loaded.Root,
				Bundles:     loaded.Distribution.Bundles,
				Diagnostics: loaded.Diagnostics,
			}
			switch opts.output {
			case "", "tree", "pretty":
				return resourcediscovery.RenderTree(cmd.OutOrStdout(), result)
			case "json":
				return resourcediscovery.RenderJSON(cmd.OutOrStdout(), result)
			case "yaml":
				return resourcediscovery.RenderYAML(cmd.OutOrStdout(), result)
			default:
				return fmt.Errorf("discover: unsupported output %q", opts.output)
			}
		},
	}
	cmd.Flags().StringVarP(&opts.output, "output", "o", "tree", "Output format: tree|json|yaml")
	return cmd
}
