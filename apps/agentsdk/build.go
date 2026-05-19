package agentsdk

import (
	"context"
	"fmt"
	"io"
	"strings"

	distdeploy "github.com/fluxplane/agentruntime/adapters/distribution/deploy"
	"github.com/spf13/cobra"
)

type buildOptions struct {
	docker         bool
	tags           []string
	platforms      []string
	push           bool
	dryRun         bool
	connectorsPath string
	runner         distdeploy.CommandRunner
}

func newBuildCommand() *cobra.Command {
	return newBuildCommandWithRunner(nil)
}

func newBuildCommandWithRunner(runner distdeploy.CommandRunner) *cobra.Command {
	var opts buildOptions
	opts.runner = runner
	cmd := &cobra.Command{
		Use:   "build [app-dir]",
		Short: "Build a local app distribution",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuild(cmd.Context(), opts, optionalAppDir(args), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&opts.docker, "docker", false, "build a Docker image")
	cmd.Flags().StringArrayVarP(&opts.tags, "tag", "t", nil, "Docker image tag; may be repeated")
	cmd.Flags().StringArrayVar(&opts.platforms, "platform", nil, "Docker target platform; may be repeated or comma-separated")
	cmd.Flags().BoolVar(&opts.push, "push", false, "push Docker build output")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved Docker build inputs without running Docker")
	cmd.Flags().StringVar(&opts.connectorsPath, "connectors-path", "/connectors", "container connector credential path")
	return cmd
}

func runBuild(ctx context.Context, opts buildOptions, appDir string, out, errOut io.Writer) error {
	if !opts.docker {
		return fmt.Errorf("build: only Docker builds are supported; pass --docker")
	}
	_, err := distdeploy.BuildDocker(ctx, distdeploy.DockerBuildOptions{
		AppDir:         appDir,
		Tags:           opts.tags,
		Platforms:      opts.platforms,
		Push:           opts.push,
		DryRun:         opts.dryRun,
		ConnectorsPath: opts.connectorsPath,
		Out:            out,
		Err:            errOut,
		Runner:         opts.runner,
	})
	return err
}

func optionalAppDir(args []string) string {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "."
	}
	return args[0]
}
