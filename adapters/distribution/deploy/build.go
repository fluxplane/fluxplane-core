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
)

const defaultConnectorsPath = "/connectors"

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
	Command    []string
	DryRun     bool
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
	repoRoot, err := findRepoRoot(loaded.Root)
	if err != nil {
		return DockerBuildResult{}, err
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

	tempDir, err := os.MkdirTemp("", "agentsdk-docker-build-*")
	if err != nil {
		return DockerBuildResult{}, fmt.Errorf("distribution build: create temp context: %w", err)
	}
	cleanup := !opts.DryRun
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()

	assets, err := prepareContext(ctx, tempDir, repoRoot, loaded.Root, spec.Build.Assets, connectorsPath)
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

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func prepareContext(ctx context.Context, contextDir, repoRoot, appRoot string, assetPatterns []string, connectorsPath string) ([]string, error) {
	if err := copyDir(ctx, repoRoot, filepath.Join(contextDir, "src", "agentruntime"), sourceSkip); err != nil {
		return nil, fmt.Errorf("distribution build: copy source: %w", err)
	}
	replaceCopies, err := copyLocalReplaces(ctx, repoRoot, contextDir)
	if err != nil {
		return nil, err
	}
	assets, err := copyAssets(ctx, appRoot, filepath.Join(contextDir, "app"), assetPatterns)
	if err != nil {
		return nil, err
	}
	if err := writeDockerfile(filepath.Join(contextDir, "Dockerfile"), connectorsPath, replaceCopies); err != nil {
		return nil, err
	}
	return assets, nil
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
	if !hasGlob(pattern) {
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
	var out []string
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
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
		ok, err := matchGlob(pattern, rel)
		if err != nil {
			return err
		}
		if ok {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func hasGlob(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func matchGlob(pattern, rel string) (bool, error) {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

func matchSegments(pattern, rel []string) (bool, error) {
	if len(pattern) == 0 {
		return len(rel) == 0, nil
	}
	if pattern[0] == "**" {
		if ok, err := matchSegments(pattern[1:], rel); ok || err != nil {
			return ok, err
		}
		for i := range rel {
			ok, err := matchSegments(pattern[1:], rel[i+1:])
			if ok || err != nil {
				return ok, err
			}
		}
		return false, nil
	}
	if len(rel) == 0 {
		return false, nil
	}
	ok, err := path.Match(pattern[0], rel[0])
	if err != nil || !ok {
		return ok, err
	}
	return matchSegments(pattern[1:], rel[1:])
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

func writeDockerfile(filename, connectorsPath string, replaces []replaceCopy) error {
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
	b.WriteString("RUN go build -trimpath -ldflags=\"-s -w\" -o /out/agentsdk ./cmd/agentsdk\n\n")
	b.WriteString("FROM debian:bookworm-slim AS runtime\n")
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/*\n")
	b.WriteString("COPY --from=builder /out/agentsdk /usr/local/bin/agentsdk\n")
	b.WriteString("COPY app /app\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("ENV AGENTSDK_CONNECTORS_PATH=")
	b.WriteString(shellQuote(connectorsPath))
	b.WriteByte('\n')
	b.WriteString("ENTRYPOINT [\"/usr/local/bin/agentsdk\"]\n")
	cmd, _ := json.Marshal([]string{"serve", "/app", "--connectors-path", connectorsPath})
	b.WriteString("CMD ")
	b.Write(cmd)
	b.WriteByte('\n')
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

func shellQuote(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func printDryRun(out io.Writer, result DockerBuildResult) {
	_, _ = fmt.Fprintf(out, "context=%s\n", result.ContextDir)
	_, _ = fmt.Fprintf(out, "dockerfile=%s\n", result.Dockerfile)
	_, _ = fmt.Fprintf(out, "tags=%s\n", strings.Join(result.Tags, ","))
	if len(result.Platforms) > 0 {
		_, _ = fmt.Fprintf(out, "platforms=%s\n", strings.Join(result.Platforms, ","))
	}
	_, _ = fmt.Fprintf(out, "assets=%s\n", strings.Join(result.Assets, ","))
	_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(result.Command, " "))
}
