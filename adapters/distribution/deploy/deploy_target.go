package deploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
)

const (
	BuildPolicyAuto   = "auto"
	BuildPolicyAlways = "always"
	BuildPolicyNever  = "never"
)

// TargetDeployOptions configures deployment from named distribution targets.
type TargetDeployOptions struct {
	AppDir             string
	Profile            string
	Profiles           []string
	Target             string
	BuildPolicy        string
	Image              string
	BaseImage          string
	AuthPath           string
	AllowPluginAuthEnv bool
	Provider           string
	Model              string
	Effort             string
	DryRun             bool
	Force              bool
	Detach             bool
	Out                io.Writer
	Err                io.Writer
	Runner             CommandRunner
}

// TargetUndeployOptions configures teardown for named distribution targets.
type TargetUndeployOptions struct {
	AppDir  string
	Target  string
	DryRun  bool
	Volumes bool
	Out     io.Writer
	Err     io.Writer
	Runner  CommandRunner
}

// DeployTarget builds required artifacts according to policy and deploys the
// named deployment target.
func DeployTarget(ctx context.Context, opts TargetDeployOptions) error {
	loaded, err := distlocal.LoadWithOptions(ctx, firstNonEmpty(strings.TrimSpace(opts.AppDir), "."), distlocal.LoadOptions{Profile: opts.Profile, Profiles: opts.Profiles})
	if err != nil {
		return err
	}
	target, err := resolveDeployTarget(loaded.Distribution.Spec, opts.Target)
	if err != nil {
		return err
	}
	out, errOut := deployWriters(opts.Out, opts.Err)
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	buildPolicy := firstNonEmpty(strings.TrimSpace(opts.BuildPolicy), BuildPolicyAuto)
	if buildPolicy != BuildPolicyAuto && buildPolicy != BuildPolicyAlways && buildPolicy != BuildPolicyNever {
		return fmt.Errorf("distribution deploy: unsupported build policy %q", opts.BuildPolicy)
	}
	if err := ensureDeployArtifacts(ctx, loaded.Root, target.Spec.Build, buildPolicy, TargetDeployOptions{
		AppDir:             loaded.Root,
		Profile:            opts.Profile,
		Profiles:           opts.Profiles,
		Image:              opts.Image,
		BaseImage:          opts.BaseImage,
		AuthPath:           opts.AuthPath,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
		Provider:           opts.Provider,
		Model:              opts.Model,
		Effort:             opts.Effort,
		DryRun:             opts.DryRun,
		Force:              opts.Force,
		Out:                out,
		Err:                errOut,
		Runner:             opts.Runner,
	}); err != nil {
		return err
	}
	index, _ := readArtifactIndex(loaded.Root)
	switch target.Spec.Kind {
	case deployKindDockerCompose:
		composeFile := firstNonEmpty(target.Spec.ComposeFile, artifactPath(index, target.Spec.Build, buildKindDockerCompose), "docker-compose.yaml")
		composeFile = absArtifactPath(loaded.Root, composeFile)
		envFile, err := ensureComposeRuntimeEnvFile(loaded.Root, loaded.Launch, opts.DryRun, out)
		if err != nil {
			return err
		}
		command := dockerComposeUpCommand(composeFile, opts.Detach || target.Spec.Detach, envFile)
		return runDeployCommand(ctx, runner, loaded.Root, command, opts.DryRun, out, errOut)
	case deployKindKubectl:
		manifest := firstNonEmpty(target.Spec.Manifest, artifactPath(index, target.Spec.Build, buildKindKubernetesManifest), filepath.Join(".deploy", "kubernetes.yaml"))
		command := []string{"kubectl", "apply", "-f", absArtifactPath(loaded.Root, manifest)}
		return runDeployCommand(ctx, runner, loaded.Root, command, opts.DryRun, out, errOut)
	case deployKindHelm:
		chart := firstNonEmpty(target.Spec.Chart, artifactPath(index, target.Spec.Build, buildKindHelmChart))
		if chart == "" {
			return fmt.Errorf("distribution deploy: helm target %q has no chart artifact", target.Name)
		}
		release := firstNonEmpty(target.Spec.Release, loaded.Distribution.Spec.Name, "app")
		namespace := firstNonEmpty(target.Spec.Namespace, release)
		command := []string{"helm", "upgrade", "--install", release, absArtifactPath(loaded.Root, chart), "--namespace", namespace, "--create-namespace"}
		for _, key := range sortedKeys(target.Spec.Values) {
			value := strings.TrimSpace(target.Spec.Values[key])
			if strings.TrimSpace(key) == "" || value == "" {
				continue
			}
			command = append(command, "--set", key+"="+value)
		}
		return runDeployCommand(ctx, runner, loaded.Root, command, opts.DryRun, out, errOut)
	default:
		return fmt.Errorf("distribution deploy: unsupported deploy kind %q", target.Spec.Kind)
	}
}

func dockerComposeUpCommand(composeFile string, detach bool, envFile string) []string {
	command := []string{"docker", "compose"}
	if strings.TrimSpace(envFile) != "" {
		command = append(command, "--env-file", envFile)
	}
	command = append(command, "-f", composeFile, "up")
	if detach {
		command = append(command, "-d")
	}
	return append(command, "--wait", "--wait-timeout", dockerComposeWaitTimeoutSeconds)
}

func dockerComposeDownCommand(composeFile, envFile string, volumes bool) []string {
	command := []string{"docker", "compose"}
	if strings.TrimSpace(envFile) != "" {
		command = append(command, "--env-file", envFile)
	}
	command = append(command, "-f", composeFile, "down")
	if volumes {
		command = append(command, "-v")
	}
	return command
}

func UndeployTarget(ctx context.Context, opts TargetUndeployOptions) error {
	loaded, err := distlocal.Load(ctx, firstNonEmpty(strings.TrimSpace(opts.AppDir), "."))
	if err != nil {
		return err
	}
	target, err := resolveDeployTarget(loaded.Distribution.Spec, opts.Target)
	if err != nil {
		return err
	}
	out, errOut := deployWriters(opts.Out, opts.Err)
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	index, _ := readArtifactIndex(loaded.Root)
	switch target.Spec.Kind {
	case deployKindDockerCompose:
		composeFile := firstNonEmpty(target.Spec.ComposeFile, artifactPath(index, target.Spec.Build, buildKindDockerCompose), "docker-compose.yaml")
		envFile, err := composeRuntimeEnvFileForTeardown(loaded.Root, loaded.Launch, opts.DryRun, out)
		if err != nil {
			return err
		}
		command := dockerComposeDownCommand(absArtifactPath(loaded.Root, composeFile), envFile, opts.Volumes)
		return runDeployCommand(ctx, runner, loaded.Root, command, opts.DryRun, out, errOut)
	case deployKindKubectl:
		manifest := firstNonEmpty(target.Spec.Manifest, artifactPath(index, target.Spec.Build, buildKindKubernetesManifest), filepath.Join(".deploy", "kubernetes.yaml"))
		command := []string{"kubectl", "delete", "-f", absArtifactPath(loaded.Root, manifest), "--ignore-not-found"}
		return runDeployCommand(ctx, runner, loaded.Root, command, opts.DryRun, out, errOut)
	case deployKindHelm:
		release := firstNonEmpty(target.Spec.Release, loaded.Distribution.Spec.Name, "app")
		namespace := firstNonEmpty(target.Spec.Namespace, release)
		command := []string{"helm", "uninstall", release, "--namespace", namespace}
		return runDeployCommand(ctx, runner, loaded.Root, command, opts.DryRun, out, errOut)
	default:
		return fmt.Errorf("distribution deploy: unsupported deploy kind %q", target.Spec.Kind)
	}
}

func ensureDeployArtifacts(ctx context.Context, appRoot string, targets []string, policy string, opts TargetDeployOptions) error {
	if len(targets) == 0 {
		return nil
	}
	needsBuild := policy == BuildPolicyAlways
	index, err := readArtifactIndex(appRoot)
	if err != nil {
		if policy == BuildPolicyNever {
			return fmt.Errorf("distribution deploy: build artifacts are missing; run fluxplane build first")
		}
		needsBuild = true
	}
	if !needsBuild {
		for _, target := range targets {
			artifact, ok := artifactByTarget(index, target)
			if !ok || !artifactFilesExist(appRoot, artifact) {
				if policy == BuildPolicyNever {
					return fmt.Errorf("distribution deploy: build artifact %q is missing; run fluxplane build first", target)
				}
				needsBuild = true
				break
			}
		}
	}
	if !needsBuild {
		return nil
	}
	_, err = BuildApp(ctx, AppBuildOptions{
		AppDir:             opts.AppDir,
		Profile:            opts.Profile,
		Profiles:           opts.Profiles,
		Targets:            targets,
		Image:              opts.Image,
		BaseImage:          opts.BaseImage,
		AuthPath:           opts.AuthPath,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
		Provider:           opts.Provider,
		Model:              opts.Model,
		Effort:             opts.Effort,
		DryRun:             opts.DryRun,
		Force:              opts.Force,
		Out:                opts.Out,
		Err:                opts.Err,
		Runner:             opts.Runner,
	})
	return err
}

func artifactPath(index ArtifactIndex, targets []string, kind string) string {
	for _, target := range targets {
		artifact, ok := artifactByTarget(index, target)
		if ok && artifact.Kind == kind && artifact.Path != "" {
			return artifact.Path
		}
		if ok && kind == buildKindHelmChart && artifact.Kind == buildKindRuntimeStack && artifact.Path != "" {
			return artifact.Path
		}
	}
	for _, artifact := range index.Targets {
		if artifact.Kind == kind && artifact.Path != "" {
			return artifact.Path
		}
		if kind == buildKindHelmChart && artifact.Kind == buildKindRuntimeStack && artifact.Path != "" {
			return artifact.Path
		}
	}
	return ""
}

func absArtifactPath(root, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func runDeployCommand(ctx context.Context, runner CommandRunner, dir string, command []string, dryRun bool, out, errOut io.Writer) error {
	if dryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
		return nil
	}
	return runner.Run(ctx, dir, command[0], command[1:], out, errOut)
}

func deployWriters(out, errOut io.Writer) (io.Writer, io.Writer) {
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	return out, errOut
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func artifactFilesExist(root string, artifact BuildArtifact) bool {
	if artifact.Kind == buildKindDockerImage || artifact.Kind == "docker-base" {
		return false
	}
	if artifact.Path != "" && !fileExists(absArtifactPath(root, artifact.Path)) {
		return false
	}
	for _, path := range artifact.Paths {
		if !fileExists(absArtifactPath(root, path)) {
			return false
		}
	}
	return true
}
