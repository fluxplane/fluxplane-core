// Package deploy adapts distributions into deployable artifacts such as
// container images and Helm charts.
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
	"strings"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/pathpattern"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
)

const (
	defaultConnectorsPath = "/connectors"
	defaultBaseImage      = "fluxplane/coder-base:local"
	defaultAppImage       = "agentruntime-app:latest"
	defaultMySQLDSNEnv    = "AGENTRUNTIME_DATASTORE_MYSQL_DSN"
	defaultNATSDSNEnv     = "AGENTRUNTIME_EVENTSTORE_NATS_DSN"
	defaultHealthAddr     = "127.0.0.1:18080"
	defaultHealthURL      = "http://127.0.0.1:18080/control/status"
)

// CommandRunner runs an external command.
type CommandRunner interface {
	Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error
}

// CommandRunnerFunc adapts a function to CommandRunner.
type CommandRunnerFunc func(context.Context, string, string, []string, io.Writer, io.Writer) error

func (f CommandRunnerFunc) Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	return f(ctx, dir, name, args, stdout, stderr)
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
	AppDir string
	Image  string
	DryRun bool
	Out    io.Writer
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
	baseImage := strings.TrimSpace(opts.BaseImage)
	if baseImage == "" {
		baseImage = defaultBaseImage
	}
	tags := resolveAppBuildTags(loaded.Distribution.Spec, opts)
	name := distributionName(loaded.Distribution.Spec)
	binaryPath := filepath.Join(outDir, "bin", composeServiceName(name))
	dockerfilePath := filepath.Join(outDir, "Dockerfile")
	composePath := filepath.Join(outDir, "docker-compose.yaml")
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
			if err := maybeWriteFile(dockerfilePath, workspaceDockerfile(baseImage, connectorsPath), 0o600, opts.DryRun, opts.Force, out); err != nil {
				return AppBuildResult{}, err
			}
		case "docker-compose":
			result.Artifacts = append(result.Artifacts, composePath)
			if err := maybeWriteFile(composePath, dockerComposeContent(name, composeImage, connectorsPath, loaded.Launch), 0o600, opts.DryRun, opts.Force, out); err != nil {
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
				if err := ensureDockerfileForImage(dockerfilePath, baseImage, connectorsPath, opts.Force); err != nil {
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

	assets, err := prepareAppContext(ctx, tempDir, loaded.Root, spec.Build.Assets, baseImage, connectorsPath)
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
	content := dockerComposeContent(loaded.Distribution.Spec.Name, image, defaultConnectorsPath, loaded.Launch)
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

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func prepareAppContext(ctx context.Context, contextDir, appRoot string, assetPatterns []string, baseImage, connectorsPath string) ([]string, error) {
	assets, err := copyAssets(ctx, appRoot, filepath.Join(contextDir, "app"), assetPatterns)
	if err != nil {
		return nil, err
	}
	if err := writeAppDockerfile(filepath.Join(contextDir, "Dockerfile"), baseImage, connectorsPath); err != nil {
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
		name = "agentsdk-app"
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
		case "binary", "dockerfile", "docker-image", "docker-compose":
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

func writeAppDockerfile(filename, baseImage, connectorsPath string) error {
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(baseImage)
	b.WriteByte('\n')
	b.WriteString("COPY app /app\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("ENTRYPOINT [\"/usr/local/bin/coder\"]\n")
	cmd, _ := json.Marshal(appServeCommand(connectorsPath))
	b.WriteString("CMD ")
	b.Write(cmd)
	b.WriteByte('\n')
	b.WriteString("HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD ")
	health, _ := json.Marshal(appHealthcheckCommand())
	b.Write(health)
	b.WriteByte('\n')
	return os.WriteFile(filename, []byte(b.String()), 0o600)
}

func workspaceDockerfile(baseImage, connectorsPath string) string {
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(baseImage)
	b.WriteByte('\n')
	b.WriteString("COPY . /app\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("ENTRYPOINT [\"/usr/local/bin/coder\"]\n")
	cmd, _ := json.Marshal(appServeCommand(connectorsPath))
	b.WriteString("CMD ")
	b.Write(cmd)
	b.WriteByte('\n')
	b.WriteString("HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD ")
	health, _ := json.Marshal(appHealthcheckCommand())
	b.Write(health)
	b.WriteByte('\n')
	return b.String()
}

func appServeCommand(connectorsPath string) []string {
	connectorsPath = strings.TrimSpace(connectorsPath)
	if connectorsPath == "" {
		connectorsPath = defaultConnectorsPath
	}
	return []string{"app", "serve", "/app", "--connectors-path", connectorsPath, "--health-addr", defaultHealthAddr}
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

func ensureDockerfileForImage(filename, baseImage, connectorsPath string, force bool) error {
	if _, err := os.Stat(filename); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return maybeWriteFile(filename, workspaceDockerfile(baseImage, connectorsPath), 0o600, false, force, io.Discard)
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

func dockerComposeContent(name, image, connectorsPath string, launch distribution.LaunchConfig) string {
	service := strings.TrimSpace(name)
	if service == "" {
		service = "app"
	}
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
	command, _ := json.Marshal(appServeCommand(connectorsPath))
	b.WriteString("    command: ")
	b.Write(command)
	b.WriteByte('\n')
	writeComposeEnv(&b, launch)
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

func writeComposeEnv(b *strings.Builder, launch distribution.LaunchConfig) {
	values := composeEnv(launch)
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

func composeEnv(launch distribution.LaunchConfig) map[string]string {
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
