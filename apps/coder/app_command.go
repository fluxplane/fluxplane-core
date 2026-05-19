package coder

import (
	"context"
	"fmt"
	"io"
	"strings"

	distdeploy "github.com/fluxplane/agentruntime/adapters/distribution/deploy"
	distdescribe "github.com/fluxplane/agentruntime/adapters/distribution/describe"
	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/spf13/cobra"
)

type appCommandOptions struct {
	runLoader    launch.Loader
	runCommand   *cobra.Command
	serveRunner  launch.ServeRunner
	buildRunner  distdeploy.CommandRunner
	configLoader launch.Loader
	editorRunner launch.EditorRunner
}

func newAppCommand() *cobra.Command {
	return newAppCommandWithOptions(appCommandOptions{})
}

func newAppCommandWithOptions(opts appCommandOptions) *cobra.Command {
	if opts.runLoader == nil {
		opts.runLoader = distlocal.Load
	}
	if opts.configLoader == nil {
		opts.configLoader = distlocal.Load
	}
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Run and manage local agentruntime apps",
	}
	runCommand := opts.runCommand
	if runCommand == nil {
		runCommand = launch.NewRunCommandWithLoader(opts.runLoader)
	}
	cmd.AddCommand(launch.NewInitCommand())
	cmd.AddCommand(runCommand)
	cmd.AddCommand(launch.NewServeCommandWithRunner(opts.serveRunner))
	cmd.AddCommand(newAppBuildCommandWithRunner(opts.buildRunner))
	cmd.AddCommand(newAppDeployCommand())
	cmd.AddCommand(newAppConfigCommand(opts.configLoader, opts.editorRunner))
	return cmd
}

type appBuildOptions struct {
	target         string
	docker         bool
	tags           []string
	platforms      []string
	push           bool
	dryRun         bool
	baseImage      string
	connectorsPath string
	runner         distdeploy.CommandRunner
}

func newAppBuildCommandWithRunner(runner distdeploy.CommandRunner) *cobra.Command {
	var opts appBuildOptions
	opts.runner = runner
	cmd := &cobra.Command{
		Use:   "build [path]",
		Short: "Build a local app distribution",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppBuild(cmd.Context(), opts, optionalPath(args), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&opts.docker, "docker", false, "build a Docker image")
	cmd.Flags().StringVar(&opts.target, "target", "", "Build target: docker-image")
	cmd.Flags().StringArrayVarP(&opts.tags, "tag", "t", nil, "Docker image tag; may be repeated")
	cmd.Flags().StringArrayVar(&opts.platforms, "platform", nil, "Docker target platform; may be repeated or comma-separated")
	cmd.Flags().BoolVar(&opts.push, "push", false, "push Docker build output")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved Docker build inputs without running Docker")
	cmd.Flags().StringVar(&opts.baseImage, "base-image", "", "Docker base image for app containers")
	cmd.Flags().StringVar(&opts.connectorsPath, "connectors-path", "/connectors", "container connector credential path")
	return cmd
}

func runAppBuild(ctx context.Context, opts appBuildOptions, appDir string, out, errOut io.Writer) error {
	target := strings.TrimSpace(opts.target)
	if target == "" && opts.docker {
		target = "docker-image"
	}
	if target == "" {
		return fmt.Errorf("app build: specify --target docker-image")
	}
	if target != "docker-image" {
		return fmt.Errorf("app build: unsupported target %q", target)
	}
	_, err := distdeploy.BuildDocker(ctx, distdeploy.DockerBuildOptions{
		AppDir:         appDir,
		Tags:           opts.tags,
		Platforms:      opts.platforms,
		Push:           opts.push,
		DryRun:         opts.dryRun,
		BaseImage:      opts.baseImage,
		ConnectorsPath: opts.connectorsPath,
		Out:            out,
		Err:            errOut,
		Runner:         opts.runner,
	})
	return err
}

type appDeployOptions struct {
	target string
	image  string
	dryRun bool
}

func newAppDeployCommand() *cobra.Command {
	var opts appDeployOptions
	cmd := &cobra.Command{
		Use:   "deploy [path]",
		Short: "Generate local app deployment resources",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppDeploy(cmd.Context(), opts, optionalPath(args), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.target, "target", "", "Deploy target: docker-compose")
	cmd.Flags().StringVar(&opts.image, "image", "", "App image to reference in generated deployment resources")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print generated deployment resources")
	return cmd
}

func runAppDeploy(ctx context.Context, opts appDeployOptions, appDir string, out io.Writer) error {
	target := strings.TrimSpace(opts.target)
	if target == "" {
		return fmt.Errorf("app deploy: specify --target docker-compose")
	}
	if target != "docker-compose" {
		return fmt.Errorf("app deploy: unsupported target %q", target)
	}
	_, err := distdeploy.GenerateDockerCompose(ctx, distdeploy.ComposeOptions{
		AppDir: appDir,
		Image:  opts.image,
		DryRun: opts.dryRun,
		Out:    out,
	})
	return err
}

func newAppConfigCommand(loader launch.Loader, editor launch.EditorRunner) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect local app configuration",
	}
	cmd.AddCommand(newAppConfigShowCommand(loader))
	cmd.AddCommand(newAppConfigEditCommand(loader, editor))
	return cmd
}

func newAppConfigShowCommand(loader launch.Loader) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "show [path]",
		Short: "Show the resolved local app configuration",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := loader(cmd.Context(), optionalPath(args))
			if err != nil {
				return err
			}
			if strings.TrimSpace(loaded.Manifest) == "" {
				return fmt.Errorf("app config show: no app manifest found in %s", loaded.Root)
			}
			dist := distribution.Distribution{
				Spec:    loaded.Distribution.Spec,
				Bundles: loaded.Distribution.Bundles,
			}
			switch output {
			case "", "tree", "pretty":
				return distdescribe.RenderTree(cmd.OutOrStdout(), dist)
			case "json":
				return distdescribe.RenderJSON(cmd.OutOrStdout(), dist)
			case "yaml":
				return distdescribe.RenderYAML(cmd.OutOrStdout(), dist)
			default:
				return fmt.Errorf("app config show: unsupported output %q", output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "tree", "Output format: tree|json|yaml")
	return cmd
}

func newAppConfigEditCommand(loader launch.Loader, editor launch.EditorRunner) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit [path]",
		Short: "Edit the local app manifest",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := loader(cmd.Context(), optionalPath(args))
			if err != nil {
				return err
			}
			if strings.TrimSpace(loaded.Manifest) == "" {
				return fmt.Errorf("app config edit: no app manifest found in %s", loaded.Root)
			}
			if editor == nil {
				editor = launch.OpenEditor
			}
			return editor(cmd.Context(), loaded.Manifest, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	return cmd
}

func optionalPath(args []string) string {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "."
	}
	return args[0]
}
