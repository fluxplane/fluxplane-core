package coder

import (
	"context"
	"fmt"
	"io"
	"strings"

	distdeploy "github.com/fluxplane/agentruntime/adapters/distribution/deploy"
	"github.com/spf13/cobra"
)

type buildOptions struct {
	target    string
	tags      []string
	platforms []string
	push      bool
	dryRun    bool
	runner    distdeploy.CommandRunner
}

func newBuildCommand() *cobra.Command {
	return newBuildCommandWithRunner(nil)
}

func newBuildCommandWithRunner(runner distdeploy.CommandRunner) *cobra.Command {
	var opts buildOptions
	opts.runner = runner
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build coder platform artifacts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBuild(cmd.Context(), opts, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&opts.target, "target", "", "Build target: docker-base")
	cmd.Flags().StringArrayVarP(&opts.tags, "tag", "t", nil, "Docker image tag; may be repeated")
	cmd.Flags().StringArrayVar(&opts.platforms, "platform", nil, "Docker target platform; may be repeated or comma-separated")
	cmd.Flags().BoolVar(&opts.push, "push", false, "push Docker build output")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved build inputs without running Docker")
	return cmd
}

func runBuild(ctx context.Context, opts buildOptions, out, errOut io.Writer) error {
	target := strings.TrimSpace(opts.target)
	if target == "" {
		return fmt.Errorf("build: specify --target docker-base")
	}
	if target != "docker-base" {
		return fmt.Errorf("build: unsupported target %q", target)
	}
	_, err := distdeploy.BuildCoderBaseDocker(ctx, distdeploy.BaseImageOptions{
		Tags:      opts.tags,
		Platforms: opts.platforms,
		Push:      opts.push,
		DryRun:    opts.dryRun,
		Out:       out,
		Err:       errOut,
		Runner:    opts.runner,
	})
	return err
}
