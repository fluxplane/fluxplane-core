package coderapp

import (
	"context"
	"io"
	"strings"

	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/spf13/cobra"
)

// RunOptions configures a programmatic app run through the coder product.
type RunOptions struct {
	Path                string
	Session             string
	Conversation        string
	Provider            string
	Model               string
	Thinking            string
	ThinkingSet         bool
	Effort              string
	EffortSet           bool
	Input               string
	Goal                string
	GoalSet             bool
	MaxContinuations    int
	MaxContinuationsSet bool
	Debug               bool
	Usage               bool
	Yolo                bool
	Dev                 bool
	AuthPath            string
	WorkspaceRoots      []string
	EnvFiles            []string
	Loader              launch.Loader
	In                  io.Reader
	Out                 io.Writer
	Err                 io.Writer
}

// Run runs the selected agentruntime app facet with coder configuration
// defaults applied.
func (a *App) Run(ctx context.Context, opts RunOptions) error {
	if a == nil {
		a = &App{}
	}
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = "."
	}
	workspace, err := mergedRunWorkspace(a.config.Workspace, opts.WorkspaceRoots, opts.EnvFiles)
	if err != nil {
		return err
	}
	return launch.RunPathWithLoader(ctx, opts.Loader, path, launch.RunPathOptions{
		Session:             opts.Session,
		Conversation:        opts.Conversation,
		Provider:            opts.Provider,
		Model:               opts.Model,
		Thinking:            opts.Thinking,
		ThinkingSet:         opts.ThinkingSet,
		Effort:              opts.Effort,
		EffortSet:           opts.EffortSet,
		Input:               opts.Input,
		Goal:                opts.Goal,
		GoalSet:             opts.GoalSet,
		MaxContinuations:    opts.MaxContinuations,
		MaxContinuationsSet: opts.MaxContinuationsSet,
		Debug:               opts.Debug,
		Usage:               opts.Usage,
		Yolo:                opts.Yolo,
		Dev:                 opts.Dev,
		AuthPath:            opts.AuthPath,
		Workspace:           workspace,
		In:                  opts.In,
		Out:                 opts.Out,
		Err:                 opts.Err,
	})
}

func (a *App) newAppRunCommand() *cobra.Command {
	opts := RunOptions{Thinking: "auto", MaxContinuations: 20}
	cmd := &cobra.Command{
		Use:   "run [path]",
		Short: "Run a local app distribution",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := distrun.ValidateReasoningFlags(opts.Thinking, cmd.Flags().Changed("thinking"), opts.Effort, cmd.Flags().Changed("effort")); err != nil {
				return err
			}
			if len(args) > 0 {
				opts.Path = args[0]
			} else {
				opts.Path = "."
			}
			opts.ThinkingSet = cmd.Flags().Changed("thinking")
			opts.EffortSet = cmd.Flags().Changed("effort")
			opts.GoalSet = cmd.Flags().Changed("goal")
			opts.MaxContinuationsSet = cmd.Flags().Changed("max-continuations")
			opts.In = cmd.InOrStdin()
			opts.Out = cmd.OutOrStdout()
			opts.Err = cmd.ErrOrStderr()
			return a.Run(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Session, "session", "", "configured session name to open")
	cmd.Flags().StringVar(&opts.Conversation, "conversation", "", "conversation id")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "model provider")
	cmd.Flags().StringVar(&opts.Model, "model", "", "model name or provider/model")
	cmd.Flags().StringVar(&opts.Thinking, "thinking", opts.Thinking, "thinking mode: auto|on|off")
	cmd.Flags().StringVar(&opts.Effort, "effort", "", "reasoning effort: low|medium|high|max")
	cmd.Flags().StringVar(&opts.Input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.Flags().StringVar(&opts.Goal, "goal", "", "run a goal-driven task and exit")
	cmd.Flags().IntVar(&opts.MaxContinuations, "max-continuations", opts.MaxContinuations, "maximum goal continuations")
	cmd.Flags().BoolVar(&opts.Debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.Flags().BoolVar(&opts.Usage, "usage", false, "print usage events after each response")
	cmd.Flags().BoolVar(&opts.Yolo, "yolo", false, "auto-approve local operation risk gates for this run")
	cmd.Flags().BoolVar(&opts.Dev, "dev", false, "enable local developer diagnostics and session history datasource")
	cmd.Flags().StringVar(&opts.AuthPath, "connectors-path", "~/.connectors", "connector credential store path")
	cmd.Flags().StringArrayVar(&opts.WorkspaceRoots, "workspace-root", nil, "additional workspace root as PATH or NAME=PATH; may be repeated")
	cmd.Flags().StringArrayVar(&opts.EnvFiles, "env-file", nil, "root workspace env file or glob to load; may be repeated")
	return cmd
}

func mergedRunWorkspace(workspace distribution.WorkspaceConfig, rootOverrides, envFileOverrides []string) (distribution.WorkspaceConfig, error) {
	roots, err := distribution.ParseWorkspaceRoots(rootOverrides)
	if err != nil {
		return distribution.WorkspaceConfig{}, err
	}
	return mergeWorkspace(workspace, distribution.WorkspaceConfig{
		Roots:    roots,
		EnvFiles: trimStrings(envFileOverrides),
	}), nil
}
