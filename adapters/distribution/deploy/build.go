// Package deploy adapts distributions into deployable artifacts such as
// container images and Kubernetes manifests.
package deploy

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	distlocal "github.com/fluxplane/fluxplane-core/adapters/distribution/local"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
)

// AppBuildOptions configures app-local build artifact generation.
type AppBuildOptions struct {
	AppDir             string
	Profile            string
	Profiles           []string
	OutDir             string
	Targets            []string
	Tags               []string
	Image              string
	Platforms          []string
	Push               bool
	DryRun             bool
	Force              bool
	BaseImage          string
	AuthPath           string
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
	AppDir          string
	OutDir          string
	Name            string
	Targets         []string
	Tags            []string
	Artifacts       []string
	Command         []string
	DryRun          bool
	Dockerfile      string
	Compose         string
	Kubernetes      string
	Binary          string
	Documentation   string
	HelmChart       string
	RuntimeStack    string
	ArtifactIndex   string
	TargetArtifacts []BuildArtifact
}

// BuildApp builds app-local artifacts such as bin launchers, Dockerfiles,
// Docker Compose resources, and Docker images.
func BuildApp(ctx context.Context, opts AppBuildOptions) (AppBuildResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.LoadWithOptions(ctx, appDir, distlocal.LoadOptions{Profile: opts.Profile, Profiles: opts.Profiles})
	if err != nil {
		return AppBuildResult{}, err
	}
	targets, err := resolveBuildTargets(loaded.Distribution.Spec, opts.Targets)
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
	name := distributionName(loaded.Distribution.Spec)
	binaryPath := filepath.Join(outDir, "bin", composeServiceName(name))
	dockerfilePath := filepath.Join(outDir, "Dockerfile")
	composePath := filepath.Join(outDir, "docker-compose.yaml")
	kubernetesPath := kubernetesManifestPath(loaded.Root, outDir, opts.OutDir)
	documentationPath := filepath.Join(outDir, composeServiceName(name)+".md")
	helmChartPath := filepath.Join(outDir, "charts", composeServiceName(name))
	runtimeStackPath := filepath.Join(outDir, "charts", defaultRuntimeStack)
	result := AppBuildResult{
		AppDir:        loaded.Root,
		OutDir:        outDir,
		Name:          name,
		Targets:       targetNames(targets),
		DryRun:        opts.DryRun,
		Dockerfile:    dockerfilePath,
		Compose:       composePath,
		Kubernetes:    kubernetesPath,
		Binary:        binaryPath,
		Documentation: documentationPath,
		HelmChart:     helmChartPath,
		RuntimeStack:  runtimeStackPath,
		ArtifactIndex: artifactIndexPath(loaded.Root),
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
	builtBaseImages := map[string]struct{}{}
	for _, target := range targets {
		targetSpec := target.Spec
		kind := normalizedBuildKind(targetSpec.Kind)
		authPath := firstNonEmpty(strings.TrimSpace(targetSpec.AuthPath), strings.TrimSpace(opts.AuthPath), defaultAuthPath)
		baseImage := firstNonEmpty(strings.TrimSpace(targetSpec.BaseImage), strings.TrimSpace(opts.BaseImage), defaultBaseImage)
		appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
			Provider:           firstNonEmpty(targetSpec.Provider, opts.Provider),
			Model:              firstNonEmpty(targetSpec.Model, opts.Model),
			Effort:             firstNonEmpty(targetSpec.Effort, opts.Effort),
			AllowPluginAuthEnv: targetSpec.AllowPluginAuthEnv || opts.AllowPluginAuthEnv,
			Profiles:           selectedRuntimeProfiles(opts.Profile, opts.Profiles),
		})
		tags := resolveTargetTags(loaded.Distribution.Spec, targetSpec, opts)
		image := firstTag(tags)
		if image == "" {
			image = defaultAppImage
		}
		result.Tags = append(result.Tags, tags...)
		switch kind {
		case buildKindBinary:
			path := targetOutput(outDir, targetSpec.Output, binaryPath)
			result.Binary = path
			result.Artifacts = append(result.Artifacts, path)
			command, err := buildEmbeddedBinary(ctx, loaded.Root, path, loaded.Distribution.Spec.Build.Assets, opts.DryRun, opts.Force, out, errOut, runner)
			if err != nil {
				return AppBuildResult{}, err
			}
			result.Command = command
			result.TargetArtifacts = append(result.TargetArtifacts, BuildArtifact{Target: target.Name, Kind: kind, Path: path, Command: command})
		case buildKindDockerfile:
			path := targetOutput(outDir, targetSpec.Output, dockerfilePath)
			result.Dockerfile = path
			result.Artifacts = append(result.Artifacts, path)
			if err := maybeWriteFile(path, workspaceDockerfile(baseImage, authPath, appRuntime), 0o600, opts.DryRun, opts.Force, out); err != nil {
				return AppBuildResult{}, err
			}
			result.TargetArtifacts = append(result.TargetArtifacts, BuildArtifact{Target: target.Name, Kind: kind, Path: path})
		case buildKindDockerCompose:
			path := targetOutput(outDir, targetSpec.Output, composePath)
			result.Compose = path
			result.Artifacts = append(result.Artifacts, path)
			content, err := dockerComposeContent(loaded.Root, filepath.Dir(path), name, image, authPath, appRuntime, loaded.Launch)
			if err != nil {
				return AppBuildResult{}, err
			}
			if err := maybeWriteFile(path, content, 0o600, opts.DryRun, opts.Force, out); err != nil {
				return AppBuildResult{}, err
			}
			result.TargetArtifacts = append(result.TargetArtifacts, BuildArtifact{Target: target.Name, Kind: kind, Path: path, Image: image})
		case buildKindKubernetesManifest:
			path := targetOutput(outDir, targetSpec.Output, kubernetesPath)
			result.Kubernetes = path
			result.Artifacts = append(result.Artifacts, path)
			content, err := kubernetesContent(loaded, kubernetesRenderOptions{
				Name:              name,
				Namespace:         kubernetesName(firstNonEmpty(targetSpec.Namespace, name)),
				Image:             image,
				ImagePullPolicy:   targetSpec.ImagePullPolicy,
				EnvSecretName:     targetSpec.EnvSecretName,
				RuntimeSecretName: targetSpec.RuntimeSecretName,
				AuthPath:          authPath,
				AppRuntime:        appRuntime,
				NodeSelectors:     targetSpec.NodeSelectors,
				IncludeRegistry:   false,
			})
			if err != nil {
				return AppBuildResult{}, err
			}
			if err := maybeWriteKubernetesManifest(loaded.Root, path, content.Content, opts.DryRun, opts.Force, out); err != nil {
				return AppBuildResult{}, err
			}
			result.TargetArtifacts = append(result.TargetArtifacts, BuildArtifact{Target: target.Name, Kind: kind, Path: path, Image: image})
		case "docker-base":
			base, err := BuildFluxplaneBaseDocker(ctx, BaseImageOptions{
				Tags:         []string{baseImage},
				Platforms:    firstNonEmptySlice(targetSpec.Platforms, opts.Platforms),
				Push:         targetSpec.Push || opts.Push,
				DryRun:       opts.DryRun,
				Out:          out,
				Err:          errOut,
				Runner:       runner,
				dockerClient: dockerClient,
			})
			if err != nil {
				return AppBuildResult{}, err
			}
			result.Command = base.Command
			result.TargetArtifacts = append(result.TargetArtifacts, BuildArtifact{Target: target.Name, Kind: kind, Image: firstTag(base.Tags), Command: base.Command})
		case buildKindDockerImage:
			platforms := firstNonEmptySlice(targetSpec.Platforms, opts.Platforms)
			push := targetSpec.Push || opts.Push
			if err := ensureManagedFluxplaneBaseImage(ctx, baseImage, platforms, opts, out, errOut, runner, dockerClient, builtBaseImages); err != nil {
				return AppBuildResult{}, err
			}
			command, err := dockerCommand(tags, cleanStrings(platforms), push)
			if err != nil {
				return AppBuildResult{}, err
			}
			dockerfile := targetOutput(outDir, targetSpec.Dockerfile, dockerfilePath)
			managedDockerfile := strings.TrimSpace(targetSpec.Dockerfile) == ""
			command = append(command, "-f", dockerfile, loaded.Root)
			result.Command = command
			printAppBuildCommand(out, command, opts.DryRun)
			if !opts.DryRun {
				if err := ensureDockerfileForImage(dockerfile, baseImage, authPath, appRuntime, opts.Force || managedDockerfile); err != nil {
					return AppBuildResult{}, err
				}
				if opts.Runner != nil && opts.dockerClient == nil {
					if err := runner.Run(ctx, "", command[0], command[1:], out, errOut); err != nil {
						return AppBuildResult{}, err
					}
				} else if err := dockerClient.BuildImage(ctx, loaded.Root, dockerfile, tags, cleanStrings(platforms), push, out, errOut); err != nil {
					return AppBuildResult{}, err
				}
			}
			result.TargetArtifacts = append(result.TargetArtifacts, BuildArtifact{Target: target.Name, Kind: kind, Image: image, Command: command})
		case buildKindHelmChart:
			path := targetOutput(outDir, targetSpec.Output, helmChartPath)
			result.HelmChart = path
			paths, err := writeHelmChart(loaded, path, image, targetSpec.Release, targetSpec.Namespace, targetSpec.ImagePullPolicy, targetSpec.EnvSecretName, targetSpec.RuntimeSecretName, authPath, appRuntime, targetSpec.NodeSelectors, targetSpec.Values, opts.DryRun, opts.Force, out)
			if err != nil {
				return AppBuildResult{}, err
			}
			result.Artifacts = append(result.Artifacts, paths...)
			result.TargetArtifacts = append(result.TargetArtifacts, BuildArtifact{Target: target.Name, Kind: kind, Path: path, Paths: paths, Image: image})
		case buildKindRuntimeStack:
			if backend := strings.ToLower(strings.TrimSpace(targetSpec.Backend)); backend != "" && backend != "kubernetes" {
				return AppBuildResult{}, fmt.Errorf("distribution build: runtime-stack target %q has unsupported backend %q", target.Name, targetSpec.Backend)
			}
			path := targetOutput(outDir, targetSpec.Output, runtimeStackPath)
			result.RuntimeStack = path
			paths, err := writeRuntimeStackHelmChart(loaded, path, targetSpec.Release, targetSpec.Namespace, targetSpec.RuntimeSecretName, targetSpec.Values, opts.DryRun, opts.Force, out)
			if err != nil {
				return AppBuildResult{}, err
			}
			result.Artifacts = append(result.Artifacts, paths...)
			result.TargetArtifacts = append(result.TargetArtifacts, BuildArtifact{Target: target.Name, Kind: kind, Path: path, Paths: paths})
		case buildKindDocumentation:
			path := targetOutput(outDir, targetSpec.Output, documentationPath)
			result.Documentation = path
			result.Artifacts = append(result.Artifacts, path)
			if err := maybeWriteFile(path, distributionDocumentation(loaded), 0o600, opts.DryRun, opts.Force, out); err != nil {
				return AppBuildResult{}, err
			}
			result.TargetArtifacts = append(result.TargetArtifacts, BuildArtifact{Target: target.Name, Kind: kind, Path: path})
		default:
			return AppBuildResult{}, fmt.Errorf("distribution build: unsupported app target kind %q", kind)
		}
	}
	index := ArtifactIndex{
		Version:  artifactIndexVersion,
		AppDir:   loaded.Root,
		Profile:  loaded.Profile,
		Profiles: loaded.Profiles,
		Targets:  result.TargetArtifacts,
	}
	if existing, err := readArtifactIndex(loaded.Root); err == nil {
		index.Targets = mergeBuildArtifacts(existing.Targets, result.TargetArtifacts)
	}
	if err := writeArtifactIndex(loaded.Root, index, opts.DryRun, out); err != nil {
		return AppBuildResult{}, err
	}
	return result, nil
}

func ensureManagedFluxplaneBaseImage(ctx context.Context, baseImage string, platforms []string, opts AppBuildOptions, out, errOut io.Writer, runner CommandRunner, dockerClient DockerClient, built map[string]struct{}) error {
	if strings.TrimSpace(baseImage) != defaultBaseImage {
		return nil
	}
	key := strings.Join(append([]string{baseImage}, cleanStrings(platforms)...), "\x00")
	if _, ok := built[key]; ok {
		return nil
	}
	_, err := BuildFluxplaneBaseDocker(ctx, BaseImageOptions{
		Tags:         []string{baseImage},
		Platforms:    cleanStrings(platforms),
		DryRun:       opts.DryRun,
		Out:          out,
		Err:          errOut,
		Runner:       runner,
		dockerClient: dockerClient,
	})
	if err != nil {
		return err
	}
	built[key] = struct{}{}
	return nil
}

func mergeBuildArtifacts(existing, updated []BuildArtifact) []BuildArtifact {
	seen := map[string]struct{}{}
	out := make([]BuildArtifact, 0, len(existing)+len(updated))
	for _, artifact := range updated {
		if artifact.Target == "" {
			continue
		}
		seen[artifact.Target] = struct{}{}
	}
	for _, artifact := range existing {
		if artifact.Target == "" {
			continue
		}
		if _, ok := seen[artifact.Target]; ok {
			continue
		}
		out = append(out, artifact)
	}
	out = append(out, updated...)
	return out
}

func targetNames(targets []namedBuildTarget) []string {
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		out = append(out, target.Name)
	}
	return out
}

func normalizedBuildKind(kind string) string {
	if kind == "kubernetes" {
		return buildKindKubernetesManifest
	}
	return strings.TrimSpace(kind)
}

func targetOutput(outDir, configured, fallback string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return fallback
	}
	if filepath.IsAbs(configured) {
		return filepath.Clean(configured)
	}
	return filepath.Join(outDir, configured)
}

func resolveTargetTags(spec coredistribution.Spec, target coredistribution.BuildTargetSpec, opts AppBuildOptions) []string {
	if image := strings.TrimSpace(opts.Image); image != "" {
		var override []string
		override = append(override, image)
		override = append(override, opts.Tags...)
		return resolveTags(spec, override)
	}
	if image := strings.TrimSpace(target.Image); image != "" {
		tags := cleanStrings(target.Tags)
		if len(tags) == 0 {
			if strings.Contains(image, "/") || strings.Contains(image, ":") {
				return []string{image}
			}
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
	if len(target.Tags) > 0 {
		return resolveTags(spec, target.Tags)
	}
	return resolveAppBuildTags(spec, opts)
}

func firstNonEmptySlice(values ...[]string) []string {
	for _, value := range values {
		if len(cleanStrings(value)) > 0 {
			return value
		}
	}
	return nil
}
