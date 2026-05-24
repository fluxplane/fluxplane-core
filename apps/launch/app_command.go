package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	distdeploy "github.com/fluxplane/engine/adapters/distribution/deploy"
	distdescribe "github.com/fluxplane/engine/adapters/distribution/describe"
	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
	distrun "github.com/fluxplane/engine/adapters/distribution/run"
	"github.com/fluxplane/engine/adapters/resources/appconfig"
	coredata "github.com/fluxplane/engine/core/data"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/orchestration/distribution"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/plugins/native/datasource"
	"github.com/fluxplane/engine/runtime/system"
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
	targets      []string
	docker       bool
	image        string
	outDir       string
	tags         []string
	platforms    []string
	push         bool
	dryRun       bool
	force        bool
	baseImage    string
	authPath     string
	allowAuthEnv bool
	provider     string
	model        string
	effort       string
	runner       distdeploy.CommandRunner
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
	cmd.Flags().StringVar(&opts.authPath, "auth-path", "/auth", "container plugin auth store path")
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
		AuthPath:           opts.authPath,
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
	authPath        string
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
	cmd.Flags().StringVar(&opts.authPath, "auth-path", "/auth", "container plugin auth store path")
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
			AuthPath:           opts.authPath,
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
		AuthPath:           opts.authPath,
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
	cmd.AddCommand(newAppConfigSchemaCommand(loader))
	cmd.AddCommand(newAppConfigShowCommand(loader))
	cmd.AddCommand(newAppConfigValidateCommand(loader))
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

func newAppConfigValidateCommand(loader Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [path]",
		Short: "Validate the local app manifest",
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
				return fmt.Errorf("app config validate: no app manifest found in %s", loaded.Root)
			}
			var errorDiagnostics []resource.Diagnostic
			for _, diagnostic := range loaded.Diagnostics {
				if diagnostic.Severity == resource.SeverityError {
					errorDiagnostics = append(errorDiagnostics, diagnostic)
				}
			}
			if len(errorDiagnostics) > 0 {
				for _, diagnostic := range errorDiagnostics {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", diagnostic.Severity, diagnostic.Message)
				}
				return fmt.Errorf("app config validate: %d error diagnostic(s)", len(errorDiagnostics))
			}
			schemaData, err := configSchemaData(cmd.Context(), loader, optionalPath(args))
			if err != nil {
				return err
			}
			manifestData, err := os.ReadFile(loaded.Manifest)
			if err != nil {
				return fmt.Errorf("app config validate: read %s: %w", loaded.Manifest, err)
			}
			if err := appconfig.ValidateManifestWithSchema(schemaData, manifestData); err != nil {
				return fmt.Errorf("app config validate: %s: %w", loaded.Manifest, err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "valid %s\n", loaded.Manifest)
			return nil
		},
	}
	return cmd
}

func newAppConfigSchemaCommand(loader Loader) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "schema [path]",
		Short: "Write the base local app manifest JSON Schema",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := configSchemaData(cmd.Context(), loader, optionalPath(args))
			if err != nil {
				return err
			}
			data = append(data, '\n')
			if output == "-" {
				_, err := cmd.OutOrStdout().Write(data)
				return err
			}
			path := output
			if strings.TrimSpace(path) == "" {
				path = filepath.Join(".fluxplane", "schema.json")
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(optionalPath(args), path)
			}
			path = filepath.Clean(path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("app config schema: create %s: %w", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return fmt.Errorf("app config schema: write %s: %w", path, err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "schema output path relative to app path, or - for stdout (default .fluxplane/schema.json)")
	return cmd
}

func configSchemaData(ctx context.Context, loader Loader, appDir string) ([]byte, error) {
	available := configSchemaAvailablePlugins(appDir)
	plugins, err := configSchemaPlugins(available)
	if err != nil {
		return nil, err
	}
	opts := appconfig.ManifestSchemaOptions{Plugins: plugins}
	if resources, datasources, ok, err := configSchemaResources(ctx, loader, appDir, available); err != nil {
		return nil, err
	} else if ok {
		opts.Resources = resources
		opts.Datasources = datasources
	}
	return appconfig.ManifestSchemaWithOptions(opts)
}

func configSchemaAvailablePlugins(appDir string) []pluginhost.Plugin {
	hostSystem := configSchemaSystem(appDir)
	available := availablePlugins(hostSystem, nil, nil, "", false)
	return appendPluginIfMissing(available, datasource.New(nil))
}

func configSchemaSystem(appDir string) system.System {
	if strings.TrimSpace(appDir) == "" {
		appDir = "."
	}
	hostSystem, err := system.NewHost(system.Config{Root: appDir, AllowPrivateNetwork: true})
	if err != nil {
		return nil
	}
	return hostSystem
}

func configSchemaPlugins(available []pluginhost.Plugin) ([]appconfig.PluginSchema, error) {
	out := make([]appconfig.PluginSchema, 0, len(available))
	for _, plugin := range available {
		if plugin == nil {
			continue
		}
		manifest := plugin.Manifest()
		schema := appconfig.PluginSchema{
			Kind:        strings.TrimSpace(manifest.Name),
			Description: strings.TrimSpace(manifest.Description),
		}
		if provider, ok := plugin.(pluginhost.ConfigSchemaProvider); ok {
			data, err := provider.ConfigSchema()
			if err != nil {
				return nil, fmt.Errorf("app config schema: plugin %q config schema: %w", schema.Kind, err)
			}
			if len(data) > 0 {
				var configSchema map[string]any
				if err := json.Unmarshal(data, &configSchema); err != nil {
					return nil, fmt.Errorf("app config schema: plugin %q config schema JSON: %w", schema.Kind, err)
				}
				schema.ConfigSchema = configSchema
			}
		}
		out = append(out, schema)
	}
	return out, nil
}

func configSchemaResources(ctx context.Context, loader Loader, appDir string, available []pluginhost.Plugin) (appconfig.ResourceSchema, []appconfig.DatasourceSchema, bool, error) {
	if loader == nil {
		loader = distlocal.Load
	}
	if strings.TrimSpace(appDir) == "" {
		appDir = "."
	}
	manifestPath := filepath.Join(appDir, appconfig.DefaultManifestName)
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return appconfig.ResourceSchema{}, nil, false, nil
		}
		return appconfig.ResourceSchema{}, nil, false, fmt.Errorf("app config schema: stat manifest %s: %w", manifestPath, err)
	}
	loaded, err := loader(ctx, appDir)
	if err != nil {
		return appconfig.ResourceSchema{}, nil, false, err
	}
	static := StaticPluginView(ctx, StaticPluginOptions{
		Bundles:                          loaded.Distribution.Bundles,
		Launch:                           loaded.Launch,
		IncludeConfigSchemaContributions: true,
		Plugins: func(system.System) []pluginhost.Plugin {
			return available
		},
	})
	resources := appconfig.ResourceSchema{
		EntitiesByKind: map[string][]string{},
	}
	var datasourceSchemas []appconfig.DatasourceSchema
	for _, bundle := range static.Bundles {
		for _, spec := range bundle.Agents {
			resources.Agents = append(resources.Agents, string(spec.Name))
		}
		for _, spec := range bundle.Sessions {
			resources.Sessions = append(resources.Sessions, string(spec.Name))
		}
		for _, spec := range bundle.Workflows {
			resources.Workflows = append(resources.Workflows, string(spec.Name))
		}
		for _, spec := range bundle.Operations {
			resources.Operations = append(resources.Operations, string(spec.Ref.Name))
			resources.Tools = append(resources.Tools, string(spec.Ref.Name))
		}
		for _, spec := range bundle.Datasources {
			resources.Datasources = append(resources.Datasources, string(spec.Name))
			resources.DatasourceKinds = append(resources.DatasourceKinds, spec.Kind)
			for _, entity := range spec.Entities {
				resources.Entities = append(resources.Entities, string(entity))
				resources.EntitiesByKind[spec.Kind] = append(resources.EntitiesByKind[spec.Kind], string(entity))
			}
		}
		for _, spec := range bundle.DataSources {
			resources.DatasourceKinds = append(resources.DatasourceKinds, spec.Kind)
			for _, entity := range spec.Entities {
				resources.Entities = append(resources.Entities, string(entity.Type))
				resources.EntitiesByKind[spec.Kind] = append(resources.EntitiesByKind[spec.Kind], string(entity.Type))
			}
			datasourceSchema, err := configSchemaDatasource(spec)
			if err != nil {
				return appconfig.ResourceSchema{}, nil, false, err
			}
			if datasourceSchema.Kind != "" {
				datasourceSchemas = append(datasourceSchemas, datasourceSchema)
			}
		}
		for _, spec := range bundle.Skills {
			resources.Skills = append(resources.Skills, string(spec.Name))
		}
		for _, spec := range bundle.ContextProviders {
			resources.ContextProviders = append(resources.ContextProviders, string(spec.Name))
		}
		for _, spec := range bundle.ToolSets {
			for _, name := range spec.Tools {
				resources.Tools = append(resources.Tools, string(name))
			}
			if spec.Action != nil && spec.Action.Tool != "" {
				resources.Tools = append(resources.Tools, string(spec.Action.Tool))
			}
		}
		for _, provider := range bundle.LLMProviders {
			for _, model := range provider.Models {
				ref := model.Ref
				if ref.Provider == "" {
					ref.Provider = provider.Name
				}
				resources.Models = append(resources.Models, ref.String())
				if ref.Name != "" {
					resources.Models = append(resources.Models, string(ref.Name))
				}
				for _, alias := range model.Aliases {
					resources.Models = append(resources.Models, string(alias))
				}
			}
		}
		for _, alias := range bundle.LLMModelAliases {
			resources.Models = append(resources.Models, alias.Name)
		}
	}
	for _, listener := range loaded.Launch.Listeners {
		resources.Listeners = append(resources.Listeners, listener.Name)
	}
	for _, channel := range loaded.Launch.Channels {
		resources.Channels = append(resources.Channels, channel.Name)
		if channel.Session != "" {
			resources.Sessions = append(resources.Sessions, channel.Session)
		}
		if channel.Listener != "" {
			resources.Listeners = append(resources.Listeners, channel.Listener)
		}
	}
	if loaded.Distribution.Spec.DefaultSession.Name != "" {
		resources.Sessions = append(resources.Sessions, string(loaded.Distribution.Spec.DefaultSession.Name))
	}
	if loaded.Distribution.Spec.DefaultModel.Model != "" {
		resources.Models = append(resources.Models, loaded.Distribution.Spec.DefaultModel.Model)
	}
	return resources, datasourceSchemas, true, nil
}

func configSchemaDatasource(spec coredata.SourceSpec) (appconfig.DatasourceSchema, error) {
	out := appconfig.DatasourceSchema{
		Kind:        strings.TrimSpace(spec.Kind),
		Description: strings.TrimSpace(spec.Description),
	}
	if out.Kind == "" {
		return out, nil
	}
	for _, entity := range spec.Entities {
		out.Entities = append(out.Entities, string(entity.Type))
	}
	if len(spec.ConfigSchema.Data) == 0 {
		return out, nil
	}
	if spec.ConfigSchema.Format != "" && spec.ConfigSchema.Format != "json-schema" {
		return out, fmt.Errorf("app config schema: datasource %q config schema has unsupported format %q", out.Kind, spec.ConfigSchema.Format)
	}
	configSchema := map[string]any{}
	if err := json.Unmarshal(spec.ConfigSchema.Data, &configSchema); err != nil {
		return out, fmt.Errorf("app config schema: datasource %q config schema JSON: %w", out.Kind, err)
	}
	out.ConfigSchema = configSchema
	return out, nil
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
