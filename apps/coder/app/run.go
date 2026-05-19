package coderapp

import (
	"context"
	"io"
	"strings"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
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
	return launch.RunPathWithLoader(ctx, a.loaderWithCoderConfig(opts.Loader), path, launch.RunPathOptions{
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

func (a *App) loaderWithCoderConfig(loader launch.Loader) launch.Loader {
	bundles := coderConfigBundles(a.config)
	if len(bundles) == 0 {
		return loader
	}
	return func(ctx context.Context, path string) (distribution.Loaded, error) {
		load := loader
		if load == nil {
			load = distlocal.Load
		}
		loaded, err := load(ctx, path)
		if err != nil {
			return distribution.Loaded{}, err
		}
		loaded.Distribution.Bundles = append(loaded.Distribution.Bundles, bundles...)
		return loaded, nil
	}
}

func (a *App) newAppRunCommand() *cobra.Command {
	opts := RunOptions{MaxContinuations: 20}
	modelFlags := launch.ModelFlags{Thinking: "auto"}
	runtimeFlags := launch.LocalRuntimeFlags{}
	environmentFlags := launch.LaunchEnvironmentFlags{}
	cmd := &cobra.Command{
		Use:   "run [path]",
		Short: "Run a local app distribution",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			modelFlags.CaptureChanged(cmd.Flags())
			if err := modelFlags.Validate(); err != nil {
				return err
			}
			if len(args) > 0 {
				opts.Path = args[0]
			} else {
				opts.Path = "."
			}
			opts.Provider = modelFlags.Provider
			opts.Model = modelFlags.Model
			opts.Thinking = modelFlags.Thinking
			opts.ThinkingSet = modelFlags.ThinkingSet
			opts.Effort = modelFlags.Effort
			opts.EffortSet = modelFlags.EffortSet
			opts.Debug = runtimeFlags.Debug
			opts.Yolo = runtimeFlags.Yolo
			opts.Dev = runtimeFlags.Dev
			opts.AuthPath = environmentFlags.AuthPath
			opts.EnvFiles = environmentFlags.EnvFiles
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
	launch.BindModelFlags(cmd.Flags(), &modelFlags, modelFlags)
	cmd.Flags().StringVar(&opts.Input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.Flags().StringVar(&opts.Goal, "goal", "", "run a goal-driven task and exit")
	cmd.Flags().IntVar(&opts.MaxContinuations, "max-continuations", opts.MaxContinuations, "maximum goal continuations")
	launch.BindLocalRuntimeFlags(cmd.Flags(), &runtimeFlags, launch.LocalRuntimeFlagHelp{
		Debug: "print run events as highlighted JSON markdown",
		Yolo:  "auto-approve local operation risk gates for this run",
	})
	cmd.Flags().BoolVar(&opts.Usage, "usage", false, "print usage events after each response")
	launch.BindLaunchEnvironmentFlags(cmd.Flags(), &environmentFlags)
	cmd.Flags().StringArrayVar(&opts.WorkspaceRoots, "workspace-root", nil, "additional workspace root as PATH or NAME=PATH; may be repeated")
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
