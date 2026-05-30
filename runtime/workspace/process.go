package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/hostsystem"
)

// HostProcess executes direct host processes without a shell interpreter.
type HostProcess struct {
	workspace *HostWorkspace
	env       *WorkspaceEnvironment
	process   *hostsystem.Process
}

// NewHostProcess returns a host process manager.
func NewProcessManager(workspace *HostWorkspace) *HostProcess {
	env, _ := NewEnvironment(workspace)
	return NewProcessManagerWithEnvironment(workspace, env)
}

// NewHostProcessWithEnvironment returns a host process manager using env.
func NewProcessManagerWithEnvironment(workspace *HostWorkspace, env *WorkspaceEnvironment) *HostProcess {
	if env == nil {
		env, _ = NewEnvironment(workspace)
	}
	return &HostProcess{
		workspace: workspace,
		env:       env,
		process:   hostsystem.NewProcess(workspace.Root(), workspaceProcessEnvironment{}, nil),
	}
}

// Run executes one direct process and waits for completion.
func (p *HostProcess) Run(ctx context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessResult, error) {
	maxBytes := req.MaxStdout
	if req.MaxStderr > maxBytes {
		maxBytes = req.MaxStderr
	}
	capture, err := fpsystem.RunProcessCapture(ctx, p, req, maxBytes)
	return capture.Result, err
}

// Start launches one direct process under management.
func (p *HostProcess) Start(ctx context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessHandle, error) {
	preparedCtx, prepared, err := p.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	return p.process.Start(preparedCtx, prepared)
}

// Ensure returns a running process for req.Label or starts a new one.
func (p *HostProcess) Ensure(ctx context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessHandle, bool, error) {
	preparedCtx, prepared, err := p.prepare(ctx, req)
	if err != nil {
		return nil, false, err
	}
	return p.process.Ensure(preparedCtx, prepared)
}

// Group returns controls for a managed process group.
func (p *HostProcess) Group(name string) fpsystem.ProcessGroup {
	return p.process.Group(name)
}

// List returns known managed processes.
func (p *HostProcess) List(context.Context) ([]fpsystem.ProcessInfo, error) {
	return p.process.List(context.Background())
}

func (p *HostProcess) prepare(ctx context.Context, req fpsystem.ProcessRequest) (context.Context, fpsystem.ProcessRequest, error) {
	if p == nil || p.workspace == nil {
		return ctx, req, fmt.Errorf("host process workspace is nil")
	}
	workdir := p.workspace.Root()
	processRoot := p.workspace.roots[0]
	if strings.TrimSpace(req.Workdir) != "" {
		resolved, err := p.workspace.ResolveProcessWorkdir(req.Workdir)
		if err != nil {
			return ctx, req, err
		}
		info, err := os.Stat(resolved.Abs)
		if err != nil {
			return ctx, req, err
		}
		if !info.IsDir() {
			return ctx, req, fmt.Errorf("workdir is not a directory")
		}
		workdir = resolved.Abs
		if root, ok := p.workspace.rootForAbs(workdir); ok {
			processRoot = root
		}
	}
	env, err := p.env.processEnv(processRoot, req.Env)
	if err != nil {
		return ctx, req, err
	}
	req.Workdir = workdir
	return context.WithValue(ctxOrBackground(ctx), workspaceProcessEnvKey{}, env), req, nil
}

type workspaceProcessEnvKey struct{}

type workspaceProcessEnvironment struct{}

func (workspaceProcessEnvironment) Lookup(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func (workspaceProcessEnvironment) ProcessEnv(ctx context.Context, overrides []string) ([]string, error) {
	if env, ok := ctx.Value(workspaceProcessEnvKey{}).([]string); ok {
		return append([]string(nil), env...), nil
	}
	return hostsystem.Environment{}.ProcessEnv(ctx, overrides)
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// DefaultProcessEnv returns a small environment for host process execution.
func DefaultProcessEnv() []string {
	env := make([]string, 0, len(defaultProcessEnvKeys))
	for _, key := range defaultProcessEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

// matchFilterPattern reports whether relPath (slash-delimited, workspace-relative)
// matches the glob pattern. For directories, both the path itself and a wildcard
// suffix are tried so that a pattern like "*.go" still allows walking into
// directories that may contain matching files.
func matchFilterPattern(pattern, relPath string, isDir bool) bool {
	matched, err := filepath.Match(pattern, relPath)
	if err != nil {
		return false
	}
	if matched {
		return true
	}
	// Also match the base name alone so "*.go" matches "sub/file.go".
	base := filepath.Base(relPath)
	if base != relPath {
		if m, _ := filepath.Match(pattern, base); m {
			return true
		}
	}
	return false
}
