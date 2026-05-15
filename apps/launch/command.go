package launch

import (
	"context"

	"github.com/spf13/cobra"
)

type serveCommandOptions struct {
	debug    bool
	yolo     bool
	dev      bool
	authPath string
}

type ServeRunner func(context.Context, Options) error

func NewServeCommand() *cobra.Command {
	return NewServeCommandWithRunner(Serve)
}

func NewServeCommandWithRunner(runner ServeRunner) *cobra.Command {
	if runner == nil {
		runner = Serve
	}
	var opts serveCommandOptions
	cmd := &cobra.Command{
		Use:   "serve [app-dir]",
		Short: "Run an app daemon",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			return runner(cmd.Context(), Options{
				AppDir:   path,
				Debug:    opts.debug,
				Yolo:     opts.yolo,
				Dev:      opts.dev,
				AuthPath: opts.authPath,
			})
		},
	}
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print daemon startup details")
	cmd.Flags().BoolVar(&opts.yolo, "yolo", false, "auto-approve local operation risk gates for served sessions")
	cmd.Flags().BoolVar(&opts.dev, "dev", false, "enable local developer diagnostics and session history datasource")
	cmd.Flags().StringVar(&opts.authPath, "connectors-path", "~/.connectors", "connector credential store path")
	return cmd
}
