// Package cli adapts runnable distributions into Cobra terminal commands.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	distdescribe "github.com/fluxplane/agentruntime/adapters/distribution/describe"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/spf13/cobra"
)

// NewCommand builds a Cobra command for a distribution.
func NewCommand(dist distribution.Distribution) *cobra.Command {
	opts := options{
		provider: dist.Spec.DefaultModel.Provider,
		model:    dist.Spec.DefaultModel.Model,
	}
	cmd := &cobra.Command{
		Use:   dist.Spec.Name,
		Short: shortDescription(dist),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(opts.input) != "" {
				return runOneShot(cmd.Context(), dist, opts)
			}
			return runREPL(cmd.Context(), dist, opts)
		},
	}
	cmd.PersistentFlags().StringVar(&opts.provider, "provider", opts.provider, "model provider")
	cmd.PersistentFlags().StringVar(&opts.model, "model", opts.model, "model name or provider/model")
	cmd.PersistentFlags().StringVar(&opts.input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.PersistentFlags().BoolVar(&opts.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.PersistentFlags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	cmd.AddCommand(newDescribeCommand(dist))
	return cmd
}

type options struct {
	provider string
	model    string
	input    string
	debug    bool
	usage    bool
}

func runOneShot(ctx context.Context, dist distribution.Distribution, opts options) error {
	session, err := openSession(ctx, dist, opts)
	if err != nil {
		return err
	}
	return terminalui.RunTurn(ctx, session, opts.input, terminalOptions(opts), usage.NewTracker())
}

func runREPL(ctx context.Context, dist distribution.Distribution, opts options) error {
	session, err := openSession(ctx, dist, opts)
	if err != nil {
		return err
	}
	name := dist.Spec.Name
	tracker := usage.NewTracker()
	_, _ = fmt.Fprintf(os.Stderr, "agentsdk %s repl. Type /exit or /quit to stop.\n", name)
	scanner := bufio.NewScanner(os.Stdin)
	for {
		_, _ = fmt.Fprintf(os.Stdout, "%s> ", name)
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
		if err := terminalui.RunTurn(ctx, session, prompt, terminalOptions(opts), tracker); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func openSession(ctx context.Context, dist distribution.Distribution, opts options) (clientapi.SessionHandle, error) {
	if dist.Runtime == nil {
		return nil, fmt.Errorf("distribution %q has no runtime", dist.Spec.Name)
	}
	return dist.Runtime.OpenSession(ctx, distribution.OpenRequest{
		Provider: opts.provider,
		Model:    opts.model,
		Debug:    opts.debug,
	})
}

func terminalOptions(opts options) terminalui.TurnOptions {
	return terminalui.TurnOptions{
		Debug: opts.debug,
		Usage: opts.usage,
		Out:   os.Stdout,
		Err:   os.Stderr,
	}
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

func shortDescription(dist distribution.Distribution) string {
	if dist.Spec.Description != "" {
		return dist.Spec.Description
	}
	if dist.Spec.Title != "" {
		return dist.Spec.Title
	}
	return "Run " + dist.Spec.Name
}
