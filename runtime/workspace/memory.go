// Memory-backed runtime system implementations for tests and lightweight fixtures.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/memsystem"
	"github.com/fluxplane/fluxplane-system/systemkit"
	fpsystemtest "github.com/fluxplane/fluxplane-system/systemtest"
)

// MemorySystem is a mutable in-memory system for Workspace-focused tests.
type MemorySystem struct {
	WorkspaceValue *MemoryWorkspace
}

// NewMemory returns a memory-backed test system.
func NewMemory() *MemorySystem {
	return &MemorySystem{WorkspaceValue: NewMemoryWorkspace()}
}

func (s *MemorySystem) Workspace() Workspace              { return s.WorkspaceValue }
func (s *MemorySystem) Network() fpsystem.Network         { return network{} }
func (s *MemorySystem) Process() fpsystem.ProcessManager  { return nil }
func (s *MemorySystem) Environment() fpsystem.Environment { return environment{} }
func (s *MemorySystem) FileSystem() fpsystem.FileSystem {
	return s.WorkspaceValue.System().FileSystem()
}
func (s *MemorySystem) Clock() fpsystem.Clock { return s.WorkspaceValue.System().Clock() }

type environment struct{}
type network struct {
	fpsystemtest.UnsupportedNetwork
}

func (environment) Lookup(context.Context, string) (string, bool, error) { return "", false, nil }

// MemoryWorkspace is a root-confined mutable Workspace for tests.
type MemoryWorkspace struct {
	root string
	fsys *memsystem.FileSystem
}

// NewMemoryWorkspace returns an empty memory workspace.
func NewMemoryWorkspace() *MemoryWorkspace {
	return &MemoryWorkspace{root: "/memory-workspace", fsys: memsystem.NewFileSystem()}
}

func (w *MemoryWorkspace) Root() string { return w.root }

func (w *MemoryWorkspace) System() fpsystem.System {
	sys, _ := systemkit.NewSystem().WithFileSystem(w.fsys).Build()
	return sys
}

// Roots returns the single in-memory workspace root.
func (w *MemoryWorkspace) Roots() []Root {
	if w == nil {
		return nil
	}
	return []Root{{Path: w.root, Rel: ".", Read: true, Write: true}}
}

func (w *MemoryWorkspace) ResolveExisting(_ context.Context, raw string) (ResolvedPath, error) {
	rel, err := w.clean(raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if _, err := w.fsys.Stat(relName(rel)); err != nil {
		return ResolvedPath{}, err
	}
	return w.resolved(raw, rel), nil
}

func (w *MemoryWorkspace) ResolveCreate(_ context.Context, raw string) (ResolvedPath, error) {
	rel, err := w.clean(raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	return w.resolved(raw, rel), nil
}

func (w *MemoryWorkspace) ReadFile(ctx context.Context, raw string, maxBytes int64) ([]byte, bool, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	info, err := w.fsys.Stat(relName(resolved.Rel))
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	if info.IsDir() {
		return nil, false, ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, w.fsys, relName(resolved.Rel), maxBytes)
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	return data, truncated, resolved, nil
}

func (w *MemoryWorkspace) ReadFileLines(ctx context.Context, raw string, start, end int, maxBytes int64) ([]byte, int, bool, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, 0, false, ResolvedPath{}, err
	}
	info, err := w.fsys.Stat(relName(resolved.Rel))
	if err != nil {
		return nil, 0, false, ResolvedPath{}, err
	}
	if info.IsDir() {
		return nil, 0, false, ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	data, firstLine, truncated, err := fpsystem.ReadFileLines(ctx, w.fsys, relName(resolved.Rel), start, end, maxBytes)
	return data, firstLine, truncated, resolved, err
}

func (w *MemoryWorkspace) WriteFile(ctx context.Context, raw string, data []byte, mode os.FileMode, overwrite bool) (ResolvedPath, error) {
	resolved, err := w.ResolveCreate(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if resolved.Rel == "" {
		return ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	return resolved, w.fsys.WriteFile(ctx, relName(resolved.Rel), data, fpsystem.WriteFileOptions{Perm: mode, Overwrite: overwrite})
}

func (w *MemoryWorkspace) CopyFile(ctx context.Context, src, dst string, overwrite bool) (ResolvedPath, ResolvedPath, int64, error) {
	data, _, srcResolved, err := w.ReadFile(ctx, src, 0)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	dstResolved, err := w.WriteFile(ctx, dst, data, 0644, overwrite)
	return srcResolved, dstResolved, int64(len(data)), err
}

func (w *MemoryWorkspace) MoveFile(ctx context.Context, src, dst string, overwrite bool) (ResolvedPath, ResolvedPath, int64, error) {
	srcResolved, dstResolved, n, err := w.CopyFile(ctx, src, dst, overwrite)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	if srcResolved.Rel != dstResolved.Rel {
		_ = w.fsys.Remove(ctx, relName(srcResolved.Rel))
	}
	return srcResolved, dstResolved, n, nil
}

func (w *MemoryWorkspace) MkdirAll(ctx context.Context, raw string, mode os.FileMode) (ResolvedPath, error) {
	resolved, err := w.ResolveCreate(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	return resolved, w.fsys.MkdirAll(ctx, relName(resolved.Rel), fpsystem.MkdirOptions{Perm: mode})
}

func (w *MemoryWorkspace) Remove(ctx context.Context, raw string) (ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if resolved.Rel == "" {
		return ResolvedPath{}, fmt.Errorf("cannot remove workspace root")
	}
	return resolved, w.fsys.Remove(ctx, relName(resolved.Rel))
}

func (w *MemoryWorkspace) Stat(ctx context.Context, raw string) (fs.FileInfo, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, err
	}
	info, err := w.fsys.Stat(relName(resolved.Rel))
	return info, resolved, err
}

func (w *MemoryWorkspace) ReadDir(ctx context.Context, raw string) ([]fs.DirEntry, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, err
	}
	entries, err := w.fsys.ReadDir(relName(resolved.Rel))
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, resolved, err
}

func (w *MemoryWorkspace) Walk(ctx context.Context, raw string, opts WalkOptions) ([]WalkEntry, ResolvedPath, bool, error) {
	root, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, false, err
	}
	systemEntries, truncated, err := fpsystem.Walk(ctx, w.fsys, relName(root.Rel), fpsystem.WalkOptions{
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
		path := w.resolved(entry.Path, normalizeRel(entry.Path))
		entries = append(entries, WalkEntry{Path: path, Name: entry.Name, Kind: entry.Kind, Size: entry.Size, Mode: entry.Mode, ModTime: entry.ModTime, Level: entry.Level})
	}
	return entries, root, truncated, nil
}

func (w *MemoryWorkspace) Glob(ctx context.Context, pattern string, opts GlobOptions) ([]ResolvedPath, bool, error) {
	base := strings.TrimSpace(opts.Base)
	if base == "" {
		base = "."
	}
	basePath, err := w.ResolveExisting(ctx, base)
	if err != nil {
		return nil, false, err
	}
	names, truncated, err := fpsystem.Glob(ctx, w.fsys, pattern, fpsystem.GlobOptions{Base: relName(basePath.Rel), MaxResults: opts.MaxResults, MaxScanned: opts.MaxScanned, SkipDirs: opts.SkipDirs})
	if err != nil {
		return nil, false, err
	}
	matches := make([]ResolvedPath, 0, len(names))
	for _, name := range names {
		matches = append(matches, w.resolved(name, normalizeRel(name)))
	}
	return matches, truncated, nil
}

func (w *MemoryWorkspace) CreateScratch(context.Context, string) (ScratchDir, error) {
	return nil, errors.ErrUnsupported
}

func (w *MemoryWorkspace) clean(raw string) (string, error) {
	raw = strings.TrimSpace(filepath.ToSlash(raw))
	if raw == "" || raw == "." {
		return "", nil
	}
	root := filepath.ToSlash(w.root)
	if raw == root {
		return "", nil
	}
	if strings.HasPrefix(raw, root+"/") {
		raw = strings.TrimPrefix(raw, root+"/")
	}
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("path escapes workspace root")
	}
	clean := normalizeRel(raw)
	if clean == "" {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes workspace root")
	}
	return clean, nil
}

func (w *MemoryWorkspace) resolved(input, rel string) ResolvedPath {
	abs := w.root
	if rel != "" {
		abs = filepath.Join(w.root, filepath.FromSlash(rel))
	}
	return ResolvedPath{Input: input, Abs: abs, Rel: rel}
}

func relName(rel string) string {
	if strings.TrimSpace(rel) == "" {
		return "."
	}
	return rel
}

func normalizeRel(raw string) string {
	raw = strings.TrimSpace(filepath.ToSlash(raw))
	if raw == "" || raw == "." {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if clean == "." {
		return ""
	}
	return clean
}

var _ fpsystem.System = (*MemorySystem)(nil)
var _ Workspace = (*MemoryWorkspace)(nil)
