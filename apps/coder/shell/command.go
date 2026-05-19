package codershell

import (
	"context"
	"strings"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/spf13/cobra"
)

// CommandOptions configures the shell command.
type CommandOptions struct {
	ClientFactory ClientFactoryFunc
}

// ClientFactoryFunc resolves the local direct channel client used when
// --connect is not set.
type ClientFactoryFunc func(context.Context, string) (agentruntime.ChannelClient, func(), error)

// NewCommand returns the standalone coder shell command. It is reusable by the
// cmd/codershell binary and by the main coder CLI as an injected subcommand.
func NewCommand() *cobra.Command {
	return NewCommandWithOptions(CommandOptions{})
}

func NewCommandWithOptions(commandOpts CommandOptions) *cobra.Command {
	var opts Options
	cmd := &cobra.Command{
		Use:   "shell [path]",
		Short: "Start the experimental coder shell TUI",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				path = args[0]
			}
			if strings.TrimSpace(opts.Connect) == "" {
				opts.Connect = "direct"
			}
			var cleanup func()
			if commandOpts.ClientFactory != nil && strings.TrimSpace(opts.Connect) == "direct" {
				client, closeClient, err := commandOpts.ClientFactory(cmd.Context(), path)
				if err != nil {
					return err
				}
				opts.DirectClient = client
				cleanup = closeClient
			}
			if cleanup != nil {
				defer cleanup()
			}
			opts.Path = path
			opts.In = cmd.InOrStdin()
			opts.Out = cmd.OutOrStdout()
			return Run(opts)
		},
	}
	cmd.Flags().StringVar(&opts.Connect, "connect", "direct", "shell endpoint: fake, direct, unix://PATH, http(s)://URL, or target URL")
	return cmd
}
