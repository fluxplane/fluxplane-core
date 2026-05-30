// Package system defines runtime IO boundaries used by concrete operations.
package system

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/hostsystem"
	"github.com/fluxplane/fluxplane-system/mountfs"
	"github.com/fluxplane/fluxplane-system/systemkit"
)

// System groups the runtime boundaries that can touch the outside world.
type System interface {
	Workspace() Workspace
	Network() Network
	Process() ProcessManager
	Environment() Environment
}

// Config configures the host-backed system implementation.
type Config struct {
	Root                string
	Workspace           WorkspaceConfig
	AllowPrivateNetwork bool
}

// WorkspaceConfig configures additional workspace filesystem roots.
type WorkspaceConfig struct {
	Roots       []WorkspaceRootConfig
	ScratchRoot string
	EnvFiles    []string
}

// WorkspaceRootConfig configures one named workspace root.
type WorkspaceRootConfig struct {
	Name     string
	Path     string
	Access   WorkspaceAccess
	Create   bool
	EnvFiles []string
}

// WorkspaceAccess controls permitted operations for a workspace root.
type WorkspaceAccess string

const (
	WorkspaceAccessReadOnly  WorkspaceAccess = "read_only"
	WorkspaceAccessReadWrite WorkspaceAccess = "read_write"
)

// Host is the default host-guarded system implementation.
type Host struct {
	workspace *HostWorkspace
	network   *HostNetwork
	process   *HostProcess
	env       *WorkspaceEnvironment
}

// NewHost returns a host-backed system rooted at cfg.Root.
func NewHost(cfg Config) (*Host, error) {
	root := cfg.Root
	if strings.TrimSpace(root) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	workspace, err := newHostWorkspace(realRoot, cfg.Workspace)
	if err != nil {
		return nil, err
	}
	env, err := newWorkspaceEnvironment(workspace)
	if err != nil {
		return nil, err
	}
	return &Host{
		workspace: workspace,
		network:   &HostNetwork{allowPrivate: cfg.AllowPrivateNetwork},
		process:   NewHostProcessWithEnvironment(workspace, env),
		env:       env,
	}, nil
}

// Workspace returns the workspace boundary.
func (h *Host) Workspace() Workspace { return h.workspace }

// Network returns the network boundary.
func (h *Host) Network() Network { return h.network }

// Process returns the process boundary.
func (h *Host) Process() ProcessManager { return h.process }

// Environment returns the host environment boundary.
func (h *Host) Environment() Environment { return h.env }

// Environment is a read-only boundary for host environment variables.
type Environment = fpsystem.Environment

// ExecutableResolver optionally resolves executables using an environment's
// process PATH without exposing the PATH value itself.
type ExecutableResolver = fpsystem.ExecutableResolver

// HostEnvironment implements Environment using an explicitly allowed env set.
type HostEnvironment struct {
	values map[string]string
}

// Lookup returns an allowed environment variable value for key.
func (e HostEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e.values[strings.TrimSpace(key)]
	return value, ok, nil
}

// ResolveExecutable resolves an executable using the environment PATH.
func (e HostEnvironment) ResolveExecutable(ctx context.Context, name string) (string, bool, error) {
	pathValue, ok, err := e.Lookup(ctx, "PATH")
	if err != nil || !ok {
		return "", false, err
	}
	return resolveExecutableInPath(name, pathValue)
}

// Workspace is a root-confined filesystem boundary.
type Workspace = runtimeworkspace.Workspace

// ResolvedPath is a canonical workspace path.
type ResolvedPath = runtimeworkspace.ResolvedPath

// WorkspaceRoot describes one runtime filesystem root exposed by a Workspace.
type WorkspaceRoot = runtimeworkspace.Root

// WalkOptions bounds workspace tree traversal.
type WalkOptions struct {
	Depth         int
	ShowHidden    bool
	MaxEntries    int
	FilesOnly     bool
	SkipDirs      []string
	FilterPattern string // optional glob applied to each entry's workspace-relative path
}

// WalkEntry describes one workspace path discovered by Walk.
type WalkEntry struct {
	Path    ResolvedPath `json:"path"`
	Name    string       `json:"name"`
	Kind    string       `json:"kind"`
	Size    int64        `json:"size,omitempty"`
	Mode    string       `json:"mode,omitempty"`
	ModTime time.Time    `json:"mod_time,omitempty"`
	Level   int          `json:"level,omitempty"`
}

// GlobOptions bounds workspace glob matching.
type GlobOptions struct {
	Base       string
	MaxResults int
	MaxScanned int
	SkipDirs   []string
}

// ScratchDir is an isolated temporary directory owned by the runtime system.
type ScratchDir = runtimeworkspace.ScratchDir

// HostWorkspace implements Workspace using the local filesystem.
type HostWorkspace struct {
	root        string
	base        *hostsystem.FileSystem
	files       *mountfs.FileSystem
	system      fpsystem.System
	roots       []workspaceRoot
	scratchRoot string
}

type workspaceRoot struct {
	name     string
	root     string
	mount    string
	rel      string
	read     bool
	write    bool
	envFiles []string
}

// Root returns the canonical workspace root.
func (w *HostWorkspace) Root() string { return w.root }

// System returns the scoped system exposed by the workspace.
func (w *HostWorkspace) System() fpsystem.System {
	if w == nil {
		return nil
	}
	return w.system
}

// Roots returns the runtime filesystem roots exposed by the workspace. The
// first root is the primary root; additional roots are addressable by their Rel
// prefixes such as "@docs".
func (w *HostWorkspace) Roots() []WorkspaceRoot {
	if w == nil {
		return nil
	}
	out := make([]WorkspaceRoot, 0, len(w.roots))
	for i, root := range w.roots {
		name := root.name
		rel := root.rel
		if i == 0 && rel == "" {
			rel = "."
		}
		out = append(out, WorkspaceRoot{
			Name:    name,
			Path:    root.root,
			Rel:     rel,
			Read:    root.read,
			Write:   root.write,
			Scratch: name != "" && name == w.scratchRoot,
		})
	}
	return out
}

func newHostWorkspace(primary string, cfg WorkspaceConfig) (*HostWorkspace, error) {
	w := &HostWorkspace{
		root: primary,
		roots: []workspaceRoot{{
			root:     primary,
			read:     true,
			write:    true,
			envFiles: trimStrings(cfg.EnvFiles),
		}},
		scratchRoot: strings.TrimSpace(cfg.ScratchRoot),
	}
	seen := map[string]struct{}{}
	for i, rootCfg := range cfg.Roots {
		name := strings.TrimSpace(rootCfg.Name)
		if err := validateWorkspaceRootName(name); err != nil {
			return nil, fmt.Errorf("workspace roots[%d].name: %w", i, err)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("workspace roots[%d].name: duplicate root %q", i, name)
		}
		seen[name] = struct{}{}
		path := strings.TrimSpace(rootCfg.Path)
		if path == "" {
			return nil, fmt.Errorf("workspace roots[%d].path is empty", i)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("workspace roots[%d].path: %w", i, err)
		}
		if rootCfg.Create {
			if err := os.MkdirAll(abs, 0755); err != nil {
				return nil, fmt.Errorf("workspace roots[%d].path: %w", i, err)
			}
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("workspace roots[%d].path: %w", i, err)
		}
		read, write, err := workspaceAccess(rootCfg.Access)
		if err != nil {
			return nil, fmt.Errorf("workspace roots[%d].access: %w", i, err)
		}
		w.roots = append(w.roots, workspaceRoot{
			name:     name,
			root:     real,
			rel:      "@" + name,
			read:     read,
			write:    write,
			envFiles: trimStrings(rootCfg.EnvFiles),
		})
	}
	if w.scratchRoot != "" {
		if _, ok := seen[w.scratchRoot]; !ok {
			return nil, fmt.Errorf("workspace scratch_root %q is not configured", w.scratchRoot)
		}
	}
	baseRoot, err := workspaceBaseRoot(w.roots)
	if err != nil {
		return nil, err
	}
	w.base = hostsystem.NewFileSystem(baseRoot)
	mountRoots := make([]mountfs.Root, 0, len(w.roots))
	for i := range w.roots {
		root := &w.roots[i]
		mountPath, err := workspaceMountPath(baseRoot, root.root)
		if err != nil {
			return nil, err
		}
		root.mount = mountPath
		mountRoots = append(mountRoots, mountfs.Root{
			Name:    root.name,
			Path:    mountPath,
			Access:  workspaceMountAccess(root.write),
			Scratch: root.name != "" && root.name == w.scratchRoot,
		})
	}
	files, err := mountfs.New(w.base, mountfs.Spec{Roots: mountRoots})
	if err != nil {
		return nil, err
	}
	w.files = files
	scoped, err := systemkit.NewSystem().WithFileSystem(files).Build()
	if err != nil {
		return nil, err
	}
	w.system = scoped
	return w, nil
}

func validateWorkspaceRootName(name string) error {
	if name == "" {
		return fmt.Errorf("is empty")
	}
	if name == "." || name == ".." || strings.HasPrefix(name, "@") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid root name %q", name)
	}
	return nil
}

func workspaceAccess(access WorkspaceAccess) (bool, bool, error) {
	switch access {
	case "", WorkspaceAccessReadWrite:
		return true, true, nil
	case WorkspaceAccessReadOnly:
		return true, false, nil
	default:
		return false, false, fmt.Errorf("invalid access %q", access)
	}
}

func workspaceMountAccess(write bool) mountfs.Access {
	if write {
		return mountfs.ReadWrite
	}
	return mountfs.ReadOnly
}

func workspaceBaseRoot(roots []workspaceRoot) (string, error) {
	if len(roots) == 0 {
		return "", fmt.Errorf("workspace has no roots")
	}
	base := filepath.Clean(roots[0].root)
	for _, root := range roots[1:] {
		next, err := commonPathAncestor(base, filepath.Clean(root.root))
		if err != nil {
			return "", err
		}
		base = next
	}
	return base, nil
}

func commonPathAncestor(a, b string) (string, error) {
	volumeA := filepath.VolumeName(a)
	volumeB := filepath.VolumeName(b)
	if !strings.EqualFold(volumeA, volumeB) {
		return "", fmt.Errorf("workspace roots must share a filesystem volume")
	}
	rel, err := filepath.Rel(a, b)
	if err == nil && !pathEscapes(rel) {
		return a, nil
	}
	for {
		parent := filepath.Dir(a)
		if parent == a {
			if volumeA != "" {
				return volumeA + string(os.PathSeparator), nil
			}
			return parent, nil
		}
		a = parent
		rel, err = filepath.Rel(a, b)
		if err == nil && !pathEscapes(rel) {
			return a, nil
		}
	}
}

func workspaceMountPath(base, root string) (string, error) {
	rel, err := filepath.Rel(base, root)
	if err != nil {
		return "", err
	}
	if pathEscapes(rel) {
		return "", fmt.Errorf("workspace root %q escapes workspace base %q", root, base)
	}
	if rel == "." || rel == "" {
		return ".", nil
	}
	return filepath.ToSlash(rel), nil
}

func pathEscapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel)
}

// ResolveExisting resolves an existing path and rejects symlink escapes.
func (w *HostWorkspace) ResolveExisting(_ context.Context, raw string) (ResolvedPath, error) {
	root, candidate, err := w.candidate(raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if !root.read {
		return ResolvedPath{}, fmt.Errorf("workspace root is not readable")
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return ResolvedPath{}, err
	}
	return w.resolved(raw, root, real)
}

// ResolveProcessWorkdir resolves a process working directory. Host processes
// can write through normal OS permissions, so read-only workspace roots are not
// valid process workdirs.
func (w *HostWorkspace) ResolveProcessWorkdir(raw string) (ResolvedPath, error) {
	root, candidate, err := w.candidate(raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if !root.write {
		return ResolvedPath{}, fmt.Errorf("workspace root is not writable")
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return ResolvedPath{}, err
	}
	return w.resolved(raw, root, real)
}

// ResolveCreate resolves a path whose final component may not exist.
func (w *HostWorkspace) ResolveCreate(_ context.Context, raw string) (ResolvedPath, error) {
	root, candidate, err := w.candidate(raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if !root.write {
		return ResolvedPath{}, fmt.Errorf("workspace root is not writable")
	}
	if _, err := os.Lstat(candidate); err == nil {
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return ResolvedPath{}, err
		}
		return w.resolved(raw, root, real)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResolvedPath{}, err
	}

	missing := []string{filepath.Base(candidate)}
	parent := filepath.Dir(candidate)
	for {
		if _, err := os.Lstat(parent); err == nil {
			realParent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return ResolvedPath{}, err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				realParent = filepath.Join(realParent, missing[i])
			}
			return w.resolved(raw, root, realParent)
		} else if !errors.Is(err, os.ErrNotExist) {
			return ResolvedPath{}, err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return ResolvedPath{}, fmt.Errorf("path escapes workspace root")
		}
		missing = append(missing, filepath.Base(parent))
		parent = next
	}
}

// ReadFile reads a bounded file from the workspace.
func (w *HostWorkspace) ReadFile(ctx context.Context, raw string, maxBytes int64) ([]byte, bool, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	fsys, name := w.filesystemName(resolved)
	info, err := fsys.Stat(name)
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	if info.IsDir() {
		return nil, false, ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, fsys, name, maxBytes)
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	return data, truncated, resolved, nil
}

// ReadFileLines reads a bounded 1-indexed line window from a workspace file.
func (w *HostWorkspace) ReadFileLines(ctx context.Context, raw string, start, end int, maxBytes int64) ([]byte, int, bool, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, 0, false, ResolvedPath{}, err
	}
	fsys, name := w.filesystemName(resolved)
	info, err := fsys.Stat(name)
	if err != nil {
		return nil, 0, false, ResolvedPath{}, err
	}
	if info.IsDir() {
		return nil, 0, false, ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	data, firstLine, truncated, err := fpsystem.ReadFileLines(ctx, fsys, name, start, end, maxBytes)
	return data, firstLine, truncated, resolved, err
}

// WriteFile writes a file, optionally refusing to overwrite existing paths.
func (w *HostWorkspace) WriteFile(ctx context.Context, raw string, data []byte, mode os.FileMode, overwrite bool) (ResolvedPath, error) {
	resolved, err := w.ResolveCreate(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	return resolved, w.files.WriteFile(ctx, workspaceName(resolved), data, fpsystem.WriteFileOptions{Perm: mode, Overwrite: overwrite})
}

// CopyFile copies one complete file within the workspace.
func (w *HostWorkspace) CopyFile(ctx context.Context, rawSrc, rawDst string, overwrite bool) (ResolvedPath, ResolvedPath, int64, error) {
	src, err := w.ResolveExisting(ctx, rawSrc)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	info, err := w.files.Stat(workspaceName(src))
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	if info.IsDir() {
		return ResolvedPath{}, ResolvedPath{}, 0, fmt.Errorf("source path is a directory")
	}
	dst, err := w.ResolveCreate(ctx, rawDst)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	if src.Abs == dst.Abs {
		return src, dst, info.Size(), nil
	}
	if !overwrite {
		if _, err := os.Lstat(dst.Abs); err == nil {
			return ResolvedPath{}, ResolvedPath{}, 0, fmt.Errorf("path already exists")
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst.Abs), 0755); err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	in, err := os.Open(src.Abs)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	defer func() { _ = in.Close() }()
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !overwrite {
		flags |= os.O_EXCL
	}
	out, err := os.OpenFile(dst.Abs, flags, info.Mode().Perm())
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	written, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return ResolvedPath{}, ResolvedPath{}, written, copyErr
	}
	if closeErr != nil {
		return ResolvedPath{}, ResolvedPath{}, written, closeErr
	}
	return src, dst, written, nil
}

// MoveFile moves one complete file within the workspace.
func (w *HostWorkspace) MoveFile(ctx context.Context, rawSrc, rawDst string, overwrite bool) (ResolvedPath, ResolvedPath, int64, error) {
	srcRoot, _, err := w.candidate(rawSrc)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	if !srcRoot.write {
		return ResolvedPath{}, ResolvedPath{}, 0, fmt.Errorf("workspace root is not writable")
	}
	src, dst, written, err := w.CopyFile(ctx, rawSrc, rawDst, overwrite)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	if src.Abs == dst.Abs {
		return src, dst, written, nil
	}
	if err := os.Remove(src.Abs); err != nil {
		return ResolvedPath{}, ResolvedPath{}, written, err
	}
	return src, dst, written, nil
}

// MkdirAll creates a directory and parents.
func (w *HostWorkspace) MkdirAll(ctx context.Context, raw string, mode os.FileMode) (ResolvedPath, error) {
	resolved, err := w.ResolveCreate(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	return resolved, w.files.MkdirAll(ctx, workspaceName(resolved), fpsystem.MkdirOptions{Perm: mode})
}

// Remove removes a file or empty directory.
func (w *HostWorkspace) Remove(ctx context.Context, raw string) (ResolvedPath, error) {
	root, candidate, err := w.candidate(raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if !root.write {
		return ResolvedPath{}, fmt.Errorf("workspace root is not writable")
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return ResolvedPath{}, err
	}
	resolved, err := w.resolved(raw, root, real)
	if err != nil {
		return ResolvedPath{}, err
	}
	return resolved, w.files.Remove(ctx, workspaceName(resolved))
}

// Stat stats a workspace path.
func (w *HostWorkspace) Stat(ctx context.Context, raw string) (fs.FileInfo, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, err
	}
	fsys, name := w.filesystemName(resolved)
	info, err := fsys.Stat(name)
	return info, resolved, err
}

// ReadDir lists a workspace directory.
func (w *HostWorkspace) ReadDir(ctx context.Context, raw string) ([]fs.DirEntry, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, err
	}
	fsys, name := w.filesystemName(resolved)
	entries, err := fsys.ReadDir(name)
	return entries, resolved, err
}

// Walk returns a bounded tree traversal rooted at raw.
func (w *HostWorkspace) Walk(ctx context.Context, raw string, opts WalkOptions) ([]WalkEntry, ResolvedPath, bool, error) {
	root, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, false, err
	}
	fsys, rootName := w.filesystemName(root)
	systemEntries, truncated, err := fpsystem.Walk(ctx, fsys, rootName, fpsystem.WalkOptions{
		Depth:         opts.Depth,
		ShowHidden:    opts.ShowHidden,
		MaxEntries:    opts.MaxEntries,
		FilesOnly:     opts.FilesOnly,
		SkipDirs:      opts.SkipDirs,
		FilterPattern: opts.FilterPattern,
	})
	if err != nil {
		return nil, ResolvedPath{}, false, err
	}
	entries := make([]WalkEntry, 0, len(systemEntries))
	for _, entry := range systemEntries {
		name := entry.Path
		if fsys == w.base {
			if rel, err := filepath.Rel(rootName, entry.Path); err == nil {
				name = rel
			}
		}
		path, err := w.ResolveExisting(ctx, name)
		if err != nil {
			continue
		}
		entries = append(entries, WalkEntry{
			Path:    path,
			Name:    entry.Name,
			Kind:    entry.Kind,
			Size:    entry.Size,
			Mode:    entry.Mode,
			ModTime: entry.ModTime,
			Level:   entry.Level,
		})
	}
	return entries, root, truncated, nil
}

// Glob returns workspace paths matching a slash-style glob under opts.Base.
func (w *HostWorkspace) Glob(ctx context.Context, pattern string, opts GlobOptions) ([]ResolvedPath, bool, error) {
	base := opts.Base
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	basePath, err := w.ResolveExisting(ctx, base)
	if err != nil {
		return nil, false, err
	}
	fsys, baseName := w.filesystemName(basePath)
	names, truncated, err := fpsystem.Glob(ctx, fsys, pattern, fpsystem.GlobOptions{
		Base:       baseName,
		MaxResults: opts.MaxResults,
		MaxScanned: opts.MaxScanned,
		SkipDirs:   opts.SkipDirs,
	})
	if err != nil {
		return nil, false, err
	}
	matches := make([]ResolvedPath, 0, len(names))
	for _, name := range names {
		if fsys == w.base {
			if rel, err := filepath.Rel(baseName, name); err == nil {
				name = rel
			}
		}
		resolved, err := w.ResolveExisting(ctx, name)
		if err != nil {
			continue
		}
		matches = append(matches, resolved)
	}
	return matches, truncated, nil
}

// CreateScratch creates an isolated temporary directory for runtime-owned work.
func (w *HostWorkspace) CreateScratch(_ context.Context, prefix string) (ScratchDir, error) {
	if strings.TrimSpace(prefix) == "" {
		prefix = "fluxplane-*"
	}
	base := ""
	var root workspaceRoot
	if w.scratchRoot != "" {
		var ok bool
		root, ok = w.rootByName(w.scratchRoot)
		if !ok {
			return nil, fmt.Errorf("workspace scratch_root %q is not configured", w.scratchRoot)
		}
		if !root.write {
			return nil, fmt.Errorf("workspace scratch_root %q is not writable", w.scratchRoot)
		}
		base = root.root
	}
	dir, err := os.MkdirTemp(base, prefix)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	rel := ""
	if w.scratchRoot != "" {
		resolved, err := w.resolved(dir, root, real)
		if err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
		rel = resolved.Rel
	}
	return &hostScratchDir{root: real, rel: rel}, nil
}

type hostScratchDir struct {
	root string
	rel  string
}

func (s *hostScratchDir) Root() string { return s.root }

func (s *hostScratchDir) WriteFile(_ context.Context, raw string, data []byte, mode os.FileMode) (ResolvedPath, error) {
	resolved, err := s.resolveCreate(raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if err := os.MkdirAll(filepath.Dir(resolved.Abs), 0755); err != nil {
		return ResolvedPath{}, err
	}
	return resolved, os.WriteFile(resolved.Abs, data, mode)
}

func (s *hostScratchDir) RemoveAll(context.Context) error {
	return os.RemoveAll(s.root)
}

func (s *hostScratchDir) resolveCreate(raw string) (ResolvedPath, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ResolvedPath{}, fmt.Errorf("scratch path is empty")
	}
	clean := filepath.Clean(raw)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return ResolvedPath{}, fmt.Errorf("scratch path escapes root")
	}
	abs := filepath.Join(s.root, clean)
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return ResolvedPath{}, err
	}
	real := filepath.Join(parent, filepath.Base(abs))
	if err := pathWithin(s.root, real); err != nil {
		return ResolvedPath{}, err
	}
	rel, err := filepath.Rel(s.root, real)
	if err != nil {
		return ResolvedPath{}, err
	}
	rel = filepath.ToSlash(rel)
	if s.rel != "" {
		rel = filepath.ToSlash(filepath.Join(s.rel, rel))
	}
	return ResolvedPath{Input: raw, Abs: real, Rel: rel}, nil
}

func (w *HostWorkspace) candidate(raw string) (workspaceRoot, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "."
	}
	if filepath.IsAbs(raw) {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return workspaceRoot{}, "", err
		}
		root, ok := w.rootForAbs(abs)
		if !ok {
			return workspaceRoot{}, "", fmt.Errorf("path escapes workspace root")
		}
		return root, abs, nil
	}
	if raw == "." {
		root := w.roots[0]
		return root, root.root, nil
	}

	if w.files == nil || w.base == nil {
		return workspaceRoot{}, "", fmt.Errorf("workspace filesystem is not initialized")
	}
	name, err := workspaceMountName(raw)
	if err != nil {
		return workspaceRoot{}, "", err
	}
	mounted, err := w.files.Resolve(name)
	if err != nil {
		return workspaceRoot{}, "", err
	}
	root, ok := w.rootByName(mounted.Root.Name)
	if !ok {
		return workspaceRoot{}, "", fmt.Errorf("unknown workspace root %q", mounted.Root.Name)
	}
	abs := filepath.Join(w.base.Root(), filepath.FromSlash(mounted.Path))
	return root, abs, nil
}

func workspaceMountName(raw string) (string, error) {
	if strings.HasPrefix(raw, "@") {
		name, rest := splitWorkspaceRootPath(raw)
		clean := filepath.Clean(filepath.FromSlash(rest))
		if clean == "." {
			return "@" + name, nil
		}
		if pathEscapes(clean) || filepath.IsAbs(clean) {
			return "", fmt.Errorf("path escapes workspace root")
		}
		return "@" + name + "/" + filepath.ToSlash(clean), nil
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	if pathEscapes(clean) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("path escapes workspace root")
	}
	return filepath.ToSlash(clean), nil
}

func splitWorkspaceRootPath(raw string) (string, string) {
	trimmed := strings.TrimPrefix(filepath.ToSlash(raw), "@")
	name, rest, ok := strings.Cut(trimmed, "/")
	if !ok {
		return name, ""
	}
	return name, rest
}

func (w *HostWorkspace) rootByName(name string) (workspaceRoot, bool) {
	for _, root := range w.roots {
		if root.name == name {
			return root, true
		}
	}
	return workspaceRoot{}, false
}

func (w *HostWorkspace) rootForAbs(abs string) (workspaceRoot, bool) {
	var best workspaceRoot
	bestLen := -1
	for _, root := range w.roots {
		if pathWithin(root.root, abs) == nil && len(root.root) > bestLen {
			best = root
			bestLen = len(root.root)
		}
	}
	if bestLen < 0 {
		return workspaceRoot{}, false
	}
	return best, true
}

func (w *HostWorkspace) resolved(input string, root workspaceRoot, abs string) (ResolvedPath, error) {
	abs, err := filepath.Abs(abs)
	if err != nil {
		return ResolvedPath{}, err
	}
	if err := pathWithin(root.root, abs); err != nil {
		return ResolvedPath{}, err
	}
	rel, err := filepath.Rel(root.root, abs)
	if err != nil {
		return ResolvedPath{}, err
	}
	if rel == "." {
		rel = ""
	}
	rel = filepath.ToSlash(rel)
	if root.rel != "" {
		if rel == "" {
			rel = root.rel
		} else {
			rel = filepath.ToSlash(filepath.Join(root.rel, rel))
		}
	}
	return ResolvedPath{Input: input, Abs: abs, Rel: rel}, nil
}

func workspaceName(resolved ResolvedPath) string {
	return runtimeworkspace.PathName(resolved)
}

// WorkspacePathName returns the scoped filesystem name for resolved.
func WorkspacePathName(resolved ResolvedPath) string {
	return runtimeworkspace.PathName(resolved)
}

// WorkspaceFileSystem returns the scoped filesystem exposed by ws.
func WorkspaceFileSystem(ws Workspace) (fpsystem.FileSystem, error) {
	return runtimeworkspace.FileSystem(ws)
}

func (w *HostWorkspace) filesystemName(resolved ResolvedPath) (fpsystem.FileSystem, string) {
	if w != nil && w.base != nil && len(w.roots) > 0 &&
		strings.TrimSpace(resolved.Rel) == "" &&
		filepath.Clean(resolved.Abs) == filepath.Clean(w.roots[0].root) {
		return w.base, w.roots[0].mount
	}
	return w.files, workspaceName(resolved)
}

func pathWithin(root, candidate string) error {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if rel == "." || rel == "" {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes workspace root")
	}
	return nil
}

// Network provides primitive network access.
type Network = fpsystem.Network

// HostNetwork implements primitive network access with target guards.
type HostNetwork struct {
	allowPrivate bool
}

func (n *HostNetwork) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip, err := resolvePublicIP(ctx, host, n.allowPrivate)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
}

func (n *HostNetwork) Resolver() fpsystem.Resolver {
	return hostResolver{}
}

type hostResolver struct{}

func (hostResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

func (hostResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func (hostResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return net.DefaultResolver.LookupCNAME(ctx, host)
}

func (hostResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, name)
}

func (hostResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return net.DefaultResolver.LookupSRV(ctx, service, proto, name)
}

func (hostResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

// ValidatePublicURL rejects non-HTTP and private/local targets.
func ValidatePublicURL(parsed *url.URL, allowPrivate bool) error {
	if parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("url must be absolute http or https")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("url host is empty")
	}
	if ip := net.ParseIP(host); ip != nil && !allowPrivate && blockedIP(ip) {
		return fmt.Errorf("private, local, multicast, and metadata network targets are blocked")
	}
	return nil
}

// PublicNetworkTransport returns a guarded HTTP transport.
func PublicNetworkTransport(allowPrivate bool) http.RoundTripper {
	return PublicNetworkTransportWithTLS(allowPrivate, nil)
}

// PublicNetworkTransportWithTLS returns a guarded HTTP transport with optional
// caller-provided TLS settings.
func PublicNetworkTransportWithTLS(allowPrivate bool, cfg *tls.Config) http.RoundTripper {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ip, err := resolvePublicIP(ctx, host, allowPrivate)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
		TLSClientConfig: secureTLSConfig(cfg),
	}
}

func secureTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	out := cfg.Clone()
	if out.MinVersion == 0 || out.MinVersion < tls.VersionTLS12 {
		out.MinVersion = tls.VersionTLS12
	}
	return out
}

func resolvePublicIP(ctx context.Context, host string, allowPrivate bool) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !allowPrivate && blockedIP(ip) {
			return nil, fmt.Errorf("private, local, multicast, and metadata network targets are blocked")
		}
		return ip, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		if allowPrivate || !blockedIP(addr.IP) {
			return addr.IP, nil
		}
	}
	return nil, fmt.Errorf("host resolves only to private, local, multicast, or metadata addresses")
}

func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.Equal(net.ParseIP("169.254.169.254"))
}

// ProcessRunner runs a process through a system boundary.
type ProcessRunner interface {
	Run(context.Context, ProcessRequest) (ProcessResult, error)
}

// ProcessManager is the long-running process boundary for shells, dev servers,
// tests, and other streaming/background workloads.
type ProcessManager interface {
	ProcessRunner
	Start(context.Context, ProcessRequest) (ProcessHandle, error)
	Ensure(context.Context, ProcessRequest) (ProcessHandle, bool, error)
	List(context.Context) ([]ProcessInfo, error)
	Status(context.Context, string) (ProcessInfo, error)
	Output(context.Context, string) (ProcessOutput, error)
	Wait(context.Context, string, time.Duration) (ProcessResult, error)
	Stop(context.Context, string) error
	Kill(context.Context, string) error
}

// ProcessHandle identifies a running or completed managed process.
type ProcessHandle = fpsystem.ProcessHandle

// ProcessInfo describes a managed process.
type ProcessInfo = fpsystem.ProcessInfo

// ProcessEvent is emitted for streaming process output and lifecycle changes.
type ProcessEvent = fpsystem.ProcessEvent

const (
	ProcessEventStarted = fpsystem.ProcessEventStarted
	ProcessEventOutput  = fpsystem.ProcessEventOutput
	ProcessEventExited  = fpsystem.ProcessEventExited

	EventProcessStarted = fpsystem.EventProcessStarted
	EventProcessOutput  = fpsystem.EventProcessOutput
	EventProcessExited  = fpsystem.EventProcessExited
)

// ProcessRequest describes one bounded process execution.
type ProcessRequest = fpsystem.ProcessRequest

// ProcessResult is the captured process outcome.
type ProcessResult = fpsystem.ProcessResult

// ProcessOutput is a bounded output snapshot for a managed process.
type ProcessOutput = fpsystem.ProcessOutput

// HostProcess executes direct host processes without a shell interpreter.
type HostProcess struct {
	workspace *HostWorkspace
	env       *WorkspaceEnvironment
	process   *hostsystem.Process
}

// NewHostProcess returns a host process manager.
func NewHostProcess(workspace *HostWorkspace) *HostProcess {
	env, _ := newWorkspaceEnvironment(workspace)
	return NewHostProcessWithEnvironment(workspace, env)
}

// NewHostProcessWithEnvironment returns a host process manager using env.
func NewHostProcessWithEnvironment(workspace *HostWorkspace, env *WorkspaceEnvironment) *HostProcess {
	if env == nil {
		env, _ = newWorkspaceEnvironment(workspace)
	}
	return &HostProcess{
		workspace: workspace,
		env:       env,
		process:   hostsystem.NewProcess(workspace.Root(), workspaceProcessEnvironment{}, nil),
	}
}

// Run executes one direct process and waits for completion.
func (p *HostProcess) Run(ctx context.Context, req ProcessRequest) (ProcessResult, error) {
	preparedCtx, prepared, err := p.prepare(ctx, req)
	if err != nil {
		return ProcessResult{}, err
	}
	return p.process.Run(preparedCtx, prepared)
}

// Start launches one direct process under management.
func (p *HostProcess) Start(ctx context.Context, req ProcessRequest) (ProcessHandle, error) {
	preparedCtx, prepared, err := p.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	return p.process.Start(preparedCtx, prepared)
}

// Ensure returns a running process for req.Label or starts a new one.
func (p *HostProcess) Ensure(ctx context.Context, req ProcessRequest) (ProcessHandle, bool, error) {
	preparedCtx, prepared, err := p.prepare(ctx, req)
	if err != nil {
		return nil, false, err
	}
	return p.process.Ensure(preparedCtx, prepared)
}

// List returns known managed processes.
func (p *HostProcess) List(context.Context) ([]ProcessInfo, error) {
	return p.process.List(context.Background())
}

// Status returns one managed process info.
func (p *HostProcess) Status(ctx context.Context, id string) (ProcessInfo, error) {
	return p.process.Status(ctx, id)
}

// Output returns a bounded output snapshot for one managed process.
func (p *HostProcess) Output(ctx context.Context, id string) (ProcessOutput, error) {
	return p.process.Output(ctx, id)
}

// Wait waits for one managed process to exit.
func (p *HostProcess) Wait(ctx context.Context, id string, timeout time.Duration) (ProcessResult, error) {
	return p.process.Wait(ctx, id, timeout)
}

// Stop gracefully terminates a managed process.
func (p *HostProcess) Stop(ctx context.Context, id string) error {
	return p.process.Stop(ctx, id)
}

// Kill terminates a managed process.
func (p *HostProcess) Kill(ctx context.Context, id string) error {
	return p.process.Kill(ctx, id)
}

func (p *HostProcess) prepare(ctx context.Context, req ProcessRequest) (context.Context, ProcessRequest, error) {
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
