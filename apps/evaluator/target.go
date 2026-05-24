package evaluator

import (
	"fmt"
	"os"
	"strings"

	distcli "github.com/fluxplane/fluxplane-core/adapters/distribution/cli"
	"github.com/fluxplane/fluxplane-core/adapters/distribution/run"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	"github.com/spf13/cobra"
)

type targetOptions struct {
	baseURL      string
	socket       string
	session      string
	conversation string
	targetKind   string
	timeout      string
	probe        string
	model        string
	debug        bool
	usage        bool
	yolo         bool
	dev          bool
}

func newTargetCommand(dist distribution.Distribution) *cobra.Command {
	opts := targetOptions{
		baseURL:    "http://unix",
		session:    "coder",
		targetKind: "coder",
		timeout:    "30s",
		model:      DefaultModel,
	}
	cmd := &cobra.Command{
		Use:   "target",
		Short: "Evaluate a target app over its public channel endpoint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, err := targetInput(opts)
			if err != nil {
				return err
			}
			modelSelection := run.ResolveModelSelection("codex", opts.model)
			return distcli.Run(cmd.Context(), dist, distcli.RunOptions{
				Provider: modelSelection.Provider,
				Model:    modelSelection.Model,
				Input:    input,
				Debug:    opts.debug,
				Usage:    opts.usage,
				Yolo:     opts.yolo,
				Dev:      opts.dev,
				Prompt:   AppName,
				In:       os.Stdin,
				Out:      os.Stdout,
				Err:      os.Stderr,
			})
		},
	}
	cmd.Flags().StringVar(&opts.baseURL, "base-url", opts.baseURL, "target channel base URL; use http://unix with --socket")
	cmd.Flags().StringVar(&opts.socket, "socket", opts.socket, "target Unix socket path")
	cmd.Flags().StringVar(&opts.session, "session", opts.session, "target session name")
	cmd.Flags().StringVar(&opts.conversation, "conversation", opts.conversation, "target conversation ID")
	cmd.Flags().StringVar(&opts.targetKind, "target-kind", opts.targetKind, "target app kind label")
	cmd.Flags().StringVar(&opts.timeout, "timeout", opts.timeout, "target_submit timeout")
	cmd.Flags().StringVar(&opts.probe, "probe", opts.probe, "explicit probe prompt to submit to the target")
	cmd.Flags().StringVar(&opts.model, "model", opts.model, "evaluator model name or provider/model")
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print evaluator diagnostics")
	cmd.Flags().BoolVar(&opts.usage, "usage", false, "print usage events after the evaluator response")
	cmd.Flags().BoolVar(&opts.yolo, "yolo", false, "auto-approve local operation risk gates for this evaluation")
	cmd.Flags().BoolVar(&opts.dev, "dev", false, "enable local developer diagnostics")
	return cmd
}

func targetInput(opts targetOptions) (string, error) {
	baseURL := strings.TrimSpace(opts.baseURL)
	socket := strings.TrimSpace(opts.socket)
	session := strings.TrimSpace(opts.session)
	conversation := strings.TrimSpace(opts.conversation)
	targetKind := strings.TrimSpace(opts.targetKind)
	timeout := strings.TrimSpace(opts.timeout)
	probe := strings.TrimSpace(opts.probe)
	if baseURL == "" {
		return "", fmt.Errorf("--base-url is required")
	}
	if session == "" {
		return "", fmt.Errorf("--session is required")
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Evaluate target app at:\n")
	_, _ = fmt.Fprintf(&b, "- base_url: %s\n", baseURL)
	if socket != "" {
		_, _ = fmt.Fprintf(&b, "- unix_socket: %s\n", socket)
	}
	_, _ = fmt.Fprintf(&b, "- session: %s\n", session)
	if conversation != "" {
		_, _ = fmt.Fprintf(&b, "- conversation: %s\n", conversation)
	}
	if targetKind != "" {
		_, _ = fmt.Fprintf(&b, "- target_kind: %s\n", targetKind)
	}
	if timeout != "" {
		_, _ = fmt.Fprintf(&b, "- timeout: %s\n", timeout)
	}
	if probe != "" {
		_, _ = fmt.Fprintf(&b, "\nUse this explicit probe prompt for target_submit:\n%s\n", probe)
	}
	return b.String(), nil
}
