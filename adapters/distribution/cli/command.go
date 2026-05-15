// Package cli adapts runnable distributions into Cobra terminal commands.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	distdescribe "github.com/fluxplane/agentruntime/adapters/distribution/describe"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/fluxplane/agentruntime/adapters/modelview"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/core/channel"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/spf13/cobra"
)

// NewCommand builds a Cobra command for a distribution.
func NewCommand(dist distribution.Distribution) *cobra.Command {
	opts := options{
		provider:         dist.Spec.DefaultModel.Provider,
		model:            dist.Spec.DefaultModel.Model,
		thinking:         "auto",
		maxContinuations: 20,
	}
	cmd := &cobra.Command{
		Use:   dist.Spec.Name,
		Short: shortDescription(dist),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := distrun.ValidateReasoningFlags(opts.thinking, cmd.Flags().Changed("thinking"), opts.effort, cmd.Flags().Changed("effort")); err != nil {
				return err
			}
			return Run(cmd.Context(), dist, RunOptions{
				Provider:            opts.provider,
				Model:               opts.model,
				Thinking:            opts.thinking,
				ThinkingSet:         cmd.Flags().Changed("thinking"),
				Effort:              opts.effort,
				EffortSet:           cmd.Flags().Changed("effort"),
				Input:               opts.input,
				Goal:                opts.goal,
				GoalSet:             cmd.Flags().Changed("goal"),
				MaxContinuations:    opts.maxContinuations,
				MaxContinuationsSet: cmd.Flags().Changed("max-continuations"),
				Debug:               opts.debug,
				Usage:               opts.usage,
				Yolo:                opts.yolo,
				Dev:                 opts.dev,
				Prompt:              dist.Spec.Name,
				In:                  os.Stdin,
				Out:                 os.Stdout,
				Err:                 os.Stderr,
			})
		},
	}
	cmd.PersistentFlags().StringVar(&opts.provider, "provider", opts.provider, "model provider")
	cmd.PersistentFlags().StringVar(&opts.model, "model", opts.model, "model name or provider/model")
	cmd.PersistentFlags().StringVar(&opts.thinking, "thinking", opts.thinking, "thinking mode: auto|on|off")
	cmd.PersistentFlags().StringVar(&opts.effort, "effort", opts.effort, "reasoning effort: low|medium|high|max")
	cmd.PersistentFlags().StringVar(&opts.input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.PersistentFlags().StringVar(&opts.goal, "goal", "", "run a goal-driven task and exit")
	cmd.PersistentFlags().IntVar(&opts.maxContinuations, "max-continuations", opts.maxContinuations, "maximum goal continuations")
	cmd.PersistentFlags().BoolVar(&opts.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.PersistentFlags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	cmd.PersistentFlags().BoolVar(&opts.yolo, "yolo", false, "auto-approve local operation risk gates for this run")
	cmd.PersistentFlags().BoolVar(&opts.dev, "dev", false, "enable local developer diagnostics and session history datasource")
	cmd.AddCommand(newDescribeCommand(dist))
	cmd.AddCommand(newModelsCommand(dist))
	return cmd
}

type options struct {
	provider         string
	model            string
	thinking         string
	effort           string
	input            string
	goal             string
	maxContinuations int
	debug            bool
	usage            bool
	yolo             bool
	dev              bool
}

// RunOptions configures a distribution REPL or one-shot run.
type RunOptions struct {
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
	Prompt              string
	In                  io.Reader
	Out                 io.Writer
	Err                 io.Writer
}

// Run opens a distribution session and runs a one-shot prompt or REPL.
func Run(ctx context.Context, dist distribution.Distribution, opts RunOptions) error {
	if opts.GoalSet || opts.Goal != "" {
		return runGoal(ctx, dist, opts)
	}
	if strings.TrimSpace(opts.Input) != "" {
		return runOneShot(ctx, dist, opts)
	}
	return runREPL(ctx, dist, opts)
}

func runGoal(ctx context.Context, dist distribution.Distribution, opts RunOptions) error {
	session, err := openSession(ctx, dist, opts)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close(ctx) }()
	return terminalui.RunGoalTurn(ctx, session, opts.Goal, opts.MaxContinuations, terminalOptions(opts), usage.NewTracker())
}

func runOneShot(ctx context.Context, dist distribution.Distribution, opts RunOptions) error {
	session, err := openSession(ctx, dist, opts)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close(ctx) }()
	return terminalui.RunTurn(ctx, session, opts.Input, terminalOptions(opts), usage.NewTracker())
}

func runREPL(ctx context.Context, dist distribution.Distribution, opts RunOptions) error {
	session, err := openSession(ctx, dist, opts)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close(ctx) }()
	name := strings.TrimSpace(opts.Prompt)
	if name == "" {
		name = dist.Spec.Name
	}
	tracker := usage.NewTracker()
	uiState := terminalui.UIState{}
	errOut := writerOr(opts.Err, os.Stderr)
	stdout := writerOr(opts.Out, os.Stdout)
	_, _ = fmt.Fprintf(errOut, "agentsdk %s repl. Type /exit or /quit to stop.\n", name)
	scanner := bufio.NewScanner(readerOr(opts.In, os.Stdin))
	for {
		_, _ = fmt.Fprintf(stdout, "%s> ", name)
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/exit", "/quit":
			return nil
		}
		if handled, err := terminalui.HandleUICommand(prompt, &uiState, errOut); handled {
			if err != nil {
				_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
			}
			continue
		}
		if err := terminalui.RunTurn(ctx, session, prompt, terminalOptions(opts, uiState), tracker); err != nil {
			_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func openSession(ctx context.Context, dist distribution.Distribution, opts RunOptions) (clientapi.SessionHandle, error) {
	if dist.Runtime == nil {
		return nil, fmt.Errorf("distribution %q has no runtime", dist.Spec.Name)
	}
	return dist.Runtime.OpenSession(ctx, distribution.OpenRequest{
		Session:      coresession.Ref{Name: coresession.Name(strings.TrimSpace(opts.Session))},
		Conversation: channel.ConversationRef{ID: strings.TrimSpace(opts.Conversation)},
		Provider:     opts.Provider,
		Model:        opts.Model,
		Thinking:     opts.Thinking,
		ThinkingSet:  opts.ThinkingSet,
		Effort:       opts.Effort,
		EffortSet:    opts.EffortSet,
		Debug:        opts.Debug,
		Yolo:         opts.Yolo,
		Dev:          opts.Dev,
	})
}

func terminalOptions(opts RunOptions, states ...terminalui.UIState) terminalui.TurnOptions {
	var state terminalui.UIState
	if len(states) > 0 {
		state = states[0]
	}
	return terminalui.TurnOptions{
		Debug:     opts.Debug,
		Usage:     opts.Usage,
		Reasoning: state.Reasoning,
		Out:       writerOr(opts.Out, os.Stdout),
		Err:       writerOr(opts.Err, os.Stderr),
	}
}

func readerOr(value io.Reader, fallback io.Reader) io.Reader {
	if value != nil {
		return value
	}
	return fallback
}

func writerOr(value io.Writer, fallback io.Writer) io.Writer {
	if value != nil {
		return value
	}
	return fallback
}

func newDescribeCommand(dist distribution.Distribution) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Describe distribution metadata and bundled resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case "", "tree", "pretty":
				return distdescribe.RenderTree(cmd.OutOrStdout(), dist)
			case "json":
				return distdescribe.RenderJSON(cmd.OutOrStdout(), dist)
			case "yaml":
				return distdescribe.RenderYAML(cmd.OutOrStdout(), dist)
			default:
				return fmt.Errorf("describe: unsupported output %q", output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "tree", "Output format: tree|json|yaml")
	cmd.AddCommand(newDescribeAgentCommand(dist))
	return cmd
}

func newDescribeAgentCommand(dist distribution.Distribution) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "agent <name-or-ref>",
		Short: "Describe a bundled agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch output {
			case "", "tree", "pretty":
				return distdescribe.RenderAgentTree(cmd.OutOrStdout(), dist, args[0])
			case "json":
				return distdescribe.RenderAgentJSON(cmd.OutOrStdout(), dist, args[0])
			case "yaml":
				return distdescribe.RenderAgentYAML(cmd.OutOrStdout(), dist, args[0])
			default:
				return fmt.Errorf("describe agent: unsupported output %q", output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "tree", "Output format: tree|json|yaml")
	return cmd
}

func newModelsCommand(dist distribution.Distribution) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List available model providers and models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			providers, aliases, err := distributionModels(dist)
			if err != nil {
				return err
			}
			switch output {
			case "", "tree", "pretty":
				return modelview.RenderTree(cmd.OutOrStdout(), providers, aliases...)
			case "json":
				return modelview.RenderJSON(cmd.OutOrStdout(), providers)
			case "yaml":
				return modelview.RenderYAML(cmd.OutOrStdout(), providers)
			default:
				return fmt.Errorf("models: unsupported output %q", output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "tree", "Output format: tree|json|yaml")
	return cmd
}

func distributionModels(dist distribution.Distribution) ([]corellm.ProviderSpec, []corellm.ModelAliasSpec, error) {
	var specs []corellm.ProviderSpec
	var aliases []corellm.ModelAliasSpec
	for _, bundle := range dist.Bundles {
		specs = append(specs, bundle.LLMProviders...)
		aliases = append(aliases, bundle.LLMModelAliases...)
	}
	registry, err := distrun.DefaultModelRegistryWithAliases(specs, aliases)
	if err != nil {
		return nil, nil, err
	}
	return registry.Providers(), registry.Aliases(), nil
}

func shortDescription(dist distribution.Distribution) string {
	if dist.Spec.Description != "" {
		return dist.Spec.Description
	}
	if dist.Spec.Title != "" {
		return dist.Spec.Title
	}
	return "Run " + dist.Spec.Name
}
