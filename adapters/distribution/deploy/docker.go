package deploy

import (
	stdtar "archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	distlocal "github.com/fluxplane/fluxplane-core/adapters/distribution/local"
	"github.com/fluxplane/fluxplane-core/core/pathpattern"
	"github.com/moby/go-archive"
	dockercontainer "github.com/moby/moby/api/types/container"
	dockermount "github.com/moby/moby/api/types/mount"
	dockernetwork "github.com/moby/moby/api/types/network"
	dockerregistry "github.com/moby/moby/api/types/registry"
	dockerclient "github.com/moby/moby/client"
)

func dockerClientFor(runner CommandRunner, explicit DockerClient) DockerClient {
	if explicit != nil {
		return explicit
	}
	if runner != nil {
		return commandDockerClient{runner: runner}
	}
	return engineDockerClient{}
}

type commandDockerClient struct {
	runner CommandRunner
}

type engineDockerClient struct{}

type dockerNetworkClient interface {
	NetworkInspect(context.Context, string, dockerclient.NetworkInspectOptions) (dockerclient.NetworkInspectResult, error)
	NetworkCreate(context.Context, string, dockerclient.NetworkCreateOptions) (dockerclient.NetworkCreateResult, error)
}

type dockerContainerSpec struct {
	name       string
	image      string
	command    []string
	env        []string
	workingDir string
	aliases    []string
	mounts     []dockermount.Mount
	volumes    []string
	pull       bool
}

func dockerStackContainers(stack dockerComposeStack) ([]dockerContainerSpec, error) {
	service := composeServiceName(stack.Name)
	runtimeEnv, err := composeRuntimeEnvForStack(stack)
	if err != nil {
		return nil, err
	}
	var out []dockerContainerSpec
	if composeUsesMySQL(stack.Launch) {
		env, err := dockerEnv(mysqlDockerEnvironment(runtimeEnv))
		if err != nil {
			return nil, err
		}
		volume := dockerStackVolumeName(service, "mysql-data")
		out = append(out, dockerContainerSpec{
			name:    dockerStackContainerName(service, "mysql"),
			image:   "mysql:8.4",
			env:     env,
			aliases: []string{"mysql"},
			mounts:  []dockermount.Mount{{Type: dockermount.TypeVolume, Source: volume, Target: "/var/lib/mysql"}},
			volumes: []string{volume},
			pull:    true,
		})
	}
	if composeUsesNATS(stack.Launch) {
		volume := dockerStackVolumeName(service, "nats-data")
		out = append(out, dockerContainerSpec{
			name:    dockerStackContainerName(service, "nats"),
			image:   "nats:2.11-alpine",
			command: []string{"-js", "-sd", "/data", "-m", "8222"},
			aliases: []string{"nats"},
			mounts:  []dockermount.Mount{{Type: dockermount.TypeVolume, Source: volume, Target: "/data"}},
			volumes: []string{volume},
			pull:    true,
		})
	}
	envValues := composeEnv(stack.AppRuntime, stack.Launch)
	for key, value := range runtimeEnv {
		if _, ok := envValues[key]; ok {
			envValues[key] = value
		}
	}
	env, err := dockerEnv(envValues)
	if err != nil {
		return nil, err
	}
	out = append(out, dockerContainerSpec{
		name:       dockerStackContainerName(service, "app"),
		image:      stack.Image,
		command:    appServeCommand(stack.AuthPath, stack.AppRuntime),
		env:        env,
		workingDir: "/app",
		aliases:    []string{service, "app"},
	})
	return out, nil
}

func dockerEnv(values map[string]string) ([]string, error) {
	keys := sortedKeys(values)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		value, err := resolveDockerEnvValue(key, values[key])
		if err != nil {
			return nil, err
		}
		out = append(out, key+"="+value)
	}
	return out, nil
}

func resolveDockerEnvValue(key, value string) (string, error) {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return value, nil
	}
	expr := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	name, message, required := strings.Cut(expr, ":?")
	name = strings.TrimSpace(name)
	if name == "" {
		return value, nil
	}
	resolved, ok := os.LookupEnv(name)
	if required && (!ok || resolved == "") {
		if strings.TrimSpace(message) == "" {
			message = key + " is required"
		}
		return "", fmt.Errorf("distribution deploy: %s", message)
	}
	if ok {
		return resolved, nil
	}
	return value, nil
}

func removeDockerContainer(ctx context.Context, cli *dockerclient.Client, name string) error {
	timeout := 10
	if _, err := cli.ContainerStop(ctx, name, dockerclient.ContainerStopOptions{Timeout: &timeout}); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("distribution deploy: stop docker container %s: %w", name, err)
	}
	if _, err := cli.ContainerRemove(ctx, name, dockerclient.ContainerRemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("distribution deploy: remove docker container %s: %w", name, err)
	}
	return nil
}

func dockerStackContainerName(stack, service string) string {
	return "fluxplane-" + stack + "-" + service
}

func dockerStackNetworkName(stack string) string {
	return "fluxplane-" + stack
}

func dockerStackVolumeName(stack, volume string) string {
	return "fluxplane-" + stack + "-" + volume
}

func dockerStackLabels(stack string) map[string]string {
	return map[string]string{deployStackLabel: stack}
}

func ensureDockerNetwork(ctx context.Context, cli dockerNetworkClient, name string, labels map[string]string) error {
	inspected, err := cli.NetworkInspect(ctx, name, dockerclient.NetworkInspectOptions{})
	if err == nil {
		if want := labels[deployStackLabel]; want != "" && inspected.Network.Labels[deployStackLabel] != want {
			return fmt.Errorf("distribution deploy: docker network %s already exists and is not managed by this deploy stack", name)
		}
		return nil
	}
	if !errdefs.IsNotFound(err) {
		return fmt.Errorf("distribution deploy: inspect docker network %s: %w", name, err)
	}
	if _, err := cli.NetworkCreate(ctx, name, dockerclient.NetworkCreateOptions{
		Driver: "bridge",
		Labels: labels,
	}); err != nil && !errdefs.IsAlreadyExists(err) {
		return fmt.Errorf("distribution deploy: create docker network %s: %w", name, err)
	}
	return nil
}

// DockerBuildOptions configures a generated Docker image build.
type DockerBuildOptions struct {
	AppDir             string
	Profile            string
	Profiles           []string
	TempDir            string
	Tags               []string
	Platforms          []string
	Push               bool
	DryRun             bool
	KeepContext        bool
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

// BaseImageOptions configures a reusable CLI runtime image build.
type BaseImageOptions struct {
	RepoRoot     string
	BinaryPath   string
	TempDir      string
	Tags         []string
	Platforms    []string
	Push         bool
	DryRun       bool
	KeepContext  bool
	Out          io.Writer
	Err          io.Writer
	Runner       CommandRunner
	dockerClient DockerClient
}

// BaseImageResult describes the resolved CLI base image build.
type BaseImageResult struct {
	ContextDir string
	Dockerfile string
	Tags       []string
	Platforms  []string
	Command    []string
	DryRun     bool
}

// BuildDocker builds a Docker image for a local app distribution.
func BuildDocker(ctx context.Context, opts DockerBuildOptions) (DockerBuildResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	authPath := strings.TrimSpace(opts.AuthPath)
	if authPath == "" {
		authPath = defaultAuthPath
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
	dockerClient := dockerClientFor(opts.Runner, opts.dockerClient)

	loaded, err := distlocal.LoadWithOptions(ctx, appDir, distlocal.LoadOptions{Profile: opts.Profile, Profiles: opts.Profiles})
	if err != nil {
		return DockerBuildResult{}, err
	}
	appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
		Provider:           opts.Provider,
		Model:              opts.Model,
		Effort:             opts.Effort,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
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

	tempDir, err := os.MkdirTemp(opts.TempDir, "coder-app-docker-build-*")
	if err != nil {
		return DockerBuildResult{}, fmt.Errorf("distribution build: create temp context: %w", err)
	}
	cleanup := !opts.KeepContext
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()

	assets, err := prepareAppContext(ctx, tempDir, loaded.Root, spec.Build.Assets, baseImage, authPath, appRuntime)
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
	if err := dockerClient.BuildImage(ctx, tempDir, filepath.Join(tempDir, "Dockerfile"), tags, platforms, opts.Push, out, errOut); err != nil {
		return DockerBuildResult{}, err
	}
	return result, nil
}

type baseImageRuntime struct {
	name         string
	defaultTag   string
	binaryName   string
	buildWorkDir string
	sourcePkg    string
	tempPattern  string
}

var (
	coderBaseRuntime = baseImageRuntime{
		name:         "coder",
		defaultTag:   defaultCoderBaseImage,
		binaryName:   "coder",
		buildWorkDir: "/src/fluxplane/apps/coder",
		sourcePkg:    "./cmd/coder",
		tempPattern:  "coder-base-docker-build-*",
	}
	fluxplaneBaseRuntime = baseImageRuntime{
		name:         "fluxplane",
		defaultTag:   defaultBaseImage,
		binaryName:   "fluxplane",
		buildWorkDir: "/src/fluxplane",
		sourcePkg:    "./cmd/fluxplane",
		tempPattern:  "fluxplane-base-docker-build-*",
	}
)

// BuildCoderBaseDocker builds the reusable Docker base image containing coder.
func BuildCoderBaseDocker(ctx context.Context, opts BaseImageOptions) (BaseImageResult, error) {
	return buildRuntimeBaseDocker(ctx, opts, coderBaseRuntime)
}

// BuildFluxplaneBaseDocker builds the reusable Docker base image containing fluxplane.
func BuildFluxplaneBaseDocker(ctx context.Context, opts BaseImageOptions) (BaseImageResult, error) {
	return buildRuntimeBaseDocker(ctx, opts, fluxplaneBaseRuntime)
}

func buildRuntimeBaseDocker(ctx context.Context, opts BaseImageOptions, runtime baseImageRuntime) (BaseImageResult, error) {
	tags := cleanStrings(opts.Tags)
	if len(tags) == 0 {
		tags = []string{runtime.defaultTag}
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
	dockerClient := dockerClientFor(opts.Runner, opts.dockerClient)
	tempDir, err := os.MkdirTemp(opts.TempDir, runtime.tempPattern)
	if err != nil {
		return BaseImageResult{}, fmt.Errorf("distribution build: create base image context: %w", err)
	}
	cleanup := !opts.KeepContext
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err := prepareBaseImageContext(ctx, tempDir, opts, runtime); err != nil {
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
	if err := dockerClient.BuildImage(ctx, tempDir, filepath.Join(tempDir, "Dockerfile"), tags, platforms, opts.Push, out, errOut); err != nil {
		return BaseImageResult{}, err
	}
	return result, nil
}

func prepareAppContext(ctx context.Context, contextDir, appRoot string, assetPatterns []string, baseImage, authPath string, appRuntime appRuntimeOptions) ([]string, error) {
	assets, err := copyAssets(ctx, appRoot, filepath.Join(contextDir, "app"), assetPatterns)
	if err != nil {
		return nil, err
	}
	if err := writeAppDockerfile(filepath.Join(contextDir, "Dockerfile"), baseImage, authPath, appRuntime); err != nil {
		return nil, err
	}
	return assets, nil
}

func prepareBaseImageContext(ctx context.Context, contextDir string, opts BaseImageOptions, runtime baseImageRuntime) error {
	repoRoot := strings.TrimSpace(opts.RepoRoot)
	if repoRoot == "" {
		return prepareBinaryBaseImageContext(contextDir, opts.BinaryPath, runtime)
	}
	repoRoot, err := findRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	if err := copyDir(ctx, repoRoot, filepath.Join(contextDir, "src", "fluxplane"), sourceSkip); err != nil {
		return fmt.Errorf("distribution build: copy source: %w", err)
	}
	replaceCopies, err := copyLocalReplaces(ctx, repoRoot, contextDir)
	if err != nil {
		return err
	}
	return writeSourceBaseDockerfile(filepath.Join(contextDir, "Dockerfile"), replaceCopies, runtime)
}

func prepareBinaryBaseImageContext(contextDir, binaryPath string, runtime baseImageRuntime) error {
	binaryPath = strings.TrimSpace(binaryPath)
	if binaryPath == "" {
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("distribution build: resolve %s executable: %w", runtime.name, err)
		}
		binaryPath = executable
	}
	if resolved, err := filepath.EvalSymlinks(binaryPath); err == nil {
		binaryPath = resolved
	}
	if err := copyFile(binaryPath, filepath.Join(contextDir, runtime.binaryName)); err != nil {
		return fmt.Errorf("distribution build: copy %s executable: %w", runtime.name, err)
	}
	return writeBinaryBaseDockerfile(filepath.Join(contextDir, "Dockerfile"), runtime)
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
		containerAbs := path.Clean(path.Join("/src/fluxplane", filepath.ToSlash(rel)))
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

func writeAppDockerfile(filename, baseImage, authPath string, appRuntime appRuntimeOptions) error {
	cmd, _ := json.Marshal(appServeCommand(authPath, appRuntime))
	health, _ := json.Marshal(appHealthcheckCommand())
	content := dockerfileLines(
		"FROM "+baseImage,
		"COPY app /app",
		"WORKDIR /app",
		`ENTRYPOINT ["/usr/local/bin/fluxplane"]`,
		"CMD "+string(cmd),
		"HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD "+string(health),
	)
	return os.WriteFile(filename, []byte(content), 0o600)
}

func workspaceDockerfile(baseImage, authPath string, appRuntime appRuntimeOptions) string {
	cmd, _ := json.Marshal(appServeCommand(authPath, appRuntime))
	health, _ := json.Marshal(appHealthcheckCommand())
	return dockerfileLines(
		"FROM "+baseImage,
		"COPY . /app",
		"WORKDIR /app",
		`ENTRYPOINT ["/usr/local/bin/fluxplane"]`,
		"CMD "+string(cmd),
		"HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD "+string(health),
	)
}

func writeSourceBaseDockerfile(filename string, replaces []replaceCopy, runtime baseImageRuntime) error {
	lines := []string{
		"FROM golang:1.26-bookworm AS builder",
		"WORKDIR /src/fluxplane",
	}
	for _, repl := range replaces {
		lines = append(lines, "COPY "+repl.ContextRel+" "+repl.ContainerAbs)
	}
	lines = append(lines,
		"COPY src/fluxplane /src/fluxplane",
		"WORKDIR "+runtime.buildWorkDir,
		fmt.Sprintf(`RUN go build -trimpath -ldflags="-s -w" -o /out/%s %s`, runtime.binaryName, runtime.sourcePkg),
		"",
		"FROM debian:bookworm-slim AS runtime",
		"RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/*",
		fmt.Sprintf("COPY --from=builder /out/%s /usr/local/bin/%s", runtime.binaryName, runtime.binaryName),
		fmt.Sprintf(`ENTRYPOINT ["/usr/local/bin/%s"]`, runtime.binaryName),
	)
	return os.WriteFile(filename, []byte(dockerfileLines(lines...)), 0o600)
}

func writeBinaryBaseDockerfile(filename string, runtime baseImageRuntime) error {
	content := dockerfileLines(
		"FROM debian:bookworm-slim AS runtime",
		"RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/*",
		fmt.Sprintf("COPY %s /usr/local/bin/%s", runtime.binaryName, runtime.binaryName),
		fmt.Sprintf(`ENTRYPOINT ["/usr/local/bin/%s"]`, runtime.binaryName),
	)
	return os.WriteFile(filename, []byte(content), 0o600)
}

func dockerfileLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
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
		if err == nil && strings.Contains(string(data), "module github.com/fluxplane/fluxplane-core") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("distribution build: could not find github.com/fluxplane/fluxplane-core source root from %s", start)
		}
		dir = parent
	}
}

func ensureDockerfileForImage(filename, baseImage, authPath string, appRuntime appRuntimeOptions, force bool) error {
	if _, err := os.Stat(filename); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return maybeWriteFile(filename, workspaceDockerfile(baseImage, authPath, appRuntime), 0o600, false, force, io.Discard)
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

func runDockerPushCommand(ctx context.Context, client DockerClient, command []string, out, errOut io.Writer) error {
	if len(command) == 4 && command[0] == "docker" && command[1] == "tag" {
		return client.TagImage(ctx, command[2], command[3], out, errOut)
	}
	if len(command) == 3 && command[0] == "docker" && command[1] == "push" {
		return client.PushImage(ctx, command[2], out, errOut)
	}
	return fmt.Errorf("distribution deploy: unsupported docker image command %q", strings.Join(command, " "))
}

func (c commandDockerClient) BuildImage(ctx context.Context, contextDir, dockerfile string, tags, platformValues []string, push bool, stdout, stderr io.Writer) error {
	command, err := dockerCommand(tags, platformValues, push)
	if err != nil {
		return err
	}
	if dockerfile != "" && !sameDockerfilePath(contextDir, dockerfile, "Dockerfile") {
		command = append(command, "-f", dockerfile)
	}
	command = append(command, contextDir)
	return c.runner.Run(ctx, "", command[0], command[1:], stdout, stderr)
}

func (c commandDockerClient) TagImage(ctx context.Context, source, target string, stdout, stderr io.Writer) error {
	return c.runner.Run(ctx, "", "docker", []string{"tag", source, target}, stdout, stderr)
}

func (c commandDockerClient) PushImage(ctx context.Context, image string, stdout, stderr io.Writer) error {
	return c.runner.Run(ctx, "", "docker", []string{"push", image}, stdout, stderr)
}

func (c commandDockerClient) DeployComposeStack(ctx context.Context, stack dockerComposeStack, stdout, stderr io.Writer) error {
	envFile, err := ensureComposeRuntimeEnvFile(stack.AppDir, stack.Launch, false, stdout)
	if err != nil {
		return err
	}
	command := dockerComposeUpCommand(filepath.Join(stack.AppDir, "docker-compose.yaml"), false, envFile)
	return c.runner.Run(ctx, stack.AppDir, command[0], command[1:], stdout, stderr)
}

func (c commandDockerClient) UndeployComposeStack(ctx context.Context, stack dockerComposeStack, volumes bool, stdout, stderr io.Writer) error {
	envFile, err := composeRuntimeEnvFileForTeardown(stack.AppDir, stack.Launch, false, stdout)
	if err != nil {
		return err
	}
	command := dockerComposeDownCommand(filepath.Join(stack.AppDir, "docker-compose.yaml"), envFile, volumes)
	return c.runner.Run(ctx, stack.AppDir, command[0], command[1:], stdout, stderr)
}

func (engineDockerClient) BuildImage(ctx context.Context, contextDir, dockerfile string, tags, platformValues []string, push bool, stdout, _ io.Writer) error {
	cli, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		return fmt.Errorf("distribution deploy: create docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()
	tar, dockerfileName, err := dockerBuildContext(contextDir, dockerfile)
	if err != nil {
		return fmt.Errorf("distribution deploy: archive docker build context: %w", err)
	}
	defer func() { _ = tar.Close() }()
	opts := dockerclient.ImageBuildOptions{
		Tags:       tags,
		Dockerfile: dockerfileName,
		Remove:     true,
	}
	for _, value := range platformValues {
		platform, err := platforms.Parse(value)
		if err != nil {
			return fmt.Errorf("distribution deploy: parse docker platform %q: %w", value, err)
		}
		opts.Platforms = append(opts.Platforms, platform)
	}
	result, err := cli.ImageBuild(ctx, tar, opts)
	if err != nil {
		return fmt.Errorf("distribution deploy: docker image build: %w", err)
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if err := consumeDockerJSONStream(result.Body, stdout, "docker build"); err != nil {
		return err
	}
	if push {
		for _, tag := range tags {
			if err := (engineDockerClient{}).PushImage(ctx, tag, stdout, io.Discard); err != nil {
				return err
			}
		}
	}
	return nil
}

func (engineDockerClient) TagImage(ctx context.Context, source, target string, _, _ io.Writer) error {
	cli, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		return fmt.Errorf("distribution deploy: create docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()
	if _, err := cli.ImageTag(ctx, dockerclient.ImageTagOptions{Source: source, Target: target}); err != nil {
		return fmt.Errorf("distribution deploy: docker tag %s -> %s: %w", source, target, err)
	}
	return nil
}

func (engineDockerClient) PushImage(ctx context.Context, image string, stdout, _ io.Writer) error {
	cli, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		return fmt.Errorf("distribution deploy: create docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()
	auth, err := dockerRegistryAuth(ctx, image, true)
	if err != nil {
		return err
	}
	result, err := cli.ImagePush(ctx, image, dockerclient.ImagePushOptions{RegistryAuth: auth})
	if err != nil {
		return fmt.Errorf("distribution deploy: docker push %s: %w", image, err)
	}
	if stdout == nil {
		stdout = io.Discard
	}
	return consumeDockerJSONStream(result, stdout, "docker push "+image)
}

func dockerBuildContext(contextDir, dockerfile string) (io.ReadCloser, string, error) {
	dockerfile = strings.TrimSpace(dockerfile)
	if dockerfile == "" {
		dockerfile = filepath.Join(contextDir, "Dockerfile")
	}
	rel, err := filepath.Rel(contextDir, dockerfile)
	if err == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel) {
		stream, err := archive.TarWithOptions(contextDir, &archive.TarOptions{})
		return stream, filepath.ToSlash(rel), err
	}
	base, err := archive.TarWithOptions(contextDir, &archive.TarOptions{ExcludePatterns: []string{"Dockerfile"}})
	if err != nil {
		return nil, "", err
	}
	reader, writer := io.Pipe()
	go func() {
		err := writeDockerBuildContext(writer, base, dockerfile)
		_ = base.Close()
		_ = writer.CloseWithError(err)
	}()
	return reader, "Dockerfile", nil
}

func writeDockerBuildContext(writer *io.PipeWriter, base io.Reader, dockerfile string) error {
	tarReader := stdtar.NewReader(base)
	tarWriter := stdtar.NewWriter(writer)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = tarWriter.Close()
			return err
		}
		if path.Clean(header.Name) == "Dockerfile" {
			continue
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			_ = tarWriter.Close()
			return err
		}
		if _, err := io.Copy(tarWriter, tarReader); err != nil {
			_ = tarWriter.Close()
			return err
		}
	}
	if err := addDockerfileToTar(tarWriter, dockerfile); err != nil {
		_ = tarWriter.Close()
		return err
	}
	return tarWriter.Close()
}

func addDockerfileToTar(writer *stdtar.Writer, dockerfile string) error {
	file, err := os.Open(dockerfile)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	header, err := stdtar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = "Dockerfile"
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err = io.Copy(writer, file)
	return err
}

func sameDockerfilePath(contextDir, dockerfile, name string) bool {
	rel, err := filepath.Rel(contextDir, dockerfile)
	return err == nil && filepath.ToSlash(rel) == name
}

type dockerStreamMessage struct {
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

func consumeDockerJSONStream(stream io.ReadCloser, stdout io.Writer, operation string) error {
	defer func() { _ = stream.Close() }()
	if stdout == nil {
		stdout = io.Discard
	}
	var streamErr string
	reader := bufio.NewReader(stream)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if _, writeErr := stdout.Write(line); writeErr != nil {
				return fmt.Errorf("distribution deploy: write %s output: %w", operation, writeErr)
			}
			if message := dockerStreamError(line); message != "" {
				streamErr = message
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("distribution deploy: read %s output: %w", operation, err)
		}
	}
	if streamErr != "" {
		return fmt.Errorf("distribution deploy: %s failed: %s", operation, streamErr)
	}
	return nil
}

func dockerStreamError(line []byte) string {
	var message dockerStreamMessage
	if err := json.Unmarshal(bytes.TrimSpace(line), &message); err != nil {
		return ""
	}
	if strings.TrimSpace(message.ErrorDetail.Message) != "" {
		return strings.TrimSpace(message.ErrorDetail.Message)
	}
	return strings.TrimSpace(message.Error)
}

func pullDockerImage(ctx context.Context, cli *dockerclient.Client, image string, stdout io.Writer) error {
	auth, err := dockerRegistryAuth(ctx, image, false)
	if err != nil {
		return err
	}
	result, err := cli.ImagePull(ctx, image, dockerclient.ImagePullOptions{RegistryAuth: auth})
	if err != nil {
		return fmt.Errorf("distribution deploy: docker pull %s: %w", image, err)
	}
	return consumeDockerJSONStream(result, stdout, "docker pull "+image)
}

type dockerConfig struct {
	Auths       map[string]dockerAuthEntry `json:"auths"`
	CredsStore  string                     `json:"credsStore"`
	CredHelpers map[string]string          `json:"credHelpers"`
}

type dockerAuthEntry struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	Auth          string `json:"auth"`
	IdentityToken string `json:"identitytoken"`
	RegistryToken string `json:"registrytoken"`
	ServerAddress string `json:"serveraddress"`
}

type dockerCredentialHelperResult struct {
	Username  string `json:"Username"`
	Secret    string `json:"Secret"`
	ServerURL string `json:"ServerURL"`
}

func dockerRegistryAuth(ctx context.Context, image string, required bool) (string, error) {
	config, err := loadDockerConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	registry := dockerAuthRegistry(image)
	if helper := dockerCredentialHelper(config, registry); helper != "" {
		auth, err := dockerCredentialHelperAuth(ctx, helper, registry)
		if err != nil {
			if required {
				return "", err
			}
			return "", nil
		}
		return encodeDockerAuth(auth)
	}
	for _, key := range dockerAuthConfigKeys(registry) {
		entry, ok := config.Auths[key]
		if !ok {
			continue
		}
		auth := dockerAuthConfigFromEntry(entry, registry)
		return encodeDockerAuth(auth)
	}
	return "", nil
}

func dockerAuthConfigFromEntry(entry dockerAuthEntry, registry string) dockerregistry.AuthConfig {
	auth := dockerregistry.AuthConfig{
		Username:      entry.Username,
		Password:      entry.Password,
		IdentityToken: entry.IdentityToken,
		RegistryToken: entry.RegistryToken,
		ServerAddress: firstNonEmpty(entry.ServerAddress, registry),
	}
	if auth.Username == "" && auth.Password == "" && entry.Auth != "" {
		if decoded, err := base64.StdEncoding.DecodeString(entry.Auth); err == nil {
			username, password, ok := strings.Cut(string(decoded), ":")
			if ok {
				auth.Username = username
				auth.Password = password
			}
		}
	}
	return auth
}

func loadDockerConfig() (dockerConfig, error) {
	configDir := strings.TrimSpace(os.Getenv("DOCKER_CONFIG"))
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return dockerConfig{}, err
		}
		configDir = filepath.Join(home, ".docker")
	}
	data, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		return dockerConfig{}, err
	}
	var config dockerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return dockerConfig{}, fmt.Errorf("distribution deploy: read docker config: %w", err)
	}
	return config, nil
}

func dockerCredentialHelper(config dockerConfig, registry string) string {
	for _, key := range dockerAuthConfigKeys(registry) {
		if helper := strings.TrimSpace(config.CredHelpers[key]); helper != "" {
			return helper
		}
	}
	return strings.TrimSpace(config.CredsStore)
}

func dockerCredentialHelperAuth(ctx context.Context, helper, registry string) (dockerregistry.AuthConfig, error) {
	name := "docker-credential-" + helper
	cmd := exec.CommandContext(ctx, name, "get")
	cmd.Stdin = strings.NewReader(registry)
	output, err := cmd.Output()
	if err != nil {
		return dockerregistry.AuthConfig{}, fmt.Errorf("distribution deploy: docker credential helper %s: %w", name, err)
	}
	var result dockerCredentialHelperResult
	if err := json.Unmarshal(output, &result); err != nil {
		return dockerregistry.AuthConfig{}, fmt.Errorf("distribution deploy: parse docker credential helper output: %w", err)
	}
	auth := dockerregistry.AuthConfig{ServerAddress: firstNonEmpty(result.ServerURL, registry)}
	if result.Username == "<token>" {
		auth.IdentityToken = result.Secret
	} else {
		auth.Username = result.Username
		auth.Password = result.Secret
	}
	return auth, nil
}

func dockerAuthRegistry(image string) string {
	registry := imageRegistry(image)
	if registry == "" || registry == "docker.io" || registry == "registry-1.docker.io" {
		return "https://index.docker.io/v1/"
	}
	return registry
}

func dockerAuthConfigKeys(registry string) []string {
	if registry == "https://index.docker.io/v1/" {
		return []string{registry, "registry-1.docker.io", "docker.io"}
	}
	return []string{registry, "https://" + registry}
}

func encodeDockerAuth(auth dockerregistry.AuthConfig) (string, error) {
	if auth.Username == "" && auth.Password == "" && auth.Auth == "" && auth.IdentityToken == "" && auth.RegistryToken == "" {
		return "", nil
	}
	data, err := json.Marshal(auth)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(data), nil
}

func (engineDockerClient) DeployComposeStack(ctx context.Context, stack dockerComposeStack, stdout, stderr io.Writer) error {
	cli, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		return fmt.Errorf("distribution deploy: create docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()
	if _, err := ensureComposeRuntimeEnvFile(stack.AppDir, stack.Launch, false, stdout); err != nil {
		return err
	}
	service := composeServiceName(stack.Name)
	networkName := dockerStackNetworkName(service)
	if err := ensureDockerNetwork(ctx, cli, networkName, dockerStackLabels(service)); err != nil {
		return err
	}
	containers, err := dockerStackContainers(stack)
	if err != nil {
		return err
	}
	for _, spec := range containers {
		if spec.pull {
			if err := pullDockerImage(ctx, cli, spec.image, stdout); err != nil {
				return err
			}
		}
		if err := removeDockerContainer(ctx, cli, spec.name); err != nil {
			return err
		}
		for _, volume := range spec.volumes {
			if _, err := cli.VolumeCreate(ctx, dockerclient.VolumeCreateOptions{Name: volume, Labels: dockerStackLabels(service)}); err != nil && !errdefs.IsAlreadyExists(err) {
				return fmt.Errorf("distribution deploy: create docker volume %s: %w", volume, err)
			}
		}
		created, err := cli.ContainerCreate(ctx, dockerclient.ContainerCreateOptions{
			Name: spec.name,
			Config: &dockercontainer.Config{
				Image:      spec.image,
				Cmd:        spec.command,
				Env:        spec.env,
				WorkingDir: spec.workingDir,
				Labels:     dockerStackLabels(service),
			},
			HostConfig: &dockercontainer.HostConfig{
				RestartPolicy: dockercontainer.RestartPolicy{Name: "unless-stopped"},
				NetworkMode:   dockercontainer.NetworkMode(networkName),
				Mounts:        spec.mounts,
			},
			NetworkingConfig: &dockernetwork.NetworkingConfig{EndpointsConfig: map[string]*dockernetwork.EndpointSettings{
				networkName: {Aliases: spec.aliases},
			}},
		})
		if err != nil {
			return fmt.Errorf("distribution deploy: create docker container %s: %w", spec.name, err)
		}
		if _, err := cli.ContainerStart(ctx, created.ID, dockerclient.ContainerStartOptions{}); err != nil {
			return fmt.Errorf("distribution deploy: start docker container %s: %w", spec.name, err)
		}
	}
	return nil
}

func (engineDockerClient) UndeployComposeStack(ctx context.Context, stack dockerComposeStack, volumes bool, stdout, stderr io.Writer) error {
	cli, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		return fmt.Errorf("distribution deploy: create docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()
	service := composeServiceName(stack.Name)
	containers, err := dockerStackContainers(stack)
	if err != nil {
		return err
	}
	for _, spec := range containers {
		if err := removeDockerContainer(ctx, cli, spec.name); err != nil {
			return err
		}
		if volumes {
			for _, volume := range spec.volumes {
				if _, err := cli.VolumeRemove(ctx, volume, dockerclient.VolumeRemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
					return fmt.Errorf("distribution deploy: remove docker volume %s: %w", volume, err)
				}
			}
		}
	}
	if _, err := cli.NetworkRemove(ctx, dockerStackNetworkName(service), dockerclient.NetworkRemoveOptions{}); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("distribution deploy: remove docker network %s: %w", dockerStackNetworkName(service), err)
	}
	return nil
}
