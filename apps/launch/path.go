package launch

import (
	"context"
	"fmt"
	"io"
	"strings"

	distcli "github.com/fluxplane/agentruntime/adapters/distribution/cli"
	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/spf13/cobra"
)

type RunPathOptions struct {
	Session      string
	Conversation string
	Provider     string
	Model        string
	Thinking     string
	ThinkingSet  bool
	Effort       string
	EffortSet    bool
	Input        string
	Debug        bool
	Usage        bool
	Yolo         bool
	Dev          bool
	AuthPath     string
	In           io.Reader
	Out          io.Writer
	Err          io.Writer
}

type Loader func(context.Context, string) (distribution.Loaded, error)

func RunPath(ctx context.Context, path string, opts RunPathOptions) error {
	return RunPathWithLoader(ctx, distlocal.Load, path, opts)
}

func RunPathWithLoader(ctx context.Context, loader Loader, path string, opts RunPathOptions) error {
	if loader == nil {
		loader = distlocal.Load
	}
	loaded, err := loader(ctx, path)
	if err != nil {
		return err
	}
	if loaded.Distribution.Runtime == nil {
		return fmt.Errorf("run: distribution %q has no runtime", loaded.Distribution.Spec.Name)
	}
	if strings.TrimSpace(opts.Session) == "" && loaded.Distribution.Spec.DefaultSession.Name == "" {
		if loaded.Manifest == "" {
			return fmt.Errorf("run: %s is not initialized; run \"agentsdk init %s\" to create a minimal local app manifest", loaded.Root, path)
		}
		return fmt.Errorf("run: distribution %q has no default session", loaded.Distribution.Spec.Name)
	}
	loaded = AttachLocalRuntimeWithOptions(loaded, AttachOptions{AuthPath: opts.AuthPath, Dev: opts.Dev})
	return distcli.Run(ctx, loaded.Distribution, distcli.RunOptions{
		Session:      opts.Session,
		Conversation: opts.Conversation,
		Provider:     opts.Provider,
		Model:        opts.Model,
		Thinking:     opts.Thinking,
		ThinkingSet:  opts.ThinkingSet,
		Effort:       opts.Effort,
		EffortSet:    opts.EffortSet,
		Input:        opts.Input,
		Debug:        opts.Debug,
		Usage:        opts.Usage,
		Yolo:         opts.Yolo,
		Dev:          opts.Dev,
		Prompt:       loaded.Distribution.Spec.Name,
		In:           opts.In,
		Out:          opts.Out,
		Err:          opts.Err,
	})
}

type runCommandOptions struct {
	session      string
	conversation string
	provider     string
	model        string
	thinking     string
	effort       string
	input        string
	debug        bool
	usage        bool
	yolo         bool
	dev          bool
	authPath     string
}

func NewRunCommand() *cobra.Command {
	return NewRunCommandWithLoader(distlocal.Load)
}

func NewRunCommandWithLoader(loader Loader) *cobra.Command {
	opts := runCommandOptions{thinking: "auto"}
	cmd := &cobra.Command{
		Use:   "run [path]",
		Short: "Run a local app distribution",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := distrun.ValidateReasoningFlags(opts.thinking, cmd.Flags().Changed("thinking"), opts.effort, cmd.Flags().Changed("effort")); err != nil {
				return err
			}
			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			return RunPathWithLoader(cmd.Context(), loader, path, RunPathOptions{
				Session:      opts.session,
				Conversation: opts.conversation,
				Provider:     opts.provider,
				Model:        opts.model,
				Thinking:     opts.thinking,
				ThinkingSet:  cmd.Flags().Changed("thinking"),
				Effort:       opts.effort,
				EffortSet:    cmd.Flags().Changed("effort"),
				Input:        opts.input,
				Debug:        opts.debug,
				Usage:        opts.usage,
				Yolo:         opts.yolo,
				Dev:          opts.dev,
				AuthPath:     opts.authPath,
				In:           cmd.InOrStdin(),
				Out:          cmd.OutOrStdout(),
				Err:          cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "configured session name to open")
	cmd.Flags().StringVar(&opts.conversation, "conversation", "", "conversation id")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "model provider")
	cmd.Flags().StringVar(&opts.model, "model", "", "model name or provider/model")
	cmd.Flags().StringVar(&opts.thinking, "thinking", opts.thinking, "thinking mode: auto|on|off")
	cmd.Flags().StringVar(&opts.effort, "effort", opts.effort, "reasoning effort: low|medium|high|max")
	cmd.Flags().StringVar(&opts.input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.Flags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	cmd.Flags().BoolVar(&opts.yolo, "yolo", false, "approve all operation approval prompts for this local run")
	cmd.Flags().BoolVar(&opts.dev, "dev", false, "enable local developer diagnostics and session history datasource")
	cmd.Flags().StringVar(&opts.authPath, "connectors-path", "~/.connectors", "connector credential store path")
	return cmd
}
