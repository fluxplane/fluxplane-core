// Memory-backed runtime system implementations for tests and lightweight fixtures.
package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/fluxplane-core/core/pathpattern"
	fpsystem "github.com/fluxplane/fluxplane-system"
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

func (s *MemorySystem) Workspace() runtimeworkspace.Workspace { return s.WorkspaceValue }
func (s *MemorySystem) Network() fpsystem.Network             { return network{} }
func (s *MemorySystem) Process() fpsystem.ProcessManager      { return nil }
func (s *MemorySystem) Environment() fpsystem.Environment     { return environment{} }
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
	mu    sync.Mutex
	root  string
	nodes map[string]*node
	now   time.Time
}

type node struct {
	dir     bool
	data    []byte
	mode    os.FileMode
	modTime time.Time
}

// NewMemoryWorkspace returns an empty memory workspace.
func NewMemoryWorkspace() *MemoryWorkspace {
	now := time.Unix(1700000000, 0).UTC()
	return &MemoryWorkspace{
		root:  "/memory-workspace",
		nodes: map[string]*node{"": {dir: true, mode: 0755 | os.ModeDir, modTime: now}},
		now:   now,
	}
}

func (w *MemoryWorkspace) Root() string { return w.root }

func (w *MemoryWorkspace) System() fpsystem.System {
	sys, _ := systemkit.NewSystem().WithFileSystem(memoryFileSystem{workspace: w}).Build()
	return sys
}

// Roots returns the single in-memory workspace root.
func (w *MemoryWorkspace) Roots() []runtimeworkspace.Root {
	if w == nil {
		return nil
	}
	return []runtimeworkspace.Root{{Path: w.root, Rel: ".", Read: true, Write: true}}
}

func (w *MemoryWorkspace) ResolveExisting(_ context.Context, raw string) (runtimeworkspace.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	if _, ok := w.nodes[rel]; !ok {
		return runtimeworkspace.ResolvedPath{}, fs.ErrNotExist
	}
	return w.resolved(raw, rel), nil
}

func (w *MemoryWorkspace) ResolveCreate(_ context.Context, raw string) (runtimeworkspace.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	return w.resolved(raw, rel), nil
}

func (w *MemoryWorkspace) ReadFile(_ context.Context, raw string, maxBytes int64) ([]byte, bool, runtimeworkspace.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return nil, false, runtimeworkspace.ResolvedPath{}, err
	}
	n, ok := w.nodes[rel]
	if !ok {
		return nil, false, runtimeworkspace.ResolvedPath{}, fs.ErrNotExist
	}
	if n.dir {
		return nil, false, runtimeworkspace.ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	data := append([]byte(nil), n.data...)
	if maxBytes <= 0 {
		return data, false, w.resolved(raw, rel), nil
	}
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return data, truncated, w.resolved(raw, rel), nil
}

func (w *MemoryWorkspace) ReadFileLines(ctx context.Context, raw string, start, end int, maxBytes int64) ([]byte, int, bool, runtimeworkspace.ResolvedPath, error) {
	data, _, resolved, err := w.ReadFile(ctx, raw, 0)
	if err != nil {
		return nil, 0, false, runtimeworkspace.ResolvedPath{}, err
	}
	if start <= 0 {
		start = 1
	}
	lines := strings.SplitAfter(string(data), "\n")
	var out bytes.Buffer
	for i, line := range lines {
		lineNo := i + 1
		if lineNo < start || (end > 0 && lineNo > end) {
			continue
		}
		if maxBytes > 0 && int64(out.Len()+len(line)) > maxBytes {
			remaining := int(maxBytes) - out.Len()
			if remaining > 0 {
				out.WriteString(line[:remaining])
			}
			return out.Bytes(), start, true, resolved, nil
		}
		out.WriteString(line)
	}
	return out.Bytes(), start, false, resolved, nil
}

func (w *MemoryWorkspace) WriteFile(_ context.Context, raw string, data []byte, mode os.FileMode, overwrite bool) (runtimeworkspace.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	if rel == "" {
		return runtimeworkspace.ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	if _, ok := w.nodes[rel]; ok && !overwrite {
		return runtimeworkspace.ResolvedPath{}, fmt.Errorf("path already exists")
	}
	if err := w.ensureParentDirs(rel); err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	w.nodes[rel] = &node{data: append([]byte(nil), data...), mode: mode, modTime: w.tick()}
	return w.resolved(raw, rel), nil
}

func (w *MemoryWorkspace) CopyFile(ctx context.Context, src, dst string, overwrite bool) (runtimeworkspace.ResolvedPath, runtimeworkspace.ResolvedPath, int64, error) {
	data, _, srcResolved, err := w.ReadFile(ctx, src, 0)
	if err != nil {
		return runtimeworkspace.ResolvedPath{}, runtimeworkspace.ResolvedPath{}, 0, err
	}
	dstResolved, err := w.WriteFile(ctx, dst, data, 0644, overwrite)
	return srcResolved, dstResolved, int64(len(data)), err
}

func (w *MemoryWorkspace) MoveFile(ctx context.Context, src, dst string, overwrite bool) (runtimeworkspace.ResolvedPath, runtimeworkspace.ResolvedPath, int64, error) {
	srcResolved, dstResolved, n, err := w.CopyFile(ctx, src, dst, overwrite)
	if err != nil {
		return runtimeworkspace.ResolvedPath{}, runtimeworkspace.ResolvedPath{}, 0, err
	}
	_, _ = w.Remove(ctx, src)
	return srcResolved, dstResolved, n, nil
}

func (w *MemoryWorkspace) MkdirAll(_ context.Context, raw string, mode os.FileMode) (runtimeworkspace.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	if rel != "" {
		for _, dir := range prefixes(rel) {
			w.nodes[dir] = &node{dir: true, mode: mode | os.ModeDir, modTime: w.tick()}
		}
	}
	return w.resolved(raw, rel), nil
}

func (w *MemoryWorkspace) Remove(_ context.Context, raw string) (runtimeworkspace.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	if rel == "" {
		return runtimeworkspace.ResolvedPath{}, fmt.Errorf("cannot remove workspace root")
	}
	if _, ok := w.nodes[rel]; !ok {
		return runtimeworkspace.ResolvedPath{}, fs.ErrNotExist
	}
	for path := range w.nodes {
		if strings.HasPrefix(path, rel+"/") {
			return runtimeworkspace.ResolvedPath{}, fmt.Errorf("directory not empty")
		}
	}
	resolved := w.resolved(raw, rel)
	delete(w.nodes, rel)
	return resolved, nil
}

func (w *MemoryWorkspace) Stat(_ context.Context, raw string) (fs.FileInfo, runtimeworkspace.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, err
	}
	n, ok := w.nodes[rel]
	if !ok {
		return nil, runtimeworkspace.ResolvedPath{}, fs.ErrNotExist
	}
	return fileInfo{name: base(rel), node: n}, w.resolved(raw, rel), nil
}

func (w *MemoryWorkspace) ReadDir(_ context.Context, raw string) ([]fs.DirEntry, runtimeworkspace.ResolvedPath, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rel, err := w.clean(raw)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, err
	}
	n, ok := w.nodes[rel]
	if !ok {
		return nil, runtimeworkspace.ResolvedPath{}, fs.ErrNotExist
	}
	if !n.dir {
		return nil, runtimeworkspace.ResolvedPath{}, fmt.Errorf("path is not a directory")
	}
	prefix := ""
	if rel != "" {
		prefix = rel + "/"
	}
	seen := map[string]string{}
	for p := range w.nodes {
		if p == rel || !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		name, _, _ := strings.Cut(rest, "/")
		seen[name] = prefix + name
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		out = append(out, dirEntry{name: name, node: w.nodes[seen[name]]})
	}
	return out, w.resolved(raw, rel), nil
}

func (w *MemoryWorkspace) Walk(_ context.Context, raw string, opts WalkOptions) ([]WalkEntry, runtimeworkspace.ResolvedPath, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	root, err := w.clean(raw)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, false, err
	}
	rootNode, ok := w.nodes[root]
	if !ok {
		return nil, runtimeworkspace.ResolvedPath{}, false, fs.ErrNotExist
	}
	if !rootNode.dir {
		return []WalkEntry{w.walkEntry(root, rootNode, 0)}, w.resolved(raw, root), false, nil
	}
	depth := opts.Depth
	if depth <= 0 {
		depth = 3
	}
	limit := opts.MaxEntries
	if limit <= 0 {
		limit = 1000
	}
	skipDirs := map[string]bool{}
	for _, name := range opts.SkipDirs {
		name = strings.TrimSpace(name)
		if name != "" {
			skipDirs[name] = true
		}
	}
	prefix := ""
	if root != "" {
		prefix = root + "/"
	}
	var paths []string
	for p, n := range w.nodes {
		if p == root || !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		level := strings.Count(rest, "/") + 1
		if skippedByDir(rest, skipDirs) || level > depth || (!opts.ShowHidden && hidden(rest)) || (opts.FilesOnly && n.dir) {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	truncated := false
	if len(paths) > limit {
		paths = paths[:limit]
		truncated = true
	}
	out := make([]WalkEntry, 0, len(paths))
	for _, p := range paths {
		rest := strings.TrimPrefix(p, prefix)
		out = append(out, w.walkEntry(p, w.nodes[p], strings.Count(rest, "/")+1))
	}
	return out, w.resolved(raw, root), truncated, nil
}

func skippedByDir(rel string, skipDirs map[string]bool) bool {
	if len(skipDirs) == 0 {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if skipDirs[part] {
			return true
		}
	}
	return false
}

func (w *MemoryWorkspace) Glob(ctx context.Context, pattern string, opts GlobOptions) ([]runtimeworkspace.ResolvedPath, bool, error) {
	compiled, err := pathpattern.Compile(pattern)
	if err != nil {
		return nil, false, err
	}
	limit := opts.MaxResults
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	scanLimit := opts.MaxScanned
	if scanLimit <= 0 || scanLimit > 100000 {
		scanLimit = 10000
	}
	entries, root, truncated, err := w.Walk(ctx, opts.Base, WalkOptions{Depth: 50, ShowHidden: true, MaxEntries: scanLimit})
	if err != nil {
		return nil, false, err
	}
	out := make([]runtimeworkspace.ResolvedPath, 0)
	resultsTruncated := false
	for _, entry := range entries {
		rel := entry.Path.Rel
		matchRel := rel
		if root.Rel != "" && strings.HasPrefix(matchRel, root.Rel+"/") {
			matchRel = strings.TrimPrefix(matchRel, root.Rel+"/")
		}
		if compiled.Match(matchRel) || compiled.Match(rel) {
			if len(out) < limit {
				out = append(out, entry.Path)
			} else {
				resultsTruncated = true
			}
		}
	}
	return out, truncated || resultsTruncated, nil
}

func (w *MemoryWorkspace) CreateScratch(context.Context, string) (runtimeworkspace.ScratchDir, error) {
	return nil, errors.ErrUnsupported
}

func (w *MemoryWorkspace) clean(raw string) (string, error) {
	raw = strings.TrimSpace(filepath.ToSlash(raw))
	if raw == "" || raw == "." {
		return "", nil
	}
	if strings.HasPrefix(raw, w.root) {
		raw = strings.TrimPrefix(strings.TrimPrefix(raw, w.root), "/")
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

func (w *MemoryWorkspace) ensureParentDirs(rel string) error {
	for _, dir := range prefixes(parent(rel)) {
		if existing := w.nodes[dir]; existing != nil && !existing.dir {
			return fmt.Errorf("path component is a file")
		}
		if w.nodes[dir] == nil {
			w.nodes[dir] = &node{dir: true, mode: 0755 | os.ModeDir, modTime: w.tick()}
		}
	}
	return nil
}

func (w *MemoryWorkspace) resolved(input, rel string) runtimeworkspace.ResolvedPath {
	abs := w.root
	if rel != "" {
		abs = filepath.Join(w.root, filepath.FromSlash(rel))
	}
	return runtimeworkspace.ResolvedPath{Input: input, Abs: abs, Rel: rel}
}

func (w *MemoryWorkspace) walkEntry(rel string, n *node, level int) WalkEntry {
	kind := "file"
	if n.dir {
		kind = "dir"
	}
	return WalkEntry{Path: w.resolved(rel, rel), Name: base(rel), Kind: kind, Size: int64(len(n.data)), Mode: n.mode.String(), ModTime: n.modTime, Level: level}
}

func (w *MemoryWorkspace) tick() time.Time {
	w.now = w.now.Add(time.Second)
	return w.now
}

type fileInfo struct {
	name string
	node *node
}

func (i fileInfo) Name() string       { return i.name }
func (i fileInfo) Size() int64        { return int64(len(i.node.data)) }
func (i fileInfo) Mode() os.FileMode  { return i.node.mode }
func (i fileInfo) ModTime() time.Time { return i.node.modTime }
func (i fileInfo) IsDir() bool        { return i.node.dir }
func (i fileInfo) Sys() any           { return nil }

type dirEntry struct {
	name string
	node *node
}

func (e dirEntry) Name() string      { return e.name }
func (e dirEntry) IsDir() bool       { return e.node.dir }
func (e dirEntry) Type() fs.FileMode { return e.node.mode.Type() }
func (e dirEntry) Info() (fs.FileInfo, error) {
	return fileInfo(e), nil
}

type memoryFileSystem struct {
	workspace *MemoryWorkspace
}

func (f memoryFileSystem) Open(name string) (fs.File, error) {
	info, _, err := f.workspace.Stat(context.Background(), name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return &memoryFile{info: info}, nil
	}
	data, _, _, err := f.workspace.ReadFile(context.Background(), name, 0)
	if err != nil {
		return nil, err
	}
	return &memoryFile{Reader: bytes.NewReader(data), info: info}, nil
}

func (f memoryFileSystem) Stat(name string) (fs.FileInfo, error) {
	info, _, err := f.workspace.Stat(context.Background(), name)
	return info, err
}

func (f memoryFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, _, err := f.workspace.ReadDir(context.Background(), name)
	return entries, err
}

func (f memoryFileSystem) ReadFile(name string) ([]byte, error) {
	data, _, _, err := f.workspace.ReadFile(context.Background(), name, 0)
	return data, err
}

func (f memoryFileSystem) WriteFile(ctx context.Context, name string, data []byte, opts fpsystem.WriteFileOptions) error {
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	_, err := f.workspace.WriteFile(ctx, name, data, perm, opts.Overwrite)
	return err
}

func (f memoryFileSystem) WriteTempFile(ctx context.Context, dir, pattern string, data []byte, opts fpsystem.WriteTempFileOptions) (string, error) {
	relDir, err := f.workspace.clean(dir)
	if err != nil {
		return "", err
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		pattern = "tmp-*"
	}
	if strings.ContainsAny(pattern, `/\`) {
		return "", fmt.Errorf("temp file pattern must not contain a path separator")
	}
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	for i := 1; i <= 1_000_000; i++ {
		name := joinRel(relDir, tempFileName(pattern, i))
		if _, err := f.workspace.WriteFile(ctx, name, data, perm, false); err == nil {
			return name, nil
		} else if !strings.Contains(err.Error(), "already exists") {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate temp file")
}

func (f memoryFileSystem) MkdirAll(ctx context.Context, name string, opts fpsystem.MkdirOptions) error {
	perm := opts.Perm
	if perm == 0 {
		perm = 0755
	}
	_, err := f.workspace.MkdirAll(ctx, name, perm)
	return err
}

func (f memoryFileSystem) Remove(ctx context.Context, name string) error {
	_, err := f.workspace.Remove(ctx, name)
	return err
}

func (f memoryFileSystem) Rename(ctx context.Context, oldName, newName string, opts fpsystem.RenameOptions) error {
	_, _, _, err := f.workspace.MoveFile(ctx, oldName, newName, opts.Overwrite)
	return err
}

type memoryFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *memoryFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *memoryFile) Close() error               { return nil }
func (f *memoryFile) Read(p []byte) (int, error) {
	if f.Reader == nil {
		return 0, fs.ErrInvalid
	}
	return f.Reader.Read(p)
}

func prefixes(rel string) []string {
	if rel == "" {
		return nil
	}
	parts := strings.Split(rel, "/")
	out := make([]string, 0, len(parts))
	for i := range parts {
		out = append(out, strings.Join(parts[:i+1], "/"))
	}
	return out
}

func parent(rel string) string {
	if rel == "" || !strings.Contains(rel, "/") {
		return ""
	}
	return rel[:strings.LastIndex(rel, "/")]
}

func base(rel string) string {
	if rel == "" {
		return "."
	}
	return rel[strings.LastIndex(rel, "/")+1:]
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func tempFileName(pattern string, sequence int) string {
	token := fmt.Sprintf("%06d", sequence)
	if strings.Contains(pattern, "*") {
		return strings.Replace(pattern, "*", token, 1)
	}
	return pattern + token
}

func hidden(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

var _ fpsystem.System = (*MemorySystem)(nil)
var _ runtimeworkspace.Workspace = (*MemoryWorkspace)(nil)
