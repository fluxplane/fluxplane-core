package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	distdeploy "github.com/fluxplane/fluxplane-core/adapters/distribution/deploy"
	distdescribe "github.com/fluxplane/fluxplane-core/adapters/distribution/describe"
	distlocal "github.com/fluxplane/fluxplane-core/adapters/distribution/local"
	"github.com/fluxplane/fluxplane-core/adapters/resources/appconfig"
	"github.com/fluxplane/fluxplane-core/contrib/datasource"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
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
	cmd.AddCommand(NewAppTargetsCommand())
	cmd.AddCommand(NewAppConfigCommand(opts.ConfigLoader, opts.EditorRunner))
	cmd.AddCommand(NewAppHealthcheckCommand())
	return cmd
}

type appBuildOptions struct {
	profiles    []string
	targets     []string
	outDir      string
	dryRun      bool
	force       bool
	listTargets bool
	runner      distdeploy.CommandRunner
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
	cmd.Flags().StringArrayVar(&opts.profiles, "profile", nil, "app profile; may be repeated or comma-separated")
	cmd.Flags().StringArrayVar(&opts.targets, "target", nil, "Named build target; may be repeated or comma-separated; omitted builds all configured targets")
	cmd.Flags().StringVar(&opts.outDir, "out", "", "output directory for generated app artifacts")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved build actions without writing artifacts or running commands")
	cmd.Flags().BoolVar(&opts.force, "force", false, "overwrite existing generated artifacts")
	cmd.Flags().BoolVar(&opts.listTargets, "list-targets", false, "list available build targets")
	return cmd
}

func runAppBuild(ctx context.Context, opts appBuildOptions, appDir string, out, errOut io.Writer) error {
	if opts.listTargets {
		return runAppTargets(ctx, appTargetsOptions{profiles: opts.profiles, kind: "build", output: "table"}, appDir, out)
	}
	_, err := distdeploy.BuildApp(ctx, distdeploy.AppBuildOptions{
		AppDir:   appDir,
		Profiles: opts.profiles,
		OutDir:   opts.outDir,
		Targets:  opts.targets,
		DryRun:   opts.dryRun,
		Force:    opts.force,
		Out:      out,
		Err:      errOut,
		Runner:   opts.runner,
	})
	return err
}

type appDeployOptions struct {
	profiles    []string
	target      string
	dryRun      bool
	force       bool
	buildPolicy string
	listTargets bool
	runner      distdeploy.CommandRunner
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
	cmd.Flags().StringVar(&opts.target, "target", "", "Named deploy target")
	cmd.Flags().StringVar(&opts.buildPolicy, "build-policy", "auto", "Build policy for deploy target artifacts: auto|always|never")
	cmd.Flags().StringArrayVar(&opts.profiles, "profile", nil, "app profile; may be repeated or comma-separated")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved deploy actions without writing artifacts or running commands")
	cmd.Flags().BoolVar(&opts.force, "force", opts.force, "overwrite generated app artifacts before deploying")
	cmd.Flags().BoolVar(&opts.listTargets, "list-targets", false, "list available deploy targets")
	return cmd
}

func runAppDeploy(ctx context.Context, opts appDeployOptions, appDir string, out, errOut io.Writer) error {
	if opts.listTargets {
		return runAppTargets(ctx, appTargetsOptions{profiles: opts.profiles, kind: "deploy", output: "table"}, appDir, out)
	}
	return distdeploy.DeployTarget(ctx, distdeploy.TargetDeployOptions{
		AppDir:      appDir,
		Target:      opts.target,
		Profiles:    opts.profiles,
		BuildPolicy: opts.buildPolicy,
		DryRun:      opts.dryRun,
		Force:       opts.force,
		Out:         out,
		Err:         errOut,
		Runner:      opts.runner,
	})
}

type appTargetsOptions struct {
	profiles []string
	kind     string
	output   string
}

// NewAppTargetsCommand returns the target inspection command.
func NewAppTargetsCommand() *cobra.Command {
	var opts appTargetsOptions
	opts.kind = "all"
	opts.output = "table"
	cmd := &cobra.Command{
		Use:   "targets [path]",
		Short: "List distribution build and deploy targets",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppTargets(cmd.Context(), opts, optionalPath(args), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringArrayVar(&opts.profiles, "profile", nil, "app profile; may be repeated or comma-separated")
	cmd.Flags().StringVar(&opts.kind, "kind", opts.kind, "target kind to list: all|build|deploy")
	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "output format: table|json")
	return cmd
}

func runAppTargets(ctx context.Context, opts appTargetsOptions, appDir string, out io.Writer) error {
	result, err := distdeploy.ListTargets(ctx, distdeploy.TargetListOptions{
		AppDir:   appDir,
		Profiles: opts.profiles,
	})
	if err != nil {
		return err
	}
	kind := strings.ToLower(strings.TrimSpace(opts.kind))
	if kind == "" {
		kind = "all"
	}
	if kind != "all" && kind != "build" && kind != "deploy" {
		return fmt.Errorf("app targets: unsupported kind %q", opts.kind)
	}
	switch strings.ToLower(strings.TrimSpace(opts.output)) {
	case "", "table", "pretty":
		renderTargetTable(out, result, kind)
		return nil
	case "json":
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(filteredTargetList(result, kind))
	default:
		return fmt.Errorf("app targets: unsupported output %q", opts.output)
	}
}

func filteredTargetList(result distdeploy.TargetListResult, kind string) distdeploy.TargetListResult {
	switch kind {
	case "build":
		result.Deploy = nil
	case "deploy":
		result.Build = nil
	}
	return result
}

func renderTargetTable(out io.Writer, result distdeploy.TargetListResult, kind string) {
	if kind == "all" || kind == "build" {
		_, _ = fmt.Fprintln(out, "Build targets")
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(table, "NAME\tKIND\tOUTPUT\tIMAGE\tSTATUS\tDESCRIPTION")
		for _, target := range result.Build {
			_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", target.Name, target.Kind, target.Output, target.Image, target.Status, target.Description)
		}
		_ = table.Flush()
		if kind == "all" {
			_, _ = fmt.Fprintln(out)
		}
	}
	if kind == "all" || kind == "deploy" {
		_, _ = fmt.Fprintln(out, "Deploy targets")
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(table, "NAME\tKIND\tBUILDS\tARTIFACT\tSTATUS\tDESCRIPTION")
		for _, target := range result.Deploy {
			_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", target.Name, target.Kind, strings.Join(target.Build, ","), target.Artifact, target.Status, target.Description)
		}
		_ = table.Flush()
	}
}

type appUndeployOptions struct {
	target  string
	dryRun  bool
	volumes bool
	runner  distdeploy.CommandRunner
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
	cmd.Flags().StringVar(&opts.target, "target", "", "Named undeploy target")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print resolved teardown commands without running them")
	cmd.Flags().BoolVar(&opts.volumes, "volumes", false, "delete persistent Docker volumes or Kubernetes PVCs")
	return cmd
}

func runAppUndeploy(ctx context.Context, opts appUndeployOptions, appDir string, out, errOut io.Writer) error {
	return distdeploy.UndeployTarget(ctx, distdeploy.TargetUndeployOptions{
		AppDir:  appDir,
		Target:  opts.target,
		DryRun:  opts.dryRun,
		Volumes: opts.volumes,
		Out:     out,
		Err:     errOut,
		Runner:  opts.runner,
	})
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

func configSchemaAvailablePlugins(appDir string) []contributions.Provider {
	hostSystem := configSchemaSystem(appDir)
	available := availablePlugins(hostSystem, hostSystem.Workspace(), nil, nil, "", false)
	return appendPluginIfMissing(available, datasource.New(nil))
}

func configSchemaSystem(appDir string) *hostSystem {
	if strings.TrimSpace(appDir) == "" {
		appDir = "."
	}
	hostSystem, err := newHost(hostConfig{Root: appDir, AllowPrivateNetwork: true})
	if err != nil {
		return nil
	}
	return hostSystem
}

func configSchemaPlugins(available []contributions.Provider) ([]appconfig.PluginSchema, error) {
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
		if provider, ok := plugin.(contributions.ConfigSchemaProvider); ok {
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

func configSchemaResources(ctx context.Context, loader Loader, appDir string, available []contributions.Provider) (appconfig.ResourceSchema, []appconfig.DatasourceSchema, bool, error) {
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
		Plugins: func(PluginFactoryContext) []contributions.Provider {
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
		for _, spec := range bundle.ActivationSets {
			resources.ActivationSets = append(resources.ActivationSets, spec.Name)
			resources.ActivationSets = append(resources.ActivationSets, spec.Aliases...)
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
