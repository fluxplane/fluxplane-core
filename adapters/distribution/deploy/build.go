// Package deploy adapts distributions into deployable artifacts such as
// container images and kubectl manifests.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/pathpattern"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	defaultConnectorsPath = "/connectors"
	defaultBaseImage      = "fluxplane/coder-base:local"
	defaultAppImage       = "agentruntime-app:latest"
	defaultMySQLDSNEnv    = "AGENTRUNTIME_DATASTORE_MYSQL_DSN"
	defaultNATSDSNEnv     = "AGENTRUNTIME_EVENTSTORE_NATS_DSN"
	defaultHealthAddr     = "127.0.0.1:18080"
	defaultKubeHealthAddr = "0.0.0.0:18080"
	defaultHealthURL      = "http://127.0.0.1:18080/control/status"
	openRouterAPIKeyEnv   = "OPENROUTER_API_KEY"
	defaultRegistryPort   = "5000"
)

const (
	// DefaultAppProvider is the model provider used by generated app containers.
	DefaultAppProvider = "openrouter"
	// DefaultAppModel is the model used by generated app containers.
	DefaultAppModel = "openai/gpt-5.5"
	// DefaultAppEffort is the reasoning effort used by generated app containers.
	DefaultAppEffort = "medium"
)

var detectK3DClusterName = currentK3DClusterName

// CommandRunner runs an external command.
type CommandRunner interface {
	Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error
}

// CommandRunnerFunc adapts a function to CommandRunner.
type CommandRunnerFunc func(context.Context, string, string, []string, io.Writer, io.Writer) error

func (f CommandRunnerFunc) Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	return f(ctx, dir, name, args, stdout, stderr)
}

type appRuntimeOptions struct {
	Provider string
	Model    string
	Effort   string
}

func (opts appRuntimeOptions) withDefaults() appRuntimeOptions {
	opts.Provider = firstNonEmpty(strings.TrimSpace(opts.Provider), DefaultAppProvider)
	opts.Model = firstNonEmpty(strings.TrimSpace(opts.Model), DefaultAppModel)
	opts.Effort = firstNonEmpty(strings.TrimSpace(opts.Effort), DefaultAppEffort)
	return opts
}

func resolveAppRuntime(loaded distribution.Loaded, opts appRuntimeOptions) appRuntimeOptions {
	providerOverride := strings.TrimSpace(opts.Provider)
	model := firstNonEmpty(strings.TrimSpace(opts.Model), strings.TrimSpace(loaded.Distribution.Spec.Deploy.Model), strings.TrimSpace(loaded.Distribution.Spec.DefaultModel.Model))
	if model == "" {
		model = DefaultAppModel
		if providerOverride == "" {
			providerOverride = DefaultAppProvider
		}
	}
	provider := firstNonEmpty(providerOverride, strings.TrimSpace(loaded.Distribution.Spec.DefaultModel.Provider), "openai")
	effort := strings.TrimSpace(opts.Effort)
	registry, err := distrun.DefaultModelRegistryWithAliases(llmProviderSpecs(loaded.Distribution.Bundles), llmModelAliases(loaded.Distribution.Bundles))
	if err == nil {
		selection := registry.ResolveModelSelection(provider, model)
		provider = selection.Provider
		model = selection.Model
		if effort == "" {
			if _, modelSpec, ok := registry.ModelSpec(provider, model); ok {
				effort = strings.TrimSpace(modelSpec.Params.ReasoningEffort)
			}
		}
	}
	return appRuntimeOptions{
		Provider: provider,
		Model:    model,
		Effort:   firstNonEmpty(effort, DefaultAppEffort),
	}.withDefaults()
}

func llmProviderSpecs(bundles []resource.ContributionBundle) []corellm.ProviderSpec {
	var out []corellm.ProviderSpec
	for _, bundle := range bundles {
		out = append(out, bundle.LLMProviders...)
	}
	return out
}

func llmModelAliases(bundles []resource.ContributionBundle) []corellm.ModelAliasSpec {
	var out []corellm.ModelAliasSpec
	for _, bundle := range bundles {
		out = append(out, bundle.LLMModelAliases...)
	}
	return out
}

// DockerBuildOptions configures a generated Docker image build.
type DockerBuildOptions struct {
	AppDir         string
	Tags           []string
	Platforms      []string
	Push           bool
	DryRun         bool
	BaseImage      string
	ConnectorsPath string
	Provider       string
	Model          string
	Effort         string
	Out            io.Writer
	Err            io.Writer
	Runner         CommandRunner
}

// DockerBuildResult describes the resolved image build.
type DockerBuildResult struct {
	ContextDir string
	Dockerfile string
	Tags       []string
	Platforms  []string
	Assets     []string
	BaseImage  string
	Command    []string
	DryRun     bool
}

// AppBuildOptions configures app-local build artifact generation.
type AppBuildOptions struct {
	AppDir         string
	OutDir         string
	Targets        []string
	Tags           []string
	Image          string
	Platforms      []string
	Push           bool
	DryRun         bool
	Force          bool
	BaseImage      string
	ConnectorsPath string
	Provider       string
	Model          string
	Effort         string
	Out            io.Writer
	Err            io.Writer
	Runner         CommandRunner
}

// AppBuildResult describes generated app-local artifacts.
type AppBuildResult struct {
	AppDir     string
	OutDir     string
	Name       string
	Targets    []string
	Tags       []string
	Artifacts  []string
	Command    []string
	DryRun     bool
	Dockerfile string
	Compose    string
	Kubernetes string
	Binary     string
}

// BaseImageOptions configures the reusable coder runtime image build.
type BaseImageOptions struct {
	RepoRoot  string
	CoderPath string
	Tags      []string
	Platforms []string
	Push      bool
	DryRun    bool
	Out       io.Writer
	Err       io.Writer
	Runner    CommandRunner
}

// BaseImageResult describes the resolved coder base image build.
type BaseImageResult struct {
	ContextDir string
	Dockerfile string
	Tags       []string
	Platforms  []string
	Command    []string
	DryRun     bool
}

// ComposeOptions configures Docker Compose artifact generation.
type ComposeOptions struct {
	AppDir   string
	Image    string
	Provider string
	Model    string
	Effort   string
	DryRun   bool
	Out      io.Writer
}

// ComposeResult describes the generated Docker Compose artifact.
type ComposeResult struct {
	AppDir  string
	Image   string
	Content string
	DryRun  bool
}

// ComposeDeployOptions configures local Docker Compose deployment.
type ComposeDeployOptions struct {
	AppDir         string
	Image          string
	BaseImage      string
	ConnectorsPath string
	Provider       string
	Model          string
	Effort         string
	DryRun         bool
	Force          bool
	Detach         bool
	Out            io.Writer
	Err            io.Writer
	Runner         CommandRunner
}

// ComposeDeployResult describes the local Docker Compose deployment steps.
type ComposeDeployResult struct {
	BaseImage BaseImageResult
	AppBuild  AppBuildResult
	Command   []string
	DryRun    bool
}

// ComposeUndeployOptions configures local Docker Compose teardown.
type ComposeUndeployOptions struct {
	AppDir  string
	DryRun  bool
	Volumes bool
	Out     io.Writer
	Err     io.Writer
	Runner  CommandRunner
}

// ComposeUndeployResult describes the local Docker Compose teardown command.
type ComposeUndeployResult struct {
	AppDir  string
	Compose string
	Command []string
	DryRun  bool
	Volumes bool
}

// KubernetesOptions configures kubectl-manifest deployment.
type KubernetesOptions struct {
	AppDir         string
	Image          string
	BaseImage      string
	ConnectorsPath string
	Provider       string
	Model          string
	Effort         string
	Namespace      string
	RegistryMode   string
	Registry       string
	DryRun         bool
	Force          bool
	Out            io.Writer
	Err            io.Writer
	Runner         CommandRunner
}

// KubernetesResult describes generated Kubernetes deployment artifacts.
type KubernetesResult struct {
	BaseImage  BaseImageResult
	AppBuild   AppBuildResult
	Manifest   string
	Namespace  string
	Image      string
	Registry   string
	Commands   [][]string
	DryRun     bool
	SecretName string
}

// KubernetesUndeployOptions configures kubectl-manifest teardown.
type KubernetesUndeployOptions struct {
	AppDir    string
	Namespace string
	DryRun    bool
	Volumes   bool
	Out       io.Writer
	Err       io.Writer
	Runner    CommandRunner
}

// KubernetesUndeployResult describes generated Kubernetes teardown steps.
type KubernetesUndeployResult struct {
	AppDir    string
	Name      string
	Namespace string
	Commands  [][]string
	DryRun    bool
	Volumes   bool
}

// KubernetesManifestOptions configures plain Kubernetes manifest generation.
type KubernetesManifestOptions struct {
	AppDir         string
	Image          string
	ConnectorsPath string
	Provider       string
	Model          string
	Effort         string
	Namespace      string
	RegistryMode   string
	Registry       string
	DryRun         bool
	Out            io.Writer
}

// KubernetesManifestResult describes generated Kubernetes manifest content.
type KubernetesManifestResult struct {
	AppDir     string
	Name       string
	Namespace  string
	Image      string
	SecretName string
	Content    string
	DryRun     bool
}

// BuildApp builds app-local artifacts such as bin launchers, Dockerfiles,
// Docker Compose resources, and Docker images.
func BuildApp(ctx context.Context, opts AppBuildOptions) (AppBuildResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	targets, err := appBuildTargets(opts.Targets)
	if err != nil {
		return AppBuildResult{}, err
	}
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return AppBuildResult{}, err
	}
	outDir := strings.TrimSpace(opts.OutDir)
	if outDir == "" {
		outDir = loaded.Root
	}
	outDir, err = filepath.Abs(outDir)
	if err != nil {
		return AppBuildResult{}, err
	}
	connectorsPath := strings.TrimSpace(opts.ConnectorsPath)
	if connectorsPath == "" {
		connectorsPath = defaultConnectorsPath
	}
	appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
		Provider: opts.Provider,
		Model:    opts.Model,
		Effort:   opts.Effort,
	})
	baseImage := strings.TrimSpace(opts.BaseImage)
	if baseImage == "" {
		baseImage = defaultBaseImage
	}
	tags := resolveAppBuildTags(loaded.Distribution.Spec, opts)
	name := distributionName(loaded.Distribution.Spec)
	binaryPath := filepath.Join(outDir, "bin", composeServiceName(name))
	dockerfilePath := filepath.Join(outDir, "Dockerfile")
	composePath := filepath.Join(outDir, "docker-compose.yaml")
	kubernetesPath := kubernetesManifestPath(loaded.Root, outDir, opts.OutDir)
	composeImage := firstTag(tags)
	if composeImage == "" {
		composeImage = defaultAppImage
	}
	result := AppBuildResult{
		AppDir:     loaded.Root,
		OutDir:     outDir,
		Name:       name,
		Targets:    targets,
		Tags:       tags,
		DryRun:     opts.DryRun,
		Dockerfile: dockerfilePath,
		Compose:    composePath,
		Kubernetes: kubernetesPath,
		Binary:     binaryPath,
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	for _, target := range targets {
		switch target {
		case "binary":
			result.Artifacts = append(result.Artifacts, binaryPath)
			if err := maybeWriteFile(binaryPath, appLauncherScript(loaded.Root), 0o755, opts.DryRun, opts.Force, out); err != nil {
				return AppBuildResult{}, err
			}
		case "dockerfile":
			result.Artifacts = append(result.Artifacts, dockerfilePath)
			if err := maybeWriteFile(dockerfilePath, workspaceDockerfile(baseImage, connectorsPath, appRuntime), 0o600, opts.DryRun, opts.Force, out); err != nil {
				return AppBuildResult{}, err
			}
		case "docker-compose":
			result.Artifacts = append(result.Artifacts, composePath)
			if err := maybeWriteFile(composePath, dockerComposeContent(name, composeImage, connectorsPath, appRuntime, loaded.Launch), 0o600, opts.DryRun, opts.Force, out); err != nil {
				return AppBuildResult{}, err
			}
		case "kubernetes":
			result.Artifacts = append(result.Artifacts, kubernetesPath)
			content, err := kubernetesContent(loaded, kubernetesRenderOptions{
				Name:            name,
				Namespace:       kubernetesName(name),
				Image:           composeImage,
				ConnectorsPath:  connectorsPath,
				AppRuntime:      appRuntime,
				IncludeRegistry: false,
			})
			if err != nil {
				return AppBuildResult{}, err
			}
			if err := maybeWriteKubernetesManifest(loaded.Root, kubernetesPath, content.Content, opts.DryRun, opts.Force, out); err != nil {
				return AppBuildResult{}, err
			}
		case "docker-image":
			command, err := dockerCommand(tags, cleanStrings(opts.Platforms), opts.Push)
			if err != nil {
				return AppBuildResult{}, err
			}
			command = append(command, "-f", dockerfilePath, loaded.Root)
			result.Command = command
			printAppBuildCommand(out, command, opts.DryRun)
			if !opts.DryRun {
				if err := ensureDockerfileForImage(dockerfilePath, baseImage, connectorsPath, appRuntime, opts.Force); err != nil {
					return AppBuildResult{}, err
				}
				if err := runner.Run(ctx, "", command[0], command[1:], out, errOut); err != nil {
					return AppBuildResult{}, err
				}
			}
		default:
			return AppBuildResult{}, fmt.Errorf("distribution build: unsupported app target %q", target)
		}
	}
	return result, nil
}

// DeployDockerCompose builds local app artifacts and starts them with Docker Compose.
func DeployDockerCompose(ctx context.Context, opts ComposeDeployOptions) (ComposeDeployResult, error) {
	baseImage := strings.TrimSpace(opts.BaseImage)
	if baseImage == "" {
		baseImage = defaultBaseImage
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	base, err := BuildCoderBaseDocker(ctx, BaseImageOptions{
		Tags:   []string{baseImage},
		DryRun: opts.DryRun,
		Out:    out,
		Err:    errOut,
		Runner: runner,
	})
	if err != nil {
		return ComposeDeployResult{}, err
	}
	app, err := BuildApp(ctx, AppBuildOptions{
		AppDir:         opts.AppDir,
		Targets:        []string{"dockerfile", "docker-compose", "docker-image"},
		Image:          opts.Image,
		DryRun:         opts.DryRun,
		Force:          opts.Force,
		BaseImage:      baseImage,
		ConnectorsPath: opts.ConnectorsPath,
		Provider:       opts.Provider,
		Model:          opts.Model,
		Effort:         opts.Effort,
		Out:            out,
		Err:            errOut,
		Runner:         runner,
	})
	if err != nil {
		return ComposeDeployResult{}, err
	}
	command := []string{"docker", "compose", "-f", app.Compose, "up"}
	if opts.Detach {
		command = append(command, "-d")
	}
	result := ComposeDeployResult{
		BaseImage: base,
		AppBuild:  app,
		Command:   command,
		DryRun:    opts.DryRun,
	}
	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
		return result, nil
	}
	if err := runner.Run(ctx, app.AppDir, command[0], command[1:], out, errOut); err != nil {
		return ComposeDeployResult{}, err
	}
	return result, nil
}

// UndeployDockerCompose stops a local Docker Compose app deployment.
func UndeployDockerCompose(ctx context.Context, opts ComposeUndeployOptions) (ComposeUndeployResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return ComposeUndeployResult{}, err
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	composePath := filepath.Join(loaded.Root, "docker-compose.yaml")
	command := []string{"docker", "compose", "-f", composePath, "down"}
	if opts.Volumes {
		command = append(command, "-v")
	}
	result := ComposeUndeployResult{
		AppDir:  loaded.Root,
		Compose: composePath,
		Command: command,
		DryRun:  opts.DryRun,
		Volumes: opts.Volumes,
	}
	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
		return result, nil
	}
	if err := runner.Run(ctx, loaded.Root, command[0], command[1:], out, errOut); err != nil {
		return ComposeUndeployResult{}, err
	}
	return result, nil
}

// DeployKubernetes builds local app artifacts, pushes the app image according to
// the registry mode, and applies generated kubectl manifests.
func DeployKubernetes(ctx context.Context, opts KubernetesOptions) (KubernetesResult, error) {
	baseImage := strings.TrimSpace(opts.BaseImage)
	if baseImage == "" {
		baseImage = defaultBaseImage
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	loaded, err := distlocal.Load(ctx, firstNonEmpty(strings.TrimSpace(opts.AppDir), "."))
	if err != nil {
		return KubernetesResult{}, err
	}
	name := distributionName(loaded.Distribution.Spec)
	namespace := kubernetesName(firstNonEmpty(strings.TrimSpace(opts.Namespace), name))
	registryMode := strings.ToLower(firstNonEmpty(strings.TrimSpace(opts.RegistryMode), "namespace"))
	if registryMode != "namespace" && registryMode != "external" {
		return KubernetesResult{}, fmt.Errorf("distribution deploy: unsupported kubernetes registry mode %q", opts.RegistryMode)
	}
	tags := resolveAppBuildTags(loaded.Distribution.Spec, AppBuildOptions{Image: opts.Image})
	sourceImage := firstTag(tags)
	if sourceImage == "" {
		sourceImage = defaultAppImage
	}
	refs := kubernetesImageRefs(namespace, name, sourceImage, registryMode, opts.Registry)
	k3dCluster := "<current-k3d-cluster>"
	if registryMode == "namespace" && !opts.DryRun {
		var err error
		k3dCluster, err = detectK3DClusterName(ctx)
		if err != nil {
			return KubernetesResult{}, err
		}
	}
	appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
		Provider: opts.Provider,
		Model:    opts.Model,
		Effort:   opts.Effort,
	})

	base, err := BuildCoderBaseDocker(ctx, BaseImageOptions{
		Tags:   []string{baseImage},
		DryRun: opts.DryRun,
		Out:    out,
		Err:    errOut,
		Runner: runner,
	})
	if err != nil {
		return KubernetesResult{}, err
	}
	app, err := BuildApp(ctx, AppBuildOptions{
		AppDir:         loaded.Root,
		Targets:        []string{"dockerfile", "docker-image"},
		Image:          sourceImage,
		DryRun:         opts.DryRun,
		Force:          opts.Force,
		BaseImage:      baseImage,
		ConnectorsPath: opts.ConnectorsPath,
		Provider:       opts.Provider,
		Model:          opts.Model,
		Effort:         opts.Effort,
		Out:            out,
		Err:            errOut,
		Runner:         runner,
	})
	if err != nil {
		return KubernetesResult{}, err
	}
	manifest := kubernetesManifestPath(loaded.Root, app.OutDir, "")
	rendered, err := kubernetesContent(loaded, kubernetesRenderOptions{
		Name:            name,
		Namespace:       namespace,
		Image:           refs.Cluster,
		ConnectorsPath:  opts.ConnectorsPath,
		AppRuntime:      appRuntime,
		IncludeRegistry: false,
	})
	if err != nil {
		return KubernetesResult{}, err
	}
	if err := maybeWriteKubernetesManifest(loaded.Root, manifest, rendered.Content, opts.DryRun, opts.Force, out); err != nil {
		return KubernetesResult{}, err
	}

	result := KubernetesResult{
		BaseImage:  base,
		AppBuild:   app,
		Manifest:   manifest,
		Namespace:  namespace,
		Image:      refs.Cluster,
		Registry:   refs.Registry,
		DryRun:     opts.DryRun,
		SecretName: rendered.SecretName,
	}
	if rendered.SecretName != "" {
		printKubernetesSecretSummary(out, rendered.SecretName, rendered.SecretKeys, opts.DryRun)
	}

	if registryMode == "namespace" {
		command := []string{"k3d", "image", "import", sourceImage, "--cluster", k3dCluster}
		result.Commands = append(result.Commands, command)
		if opts.DryRun {
			_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
		} else if err := runner.Run(ctx, "", command[0], command[1:], out, errOut); err != nil {
			return KubernetesResult{}, err
		}
		cleanup := []string{"kubectl", "delete", "deployment/coder-registry", "service/coder-registry", "pvc/coder-registry-data", "-n", namespace, "--ignore-not-found"}
		result.Commands = append(result.Commands, cleanup)
		if opts.DryRun {
			_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(cleanup, " "))
		} else if err := runner.Run(ctx, "", cleanup[0], cleanup[1:], out, errOut); err != nil {
			return KubernetesResult{}, err
		}
	}

	if registryMode == "external" {
		pushCommands := kubernetesPushCommands(sourceImage, refs.Push)
		for _, command := range pushCommands {
			result.Commands = append(result.Commands, command)
			if opts.DryRun {
				_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
				continue
			}
			if err := runner.Run(ctx, "", command[0], command[1:], out, errOut); err != nil {
				return KubernetesResult{}, err
			}
		}
	}
	apply := []string{"kubectl", "apply", "-f", manifest}
	result.Commands = append(result.Commands, apply)
	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(apply, " "))
		return result, nil
	}
	if err := runner.Run(ctx, "", apply[0], apply[1:], out, errOut); err != nil {
		return KubernetesResult{}, err
	}
	return result, nil
}

// UndeployKubernetes deletes generated Kubernetes app resources.
func UndeployKubernetes(ctx context.Context, opts KubernetesUndeployOptions) (KubernetesUndeployResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return KubernetesUndeployResult{}, err
	}
	name := distributionName(loaded.Distribution.Spec)
	namespace := kubernetesName(firstNonEmpty(strings.TrimSpace(opts.Namespace), name))
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	content := kubernetesTeardownContent(loaded, kubernetesTeardownOptions{
		Name:      name,
		Namespace: namespace,
	})
	result := KubernetesUndeployResult{
		AppDir:    loaded.Root,
		Name:      name,
		Namespace: namespace,
		DryRun:    opts.DryRun,
		Volumes:   opts.Volumes,
	}
	deleteCommand := []string{"kubectl", "delete", "-f", "<kubernetes-teardown-manifest>", "--ignore-not-found"}
	var tempFile string
	if !opts.DryRun {
		tempFile, err = writeTempManifest("coder-kubernetes-undeploy-*.yaml", content)
		if err != nil {
			return KubernetesUndeployResult{}, err
		}
		defer func() { _ = os.Remove(tempFile) }()
		deleteCommand = []string{"kubectl", "delete", "-f", tempFile, "--ignore-not-found"}
	}
	result.Commands = append(result.Commands, deleteCommand)
	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(deleteCommand, " "))
	} else if err := runner.Run(ctx, "", deleteCommand[0], deleteCommand[1:], out, errOut); err != nil {
		return KubernetesUndeployResult{}, err
	}
	if opts.Volumes {
		pvcs := kubernetesTeardownPVCs(loaded)
		if len(pvcs) > 0 {
			volumeCommand := append([]string{"kubectl", "delete", "pvc"}, pvcs...)
			volumeCommand = append(volumeCommand, "-n", namespace, "--ignore-not-found")
			result.Commands = append(result.Commands, volumeCommand)
			if opts.DryRun {
				_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(volumeCommand, " "))
			} else if err := runner.Run(ctx, "", volumeCommand[0], volumeCommand[1:], out, errOut); err != nil {
				return KubernetesUndeployResult{}, err
			}
		}
	}
	return result, nil
}

// BuildDocker builds a Docker image for a local app distribution.
func BuildDocker(ctx context.Context, opts DockerBuildOptions) (DockerBuildResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	connectorsPath := strings.TrimSpace(opts.ConnectorsPath)
	if connectorsPath == "" {
		connectorsPath = defaultConnectorsPath
	}
	baseImage := strings.TrimSpace(opts.BaseImage)
	if baseImage == "" {
		baseImage = defaultBaseImage
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}

	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return DockerBuildResult{}, err
	}
	appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
		Provider: opts.Provider,
		Model:    opts.Model,
		Effort:   opts.Effort,
	})
	spec := loaded.Distribution.Spec
	if spec.Build.Docker == nil {
		return DockerBuildResult{}, fmt.Errorf("distribution build: %q has no distribution.build.docker config", spec.Name)
	}
	if len(spec.Build.Assets) == 0 {
		return DockerBuildResult{}, fmt.Errorf("distribution build: %q has no distribution.build.assets", spec.Name)
	}
	tags := resolveTags(spec, opts.Tags)
	platforms := cleanStrings(opts.Platforms)
	if len(platforms) == 0 && spec.Build.Docker != nil {
		platforms = cleanStrings(spec.Build.Docker.Platforms)
	}
	command, err := dockerCommand(tags, platforms, opts.Push)
	if err != nil {
		return DockerBuildResult{}, err
	}

	tempDir, err := os.MkdirTemp("", "coder-app-docker-build-*")
	if err != nil {
		return DockerBuildResult{}, fmt.Errorf("distribution build: create temp context: %w", err)
	}
	cleanup := !opts.DryRun
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()

	assets, err := prepareAppContext(ctx, tempDir, loaded.Root, spec.Build.Assets, baseImage, connectorsPath, appRuntime)
	if err != nil {
		return DockerBuildResult{}, err
	}
	command = append(command, tempDir)
	result := DockerBuildResult{
		ContextDir: tempDir,
		Dockerfile: filepath.Join(tempDir, "Dockerfile"),
		Tags:       tags,
		Platforms:  platforms,
		Assets:     assets,
		BaseImage:  baseImage,
		Command:    command,
		DryRun:     opts.DryRun,
	}
	if opts.DryRun {
		printDryRun(out, result)
		return result, nil
	}
	if len(command) == 0 {
		return DockerBuildResult{}, errors.New("distribution build: empty docker command")
	}
	if err := runner.Run(ctx, "", command[0], command[1:], out, errOut); err != nil {
		return DockerBuildResult{}, err
	}
	return result, nil
}

// BuildCoderBaseDocker builds the reusable Docker base image containing coder.
func BuildCoderBaseDocker(ctx context.Context, opts BaseImageOptions) (BaseImageResult, error) {
	tags := cleanStrings(opts.Tags)
	if len(tags) == 0 {
		tags = []string{defaultBaseImage}
	}
	platforms := cleanStrings(opts.Platforms)
	command, err := dockerCommand(tags, platforms, opts.Push)
	if err != nil {
		return BaseImageResult{}, err
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	tempDir, err := os.MkdirTemp("", "coder-base-docker-build-*")
	if err != nil {
		return BaseImageResult{}, fmt.Errorf("distribution build: create base image context: %w", err)
	}
	cleanup := !opts.DryRun
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err := prepareBaseImageContext(ctx, tempDir, opts); err != nil {
		return BaseImageResult{}, err
	}
	command = append(command, tempDir)
	result := BaseImageResult{
		ContextDir: tempDir,
		Dockerfile: filepath.Join(tempDir, "Dockerfile"),
		Tags:       tags,
		Platforms:  platforms,
		Command:    command,
		DryRun:     opts.DryRun,
	}
	if opts.DryRun {
		printBaseImageDryRun(out, result)
		return result, nil
	}
	if len(command) == 0 {
		return BaseImageResult{}, errors.New("distribution build: empty docker command")
	}
	if err := runner.Run(ctx, "", command[0], command[1:], out, errOut); err != nil {
		return BaseImageResult{}, err
	}
	return result, nil
}

// GenerateDockerCompose generates a minimal Docker Compose deployment for an app image.
func GenerateDockerCompose(ctx context.Context, opts ComposeOptions) (ComposeResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return ComposeResult{}, err
	}
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = firstTag(resolveTags(loaded.Distribution.Spec, nil))
	}
	if image == "" {
		image = defaultAppImage
	}
	appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
		Provider: opts.Provider,
		Model:    opts.Model,
		Effort:   opts.Effort,
	})
	content := dockerComposeContent(loaded.Distribution.Spec.Name, image, defaultConnectorsPath, appRuntime, loaded.Launch)
	result := ComposeResult{
		AppDir:  loaded.Root,
		Image:   image,
		Content: content,
		DryRun:  opts.DryRun,
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	if opts.DryRun {
		_, _ = io.WriteString(out, content)
		return result, nil
	}
	return ComposeResult{}, errors.New("distribution deploy: docker-compose generation currently requires --dry-run")
}

// GenerateKubernetesManifests generates plain kubectl manifests for an app image.
func GenerateKubernetesManifests(ctx context.Context, opts KubernetesManifestOptions) (KubernetesManifestResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return KubernetesManifestResult{}, err
	}
	name := distributionName(loaded.Distribution.Spec)
	namespace := kubernetesName(firstNonEmpty(strings.TrimSpace(opts.Namespace), name))
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = firstTag(resolveTags(loaded.Distribution.Spec, nil))
	}
	if image == "" {
		image = defaultAppImage
	}
	appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
		Provider: opts.Provider,
		Model:    opts.Model,
		Effort:   opts.Effort,
	})
	rendered, err := kubernetesContent(loaded, kubernetesRenderOptions{
		Name:            name,
		Namespace:       namespace,
		Image:           image,
		ConnectorsPath:  opts.ConnectorsPath,
		AppRuntime:      appRuntime,
		IncludeRegistry: false,
	})
	if err != nil {
		return KubernetesManifestResult{}, err
	}
	result := KubernetesManifestResult{
		AppDir:     loaded.Root,
		Name:       name,
		Namespace:  namespace,
		Image:      image,
		SecretName: rendered.SecretName,
		Content:    rendered.Content,
		DryRun:     opts.DryRun,
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	if opts.DryRun {
		_, _ = io.WriteString(out, rendered.Content)
	}
	return result, nil
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func prepareAppContext(ctx context.Context, contextDir, appRoot string, assetPatterns []string, baseImage, connectorsPath string, appRuntime appRuntimeOptions) ([]string, error) {
	assets, err := copyAssets(ctx, appRoot, filepath.Join(contextDir, "app"), assetPatterns)
	if err != nil {
		return nil, err
	}
	if err := writeAppDockerfile(filepath.Join(contextDir, "Dockerfile"), baseImage, connectorsPath, appRuntime); err != nil {
		return nil, err
	}
	return assets, nil
}

func prepareBaseImageContext(ctx context.Context, contextDir string, opts BaseImageOptions) error {
	repoRoot := strings.TrimSpace(opts.RepoRoot)
	if repoRoot == "" {
		return prepareBinaryBaseImageContext(contextDir, opts.CoderPath)
	}
	repoRoot, err := findRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	if err := copyDir(ctx, repoRoot, filepath.Join(contextDir, "src", "agentruntime"), sourceSkip); err != nil {
		return fmt.Errorf("distribution build: copy source: %w", err)
	}
	replaceCopies, err := copyLocalReplaces(ctx, repoRoot, contextDir)
	if err != nil {
		return err
	}
	return writeSourceBaseDockerfile(filepath.Join(contextDir, "Dockerfile"), replaceCopies)
}

func prepareBinaryBaseImageContext(contextDir, coderPath string) error {
	coderPath = strings.TrimSpace(coderPath)
	if coderPath == "" {
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("distribution build: resolve coder executable: %w", err)
		}
		coderPath = executable
	}
	if resolved, err := filepath.EvalSymlinks(coderPath); err == nil {
		coderPath = resolved
	}
	if err := copyFile(coderPath, filepath.Join(contextDir, "coder")); err != nil {
		return fmt.Errorf("distribution build: copy coder executable: %w", err)
	}
	return writeBinaryBaseDockerfile(filepath.Join(contextDir, "Dockerfile"))
}

func resolveTags(spec coredistribution.Spec, override []string) []string {
	if tags := cleanStrings(override); len(tags) > 0 {
		return tags
	}
	if spec.Build.Docker != nil && strings.TrimSpace(spec.Build.Docker.Image) != "" {
		image := strings.TrimSpace(spec.Build.Docker.Image)
		tags := cleanStrings(spec.Build.Docker.Tags)
		if len(tags) == 0 {
			return []string{image + ":latest"}
		}
		out := make([]string, 0, len(tags))
		for _, tag := range tags {
			if strings.Contains(tag, "/") || strings.Contains(tag, ":") {
				out = append(out, tag)
				continue
			}
			out = append(out, image+":"+tag)
		}
		return out
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = "agentruntime-app"
	}
	return []string{name + ":latest"}
}

func resolveAppBuildTags(spec coredistribution.Spec, opts AppBuildOptions) []string {
	var override []string
	if image := strings.TrimSpace(opts.Image); image != "" {
		override = append(override, image)
	}
	override = append(override, opts.Tags...)
	return resolveTags(spec, override)
}

func distributionName(spec coredistribution.Spec) string {
	if name := strings.TrimSpace(spec.Name); name != "" {
		return name
	}
	return "app"
}

func appBuildTargets(values []string) ([]string, error) {
	raw := cleanStrings(values)
	if len(raw) == 0 {
		raw = []string{"all"}
	}
	expanded := make([]string, 0, len(raw))
	for _, target := range raw {
		switch target {
		case "all":
			expanded = append(expanded, "binary", "dockerfile", "docker-compose", "docker-image")
		case "binary", "dockerfile", "docker-image", "docker-compose", "kubernetes":
			expanded = append(expanded, target)
		default:
			return nil, fmt.Errorf("distribution build: unsupported app target %q", target)
		}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(expanded))
	for _, target := range expanded {
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	return out, nil
}

func dockerCommand(tags, platforms []string, push bool) ([]string, error) {
	if len(tags) == 0 {
		return nil, errors.New("distribution build: at least one image tag is required")
	}
	var args []string
	if len(platforms) == 0 {
		args = []string{"docker", "build"}
		for _, tag := range tags {
			args = append(args, "-t", tag)
		}
		return args, nil
	}
	args = []string{"docker", "buildx", "build", "--platform", strings.Join(platforms, ",")}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	if len(platforms) == 1 {
		if push {
			args = append(args, "--push")
		} else {
			args = append(args, "--load")
		}
		return args, nil
	}
	if !push {
		return nil, fmt.Errorf("distribution build: multiple platforms require --push")
	}
	args = append(args, "--push")
	return args, nil
}

func copyAssets(ctx context.Context, root, dstRoot string, patterns []string) ([]string, error) {
	matches := map[string]struct{}{}
	for _, pattern := range patterns {
		clean, err := cleanAssetPattern(pattern)
		if err != nil {
			return nil, err
		}
		found, err := matchAssets(ctx, root, clean)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, fmt.Errorf("distribution build: asset pattern %q matched no files", pattern)
		}
		for _, rel := range found {
			matches[rel] = struct{}{}
		}
	}
	assets := make([]string, 0, len(matches))
	for rel := range matches {
		assets = append(assets, rel)
	}
	sort.Strings(assets)
	for _, rel := range assets {
		if err := copyFile(filepath.Join(root, filepath.FromSlash(rel)), filepath.Join(dstRoot, filepath.FromSlash(rel))); err != nil {
			return nil, err
		}
	}
	return assets, nil
}

func cleanAssetPattern(pattern string) (string, error) {
	pattern = strings.TrimSpace(filepath.ToSlash(pattern))
	if pattern == "" {
		return "", fmt.Errorf("distribution build: asset pattern is empty")
	}
	if path.IsAbs(pattern) {
		return "", fmt.Errorf("distribution build: absolute asset pattern %q is not allowed", pattern)
	}
	for _, part := range strings.Split(pattern, "/") {
		if part == ".." {
			return "", fmt.Errorf("distribution build: asset pattern %q escapes the app root", pattern)
		}
	}
	return path.Clean(pattern), nil
}

func matchAssets(ctx context.Context, root, pattern string) ([]string, error) {
	if !pathpattern.HasMeta(pattern) {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		if !info.IsDir() {
			return []string{pattern}, nil
		}
		var out []string
		err = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(pattern)), func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, file)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		return out, err
	}
	compiled, err := pathpattern.Compile(pattern)
	if err != nil {
		return nil, err
	}
	var out []string
	err = filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel == ".git" || rel == ".codex" {
				return filepath.SkipDir
			}
			return nil
		}
		if compiled.Match(rel) {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

type replaceCopy struct {
	SourceRel    string
	ContextRel   string
	ContainerAbs string
}

func copyLocalReplaces(ctx context.Context, repoRoot, contextDir string) ([]replaceCopy, error) {
	paths, err := localReplacePaths(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return nil, err
	}
	var copies []replaceCopy
	seen := map[string]struct{}{}
	for _, rel := range paths {
		abs := filepath.Clean(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("distribution build: local replace %s: %w", rel, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("distribution build: local replace %s is not a directory", rel)
		}
		name := filepath.Base(abs)
		contextRel := filepath.ToSlash(filepath.Join("localmods", name))
		containerAbs := path.Clean(path.Join("/src/agentruntime", filepath.ToSlash(rel)))
		if err := copyDir(ctx, abs, filepath.Join(contextDir, filepath.FromSlash(contextRel)), sourceSkip); err != nil {
			return nil, fmt.Errorf("distribution build: copy local replace %s: %w", rel, err)
		}
		copies = append(copies, replaceCopy{SourceRel: filepath.ToSlash(rel), ContextRel: contextRel, ContainerAbs: containerAbs})
	}
	sort.Slice(copies, func(i, j int) bool { return copies[i].ContainerAbs < copies[j].ContainerAbs })
	return copies, nil
}

func localReplacePaths(goMod string) ([]string, error) {
	data, err := os.ReadFile(goMod)
	if err != nil {
		return nil, fmt.Errorf("distribution build: read go.mod: %w", err)
	}
	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || !strings.HasPrefix(line, "replace ") {
			continue
		}
		fields := strings.Fields(line)
		for i, field := range fields {
			if field != "=>" || i+1 >= len(fields) {
				continue
			}
			rel := strings.Trim(fields[i+1], `"`)
			if rel == "" || strings.Contains(rel, "://") || strings.HasPrefix(rel, "github.com/") {
				continue
			}
			if filepath.IsAbs(rel) || strings.HasPrefix(rel, ".") {
				paths = append(paths, filepath.ToSlash(rel))
			}
		}
	}
	return paths, nil
}

func writeAppDockerfile(filename, baseImage, connectorsPath string, appRuntime appRuntimeOptions) error {
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(baseImage)
	b.WriteByte('\n')
	b.WriteString("COPY app /app\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("ENTRYPOINT [\"/usr/local/bin/coder\"]\n")
	cmd, _ := json.Marshal(appServeCommand(connectorsPath, appRuntime))
	b.WriteString("CMD ")
	b.Write(cmd)
	b.WriteByte('\n')
	b.WriteString("HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD ")
	health, _ := json.Marshal(appHealthcheckCommand())
	b.Write(health)
	b.WriteByte('\n')
	return os.WriteFile(filename, []byte(b.String()), 0o600)
}

func workspaceDockerfile(baseImage, connectorsPath string, appRuntime appRuntimeOptions) string {
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(baseImage)
	b.WriteByte('\n')
	b.WriteString("COPY . /app\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("ENTRYPOINT [\"/usr/local/bin/coder\"]\n")
	cmd, _ := json.Marshal(appServeCommand(connectorsPath, appRuntime))
	b.WriteString("CMD ")
	b.Write(cmd)
	b.WriteByte('\n')
	b.WriteString("HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD ")
	health, _ := json.Marshal(appHealthcheckCommand())
	b.Write(health)
	b.WriteByte('\n')
	return b.String()
}

func appServeCommand(connectorsPath string, appRuntime appRuntimeOptions) []string {
	return appServeCommandWithHealthAddr(connectorsPath, appRuntime, defaultHealthAddr)
}

func appServeCommandWithHealthAddr(connectorsPath string, appRuntime appRuntimeOptions, healthAddr string) []string {
	connectorsPath = strings.TrimSpace(connectorsPath)
	if connectorsPath == "" {
		connectorsPath = defaultConnectorsPath
	}
	if strings.TrimSpace(healthAddr) == "" {
		healthAddr = defaultHealthAddr
	}
	appRuntime = appRuntime.withDefaults()
	return []string{
		"app", "serve", "/app",
		"--connectors-path", connectorsPath,
		"--health-addr", healthAddr,
		"--provider", appRuntime.Provider,
		"--model", appRuntime.Model,
		"--effort", appRuntime.Effort,
	}
}

func appHealthcheckCommand() []string {
	return []string{"/usr/local/bin/coder", "app", "healthcheck", "--url", defaultHealthURL}
}

func appLauncherScript(appRoot string) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env sh\n")
	b.WriteString("set -eu\n")
	b.WriteString("exec coder app run ")
	b.WriteString(shellQuote(appRoot))
	b.WriteString(" \"$@\"\n")
	return b.String()
}

func writeSourceBaseDockerfile(filename string, replaces []replaceCopy) error {
	var b strings.Builder
	b.WriteString("FROM golang:1.26-bookworm AS builder\n")
	b.WriteString("WORKDIR /src/agentruntime\n")
	for _, repl := range replaces {
		b.WriteString("COPY ")
		b.WriteString(repl.ContextRel)
		b.WriteString(" ")
		b.WriteString(repl.ContainerAbs)
		b.WriteByte('\n')
	}
	b.WriteString("COPY src/agentruntime /src/agentruntime\n")
	b.WriteString("RUN go build -trimpath -ldflags=\"-s -w\" -o /out/coder ./cmd/coder\n\n")
	b.WriteString("FROM debian:bookworm-slim AS runtime\n")
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/*\n")
	b.WriteString("COPY --from=builder /out/coder /usr/local/bin/coder\n")
	b.WriteString("ENTRYPOINT [\"/usr/local/bin/coder\"]\n")
	b.WriteByte('\n')
	return os.WriteFile(filename, []byte(b.String()), 0o600)
}

func writeBinaryBaseDockerfile(filename string) error {
	var b strings.Builder
	b.WriteString("FROM debian:bookworm-slim AS runtime\n")
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/*\n")
	b.WriteString("COPY coder /usr/local/bin/coder\n")
	b.WriteString("ENTRYPOINT [\"/usr/local/bin/coder\"]\n")
	return os.WriteFile(filename, []byte(b.String()), 0o600)
}

func sourceSkip(rel string, entry fs.DirEntry) bool {
	if !entry.IsDir() {
		return false
	}
	switch rel {
	case ".git", ".codex", ".agents/architecture":
		return true
	default:
		return false
	}
}

func copyDir(ctx context.Context, src, dst string, skip func(string, fs.DirEntry) bool) error {
	return filepath.WalkDir(src, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, file)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		relSlash := filepath.ToSlash(rel)
		if skip != nil && skip(relSlash, entry) {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(file, target)
	})
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func findRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		goMod := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(goMod)
		if err == nil && strings.Contains(string(data), "module github.com/fluxplane/agentruntime") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("distribution build: could not find github.com/fluxplane/agentruntime source root from %s", start)
		}
		dir = parent
	}
}

func cleanStrings(input []string) []string {
	var out []string
	for _, value := range input {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func maybeWriteFile(filename, content string, perm fs.FileMode, dryRun, force bool, out io.Writer) error {
	if dryRun {
		_, _ = fmt.Fprintf(out, "write=%s\n", filename)
		return nil
	}
	if _, err := os.Stat(filename); err == nil && !force {
		return fmt.Errorf("distribution build: %s already exists; pass --force to overwrite", filename)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filename, []byte(content), perm)
}

func kubernetesManifestPath(appRoot, outDir, explicitOutDir string) string {
	if strings.TrimSpace(explicitOutDir) != "" {
		return filepath.Join(outDir, "kubernetes.yaml")
	}
	return filepath.Join(appDeployDir(appRoot), "kubernetes.yaml")
}

func appDeployDir(appRoot string) string {
	return filepath.Join(appRoot, ".deploy")
}

func maybeWriteKubernetesManifest(appRoot, filename, content string, dryRun, force bool, out io.Writer) error {
	if err := maybeWriteFile(filename, content, 0o600, dryRun, force, out); err != nil {
		return err
	}
	if dryRun || !isInAppDeployDir(appRoot, filename) {
		return nil
	}
	return ensureGitignoreEntry(appRoot, ".deploy/")
}

func isInAppDeployDir(appRoot, filename string) bool {
	rel, err := filepath.Rel(appRoot, filename)
	if err != nil {
		return false
	}
	return rel == ".deploy" || strings.HasPrefix(rel, ".deploy"+string(filepath.Separator))
}

func ensureGitignoreEntry(root, entry string) error {
	filename := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(filename)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	var b strings.Builder
	b.WriteString(text)
	if text != "" && !strings.HasSuffix(text, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(entry)
	b.WriteByte('\n')
	return os.WriteFile(filename, []byte(b.String()), 0o644)
}

func writeTempManifest(pattern, content string) (string, error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if _, err := io.WriteString(file, content); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func ensureDockerfileForImage(filename, baseImage, connectorsPath string, appRuntime appRuntimeOptions, force bool) error {
	if _, err := os.Stat(filename); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return maybeWriteFile(filename, workspaceDockerfile(baseImage, connectorsPath, appRuntime), 0o600, false, force, io.Discard)
}

func printAppBuildCommand(out io.Writer, command []string, dryRun bool) {
	if dryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func printDryRun(out io.Writer, result DockerBuildResult) {
	_, _ = fmt.Fprintf(out, "context=%s\n", result.ContextDir)
	_, _ = fmt.Fprintf(out, "dockerfile=%s\n", result.Dockerfile)
	_, _ = fmt.Fprintf(out, "tags=%s\n", strings.Join(result.Tags, ","))
	_, _ = fmt.Fprintf(out, "base_image=%s\n", result.BaseImage)
	if len(result.Platforms) > 0 {
		_, _ = fmt.Fprintf(out, "platforms=%s\n", strings.Join(result.Platforms, ","))
	}
	_, _ = fmt.Fprintf(out, "assets=%s\n", strings.Join(result.Assets, ","))
	_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(result.Command, " "))
}

func printBaseImageDryRun(out io.Writer, result BaseImageResult) {
	_, _ = fmt.Fprintf(out, "context=%s\n", result.ContextDir)
	_, _ = fmt.Fprintf(out, "dockerfile=%s\n", result.Dockerfile)
	_, _ = fmt.Fprintf(out, "tags=%s\n", strings.Join(result.Tags, ","))
	if len(result.Platforms) > 0 {
		_, _ = fmt.Fprintf(out, "platforms=%s\n", strings.Join(result.Platforms, ","))
	}
	_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(result.Command, " "))
}

func firstTag(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return tags[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func dockerComposeContent(name, image, connectorsPath string, appRuntime appRuntimeOptions, launch distribution.LaunchConfig) string {
	service := strings.TrimSpace(name)
	if service == "" {
		service = "app"
	}
	appRuntime = appRuntime.withDefaults()
	service = composeServiceName(service)
	deps := composeDependencies(launch)
	var b strings.Builder
	b.WriteString("services:\n")
	b.WriteString("  ")
	b.WriteString(service)
	b.WriteString(":\n")
	b.WriteString("    image: ")
	b.WriteString(image)
	b.WriteByte('\n')
	command, _ := json.Marshal(appServeCommand(connectorsPath, appRuntime))
	b.WriteString("    command: ")
	b.Write(command)
	b.WriteByte('\n')
	writeComposeEnv(&b, appRuntime, launch)
	if len(deps) > 0 {
		b.WriteString("    depends_on:\n")
		for _, dep := range deps {
			b.WriteString("      ")
			b.WriteString(dep)
			b.WriteString(":\n")
			b.WriteString("        condition: service_healthy\n")
		}
	}
	b.WriteString("    restart: unless-stopped\n")
	if composeUsesMySQL(launch) {
		writeMySQLService(&b)
	}
	if composeUsesNATS(launch) {
		writeNATSService(&b)
	}
	writeComposeVolumes(&b, launch)
	return b.String()
}

func writeComposeEnv(b *strings.Builder, appRuntime appRuntimeOptions, launch distribution.LaunchConfig) {
	values := composeEnv(appRuntime, launch)
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	b.WriteString("    environment:\n")
	for _, key := range keys {
		b.WriteString("      ")
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(values[key])
		b.WriteByte('\n')
	}
}

func composeEnv(appRuntime appRuntimeOptions, launch distribution.LaunchConfig) map[string]string {
	env := map[string]string{}
	appRuntime = appRuntime.withDefaults()
	if strings.EqualFold(appRuntime.Provider, DefaultAppProvider) {
		env[openRouterAPIKeyEnv] = "${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is required}"
	}
	if composeUsesMySQL(launch) {
		name := strings.TrimSpace(launch.Data.Store.DSNEnv)
		if name == "" {
			name = defaultMySQLDSNEnv
		}
		env[name] = "agentruntime:agentruntime@tcp(mysql:3306)/agentruntime?parseTime=true&multiStatements=true"
	}
	if composeUsesNATS(launch) {
		name := strings.TrimSpace(launch.Events.Store.DSNEnv)
		if name == "" {
			name = defaultNATSDSNEnv
		}
		env[name] = "nats://nats:4222"
	}
	return env
}

func composeDependencies(launch distribution.LaunchConfig) []string {
	var deps []string
	if composeUsesMySQL(launch) {
		deps = append(deps, "mysql")
	}
	if composeUsesNATS(launch) {
		deps = append(deps, "nats")
	}
	return deps
}

func writeMySQLService(b *strings.Builder) {
	b.WriteString("  mysql:\n")
	b.WriteString("    image: mysql:8.4\n")
	b.WriteString("    environment:\n")
	b.WriteString("      MYSQL_DATABASE: agentruntime\n")
	b.WriteString("      MYSQL_USER: agentruntime\n")
	b.WriteString("      MYSQL_PASSWORD: agentruntime\n")
	b.WriteString("      MYSQL_ROOT_PASSWORD: agentruntime-root\n")
	b.WriteString("    volumes:\n")
	b.WriteString("      - mysql-data:/var/lib/mysql\n")
	b.WriteString("    healthcheck:\n")
	b.WriteString("      test: [\"CMD\", \"mysqladmin\", \"ping\", \"-h\", \"localhost\"]\n")
	b.WriteString("      interval: 5s\n")
	b.WriteString("      timeout: 5s\n")
	b.WriteString("      retries: 20\n")
}

func writeNATSService(b *strings.Builder) {
	b.WriteString("  nats:\n")
	b.WriteString("    image: nats:2.11-alpine\n")
	b.WriteString("    command: [\"-js\", \"-sd\", \"/data\", \"-m\", \"8222\"]\n")
	b.WriteString("    volumes:\n")
	b.WriteString("      - nats-data:/data\n")
	b.WriteString("    healthcheck:\n")
	b.WriteString("      test: [\"CMD-SHELL\", \"wget -q -O - http://127.0.0.1:8222/healthz | grep -q ok\"]\n")
	b.WriteString("      interval: 5s\n")
	b.WriteString("      timeout: 5s\n")
	b.WriteString("      retries: 20\n")
}

func writeComposeVolumes(b *strings.Builder, launch distribution.LaunchConfig) {
	if !composeUsesMySQL(launch) && !composeUsesNATS(launch) {
		return
	}
	b.WriteString("volumes:\n")
	if composeUsesMySQL(launch) {
		b.WriteString("  mysql-data:\n")
	}
	if composeUsesNATS(launch) {
		b.WriteString("  nats-data:\n")
	}
}

func composeUsesMySQL(launch distribution.LaunchConfig) bool {
	return strings.EqualFold(strings.TrimSpace(launch.Data.Store.Kind), "mysql")
}

func composeUsesNATS(launch distribution.LaunchConfig) bool {
	kind := strings.ToLower(strings.TrimSpace(launch.Events.Store.Kind))
	return kind == "nats" || kind == "jetstream" || kind == "nats-jetstream"
}

func composeServiceName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}

type kubernetesRenderOptions struct {
	Name            string
	Namespace       string
	Image           string
	ConnectorsPath  string
	AppRuntime      appRuntimeOptions
	IncludeRegistry bool
}

type kubernetesRenderResult struct {
	Content         string
	RegistryContent string
	SecretName      string
	SecretKeys      []string
}

type kubernetesEnvSecret struct {
	Name   string
	Files  []string
	Values map[string]string
}

type kubernetesImageRefSet struct {
	Registry string
	Push     string
	Cluster  string
}

type kubernetesTeardownOptions struct {
	Name      string
	Namespace string
}

func kubernetesContent(loaded distribution.Loaded, opts kubernetesRenderOptions) (kubernetesRenderResult, error) {
	name := kubernetesName(firstNonEmpty(opts.Name, loaded.Distribution.Spec.Name, "app"))
	namespace := kubernetesName(firstNonEmpty(opts.Namespace, name))
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = defaultAppImage
	}
	secret, err := kubernetesEnvSecretForLoaded(loaded, name)
	if err != nil {
		return kubernetesRenderResult{}, err
	}
	var registryContent string
	var docs []string
	docs = append(docs, kubernetesNamespace(namespace))
	if opts.IncludeRegistry {
		registryContent = kubernetesRegistryContent(namespace)
		docs = append(docs, splitYAMLDocuments(registryContent)...)
	}
	if secret.Name != "" {
		docs = append(docs, kubernetesSecret(namespace, secret))
	}
	if composeUsesMySQL(loaded.Launch) {
		docs = append(docs, splitYAMLDocuments(kubernetesMySQL(namespace))...)
	}
	if composeUsesNATS(loaded.Launch) {
		docs = append(docs, splitYAMLDocuments(kubernetesNATS(namespace))...)
	}
	docs = append(docs, kubernetesAppService(namespace, name))
	docs = append(docs, kubernetesAppDeployment(namespace, name, image, opts.ConnectorsPath, opts.AppRuntime, loaded.Launch, secret.Name))
	content := joinYAMLDocuments(docs)
	return kubernetesRenderResult{
		Content:         content,
		RegistryContent: registryContent,
		SecretName:      secret.Name,
		SecretKeys:      sortedKeys(secret.Values),
	}, nil
}

func kubernetesTeardownContent(loaded distribution.Loaded, opts kubernetesTeardownOptions) string {
	name := kubernetesName(firstNonEmpty(opts.Name, loaded.Distribution.Spec.Name, "app"))
	namespace := kubernetesName(firstNonEmpty(opts.Namespace, name))
	var docs []string
	if len(cleanStrings(loaded.Launch.Workspace.EnvFiles)) > 0 {
		docs = append(docs, kubernetesSecretIdentity(namespace, kubernetesName(name+"-env")))
	}
	if composeUsesMySQL(loaded.Launch) {
		docs = append(docs, kubernetesServiceIdentity(namespace, "mysql"))
		docs = append(docs, kubernetesStatefulSetIdentity(namespace, "mysql"))
	}
	if composeUsesNATS(loaded.Launch) {
		docs = append(docs, kubernetesServiceIdentity(namespace, "nats"))
		docs = append(docs, kubernetesStatefulSetIdentity(namespace, "nats"))
	}
	docs = append(docs, kubernetesServiceIdentity(namespace, name))
	docs = append(docs, kubernetesDeploymentIdentity(namespace, name))
	return joinYAMLDocuments(docs)
}

func kubernetesTeardownPVCs(loaded distribution.Loaded) []string {
	var pvcs []string
	if composeUsesMySQL(loaded.Launch) {
		pvcs = append(pvcs, "data-mysql-0")
	}
	if composeUsesNATS(loaded.Launch) {
		pvcs = append(pvcs, "data-nats-0")
	}
	pvcs = append(pvcs, "coder-registry-data")
	return pvcs
}

func kubernetesEnvSecretForLoaded(loaded distribution.Loaded, name string) (kubernetesEnvSecret, error) {
	for _, root := range loaded.Launch.Workspace.Roots {
		if len(cleanStrings(root.EnvFiles)) > 0 {
			return kubernetesEnvSecret{}, fmt.Errorf("distribution deploy: kubernetes target does not support env_files on workspace root %q", root.Name)
		}
	}
	patterns := cleanStrings(loaded.Launch.Workspace.EnvFiles)
	if len(patterns) == 0 {
		return kubernetesEnvSecret{}, nil
	}
	envFiles, err := system.LoadEnvFiles(loaded.Root, patterns)
	if err != nil {
		return kubernetesEnvSecret{}, err
	}
	if len(envFiles.Values) == 0 {
		return kubernetesEnvSecret{}, nil
	}
	return kubernetesEnvSecret{
		Name:   kubernetesName(name + "-env"),
		Files:  envFiles.Files,
		Values: envFiles.Values,
	}, nil
}

func kubernetesNamespace(namespace string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Namespace\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	return b.String()
}

func kubernetesSecret(namespace string, secret kubernetesEnvSecret) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Secret\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: ")
	b.WriteString(secret.Name)
	b.WriteByte('\n')
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("type: Opaque\n")
	b.WriteString("stringData:\n")
	for _, key := range sortedKeys(secret.Values) {
		b.WriteString("  ")
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(yamlString(secret.Values[key]))
		b.WriteByte('\n')
	}
	return b.String()
}

func kubernetesSecretIdentity(namespace, name string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Secret\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	return b.String()
}

func kubernetesAppService(namespace, name string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Service\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("spec:\n")
	b.WriteString("  selector:\n")
	b.WriteString("    app.kubernetes.io/name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString("  ports:\n")
	b.WriteString("    - name: control\n")
	b.WriteString("      port: 18080\n")
	b.WriteString("      targetPort: control\n")
	return b.String()
}

func kubernetesServiceIdentity(namespace, name string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Service\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	return b.String()
}

func kubernetesDeploymentIdentity(namespace, name string) string {
	var b strings.Builder
	b.WriteString("apiVersion: apps/v1\n")
	b.WriteString("kind: Deployment\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	return b.String()
}

func kubernetesStatefulSetIdentity(namespace, name string) string {
	var b strings.Builder
	b.WriteString("apiVersion: apps/v1\n")
	b.WriteString("kind: StatefulSet\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	return b.String()
}

func kubernetesAppDeployment(namespace, name, image, connectorsPath string, appRuntime appRuntimeOptions, launch distribution.LaunchConfig, secretName string) string {
	env := kubernetesRuntimeEnv(launch)
	var b strings.Builder
	b.WriteString("apiVersion: apps/v1\n")
	b.WriteString("kind: Deployment\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("spec:\n")
	b.WriteString("  replicas: 1\n")
	b.WriteString("  selector:\n")
	b.WriteString("    matchLabels:\n")
	b.WriteString("      app.kubernetes.io/name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString("  template:\n")
	b.WriteString("    metadata:\n")
	b.WriteString("      labels:\n")
	b.WriteString("        app.kubernetes.io/name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString("    spec:\n")
	b.WriteString("      containers:\n")
	b.WriteString("        - name: app\n")
	b.WriteString("          image: ")
	b.WriteString(image)
	b.WriteByte('\n')
	b.WriteString("          imagePullPolicy: IfNotPresent\n")
	args, _ := json.Marshal(appServeCommandWithHealthAddr(connectorsPath, appRuntime, defaultKubeHealthAddr))
	b.WriteString("          args: ")
	b.Write(args)
	b.WriteByte('\n')
	b.WriteString("          ports:\n")
	b.WriteString("            - name: control\n")
	b.WriteString("              containerPort: 18080\n")
	if secretName != "" {
		b.WriteString("          envFrom:\n")
		b.WriteString("            - secretRef:\n")
		b.WriteString("                name: ")
		b.WriteString(secretName)
		b.WriteByte('\n')
	}
	if len(env) > 0 {
		b.WriteString("          env:\n")
		for _, key := range sortedKeys(env) {
			b.WriteString("            - name: ")
			b.WriteString(key)
			b.WriteByte('\n')
			b.WriteString("              value: ")
			b.WriteString(yamlString(env[key]))
			b.WriteByte('\n')
		}
	}
	probe, _ := json.Marshal(appHealthcheckCommand())
	b.WriteString("          readinessProbe:\n")
	b.WriteString("            exec:\n")
	b.WriteString("              command: ")
	b.Write(probe)
	b.WriteByte('\n')
	b.WriteString("            periodSeconds: 10\n")
	b.WriteString("            failureThreshold: 12\n")
	b.WriteString("          livenessProbe:\n")
	b.WriteString("            exec:\n")
	b.WriteString("              command: ")
	b.Write(probe)
	b.WriteByte('\n')
	b.WriteString("            periodSeconds: 20\n")
	b.WriteString("            failureThreshold: 6\n")
	return b.String()
}

func kubernetesRuntimeEnv(launch distribution.LaunchConfig) map[string]string {
	env := map[string]string{}
	if composeUsesMySQL(launch) {
		name := strings.TrimSpace(launch.Data.Store.DSNEnv)
		if name == "" {
			name = defaultMySQLDSNEnv
		}
		env[name] = "agentruntime:agentruntime@tcp(mysql:3306)/agentruntime?parseTime=true&multiStatements=true"
	}
	if composeUsesNATS(launch) {
		name := strings.TrimSpace(launch.Events.Store.DSNEnv)
		if name == "" {
			name = defaultNATSDSNEnv
		}
		env[name] = "nats://nats:4222"
	}
	return env
}

func kubernetesRegistryContent(namespace string) string {
	var b strings.Builder
	b.WriteString(kubernetesNamespace(namespace))
	b.WriteString("---\n")
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: PersistentVolumeClaim\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: coder-registry-data\n")
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("spec:\n")
	b.WriteString("  accessModes: [\"ReadWriteOnce\"]\n")
	b.WriteString("  resources:\n")
	b.WriteString("    requests:\n")
	b.WriteString("      storage: 5Gi\n")
	b.WriteString("---\n")
	b.WriteString("apiVersion: apps/v1\n")
	b.WriteString("kind: Deployment\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: coder-registry\n")
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("spec:\n")
	b.WriteString("  replicas: 1\n")
	b.WriteString("  selector:\n")
	b.WriteString("    matchLabels:\n")
	b.WriteString("      app.kubernetes.io/name: coder-registry\n")
	b.WriteString("  template:\n")
	b.WriteString("    metadata:\n")
	b.WriteString("      labels:\n")
	b.WriteString("        app.kubernetes.io/name: coder-registry\n")
	b.WriteString("    spec:\n")
	b.WriteString("      containers:\n")
	b.WriteString("        - name: registry\n")
	b.WriteString("          image: registry:2\n")
	b.WriteString("          ports:\n")
	b.WriteString("            - name: registry\n")
	b.WriteString("              containerPort: 5000\n")
	b.WriteString("          volumeMounts:\n")
	b.WriteString("            - name: data\n")
	b.WriteString("              mountPath: /var/lib/registry\n")
	b.WriteString("      volumes:\n")
	b.WriteString("        - name: data\n")
	b.WriteString("          persistentVolumeClaim:\n")
	b.WriteString("            claimName: coder-registry-data\n")
	b.WriteString("---\n")
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Service\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: coder-registry\n")
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("spec:\n")
	b.WriteString("  selector:\n")
	b.WriteString("    app.kubernetes.io/name: coder-registry\n")
	b.WriteString("  ports:\n")
	b.WriteString("    - name: registry\n")
	b.WriteString("      port: 5000\n")
	b.WriteString("      targetPort: registry\n")
	return b.String()
}

func kubernetesMySQL(namespace string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Service\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: mysql\n")
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("spec:\n")
	b.WriteString("  selector:\n")
	b.WriteString("    app.kubernetes.io/name: mysql\n")
	b.WriteString("  ports:\n")
	b.WriteString("    - name: mysql\n")
	b.WriteString("      port: 3306\n")
	b.WriteString("      targetPort: mysql\n")
	b.WriteString("---\n")
	b.WriteString("apiVersion: apps/v1\n")
	b.WriteString("kind: StatefulSet\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: mysql\n")
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("spec:\n")
	b.WriteString("  serviceName: mysql\n")
	b.WriteString("  replicas: 1\n")
	b.WriteString("  selector:\n")
	b.WriteString("    matchLabels:\n")
	b.WriteString("      app.kubernetes.io/name: mysql\n")
	b.WriteString("  template:\n")
	b.WriteString("    metadata:\n")
	b.WriteString("      labels:\n")
	b.WriteString("        app.kubernetes.io/name: mysql\n")
	b.WriteString("    spec:\n")
	b.WriteString("      containers:\n")
	b.WriteString("        - name: mysql\n")
	b.WriteString("          image: mysql:8.4\n")
	b.WriteString("          ports:\n")
	b.WriteString("            - name: mysql\n")
	b.WriteString("              containerPort: 3306\n")
	b.WriteString("          env:\n")
	b.WriteString("            - name: MYSQL_DATABASE\n")
	b.WriteString("              value: agentruntime\n")
	b.WriteString("            - name: MYSQL_USER\n")
	b.WriteString("              value: agentruntime\n")
	b.WriteString("            - name: MYSQL_PASSWORD\n")
	b.WriteString("              value: agentruntime\n")
	b.WriteString("            - name: MYSQL_ROOT_PASSWORD\n")
	b.WriteString("              value: agentruntime-root\n")
	b.WriteString("          volumeMounts:\n")
	b.WriteString("            - name: data\n")
	b.WriteString("              mountPath: /var/lib/mysql\n")
	b.WriteString("  volumeClaimTemplates:\n")
	b.WriteString("    - metadata:\n")
	b.WriteString("        name: data\n")
	b.WriteString("      spec:\n")
	b.WriteString("        accessModes: [\"ReadWriteOnce\"]\n")
	b.WriteString("        resources:\n")
	b.WriteString("          requests:\n")
	b.WriteString("            storage: 5Gi\n")
	return b.String()
}

func kubernetesNATS(namespace string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Service\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: nats\n")
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("spec:\n")
	b.WriteString("  selector:\n")
	b.WriteString("    app.kubernetes.io/name: nats\n")
	b.WriteString("  ports:\n")
	b.WriteString("    - name: client\n")
	b.WriteString("      port: 4222\n")
	b.WriteString("      targetPort: client\n")
	b.WriteString("    - name: monitor\n")
	b.WriteString("      port: 8222\n")
	b.WriteString("      targetPort: monitor\n")
	b.WriteString("---\n")
	b.WriteString("apiVersion: apps/v1\n")
	b.WriteString("kind: StatefulSet\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: nats\n")
	b.WriteString("  namespace: ")
	b.WriteString(namespace)
	b.WriteByte('\n')
	b.WriteString("spec:\n")
	b.WriteString("  serviceName: nats\n")
	b.WriteString("  replicas: 1\n")
	b.WriteString("  selector:\n")
	b.WriteString("    matchLabels:\n")
	b.WriteString("      app.kubernetes.io/name: nats\n")
	b.WriteString("  template:\n")
	b.WriteString("    metadata:\n")
	b.WriteString("      labels:\n")
	b.WriteString("        app.kubernetes.io/name: nats\n")
	b.WriteString("    spec:\n")
	b.WriteString("      containers:\n")
	b.WriteString("        - name: nats\n")
	b.WriteString("          image: nats:2.11-alpine\n")
	b.WriteString("          args: [\"-js\", \"-sd\", \"/data\", \"-m\", \"8222\"]\n")
	b.WriteString("          ports:\n")
	b.WriteString("            - name: client\n")
	b.WriteString("              containerPort: 4222\n")
	b.WriteString("            - name: monitor\n")
	b.WriteString("              containerPort: 8222\n")
	b.WriteString("          volumeMounts:\n")
	b.WriteString("            - name: data\n")
	b.WriteString("              mountPath: /data\n")
	b.WriteString("  volumeClaimTemplates:\n")
	b.WriteString("    - metadata:\n")
	b.WriteString("        name: data\n")
	b.WriteString("      spec:\n")
	b.WriteString("        accessModes: [\"ReadWriteOnce\"]\n")
	b.WriteString("        resources:\n")
	b.WriteString("          requests:\n")
	b.WriteString("            storage: 2Gi\n")
	return b.String()
}

func kubernetesImageRefs(namespace, name, sourceImage, mode, registry string) kubernetesImageRefSet {
	repoTag := imageRepoTag(sourceImage, name)
	switch mode {
	case "external":
		registry = strings.Trim(strings.TrimSpace(registry), "/")
		if registry == "" {
			return kubernetesImageRefSet{Registry: imageRegistry(sourceImage), Push: sourceImage, Cluster: sourceImage}
		}
		cluster := registry + "/" + repoTag
		return kubernetesImageRefSet{Registry: registry, Push: cluster, Cluster: cluster}
	default:
		return kubernetesImageRefSet{Registry: "k3d", Push: "", Cluster: sourceImage}
	}
}

func kubernetesPushCommands(sourceImage, pushImage string) [][]string {
	if strings.TrimSpace(sourceImage) == strings.TrimSpace(pushImage) {
		return [][]string{{"docker", "push", pushImage}}
	}
	return [][]string{
		{"docker", "tag", sourceImage, pushImage},
		{"docker", "push", pushImage},
	}
}

func imageRepoTag(image, fallback string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return kubernetesName(fallback) + ":latest"
	}
	parts := strings.Split(image, "/")
	last := parts[len(parts)-1]
	if strings.Contains(last, ":") {
		return last
	}
	return kubernetesName(last) + ":latest"
}

func imageRegistry(image string) string {
	parts := strings.Split(strings.TrimSpace(image), "/")
	if len(parts) < 2 {
		return ""
	}
	first := parts[0]
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return ""
}

func kubernetesName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	if out == "" {
		return "app"
	}
	return out
}

func yamlString(value string) string {
	return strconv.Quote(value)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func joinYAMLDocuments(docs []string) string {
	var cleaned []string
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc != "" {
			cleaned = append(cleaned, doc+"\n")
		}
	}
	return strings.Join(cleaned, "---\n")
}

func splitYAMLDocuments(content string) []string {
	var docs []string
	for _, doc := range strings.Split(content, "\n---\n") {
		doc = strings.TrimSpace(doc)
		if doc != "" {
			docs = append(docs, doc+"\n")
		}
	}
	return docs
}

func printKubernetesSecretSummary(out io.Writer, name string, keys []string, dryRun bool) {
	if !dryRun {
		return
	}
	_, _ = fmt.Fprintf(out, "secret=%s keys=%s\n", name, strings.Join(keys, ","))
}

func currentK3DClusterName(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, "kubectl", "config", "current-context")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("distribution deploy: resolve current kubernetes context: %w", err)
	}
	contextName := strings.TrimSpace(string(output))
	cluster, ok := strings.CutPrefix(contextName, "k3d-")
	if !ok || cluster == "" {
		return "", fmt.Errorf("distribution deploy: registry-mode namespace requires a k3d current context; got %q, use --registry-mode external for node-reachable registries", contextName)
	}
	return cluster, nil
}
