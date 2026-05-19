package coder

import (
	"context"
	"fmt"
	"io"
	"strings"

	distdeploy "github.com/fluxplane/agentruntime/adapters/distribution/deploy"
	distdescribe "github.com/fluxplane/agentruntime/adapters/distribution/describe"
	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
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
	cmd.AddCommand(newAppUndeployCommand())
	cmd.AddCommand(newAppConfigCommand(opts.configLoader, opts.editorRunner))
	cmd.AddCommand(newAppHealthcheckCommand())
	return cmd
}

type appBuildOptions struct {
	targets        []string
	docker         bool
	image          string
	outDir         string
	tags           []string
	platforms      []string
	push           bool
	dryRun         bool
	force          bool
	baseImage      string
	connectorsPath string
	provider       string
	model          string
	effort         string
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
	cmd.Flags().StringArrayVar(&opts.targets, "target", nil, "Build target: all|binary|dockerfile|docker-image|docker-compose|kubernetes; may be repeated or comma-separated")
	cmd.Flags().StringVar(&opts.image, "image", "", "Docker image tag to use for app artifacts")
	cmd.Flags().StringVar(&opts.outDir, "out", "", "output directory for generated app artifacts")
	cmd.Flags().StringArrayVarP(&opts.tags, "tag", "t", nil, "Docker image tag; may be repeated")
	cmd.Flags().StringArrayVar(&opts.platforms, "platform", nil, "Docker target platform; may be repeated or comma-separated")
	cmd.Flags().BoolVar(&opts.push, "push", false, "push Docker build output")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved Docker build inputs without running Docker")
	cmd.Flags().BoolVar(&opts.force, "force", false, "overwrite existing generated artifacts")
	cmd.Flags().StringVar(&opts.baseImage, "base-image", "", "Docker base image for app containers")
	cmd.Flags().StringVar(&opts.connectorsPath, "connectors-path", "/connectors", "container connector credential path")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "model provider override for generated app containers")
	cmd.Flags().StringVar(&opts.model, "model", "", "model override for generated app containers")
	cmd.Flags().StringVar(&opts.effort, "effort", "", "reasoning effort override for generated app containers: low|medium|high|max")
	return cmd
}

func runAppBuild(ctx context.Context, opts appBuildOptions, appDir string, out, errOut io.Writer) error {
	targets := append([]string(nil), opts.targets...)
	if opts.docker && len(targets) == 0 {
		targets = []string{"docker-image"}
	}
	if err := distrun.ValidateReasoningFlags("", false, opts.effort, strings.TrimSpace(opts.effort) != ""); err != nil {
		return err
	}
	_, err := distdeploy.BuildApp(ctx, distdeploy.AppBuildOptions{
		AppDir:         appDir,
		OutDir:         opts.outDir,
		Targets:        targets,
		Image:          opts.image,
		Tags:           opts.tags,
		Platforms:      opts.platforms,
		Push:           opts.push,
		DryRun:         opts.dryRun,
		Force:          opts.force,
		BaseImage:      opts.baseImage,
		ConnectorsPath: opts.connectorsPath,
		Provider:       opts.provider,
		Model:          opts.model,
		Effort:         opts.effort,
		Out:            out,
		Err:            errOut,
		Runner:         opts.runner,
	})
	return err
}

type appDeployOptions struct {
	target         string
	image          string
	baseImage      string
	connectorsPath string
	dryRun         bool
	force          bool
	detach         bool
	provider       string
	model          string
	effort         string
	namespace      string
	registryMode   string
	registry       string
	runner         distdeploy.CommandRunner
}

func newAppDeployCommand() *cobra.Command {
	return newAppDeployCommandWithRunner(nil)
}

func newAppDeployCommandWithRunner(runner distdeploy.CommandRunner) *cobra.Command {
	var opts appDeployOptions
	opts.force = true
	opts.runner = runner
	cmd := &cobra.Command{
		Use:   "deploy [path]",
		Short: "Deploy a local app",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppDeploy(cmd.Context(), opts, optionalPath(args), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&opts.target, "target", "", "Deploy target: docker-compose|kubernetes")
	cmd.Flags().StringVar(&opts.image, "image", "", "App image to build and reference in generated deployment resources")
	cmd.Flags().StringVar(&opts.baseImage, "base-image", "", "Docker base image for app containers")
	cmd.Flags().StringVar(&opts.connectorsPath, "connectors-path", "/connectors", "container connector credential path")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved Docker commands without running them")
	cmd.Flags().BoolVar(&opts.force, "force", opts.force, "overwrite generated app artifacts before deploying")
	cmd.Flags().BoolVarP(&opts.detach, "detach", "d", false, "run docker compose up in detached mode")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "model provider override for generated app containers")
	cmd.Flags().StringVar(&opts.model, "model", "", "model override for generated app containers")
	cmd.Flags().StringVar(&opts.effort, "effort", "", "reasoning effort override for generated app containers: low|medium|high|max")
	cmd.Flags().StringVar(&opts.namespace, "namespace", "", "Kubernetes namespace for kubernetes deploy target")
	cmd.Flags().StringVar(&opts.registryMode, "registry-mode", "", "Kubernetes registry mode: namespace|external")
	cmd.Flags().StringVar(&opts.registry, "registry", "", "External registry prefix for kubernetes deploy target")
	return cmd
}

func runAppDeploy(ctx context.Context, opts appDeployOptions, appDir string, out, errOut io.Writer) error {
	target := strings.TrimSpace(opts.target)
	if target == "" {
		target = "docker-compose"
	}
	if target != "docker-compose" && target != "kubernetes" {
		return fmt.Errorf("app deploy: unsupported target %q", target)
	}
	if err := distrun.ValidateReasoningFlags("", false, opts.effort, strings.TrimSpace(opts.effort) != ""); err != nil {
		return err
	}
	if target == "kubernetes" {
		_, err := distdeploy.DeployKubernetes(ctx, distdeploy.KubernetesOptions{
			AppDir:         appDir,
			Image:          opts.image,
			BaseImage:      opts.baseImage,
			ConnectorsPath: opts.connectorsPath,
			Provider:       opts.provider,
			Model:          opts.model,
			Effort:         opts.effort,
			Namespace:      opts.namespace,
			RegistryMode:   opts.registryMode,
			Registry:       opts.registry,
			DryRun:         opts.dryRun,
			Force:          opts.force,
			Out:            out,
			Err:            errOut,
			Runner:         opts.runner,
		})
		return err
	}
	_, err := distdeploy.DeployDockerCompose(ctx, distdeploy.ComposeDeployOptions{
		AppDir:         appDir,
		Image:          opts.image,
		BaseImage:      opts.baseImage,
		ConnectorsPath: opts.connectorsPath,
		Provider:       opts.provider,
		Model:          opts.model,
		Effort:         opts.effort,
		DryRun:         opts.dryRun,
		Force:          opts.force,
		Detach:         opts.detach,
		Out:            out,
		Err:            errOut,
		Runner:         opts.runner,
	})
	return err
}

type appUndeployOptions struct {
	target    string
	namespace string
	dryRun    bool
	volumes   bool
	runner    distdeploy.CommandRunner
}

func newAppUndeployCommand() *cobra.Command {
	return newAppUndeployCommandWithRunner(nil)
}

func newAppUndeployCommandWithRunner(runner distdeploy.CommandRunner) *cobra.Command {
	var opts appUndeployOptions
	opts.runner = runner
	cmd := &cobra.Command{
		Use:   "undeploy [path]",
		Short: "Undeploy a local app",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppUndeploy(cmd.Context(), opts, optionalPath(args), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&opts.target, "target", "", "Undeploy target: docker-compose|kubernetes")
	cmd.Flags().StringVar(&opts.namespace, "namespace", "", "Kubernetes namespace for kubernetes undeploy target")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved teardown commands without running them")
	cmd.Flags().BoolVar(&opts.volumes, "volumes", false, "delete persistent Docker volumes or Kubernetes PVCs")
	return cmd
}

func runAppUndeploy(ctx context.Context, opts appUndeployOptions, appDir string, out, errOut io.Writer) error {
	target := strings.TrimSpace(opts.target)
	if target == "" {
		target = "docker-compose"
	}
	if target != "docker-compose" && target != "kubernetes" {
		return fmt.Errorf("app undeploy: unsupported target %q", target)
	}
	if target == "kubernetes" {
		_, err := distdeploy.UndeployKubernetes(ctx, distdeploy.KubernetesUndeployOptions{
			AppDir:    appDir,
			Namespace: opts.namespace,
			DryRun:    opts.dryRun,
			Volumes:   opts.volumes,
			Out:       out,
			Err:       errOut,
			Runner:    opts.runner,
		})
		return err
	}
	_, err := distdeploy.UndeployDockerCompose(ctx, distdeploy.ComposeUndeployOptions{
		AppDir:  appDir,
		DryRun:  opts.dryRun,
		Volumes: opts.volumes,
		Out:     out,
		Err:     errOut,
		Runner:  opts.runner,
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
