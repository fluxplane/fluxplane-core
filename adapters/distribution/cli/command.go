// Package cli adapts runnable distributions into Cobra terminal commands.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	distdescribe "github.com/fluxplane/engine/adapters/distribution/describe"
	distrun "github.com/fluxplane/engine/adapters/distribution/run"
	"github.com/fluxplane/engine/adapters/llm/modelview"
	"github.com/fluxplane/engine/adapters/ui/terminal"
	"github.com/fluxplane/engine/core/channel"
	corellm "github.com/fluxplane/engine/core/llm"
	"github.com/fluxplane/engine/core/operation"
	coresession "github.com/fluxplane/engine/core/session"
	"github.com/fluxplane/engine/core/usage"
	clientapi "github.com/fluxplane/engine/orchestration/client"
	"github.com/fluxplane/engine/orchestration/distribution"
	"github.com/spf13/cobra"
)

// CommandOptions configures distribution command defaults.
type CommandOptions struct {
	WorkspaceRoots           []string
	EnvFiles                 []string
	Workspace                distribution.WorkspaceConfig
	PromptHandler            PromptHandler
	EnablePluginAuthEnvFlag  bool
	EnablePrivateNetworkFlag bool
}

// PromptHandler handles distribution-specific terminal prompts before the
// generic slash-command/input path.
type PromptHandler func(context.Context, string, clientapi.SessionHandle, RunOptions) (bool, error)

// NewCommand builds a Cobra command for a distribution.
func NewCommand(dist distribution.Distribution) *cobra.Command {
	return NewCommandWithOptions(dist, CommandOptions{})
}

// NewCommandWithOptions builds a Cobra command for a distribution with
// configured launch defaults.
func NewCommandWithOptions(dist distribution.Distribution, cfg CommandOptions) *cobra.Command {
	opts := options{
		provider:         dist.Spec.DefaultModel.Provider,
		model:            dist.Spec.DefaultModel.Model,
		thinking:         "auto",
		maxContinuations: 20,
		workspaceRoots:   append([]string(nil), cfg.WorkspaceRoots...),
		envFiles:         append([]string(nil), cfg.EnvFiles...),
		workspace:        cloneWorkspaceConfig(cfg.Workspace),
	}
	cmd := &cobra.Command{
		Use:   dist.Spec.Name,
		Short: shortDescription(dist),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := distrun.ValidateReasoningFlags(opts.thinking, cmd.Flags().Changed("thinking"), opts.effort, cmd.Flags().Changed("effort")); err != nil {
				return err
			}
			maxToolRisk, err := operation.ParseRiskLevel(opts.allowMaxToolRisk)
			if err != nil {
				return fmt.Errorf("invalid --allow-max-tool-risk: %w", err)
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
				AllowPluginAuthEnv:  opts.allowPluginAuthEnv,
				AllowPrivateNetwork: opts.allowPrivateNetwork,
				MaxToolRisk:         maxToolRisk,
				WorkspaceRoots:      opts.workspaceRoots,
				EnvFiles:            opts.envFiles,
				Workspace:           opts.workspace,
				Prompt:              dist.Spec.Name,
				PromptHandler:       cfg.PromptHandler,
				In:                  os.Stdin,
				Out:                 os.Stdout,
				Err:                 os.Stderr,
			})
		},
	}
	cmd.Flags().StringVar(&opts.provider, "provider", opts.provider, "model provider")
	cmd.Flags().StringVar(&opts.model, "model", opts.model, "model name or provider/model")
	cmd.Flags().StringVar(&opts.thinking, "thinking", opts.thinking, "thinking mode: auto|on|off")
	cmd.Flags().StringVar(&opts.effort, "effort", opts.effort, "reasoning effort: low|medium|high|max")
	cmd.Flags().StringVar(&opts.input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.Flags().StringVar(&opts.goal, "goal", "", "run a goal-driven task and exit")
	cmd.Flags().IntVar(&opts.maxContinuations, "max-continuations", opts.maxContinuations, "maximum goal continuations")
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.Flags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	cmd.Flags().BoolVar(&opts.yolo, "yolo", false, "auto-approve local operation risk gates for this run")
	cmd.Flags().BoolVar(&opts.dev, "dev", false, "enable local developer diagnostics and session history datasource")
	if cfg.EnablePluginAuthEnvFlag {
		cmd.Flags().BoolVar(&opts.allowPluginAuthEnv, "allow-plugin-auth-env", false, "allow plugin auth methods to resolve credentials from the process environment")
	}
	if cfg.EnablePrivateNetworkFlag {
		cmd.Flags().BoolVar(&opts.allowPrivateNetwork, "allow-private-network", false, "allow runtime network access to private, local, multicast, or metadata addresses")
	}
	cmd.Flags().StringVar(&opts.allowMaxToolRisk, "allow-max-tool-risk", "", "maximum model-visible tool risk: low|medium|high|critical; omitted allows all")
	cmd.Flags().StringArrayVar(&opts.workspaceRoots, "workspace-root", opts.workspaceRoots, "additional workspace root as PATH or NAME=PATH; may be repeated")
	cmd.Flags().StringArrayVar(&opts.envFiles, "env-file", opts.envFiles, "root workspace env file or glob to load; may be repeated")
	cmd.AddCommand(newDescribeCommand(dist))
	cmd.AddCommand(newModelsCommand(dist))
	return cmd
}

type options struct {
	provider            string
	model               string
	thinking            string
	effort              string
	input               string
	goal                string
	maxContinuations    int
	debug               bool
	usage               bool
	yolo                bool
	dev                 bool
	allowPluginAuthEnv  bool
	allowPrivateNetwork bool
	allowMaxToolRisk    string
	workspaceRoots      []string
	envFiles            []string
	workspace           distribution.WorkspaceConfig
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
	AllowPluginAuthEnv  bool
	AllowPrivateNetwork bool
	MaxToolRisk         operation.RiskLevel
	WorkspaceRoots      []string
	EnvFiles            []string
	Workspace           distribution.WorkspaceConfig
	Prompt              string
	PromptHandler       PromptHandler
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
	turnOpts := terminalOptions(opts)
	turnOpts.WaitForBackgroundTasks = true
	return terminal.RunGoalTurn(ctx, session, opts.Goal, opts.MaxContinuations, turnOpts, usage.NewTracker())
}

func runOneShot(ctx context.Context, dist distribution.Distribution, opts RunOptions) error {
	session, err := openSession(ctx, dist, opts)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close(ctx) }()
	if opts.PromptHandler != nil {
		handled, err := opts.PromptHandler(ctx, opts.Input, session, opts)
		if handled || err != nil {
			return err
		}
	}
	turnOpts := terminalOptions(opts)
	turnOpts.WaitForBackgroundTasks = true
	return terminal.RunTurn(ctx, session, opts.Input, turnOpts, usage.NewTracker())
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
	uiState := terminal.UIState{}
	errOut := writerOr(opts.Err, os.Stderr)
	stdout := writerOr(opts.Out, os.Stdout)
	_, _ = fmt.Fprintf(errOut, "coder %s repl. Type /exit or /quit to stop.\n", name)
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
		if handled, err := terminal.HandleUICommand(prompt, &uiState, errOut); handled {
			if err != nil {
				_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
			}
			continue
		}
		if opts.PromptHandler != nil {
			handled, err := opts.PromptHandler(ctx, prompt, session, opts)
			if handled {
				if err != nil {
					_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
				}
				continue
			}
			if err != nil {
				_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
				continue
			}
		}
		if err := terminal.RunTurn(ctx, session, prompt, terminalOptions(opts, uiState), tracker); err != nil {
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
	roots, err := distribution.ParseWorkspaceRoots(opts.WorkspaceRoots)
	if err != nil {
		return nil, err
	}
	workspace := cloneWorkspaceConfig(opts.Workspace)
	workspace.Roots = append(workspace.Roots, roots...)
	workspace.EnvFiles = append(workspace.EnvFiles, trimStrings(opts.EnvFiles)...)
	return dist.Runtime.OpenSession(ctx, distribution.OpenRequest{
		Launch:              distribution.LaunchConfig{Workspace: workspace},
		Session:             coresession.Ref{Name: coresession.Name(strings.TrimSpace(opts.Session))},
		Conversation:        channel.ConversationRef{ID: strings.TrimSpace(opts.Conversation)},
		Provider:            opts.Provider,
		Model:               opts.Model,
		Thinking:            opts.Thinking,
		ThinkingSet:         opts.ThinkingSet,
		Effort:              opts.Effort,
		EffortSet:           opts.EffortSet,
		Debug:               opts.Debug,
		Yolo:                opts.Yolo,
		Dev:                 opts.Dev,
		AllowPluginAuthEnv:  opts.AllowPluginAuthEnv,
		AllowPrivateNetwork: opts.AllowPrivateNetwork,
		MaxToolRisk:         opts.MaxToolRisk,
	})
}

func cloneWorkspaceConfig(cfg distribution.WorkspaceConfig) distribution.WorkspaceConfig {
	out := distribution.WorkspaceConfig{
		Roots:       cloneWorkspaceRoots(cfg.Roots),
		ScratchRoot: strings.TrimSpace(cfg.ScratchRoot),
		EnvFiles:    append([]string(nil), cfg.EnvFiles...),
	}
	return out
}

func cloneWorkspaceRoots(roots []distribution.WorkspaceRoot) []distribution.WorkspaceRoot {
	if len(roots) == 0 {
		return nil
	}
	out := make([]distribution.WorkspaceRoot, 0, len(roots))
	for _, root := range roots {
		root.EnvFiles = append([]string(nil), root.EnvFiles...)
		out = append(out, root)
	}
	return out
}

func terminalOptions(opts RunOptions, states ...terminal.UIState) terminal.TurnOptions {
	var state terminal.UIState
	if len(states) > 0 {
		state = states[0]
	}
	return terminal.TurnOptions{
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

func trimStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
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
