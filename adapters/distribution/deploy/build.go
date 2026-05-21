// Package deploy adapts distributions into deployable artifacts such as
// container images and Kubernetes manifests.
package deploy

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
)

// AppBuildOptions configures app-local build artifact generation.
type AppBuildOptions struct {
	AppDir             string
	OutDir             string
	Targets            []string
	Tags               []string
	Image              string
	Platforms          []string
	Push               bool
	DryRun             bool
	Force              bool
	BaseImage          string
	ConnectorsPath     string
	AllowPluginAuthEnv bool
	Provider           string
	Model              string
	Effort             string
	Out                io.Writer
	Err                io.Writer
	Runner             CommandRunner
	dockerClient       DockerClient
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
		Provider:           opts.Provider,
		Model:              opts.Model,
		Effort:             opts.Effort,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
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
	dockerClient := dockerClientFor(opts.Runner, opts.dockerClient)
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
		case "docker-base":
			base, err := BuildFluxplaneBaseDocker(ctx, BaseImageOptions{
				Tags:         []string{baseImage},
				Platforms:    opts.Platforms,
				Push:         opts.Push,
				DryRun:       opts.DryRun,
				Out:          out,
				Err:          errOut,
				Runner:       opts.Runner,
				dockerClient: dockerClient,
			})
			if err != nil {
				return AppBuildResult{}, err
			}
			result.Command = base.Command
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
				if opts.Runner != nil && opts.dockerClient == nil {
					if err := runner.Run(ctx, "", command[0], command[1:], out, errOut); err != nil {
						return AppBuildResult{}, err
					}
				} else if err := dockerClient.BuildImage(ctx, loaded.Root, dockerfilePath, tags, cleanStrings(opts.Platforms), opts.Push, out, errOut); err != nil {
					return AppBuildResult{}, err
				}
			}
		default:
			return AppBuildResult{}, fmt.Errorf("distribution build: unsupported app target %q", target)
		}
	}
	return result, nil
}
