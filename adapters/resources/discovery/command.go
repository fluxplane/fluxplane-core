package discovery

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

// DiscoverFunc loads resources for one CLI discovery invocation.
type DiscoverFunc func(context.Context, string) (Result, error)

// CommandOptions configures the reusable discovery CLI command.
type CommandOptions struct {
	Use      string
	Short    string
	Default  string
	Args     cobra.PositionalArgs
	Discover DiscoverFunc
}

// NewCommand returns a reusable resource discovery command.
func NewCommand(opts CommandOptions) *cobra.Command {
	var output string
	use := opts.Use
	if use == "" {
		use = "discover [path]"
	}
	short := opts.Short
	if short == "" {
		short = "Discover configured resources"
	}
	args := opts.Args
	if args == nil {
		args = cobra.MaximumNArgs(1)
	}
	defaultPath := opts.Default
	if defaultPath == "" {
		defaultPath = "."
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Discover == nil {
				return fmt.Errorf("discover: loader is nil")
			}
			path := defaultPath
			if len(args) > 0 {
				path = args[0]
			}
			result, err := opts.Discover(cmd.Context(), path)
			if err != nil {
				return err
			}
			switch output {
			case "", "tree", "pretty":
				return RenderTree(cmd.OutOrStdout(), result)
			case "json":
				return RenderJSON(cmd.OutOrStdout(), result)
			case "yaml":
				return RenderYAML(cmd.OutOrStdout(), result)
			default:
				return fmt.Errorf("discover: unsupported output %q", output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "tree", "Output format: tree|json|yaml")
	return cmd
}
