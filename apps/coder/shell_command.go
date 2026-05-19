package coder

import (
	"strings"

	codershell "github.com/fluxplane/agentruntime/apps/coder/shell"
	"github.com/spf13/cobra"
)

func newShellCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell [path]",
		Short: "Start the experimental coder shell TUI",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				path = args[0]
			}
			return codershell.Run(codershell.Options{
				Path: path,
				In:   cmd.InOrStdin(),
				Out:  cmd.OutOrStdout(),
			})
		},
	}
	return cmd
}
