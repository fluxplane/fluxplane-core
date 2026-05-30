package filesystem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/pathpattern"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	fpsystem "github.com/fluxplane/fluxplane-system"
	fpsystemtest "github.com/fluxplane/fluxplane-system/systemtest"
)

type filesystemTestEnv struct {
	name      string
	root      string
	sys       system.System
	workspace system.Workspace
	host      bool
}

func runFilesystemBackends(t *testing.T, fn func(*testing.T, *filesystemTestEnv)) {
	t.Helper()
	t.Run("host", func(t *testing.T) {
		root := t.TempDir()
		sys, err := system.NewHost(system.Config{Root: root})
		if err != nil {
			t.Fatalf("NewHost: %v", err)
		}
		fn(t, &filesystemTestEnv{name: "host", root: root, sys: sys, workspace: sys.Workspace(), host: true})
	})
}

func newHostFilesystemTestEnv(t *testing.T, root string) *filesystemTestEnv {
	t.Helper()
	sys, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return &filesystemTestEnv{name: "host", root: root, sys: sys, workspace: sys.Workspace(), host: true}
}

func (e *filesystemTestEnv) Operation(t *testing.T, name string) operation.Operation {
	t.Helper()
	ops, err := New(e.sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			return op
		}
	}
	t.Fatalf("%s operation not found", name)
	return nil
}

func (e *filesystemTestEnv) WriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if _, err := writeWorkspaceFile(context.Background(), e.workspace, path, data, 0644, true); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func (e *filesystemTestEnv) ReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, truncated, _, err := readWorkspaceFile(context.Background(), e.workspace, path, 0)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if truncated {
		t.Fatalf("ReadFile(%s): unexpectedly truncated", path)
	}
	return data
}

func (e *filesystemTestEnv) Mkdir(t *testing.T, path string) {
	t.Helper()
	if _, err := mkdirWorkspace(context.Background(), e.workspace, path, 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}

func (e *filesystemTestEnv) MustNotExist(t *testing.T, path string) {
	t.Helper()
	_, _, err := statWorkspacePath(context.Background(), e.workspace, path)
	if err == nil {
		t.Fatalf("Stat(%s): path exists, want not exist", path)
	}
}

func (e *filesystemTestEnv) MustExist(t *testing.T, path string) {
	t.Helper()
	if _, _, err := statWorkspacePath(context.Background(), e.workspace, path); err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
}

type memorySystem struct {
	workspace *memoryWorkspace
}

func newMemorySystem() *memorySystem {
	return &memorySystem{workspace: newMemoryWorkspace()}
}

func (s *memorySystem) Workspace() system.Workspace     { return s.workspace }
func (s *memorySystem) Network() system.Network         { return memoryNetwork{} }
func (s *memorySystem) Process() system.ProcessManager  { return nil }
func (s *memorySystem) Environment() system.Environment { return memoryEnvironment{} }
func (memoryEnvironment) Lookup(context.Context, string) (string, bool, error) {
	return "", false, nil
}

type memoryEnvironment struct{}
type memoryNetwork struct {
	fpsystemtest.UnsupportedNetwork
}

type memoryWorkspace struct {
	mu    sync.Mutex
	root  string
	nodes map[string]*memoryNode
	now   time.Time
}

type memoryNode struct {
	dir     bool
	data    []byte
	mode    os.FileMode
	modTime time.Time
}

func newMemoryWorkspace() *memoryWorkspace {
	now := time.Unix(1700000000, 0).UTC()
	return &memoryWorkspace{
		root:  "/memory-workspace",
		nodes: map[string]*memoryNode{"": {dir: true, mode: 0755 | os.ModeDir, modTime: now}},
		now:   now,
	}
}

func (w *memoryWorkspace) Root() string { return w.root }

func (w *memoryWorkspace) System() fpsystem.System { return nil }

func (w *memoryWorkspace) Roots() []system.WorkspaceRoot {
	return []system.WorkspaceRoot{{Path: w.root, Rel: ".", Read: true, Write: true}}
}

func (w *memoryWorkspace) ResolveExisting(_ context.Context, raw string) (system.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return system.ResolvedPath{}, err
	}
	if _, ok := w.nodes[rel]; !ok {
		return system.ResolvedPath{}, fs.ErrNotExist
	}
	return w.resolved(raw, rel), nil
}

func (w *memoryWorkspace) ResolveCreate(_ context.Context, raw string) (system.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return system.ResolvedPath{}, err
	}
	return w.resolved(raw, rel), nil
}

func (w *memoryWorkspace) ReadFile(_ context.Context, raw string, maxBytes int64) ([]byte, bool, system.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return nil, false, system.ResolvedPath{}, err
	}
	node, ok := w.nodes[rel]
	if !ok {
		return nil, false, system.ResolvedPath{}, fs.ErrNotExist
	}
	if node.dir {
		return nil, false, system.ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	data := append([]byte(nil), node.data...)
	if maxBytes <= 0 {
		return data, false, w.resolved(raw, rel), nil
	}
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return data, truncated, w.resolved(raw, rel), nil
}

func (w *memoryWorkspace) ReadFileLines(ctx context.Context, raw string, start, end int, maxBytes int64) ([]byte, int, bool, system.ResolvedPath, error) {
	data, _, resolved, err := w.ReadFile(ctx, raw, 0)
	if err != nil {
		return nil, 0, false, system.ResolvedPath{}, err
	}
	if maxBytes <= 0 {
		maxBytes = int64(len(data))
	}
	if start <= 0 {
		start = 1
	}
	if end > 0 && end < start {
		end = start
	}
	lines := strings.SplitAfter(string(data), "\n")
	var out bytes.Buffer
	var written int64
	for i, line := range lines {
		if err := ctx.Err(); err != nil {
			return nil, 0, false, system.ResolvedPath{}, err
		}
		lineNo := i + 1
		if lineNo < start || (end > 0 && lineNo > end) {
			continue
		}
		remaining := maxBytes - written
		if remaining <= 0 {
			return out.Bytes(), start, true, resolved, nil
		}
		if int64(len(line)) > remaining {
			out.WriteString(line[:int(remaining)])
			return out.Bytes(), start, true, resolved, nil
		}
		out.WriteString(line)
		written += int64(len(line))
	}
	return out.Bytes(), start, false, resolved, nil
}

func (w *memoryWorkspace) WriteFile(_ context.Context, raw string, data []byte, mode os.FileMode, overwrite bool) (system.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return system.ResolvedPath{}, err
	}
	if rel == "" {
		return system.ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	if _, ok := w.nodes[rel]; ok && !overwrite {
		return system.ResolvedPath{}, fmt.Errorf("path already exists")
	}
	if err := w.ensureParentDirs(rel); err != nil {
		return system.ResolvedPath{}, err
	}
	w.nodes[rel] = &memoryNode{data: append([]byte(nil), data...), mode: mode, modTime: w.tick()}
	return w.resolved(raw, rel), nil
}

func (w *memoryWorkspace) CopyFile(_ context.Context, rawSrc, rawDst string, overwrite bool) (system.ResolvedPath, system.ResolvedPath, int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	srcRel, err := w.clean(rawSrc)
	if err != nil {
		return system.ResolvedPath{}, system.ResolvedPath{}, 0, err
	}
	src, ok := w.nodes[srcRel]
	if !ok {
		return system.ResolvedPath{}, system.ResolvedPath{}, 0, fs.ErrNotExist
	}
	if src.dir {
		return system.ResolvedPath{}, system.ResolvedPath{}, 0, fmt.Errorf("source path is a directory")
	}
	dstRel, err := w.clean(rawDst)
	if err != nil {
		return system.ResolvedPath{}, system.ResolvedPath{}, 0, err
	}
	if dstRel == "" {
		return system.ResolvedPath{}, system.ResolvedPath{}, 0, fmt.Errorf("path is a directory")
	}
	if srcRel == dstRel {
		size := int64(len(src.data))
		return w.resolved(rawSrc, srcRel), w.resolved(rawDst, dstRel), size, nil
	}
	if _, ok := w.nodes[dstRel]; ok && !overwrite {
		return system.ResolvedPath{}, system.ResolvedPath{}, 0, fmt.Errorf("path already exists")
	}
	if err := w.ensureParentDirs(dstRel); err != nil {
		return system.ResolvedPath{}, system.ResolvedPath{}, 0, err
	}
	w.nodes[dstRel] = &memoryNode{data: append([]byte(nil), src.data...), mode: src.mode, modTime: w.tick()}
	return w.resolved(rawSrc, srcRel), w.resolved(rawDst, dstRel), int64(len(src.data)), nil
}

func (w *memoryWorkspace) MoveFile(ctx context.Context, rawSrc, rawDst string, overwrite bool) (system.ResolvedPath, system.ResolvedPath, int64, error) {
	src, dst, written, err := w.CopyFile(ctx, rawSrc, rawDst, overwrite)
	if err != nil {
		return system.ResolvedPath{}, system.ResolvedPath{}, 0, err
	}
	if src.Rel == dst.Rel {
		return src, dst, written, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.nodes, src.Rel)
	return src, dst, written, nil
}

func (w *memoryWorkspace) MkdirAll(_ context.Context, raw string, mode os.FileMode) (system.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return system.ResolvedPath{}, err
	}
	if rel == "" {
		return w.resolved(raw, rel), nil
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		dir := strings.Join(parts[:i+1], "/")
		if node, ok := w.nodes[dir]; ok && !node.dir {
			return system.ResolvedPath{}, fmt.Errorf("path component is a file")
		}
		w.nodes[dir] = &memoryNode{dir: true, mode: mode | os.ModeDir, modTime: w.tick()}
	}
	return w.resolved(raw, rel), nil
}

func (w *memoryWorkspace) Remove(_ context.Context, raw string) (system.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return system.ResolvedPath{}, err
	}
	node, ok := w.nodes[rel]
	if !ok {
		return system.ResolvedPath{}, fs.ErrNotExist
	}
	if rel == "" {
		return system.ResolvedPath{}, fmt.Errorf("cannot remove workspace root")
	}
	if node.dir {
		prefix := rel + "/"
		for path := range w.nodes {
			if strings.HasPrefix(path, prefix) {
				return system.ResolvedPath{}, fmt.Errorf("directory not empty")
			}
		}
	}
	resolved := w.resolved(raw, rel)
	delete(w.nodes, rel)
	return resolved, nil
}

func (w *memoryWorkspace) Stat(_ context.Context, raw string) (fs.FileInfo, system.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return nil, system.ResolvedPath{}, err
	}
	node, ok := w.nodes[rel]
	if !ok {
		return nil, system.ResolvedPath{}, fs.ErrNotExist
	}
	return memoryFileInfo{name: pathBase(rel), node: node}, w.resolved(raw, rel), nil
}

func (w *memoryWorkspace) ReadDir(_ context.Context, raw string) ([]fs.DirEntry, system.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return nil, system.ResolvedPath{}, err
	}
	node, ok := w.nodes[rel]
	if !ok {
		return nil, system.ResolvedPath{}, fs.ErrNotExist
	}
	if !node.dir {
		return nil, system.ResolvedPath{}, fmt.Errorf("path is not a directory")
	}
	children := make([]string, 0)
	prefix := ""
	if rel != "" {
		prefix = rel + "/"
	}
	seen := map[string]struct{}{}
	for path := range w.nodes {
		if path == rel || !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		name, _, _ := strings.Cut(rest, "/")
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		children = append(children, prefix+name)
	}
	sort.Strings(children)
	entries := make([]fs.DirEntry, 0, len(children))
	for _, child := range children {
		entries = append(entries, memoryDirEntry{name: pathBase(child), node: w.nodes[child]})
	}
	return entries, w.resolved(raw, rel), nil
}

func (w *memoryWorkspace) Walk(_ context.Context, raw string, opts system.WalkOptions) ([]system.WalkEntry, system.ResolvedPath, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rootRel, err := w.clean(raw)
	if err != nil {
		return nil, system.ResolvedPath{}, false, err
	}
	rootNode, ok := w.nodes[rootRel]
	if !ok {
		return nil, system.ResolvedPath{}, false, fs.ErrNotExist
	}
	if !rootNode.dir {
		info := memoryFileInfo{name: pathBase(rootRel), node: rootNode}
		return []system.WalkEntry{w.walkEntry(rootRel, info, 0)}, w.resolved(raw, rootRel), false, nil
	}
	depth := opts.Depth
	if depth <= 0 {
		depth = 3
	}
	if depth > 50 {
		depth = 50
	}
	limit := opts.MaxEntries
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	paths := make([]string, 0)
	prefix := ""
	if rootRel != "" {
		prefix = rootRel + "/"
	}
	for path := range w.nodes {
		if path == rootRel || !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		level := strings.Count(rest, "/") + 1
		if level > depth {
			continue
		}
		if !opts.ShowHidden && containsHiddenPath(rest) {
			continue
		}
		if opts.FilesOnly && w.nodes[path].dir {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	truncated := false
	if len(paths) > limit {
		paths = paths[:limit]
		truncated = true
	}
	entries := make([]system.WalkEntry, 0, len(paths))
	for _, path := range paths {
		node := w.nodes[path]
		rest := strings.TrimPrefix(path, prefix)
		level := strings.Count(rest, "/") + 1
		entries = append(entries, w.walkEntry(path, memoryFileInfo{name: pathBase(path), node: node}, level))
	}
	return entries, w.resolved(raw, rootRel), truncated, nil
}

func (w *memoryWorkspace) Glob(ctx context.Context, pattern string, opts system.GlobOptions) ([]system.ResolvedPath, bool, error) {
	compiled, err := pathpattern.Compile(pattern)
	if err != nil {
		return nil, false, err
	}
	base := opts.Base
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	limit := opts.MaxResults
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	scanLimit := opts.MaxScanned
	if scanLimit <= 0 || scanLimit > 100000 {
		scanLimit = 10000
	}
	entries, root, truncated, err := w.Walk(ctx, base, system.WalkOptions{Depth: 50, ShowHidden: true, MaxEntries: scanLimit})
	if err != nil {
		return nil, false, err
	}
	matches := make([]system.ResolvedPath, 0)
	resultsTruncated := false
	for _, entry := range entries {
		rel := entry.Path.Rel
		matchRel := rel
		if root.Rel != "" && strings.HasPrefix(matchRel, root.Rel+"/") {
			matchRel = strings.TrimPrefix(matchRel, root.Rel+"/")
		}
		if compiled.Match(matchRel) || compiled.Match(rel) {
			if len(matches) < limit {
				matches = append(matches, entry.Path)
			} else {
				resultsTruncated = true
			}
		}
	}
	return matches, truncated || resultsTruncated, nil
}

func (w *memoryWorkspace) CreateScratch(context.Context, string) (system.ScratchDir, error) {
	return nil, errors.ErrUnsupported
}

func (w *memoryWorkspace) clean(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "."
	}
	raw = filepath.ToSlash(raw)
	if strings.HasPrefix(raw, "@") {
		return "", fmt.Errorf("unknown workspace root")
	}
	if strings.HasPrefix(raw, w.root) {
		raw = strings.TrimPrefix(raw, w.root)
		raw = strings.TrimPrefix(raw, "/")
	}
	if filepath.IsAbs(raw) {
		return "", fmt.Errorf("path escapes workspace root")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes workspace root")
	}
	return clean, nil
}

func (w *memoryWorkspace) ensureParentDirs(rel string) error {
	parent := pathDir(rel)
	if parent == "" {
		return nil
	}
	parts := strings.Split(parent, "/")
	for i := range parts {
		dir := strings.Join(parts[:i+1], "/")
		if node, ok := w.nodes[dir]; ok {
			if !node.dir {
				return fmt.Errorf("path component is a file")
			}
			continue
		}
		w.nodes[dir] = &memoryNode{dir: true, mode: 0755 | os.ModeDir, modTime: w.tick()}
	}
	return nil
}

func (w *memoryWorkspace) resolved(input, rel string) system.ResolvedPath {
	abs := w.root
	if rel != "" {
		abs = filepath.Join(w.root, filepath.FromSlash(rel))
	}
	return system.ResolvedPath{Input: input, Abs: abs, Rel: rel}
}

func (w *memoryWorkspace) walkEntry(rel string, info memoryFileInfo, level int) system.WalkEntry {
	kind := "file"
	if info.IsDir() {
		kind = "dir"
	}
	return system.WalkEntry{
		Path:    w.resolved(rel, rel),
		Name:    info.Name(),
		Kind:    kind,
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime(),
		Level:   level,
	}
}

func (w *memoryWorkspace) tick() time.Time {
	w.now = w.now.Add(time.Second)
	return w.now
}

type memoryFileInfo struct {
	name string
	node *memoryNode
}

func (i memoryFileInfo) Name() string       { return i.name }
func (i memoryFileInfo) Size() int64        { return int64(len(i.node.data)) }
func (i memoryFileInfo) Mode() os.FileMode  { return i.node.mode }
func (i memoryFileInfo) ModTime() time.Time { return i.node.modTime }
func (i memoryFileInfo) IsDir() bool        { return i.node.dir }
func (i memoryFileInfo) Sys() any           { return nil }

type memoryDirEntry struct {
	name string
	node *memoryNode
}

func (e memoryDirEntry) Name() string      { return e.name }
func (e memoryDirEntry) IsDir() bool       { return e.node.dir }
func (e memoryDirEntry) Type() fs.FileMode { return e.node.mode.Type() }
func (e memoryDirEntry) Info() (fs.FileInfo, error) {
	return memoryFileInfo(e), nil
}

func pathBase(rel string) string {
	if rel == "" {
		return "."
	}
	parts := strings.Split(rel, "/")
	return parts[len(parts)-1]
}

func pathDir(rel string) string {
	if rel == "" || !strings.Contains(rel, "/") {
		return ""
	}
	return rel[:strings.LastIndex(rel, "/")]
}

func containsHiddenPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

var _ system.System = (*memorySystem)(nil)
var _ system.Workspace = (*memoryWorkspace)(nil)
var _ system.Network = memoryNetwork{}
var _ system.Environment = memoryEnvironment{}
var _ fs.FileInfo = memoryFileInfo{}
var _ fs.DirEntry = memoryDirEntry{}
