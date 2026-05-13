package launch

import (
	"context"
	"fmt"
	"io"
	"strings"

	distcli "github.com/fluxplane/agentruntime/adapters/distribution/cli"
	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/spf13/cobra"
)

type RunPathOptions struct {
	Session      string
	Conversation string
	Provider     string
	Model        string
	Input        string
	Debug        bool
	Usage        bool
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
		return fmt.Errorf("run: distribution %q has no default session", loaded.Distribution.Spec.Name)
	}
	loaded = AttachLocalRuntimeWithOptions(loaded, AttachOptions{AuthPath: opts.AuthPath})
	return distcli.Run(ctx, loaded.Distribution, distcli.RunOptions{
		Session:      opts.Session,
		Conversation: opts.Conversation,
		Provider:     opts.Provider,
		Model:        opts.Model,
		Input:        opts.Input,
		Debug:        opts.Debug,
		Usage:        opts.Usage,
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
	input        string
	debug        bool
	usage        bool
	authPath     string
}

func NewRunCommand() *cobra.Command {
	return NewRunCommandWithLoader(distlocal.Load)
}

func NewRunCommandWithLoader(loader Loader) *cobra.Command {
	var opts runCommandOptions
	cmd := &cobra.Command{
		Use:   "run [path]",
		Short: "Run a local app distribution",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return RunPathWithLoader(cmd.Context(), loader, args[0], RunPathOptions{
				Session:      opts.session,
				Conversation: opts.conversation,
				Provider:     opts.provider,
				Model:        opts.model,
				Input:        opts.input,
				Debug:        opts.debug,
				Usage:        opts.usage,
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
	cmd.Flags().StringVar(&opts.input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.Flags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	cmd.Flags().StringVar(&opts.authPath, "connectors-path", "~/.connectors", "connector credential store path")
	return cmd
}
