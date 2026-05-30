package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/hostsystem"
	"github.com/fluxplane/fluxplane-system/mountfs"
	"github.com/fluxplane/fluxplane-system/systemkit"
)

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
func (w *HostWorkspace) Roots() []Root {
	if w == nil {
		return nil
	}
	out := make([]Root, 0, len(w.roots))
	for i, root := range w.roots {
		name := root.name
		rel := root.rel
		if i == 0 && rel == "" {
			rel = "."
		}
		out = append(out, Root{
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

func NewHostWorkspace(primary string, cfg WorkspaceConfig) (*HostWorkspace, error) {
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
	return resolved, w.files.WriteFile(ctx, hostWorkspaceName(resolved), data, fpsystem.WriteFileOptions{Perm: mode, Overwrite: overwrite})
}

// CopyFile copies one complete file within the workspace.
func (w *HostWorkspace) CopyFile(ctx context.Context, rawSrc, rawDst string, overwrite bool) (ResolvedPath, ResolvedPath, int64, error) {
	src, err := w.ResolveExisting(ctx, rawSrc)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	info, err := w.files.Stat(hostWorkspaceName(src))
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
	return resolved, w.files.MkdirAll(ctx, hostWorkspaceName(resolved), fpsystem.MkdirOptions{Perm: mode})
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
	return resolved, w.files.Remove(ctx, hostWorkspaceName(resolved))
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

func hostWorkspaceName(resolved ResolvedPath) string {
	return PathName(resolved)
}

func (w *HostWorkspace) filesystemName(resolved ResolvedPath) (fpsystem.FileSystem, string) {
	if w != nil && w.base != nil && len(w.roots) > 0 &&
		strings.TrimSpace(resolved.Rel) == "" &&
		filepath.Clean(resolved.Abs) == filepath.Clean(w.roots[0].root) {
		return w.base, w.roots[0].mount
	}
	return w.files, hostWorkspaceName(resolved)
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
