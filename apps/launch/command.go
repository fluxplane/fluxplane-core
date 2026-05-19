package launch

import (
	"context"

	"github.com/spf13/cobra"
)

type serveCommandOptions struct {
	model       ModelFlags
	runtime     LocalRuntimeFlags
	environment LaunchEnvironmentFlags
	healthAddr  string
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
			opts.model.CaptureChanged(cmd.Flags())
			if err := opts.model.Validate(); err != nil {
				return err
			}
			return runner(cmd.Context(), Options{
				AppDir:      path,
				Debug:       opts.runtime.Debug,
				Yolo:        opts.runtime.Yolo,
				Dev:         opts.runtime.Dev,
				AuthPath:    opts.environment.AuthPath,
				Provider:    opts.model.Provider,
				Model:       opts.model.Model,
				Thinking:    opts.model.Thinking,
				ThinkingSet: opts.model.ThinkingSet,
				Effort:      opts.model.Effort,
				EffortSet:   opts.model.EffortSet,
				EnvFiles:    opts.environment.EnvFiles,
				HealthAddr:  opts.healthAddr,
			})
		},
	}
	BindModelFlags(cmd.Flags(), &opts.model, ModelFlags{})
	BindLocalRuntimeFlags(cmd.Flags(), &opts.runtime, LocalRuntimeFlagHelp{
		Debug: "print daemon startup details",
		Yolo:  "auto-approve local operation risk gates for served sessions",
	})
	BindLaunchEnvironmentFlags(cmd.Flags(), &opts.environment)
	cmd.Flags().StringVar(&opts.healthAddr, "health-addr", "", "internal HTTP address for unauthenticated health checks")
	return cmd
}
