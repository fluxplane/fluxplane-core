package launch

import "github.com/spf13/cobra"

type serveCommandOptions struct {
	debug    bool
	authPath string
}

func NewServeCommand() *cobra.Command {
	var opts serveCommandOptions
	cmd := &cobra.Command{
		Use:   "serve [app-dir]",
		Short: "Run an app daemon",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return Serve(cmd.Context(), Options{
				AppDir:   args[0],
				Debug:    opts.debug,
				AuthPath: opts.authPath,
			})
		},
	}
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print daemon startup details")
	cmd.Flags().StringVar(&opts.authPath, "connectors-path", "~/.connectors", "connector credential store path")
	return cmd
}
