package launch

import (
	"context"

	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/spf13/cobra"
)

type serveCommandOptions struct {
	debug      bool
	yolo       bool
	dev        bool
	authPath   string
	provider   string
	model      string
	thinking   string
	effort     string
	envFiles   []string
	healthAddr string
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
			if err := distrun.ValidateReasoningFlags(opts.thinking, cmd.Flags().Changed("thinking"), opts.effort, cmd.Flags().Changed("effort")); err != nil {
				return err
			}
			return runner(cmd.Context(), Options{
				AppDir:      path,
				Debug:       opts.debug,
				Yolo:        opts.yolo,
				Dev:         opts.dev,
				AuthPath:    opts.authPath,
				Provider:    opts.provider,
				Model:       opts.model,
				Thinking:    opts.thinking,
				ThinkingSet: cmd.Flags().Changed("thinking"),
				Effort:      opts.effort,
				EffortSet:   cmd.Flags().Changed("effort"),
				EnvFiles:    opts.envFiles,
				HealthAddr:  opts.healthAddr,
			})
		},
	}
	cmd.Flags().StringVar(&opts.provider, "provider", "", "model provider")
	cmd.Flags().StringVar(&opts.model, "model", "", "model name or provider/model")
	cmd.Flags().StringVar(&opts.thinking, "thinking", "", "reasoning mode: auto|on|off")
	cmd.Flags().StringVar(&opts.effort, "effort", "", "reasoning effort: low|medium|high|max")
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print daemon startup details")
	cmd.Flags().BoolVar(&opts.yolo, "yolo", false, "auto-approve local operation risk gates for served sessions")
	cmd.Flags().BoolVar(&opts.dev, "dev", false, "enable local developer diagnostics and session history datasource")
	cmd.Flags().StringVar(&opts.authPath, "connectors-path", "~/.connectors", "connector credential store path")
	cmd.Flags().StringArrayVar(&opts.envFiles, "env-file", nil, "root workspace env file or glob to load; may be repeated")
	cmd.Flags().StringVar(&opts.healthAddr, "health-addr", "", "internal HTTP address for unauthenticated health checks")
	return cmd
}
