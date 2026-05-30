package workspace

import (
	"context"
	"fmt"
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
		next, err := fpsystem.CommonPathAncestor(base, filepath.Clean(root.root))
		if err != nil {
			return "", fmt.Errorf("workspace roots must share a filesystem volume")
		}
		base = next
	}
	return base, nil
}

func workspaceMountPath(base, root string) (string, error) {
	rel, err := filepath.Rel(base, root)
	if err != nil {
		return "", err
	}
	if fpsystem.RelPathEscapes(rel) {
		return "", fmt.Errorf("workspace root %q escapes workspace base %q", root, base)
	}
	if rel == "." || rel == "" {
		return ".", nil
	}
	return filepath.ToSlash(rel), nil
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
	real, err := fpsystem.ResolveExistingUnderRoot(root.root, candidate)
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
	real, err := fpsystem.ResolveExistingUnderRoot(root.root, candidate)
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
	real, err := fpsystem.ResolveCreateUnderRoot(root.root, candidate)
	if err != nil {
		return ResolvedPath{}, fmt.Errorf("path escapes workspace root")
	}
	return w.resolved(raw, root, real)
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
	written, err := fpsystem.CopyRegularFile(src.Abs, dst.Abs, overwrite)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, written, err
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
	scratch, err := fpsystem.NewHostScratchDir(base, prefix)
	if err != nil {
		return nil, err
	}
	rel := ""
	if w.scratchRoot != "" {
		resolved, err := w.resolved(scratch.Root(), root, scratch.Root())
		if err != nil {
			_ = scratch.RemoveAll(context.Background())
			return nil, err
		}
		rel = resolved.Rel
	}
	return &hostScratchDir{scratch: scratch, rel: rel}, nil
}

type hostScratchDir struct {
	scratch *fpsystem.HostScratchDir
	rel     string
}

func (s *hostScratchDir) Root() string { return s.scratch.Root() }

func (s *hostScratchDir) WriteFile(ctx context.Context, raw string, data []byte, mode os.FileMode) (ResolvedPath, error) {
	path, err := s.scratch.WriteFile(ctx, raw, data, mode)
	if err != nil {
		return ResolvedPath{}, err
	}
	rel := path.Rel
	if s.rel != "" {
		rel = filepath.ToSlash(filepath.Join(s.rel, rel))
	}
	return ResolvedPath{Input: path.Input, Abs: path.Abs, Rel: rel}, nil
}

func (s *hostScratchDir) RemoveAll(ctx context.Context) error {
	return s.scratch.RemoveAll(ctx)
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
		if fpsystem.RelPathEscapes(clean) || filepath.IsAbs(clean) {
			return "", fmt.Errorf("path escapes workspace root")
		}
		return "@" + name + "/" + filepath.ToSlash(clean), nil
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	if fpsystem.RelPathEscapes(clean) || filepath.IsAbs(clean) {
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
	if err := fpsystem.PathWithin(root, candidate); err != nil {
		return fmt.Errorf("path escapes workspace root")
	}
	return nil
}
