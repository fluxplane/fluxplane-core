package launch

import (
	"context"
	"fmt"
	"io"
	"strings"

	distdeploy "github.com/fluxplane/engine/adapters/distribution/deploy"
	distdescribe "github.com/fluxplane/engine/adapters/distribution/describe"
	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
	distrun "github.com/fluxplane/engine/adapters/distribution/run"
	"github.com/fluxplane/engine/orchestration/distribution"
	"github.com/spf13/cobra"
)

// AppCommandOptions configures reusable app lifecycle commands.
type AppCommandOptions struct {
	RunLoader    Loader
	RunCommand   *cobra.Command
	ServeRunner  ServeRunner
	BuildRunner  distdeploy.CommandRunner
	ConfigLoader Loader
	EditorRunner EditorRunner
}

// NewAppCommand returns the grouped app lifecycle command.
func NewAppCommand() *cobra.Command {
	return NewAppCommandWithOptions(AppCommandOptions{})
}

// NewAppCommandWithOptions returns the grouped app lifecycle command with
// injectable runners/loaders for product assembly and tests.
func NewAppCommandWithOptions(opts AppCommandOptions) *cobra.Command {
	if opts.RunLoader == nil {
		opts.RunLoader = distlocal.Load
	}
	if opts.ConfigLoader == nil {
		opts.ConfigLoader = distlocal.Load
	}
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Run and manage local Fluxplane apps",
	}
	runCommand := opts.RunCommand
	if runCommand == nil {
		runCommand = NewRunCommandWithLoader(opts.RunLoader)
	}
	cmd.AddCommand(NewInitCommand())
	cmd.AddCommand(runCommand)
	cmd.AddCommand(NewServeCommandWithRunner(opts.ServeRunner))
	cmd.AddCommand(NewAppBuildCommandWithRunner(opts.BuildRunner))
	cmd.AddCommand(NewAppDeployCommand())
	cmd.AddCommand(NewAppUndeployCommand())
	cmd.AddCommand(NewAppConfigCommand(opts.ConfigLoader, opts.EditorRunner))
	cmd.AddCommand(NewAppHealthcheckCommand())
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
	allowAuthEnv   bool
	provider       string
	model          string
	effort         string
	runner         distdeploy.CommandRunner
}

// NewAppBuildCommand returns the app build command.
func NewAppBuildCommand() *cobra.Command {
	return NewAppBuildCommandWithRunner(nil)
}

// NewAppBuildCommandWithRunner returns the app build command with an injectable
// command runner.
func NewAppBuildCommandWithRunner(runner distdeploy.CommandRunner) *cobra.Command {
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
	cmd.Flags().StringArrayVar(&opts.targets, "target", nil, "Build target: all|binary|dockerfile|docker-image|docker-compose|kubernetes|docker-base; may be repeated or comma-separated")
	cmd.Flags().StringVar(&opts.image, "image", "", "Docker image tag to use for app artifacts")
	cmd.Flags().StringVar(&opts.outDir, "out", "", "output directory for generated app artifacts")
	cmd.Flags().StringArrayVarP(&opts.tags, "tag", "t", nil, "Docker image tag; may be repeated")
	cmd.Flags().StringArrayVar(&opts.platforms, "platform", nil, "Docker target platform; may be repeated or comma-separated")
	cmd.Flags().BoolVar(&opts.push, "push", false, "push Docker build output")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved Docker build inputs without running Docker")
	cmd.Flags().BoolVar(&opts.force, "force", false, "overwrite existing generated artifacts")
	cmd.Flags().StringVar(&opts.baseImage, "base-image", "", "Docker base image for app containers")
	cmd.Flags().StringVar(&opts.connectorsPath, "connectors-path", "/connectors", "container connector credential path")
	cmd.Flags().BoolVar(&opts.allowAuthEnv, "allow-plugin-auth-env", false, "allow generated app containers to resolve plugin credentials from their process environment")
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
		AppDir:             appDir,
		OutDir:             opts.outDir,
		Targets:            targets,
		Image:              opts.image,
		Tags:               opts.tags,
		Platforms:          opts.platforms,
		Push:               opts.push,
		DryRun:             opts.dryRun,
		Force:              opts.force,
		BaseImage:          opts.baseImage,
		ConnectorsPath:     opts.connectorsPath,
		AllowPluginAuthEnv: opts.allowAuthEnv,
		Provider:           opts.provider,
		Model:              opts.model,
		Effort:             opts.effort,
		Out:                out,
		Err:                errOut,
		Runner:             opts.runner,
	})
	return err
}

type appDeployOptions struct {
	target          string
	image           string
	imagePullPolicy string
	baseImage       string
	connectorsPath  string
	allowAuthEnv    bool
	dryRun          bool
	force           bool
	detach          bool
	provider        string
	model           string
	effort          string
	namespace       string
	nodeSelectors   []string
	registryMode    string
	registry        string
	runner          distdeploy.CommandRunner
}

// NewAppDeployCommand returns the app deploy command.
func NewAppDeployCommand() *cobra.Command {
	return NewAppDeployCommandWithRunner(nil)
}

// NewAppDeployCommandWithRunner returns the app deploy command with an
// injectable command runner.
func NewAppDeployCommandWithRunner(runner distdeploy.CommandRunner) *cobra.Command {
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
	cmd.Flags().StringVar(&opts.imagePullPolicy, "image-pull-policy", "", "Kubernetes app image pull policy: Always|IfNotPresent|Never")
	cmd.Flags().StringVar(&opts.baseImage, "base-image", "", "Docker base image for app containers")
	cmd.Flags().StringVar(&opts.connectorsPath, "connectors-path", "/connectors", "container connector credential path")
	cmd.Flags().BoolVar(&opts.allowAuthEnv, "allow-plugin-auth-env", false, "allow generated app containers to resolve plugin credentials from their process environment")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved Docker commands without running them")
	cmd.Flags().BoolVar(&opts.force, "force", opts.force, "overwrite generated app artifacts before deploying")
	cmd.Flags().BoolVarP(&opts.detach, "detach", "d", false, "run docker compose up in detached mode")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "model provider override for generated app containers")
	cmd.Flags().StringVar(&opts.model, "model", "", "model override for generated app containers")
	cmd.Flags().StringVar(&opts.effort, "effort", "", "reasoning effort override for generated app containers: low|medium|high|max")
	cmd.Flags().StringVar(&opts.namespace, "namespace", "", "Kubernetes namespace for kubernetes deploy target")
	cmd.Flags().StringArrayVar(&opts.nodeSelectors, "node-selector", nil, "Kubernetes node selector key=value; may be repeated")
	cmd.Flags().StringVar(&opts.registryMode, "registry-mode", "", "Kubernetes registry mode: auto|k3d|namespace|external")
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
			AppDir:             appDir,
			Image:              opts.image,
			ImagePullPolicy:    opts.imagePullPolicy,
			BaseImage:          opts.baseImage,
			ConnectorsPath:     opts.connectorsPath,
			AllowPluginAuthEnv: opts.allowAuthEnv,
			Provider:           opts.provider,
			Model:              opts.model,
			Effort:             opts.effort,
			Namespace:          opts.namespace,
			NodeSelectors:      opts.nodeSelectors,
			RegistryMode:       opts.registryMode,
			Registry:           opts.registry,
			DryRun:             opts.dryRun,
			Force:              opts.force,
			Out:                out,
			Err:                errOut,
			Runner:             opts.runner,
		})
		return err
	}
	_, err := distdeploy.DeployDockerCompose(ctx, distdeploy.ComposeDeployOptions{
		AppDir:             appDir,
		Image:              opts.image,
		BaseImage:          opts.baseImage,
		ConnectorsPath:     opts.connectorsPath,
		AllowPluginAuthEnv: opts.allowAuthEnv,
		Provider:           opts.provider,
		Model:              opts.model,
		Effort:             opts.effort,
		DryRun:             opts.dryRun,
		Force:              opts.force,
		Detach:             opts.detach,
		Out:                out,
		Err:                errOut,
		Runner:             opts.runner,
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

// NewAppUndeployCommand returns the app undeploy command.
func NewAppUndeployCommand() *cobra.Command {
	return NewAppUndeployCommandWithRunner(nil)
}

// NewAppUndeployCommandWithRunner returns the app undeploy command with an
// injectable command runner.
func NewAppUndeployCommandWithRunner(runner distdeploy.CommandRunner) *cobra.Command {
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

// NewAppConfigCommand returns the app config inspection command.
func NewAppConfigCommand(loader Loader, editor EditorRunner) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect local app configuration",
	}
	cmd.AddCommand(newAppConfigShowCommand(loader))
	cmd.AddCommand(newAppConfigEditCommand(loader, editor))
	return cmd
}

// NewAppDescribeCommand returns a top-level app description command.
func NewAppDescribeCommand(loader Loader) *cobra.Command {
	return newAppConfigShowCommandWithUse(loader, "describe [path]", "Describe the resolved local app configuration")
}

func newAppConfigShowCommand(loader Loader) *cobra.Command {
	return newAppConfigShowCommandWithUse(loader, "show [path]", "Show the resolved local app configuration")
}

func newAppConfigShowCommandWithUse(loader Loader, use, short string) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if loader == nil {
				loader = distlocal.Load
			}
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

func newAppConfigEditCommand(loader Loader, editor EditorRunner) *cobra.Command {
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
				editor = OpenEditor
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
