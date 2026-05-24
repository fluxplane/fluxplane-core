// Package system defines runtime IO boundaries used by concrete operations.
package system

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/pathpattern"
	"github.com/fluxplane/fluxplane-core/runtime/httptransport"
)

// System groups the runtime boundaries that can touch the outside world.
type System interface {
	Workspace() Workspace
	Network() Network
	Process() ProcessManager
	Browser() BrowserManager
	Clarifier() Clarifier
	Environment() Environment
}

// Config configures the host-backed system implementation.
type Config struct {
	Root                string
	Workspace           WorkspaceConfig
	AllowPrivateNetwork bool
	Browser             BrowserManager
	Clarifier           Clarifier
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
	browser   BrowserManager
	clarifier Clarifier
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
		browser:   cfg.Browser,
		clarifier: cfg.Clarifier,
		env:       env,
	}, nil
}

// Workspace returns the workspace boundary.
func (h *Host) Workspace() Workspace { return h.workspace }

// Network returns the network boundary.
func (h *Host) Network() Network { return h.network }

// Process returns the process boundary.
func (h *Host) Process() ProcessManager { return h.process }

// Browser returns the configured browser manager, when one is available.
func (h *Host) Browser() BrowserManager { return h.browser }

// Clarifier returns the configured human-input boundary, when one is available.
func (h *Host) Clarifier() Clarifier { return h.clarifier }

// Environment returns the host environment boundary.
func (h *Host) Environment() Environment { return h.env }

// SetBrowser installs a browser manager after host construction.
func (h *Host) SetBrowser(browser BrowserManager) { h.browser = browser }

// SetClarifier installs a human input boundary after host construction.
func (h *Host) SetClarifier(clarifier Clarifier) { h.clarifier = clarifier }

// Environment is a read-only boundary for host environment variables.
type Environment interface {
	Lookup(context.Context, string) (string, bool, error)
}

// ExecutableResolver optionally resolves executables using an environment's
// process PATH without exposing the PATH value itself.
type ExecutableResolver interface {
	ResolveExecutable(context.Context, string) (string, bool, error)
}

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
type Workspace interface {
	Root() string
	Roots() []WorkspaceRoot
	ResolveExisting(context.Context, string) (ResolvedPath, error)
	ResolveCreate(context.Context, string) (ResolvedPath, error)
	ReadFile(context.Context, string, int64) ([]byte, bool, ResolvedPath, error)
	ReadFileLines(context.Context, string, int, int, int64) ([]byte, int, bool, ResolvedPath, error)
	WriteFile(context.Context, string, []byte, os.FileMode, bool) (ResolvedPath, error)
	CopyFile(context.Context, string, string, bool) (ResolvedPath, ResolvedPath, int64, error)
	MoveFile(context.Context, string, string, bool) (ResolvedPath, ResolvedPath, int64, error)
	MkdirAll(context.Context, string, os.FileMode) (ResolvedPath, error)
	Remove(context.Context, string) (ResolvedPath, error)
	Stat(context.Context, string) (fs.FileInfo, ResolvedPath, error)
	ReadDir(context.Context, string) ([]fs.DirEntry, ResolvedPath, error)
	Walk(context.Context, string, WalkOptions) ([]WalkEntry, ResolvedPath, bool, error)
	Glob(context.Context, string, GlobOptions) ([]ResolvedPath, bool, error)
	CreateScratch(context.Context, string) (ScratchDir, error)
}

// ResolvedPath is a canonical workspace path.
type ResolvedPath struct {
	Input string `json:"input,omitempty"`
	Abs   string `json:"abs"`
	Rel   string `json:"rel"`
}

// WorkspaceRoot describes one runtime filesystem root exposed by a Workspace.
type WorkspaceRoot struct {
	Name  string `json:"name,omitempty"`
	Path  string `json:"path"`
	Rel   string `json:"rel,omitempty"`
	Read  bool   `json:"read"`
	Write bool   `json:"write"`
}

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
type ScratchDir interface {
	Root() string
	WriteFile(context.Context, string, []byte, os.FileMode) (ResolvedPath, error)
	RemoveAll(context.Context) error
}

// HostWorkspace implements Workspace using the local filesystem.
type HostWorkspace struct {
	root        string
	roots       []workspaceRoot
	scratchRoot string
}

type workspaceRoot struct {
	name     string
	root     string
	rel      string
	read     bool
	write    bool
	envFiles []string
}

// Root returns the canonical workspace root.
func (w *HostWorkspace) Root() string { return w.root }

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
			Name:  name,
			Path:  root.root,
			Rel:   rel,
			Read:  root.read,
			Write: root.write,
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
	info, err := os.Stat(resolved.Abs)
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	if info.IsDir() {
		return nil, false, ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	file, err := os.Open(resolved.Abs)
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	defer func() { _ = file.Close() }()
	if maxBytes <= 0 {
		maxBytes = info.Size()
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return data, truncated, resolved, nil
}

// ReadFileLines reads a bounded 1-indexed line window from a workspace file.
func (w *HostWorkspace) ReadFileLines(ctx context.Context, raw string, start, end int, maxBytes int64) ([]byte, int, bool, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, 0, false, ResolvedPath{}, err
	}
	info, err := os.Stat(resolved.Abs)
	if err != nil {
		return nil, 0, false, ResolvedPath{}, err
	}
	if info.IsDir() {
		return nil, 0, false, ResolvedPath{}, fmt.Errorf("path is a directory")
	}
	file, err := os.Open(resolved.Abs)
	if err != nil {
		return nil, 0, false, ResolvedPath{}, err
	}
	defer func() { _ = file.Close() }()
	if maxBytes <= 0 {
		maxBytes = info.Size()
	}
	if start <= 0 {
		start = 1
	}
	if end > 0 && end < start {
		end = start
	}
	reader := bufio.NewReader(file)
	var out bytes.Buffer
	var written int64
	lineNo := 1
	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, false, ResolvedPath{}, err
		}
		line, err := reader.ReadString('\n')
		if lineNo >= start && (end <= 0 || lineNo <= end) {
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
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, false, ResolvedPath{}, err
		}
		if end > 0 && lineNo >= end {
			break
		}
		lineNo++
	}
	return out.Bytes(), start, false, resolved, nil
}

// WriteFile writes a file, optionally refusing to overwrite existing paths.
func (w *HostWorkspace) WriteFile(ctx context.Context, raw string, data []byte, mode os.FileMode, overwrite bool) (ResolvedPath, error) {
	resolved, err := w.ResolveCreate(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if !overwrite {
		if _, err := os.Lstat(resolved.Abs); err == nil {
			return ResolvedPath{}, fmt.Errorf("path already exists")
		}
	}
	if err := os.MkdirAll(filepath.Dir(resolved.Abs), 0755); err != nil {
		return ResolvedPath{}, err
	}
	return resolved, os.WriteFile(resolved.Abs, data, mode)
}

// CopyFile copies one complete file within the workspace.
func (w *HostWorkspace) CopyFile(ctx context.Context, rawSrc, rawDst string, overwrite bool) (ResolvedPath, ResolvedPath, int64, error) {
	src, err := w.ResolveExisting(ctx, rawSrc)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	info, err := os.Stat(src.Abs)
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
	return resolved, os.MkdirAll(resolved.Abs, mode)
}

// Remove removes a file or empty directory.
func (w *HostWorkspace) Remove(_ context.Context, raw string) (ResolvedPath, error) {
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
	return resolved, os.Remove(resolved.Abs)
}

// Stat stats a workspace path.
func (w *HostWorkspace) Stat(ctx context.Context, raw string) (fs.FileInfo, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, err
	}
	info, err := os.Stat(resolved.Abs)
	return info, resolved, err
}

// ReadDir lists a workspace directory.
func (w *HostWorkspace) ReadDir(ctx context.Context, raw string) ([]fs.DirEntry, ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, err
	}
	entries, err := os.ReadDir(resolved.Abs)
	return entries, resolved, err
}

// Walk returns a bounded tree traversal rooted at raw.
func (w *HostWorkspace) Walk(ctx context.Context, raw string, opts WalkOptions) ([]WalkEntry, ResolvedPath, bool, error) {
	root, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, false, err
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
	skipDirs := walkSkipDirs(opts.SkipDirs)
	var entries []WalkEntry
	truncated := false
	err = filepath.WalkDir(root.Abs, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if current == root.Abs {
			return nil
		}
		relToRoot, err := filepath.Rel(root.Abs, current)
		if err != nil {
			return nil
		}
		relToRoot = filepath.ToSlash(relToRoot)
		level := strings.Count(relToRoot, "/") + 1
		if level > depth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !opts.ShowHidden && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if opts.FilesOnly && d.IsDir() {
			return nil
		}
		if opts.FilterPattern != "" {
			if !matchFilterPattern(opts.FilterPattern, relToRoot, d.IsDir()) {
				if d.IsDir() {
					// Walk children; they may still match.
					return nil
				}
				return nil
			}
		}
		if len(entries) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel := relToRoot
		if root.Rel != "" {
			rel = filepath.ToSlash(filepath.Join(root.Rel, relToRoot))
		}
		kind := "file"
		if d.IsDir() {
			kind = "dir"
		} else if d.Type()&os.ModeSymlink != 0 {
			kind = "symlink"
		}
		entries = append(entries, WalkEntry{
			Path: ResolvedPath{Input: rel, Abs: current, Rel: rel},
			Name: d.Name(), Kind: kind, Size: info.Size(), Mode: info.Mode().String(), ModTime: info.ModTime(), Level: level,
		})
		return nil
	})
	return entries, root, truncated, err
}

func walkSkipDirs(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// Glob returns workspace paths matching a slash-style glob under opts.Base.
func (w *HostWorkspace) Glob(ctx context.Context, pattern string, opts GlobOptions) ([]ResolvedPath, bool, error) {
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
	entries, root, truncated, err := w.Walk(ctx, base, WalkOptions{Depth: 50, ShowHidden: true, MaxEntries: scanLimit, SkipDirs: opts.SkipDirs})
	if err != nil {
		return nil, false, err
	}
	matches := make([]ResolvedPath, 0)
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
	if strings.HasPrefix(raw, "@") {
		name, rest := splitWorkspaceRootPath(raw)
		root, ok := w.rootByName(name)
		if !ok {
			return workspaceRoot{}, "", fmt.Errorf("unknown workspace root %q", name)
		}
		clean := filepath.Clean(filepath.FromSlash(rest))
		if clean == "." {
			clean = ""
		}
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return workspaceRoot{}, "", fmt.Errorf("path escapes workspace root")
		}
		path, err := filepath.Abs(filepath.Join(root.root, clean))
		return root, path, err
	}
	path := raw
	if filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return workspaceRoot{}, "", err
		}
		root, ok := w.rootForAbs(abs)
		if !ok {
			return workspaceRoot{}, "", fmt.Errorf("path escapes workspace root")
		}
		return root, abs, nil
	}
	root := w.roots[0]
	if !filepath.IsAbs(path) {
		path = filepath.Join(root.root, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return workspaceRoot{}, "", err
	}
	return root, path, nil
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

// Network executes outbound HTTP requests through a host-guarded boundary.
type Network interface {
	DoHTTP(context.Context, HTTPRequest) (HTTPResponse, error)
}

// HTTPRequest is the neutral request shape exposed to standard operations.
type HTTPRequest struct {
	URL       string
	Method    string
	Headers   map[string]string
	Body      string
	Timeout   time.Duration
	MaxBytes  int
	UserAgent string
	TLSConfig *tls.Config
}

// HTTPResponse is the neutral response shape returned by Network.
type HTTPResponse struct {
	URL         string              `json:"url"`
	FinalURL    string              `json:"final_url,omitempty"`
	Method      string              `json:"method"`
	Status      string              `json:"status"`
	StatusCode  int                 `json:"status_code"`
	Headers     map[string][]string `json:"headers,omitempty"`
	ContentType string              `json:"content_type,omitempty"`
	Body        []byte              `json:"-"`
	Truncated   bool                `json:"truncated,omitempty"`
	Duration    time.Duration       `json:"-"`
}

// HostNetwork implements Network using net/http with target guards.
type HostNetwork struct {
	allowPrivate bool
}

// DoHTTP executes req after validating the target and redirects.
func (n *HostNetwork) DoHTTP(ctx context.Context, req HTTPRequest) (HTTPResponse, error) {
	parsed, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil {
		return HTTPResponse{}, err
	}
	if err := ValidatePublicURL(parsed, n.allowPrivate); err != nil {
		return HTTPResponse{}, err
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !AllowedHTTPMethod(method) {
		return HTTPResponse{}, fmt.Errorf("unsupported HTTP method %q", method)
	}
	timeout := req.Timeout
	if timeout <= 0 || timeout > 60*time.Second {
		timeout = 30 * time.Second
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 512 * 1024
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(reqCtx, method, parsed.String(), strings.NewReader(req.Body))
	if err != nil {
		return HTTPResponse{}, err
	}
	userAgent := req.UserAgent
	if userAgent == "" {
		userAgent = "fluxplane/0.1"
	}
	httpReq.Header.Set("User-Agent", userAgent)
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}
	retry := httptransport.DefaultRetryConfig()
	retry.RetryNonIdempotent = false
	client := &http.Client{
		Transport: httptransport.NewDefaultTransportWithRetry(PublicNetworkTransportWithTLS(n.allowPrivate, req.TLSConfig), retry),
		CheckRedirect: func(redirectReq *http.Request, _ []*http.Request) error {
			return ValidatePublicURL(redirectReq.URL, n.allowPrivate)
		},
	}
	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return HTTPResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return HTTPResponse{}, err
	}
	truncated := len(body) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}
	return HTTPResponse{
		URL: parsed.String(), FinalURL: resp.Request.URL.String(), Method: method,
		Status: resp.Status, StatusCode: resp.StatusCode, Headers: resp.Header,
		ContentType: resp.Header.Get("Content-Type"), Body: body, Truncated: truncated,
		Duration: time.Since(start),
	}, nil
}

// AllowedHTTPMethod reports whether method is enabled for the default network boundary.
func AllowedHTTPMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
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

// ProcessManager is the long-running process boundary planned for shells,
// dev servers, tests, and other streaming/background workloads.
//
// Implementations should expose stdout/stderr as events, support foreground
// attach, and allow callers to list and kill background processes. The initial
// HostProcess implementation only provides Run; this interface documents the
// target shape so operation APIs can grow without bypassing System.
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
type ProcessHandle interface {
	ID() string
	Info() ProcessInfo
	Events() <-chan ProcessEvent
	Wait(context.Context) (ProcessResult, error)
}

// ProcessInfo describes a managed process.
type ProcessInfo struct {
	ID        string            `json:"id"`
	Label     string            `json:"label,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Workdir   string            `json:"workdir,omitempty"`
	StartedAt time.Time         `json:"started_at,omitempty"`
	EndedAt   time.Time         `json:"ended_at,omitempty"`
	Running   bool              `json:"running,omitempty"`
	ExitCode  int               `json:"exit_code,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// ProcessEvent is emitted for streaming process output and lifecycle changes.
type ProcessEvent struct {
	ProcessID string    `json:"process_id"`
	Kind      string    `json:"kind"`
	Stream    string    `json:"stream,omitempty"`
	Data      string    `json:"data,omitempty"`
	Time      time.Time `json:"time,omitempty"`
}

const (
	EventProcessStarted event.Name = "process.started"
	EventProcessOutput  event.Name = "process.output"
	EventProcessExited  event.Name = "process.exited"
)

// EventName returns the runtime event name.
func (e ProcessEvent) EventName() event.Name {
	switch e.Kind {
	case "started":
		return EventProcessStarted
	case "exited":
		return EventProcessExited
	default:
		return EventProcessOutput
	}
}

// ProcessRequest describes one bounded process execution.
type ProcessRequest struct {
	Command   string
	Args      []string
	Workdir   string
	Env       []string
	Timeout   time.Duration
	Detached  bool
	MaxStdout int
	MaxStderr int
	Label     string
	Tags      []string
	Metadata  map[string]string
}

// ProcessResult is the captured process outcome.
type ProcessResult struct {
	Command         string        `json:"command"`
	Args            []string      `json:"args,omitempty"`
	Workdir         string        `json:"workdir,omitempty"`
	Stdout          string        `json:"stdout,omitempty"`
	Stderr          string        `json:"stderr,omitempty"`
	ExitCode        int           `json:"exit_code"`
	TimedOut        bool          `json:"timed_out,omitempty"`
	StdoutTruncated bool          `json:"stdout_truncated,omitempty"`
	StderrTruncated bool          `json:"stderr_truncated,omitempty"`
	Duration        time.Duration `json:"-"`
}

// ProcessOutput is a bounded output snapshot for a managed process.
type ProcessOutput struct {
	ProcessID       string `json:"process_id"`
	Stdout          string `json:"stdout,omitempty"`
	Stderr          string `json:"stderr,omitempty"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

// HostProcess executes direct host processes without a shell interpreter.
type HostProcess struct {
	workspace *HostWorkspace
	env       *WorkspaceEnvironment
	mu        sync.Mutex
	nextID    atomic.Uint64
	procs     map[string]*managedProcess
}

// NewHostProcess returns a host process manager.
func NewHostProcess(workspace *HostWorkspace) *HostProcess {
	env, _ := newWorkspaceEnvironment(workspace)
	return NewHostProcessWithEnvironment(workspace, env)
}

// NewHostProcessWithEnvironment returns a host process manager using env.
func NewHostProcessWithEnvironment(workspace *HostWorkspace, env *WorkspaceEnvironment) *HostProcess {
	return &HostProcess{workspace: workspace, env: env, procs: map[string]*managedProcess{}}
}

// Run executes one direct process and waits for completion.
func (p *HostProcess) Run(ctx context.Context, req ProcessRequest) (ProcessResult, error) {
	handle, err := p.Start(ctx, req)
	if err != nil {
		return ProcessResult{}, err
	}
	return handle.Wait(ctx)
}

// Start launches one direct process under management.
func (p *HostProcess) Start(ctx context.Context, req ProcessRequest) (ProcessHandle, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return nil, fmt.Errorf("command is empty")
	}
	if strings.ContainsAny(command, "\n\r;&|<>$`\\") {
		return nil, fmt.Errorf("shell syntax is not supported")
	}
	baseCtx := processContext(ctx, req.Detached)
	var runCtx context.Context
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(baseCtx, req.Timeout)
	} else {
		runCtx, cancel = context.WithCancel(baseCtx)
	}
	workdir := p.workspace.Root()
	processRoot := p.workspace.roots[0]
	if strings.TrimSpace(req.Workdir) != "" {
		resolved, err := p.workspace.ResolveProcessWorkdir(req.Workdir)
		if err != nil {
			cancel()
			return nil, err
		}
		info, err := os.Stat(resolved.Abs)
		if err != nil {
			cancel()
			return nil, err
		}
		if !info.IsDir() {
			cancel()
			return nil, fmt.Errorf("workdir is not a directory")
		}
		workdir = resolved.Abs
		if root, ok := p.workspace.rootForAbs(workdir); ok {
			processRoot = root
		}
	}
	cmd := exec.CommandContext(runCtx, command, req.Args...)
	cmd.Dir = workdir
	env, err := p.env.processEnv(processRoot, req.Env)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Env = env
	configureCommandProcess(cmd)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	start := time.Now()
	id := fmt.Sprintf("proc-%d", p.nextID.Add(1))
	mp := &managedProcess{
		manager: p, id: id, cmd: cmd, cancel: cancel,
		events: make(chan ProcessEvent, 128), done: make(chan struct{}),
		stdout: cappedBuffer{max: positiveOr(req.MaxStdout, 64*1024)},
		stderr: cappedBuffer{max: positiveOr(req.MaxStderr, 64*1024)},
		info: ProcessInfo{
			ID: id, Label: strings.TrimSpace(req.Label), Tags: trimStrings(req.Tags), Metadata: cloneStringMap(req.Metadata),
			Command: command, Args: append([]string(nil), req.Args...), Workdir: workdir, StartedAt: start, Running: true,
		},
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	p.mu.Lock()
	p.procs[id] = mp
	p.mu.Unlock()
	mp.emit(ProcessEvent{ProcessID: id, Kind: "started", Time: start})
	mp.wg.Add(2)
	go mp.copyOutput(stdoutPipe, "stdout")
	go mp.copyOutput(stderrPipe, "stderr")
	go mp.wait(runCtx, start)
	return mp, nil
}

func processContext(ctx context.Context, detached bool) context.Context {
	if detached || ctx == nil {
		return context.Background()
	}
	return ctx
}

// Ensure returns a running process for req.Label or starts a new one.
func (p *HostProcess) Ensure(ctx context.Context, req ProcessRequest) (ProcessHandle, bool, error) {
	label := strings.TrimSpace(req.Label)
	if label != "" {
		p.mu.Lock()
		for _, proc := range p.procs {
			info := proc.Info()
			if info.Label == label && info.Running {
				p.mu.Unlock()
				return proc, false, nil
			}
		}
		p.mu.Unlock()
	}
	handle, err := p.Start(ctx, req)
	return handle, true, err
}

// List returns known managed processes.
func (p *HostProcess) List(context.Context) ([]ProcessInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ProcessInfo, 0, len(p.procs))
	for _, proc := range p.procs {
		out = append(out, proc.Info())
	}
	return out, nil
}

// Status returns one managed process info.
func (p *HostProcess) Status(_ context.Context, id string) (ProcessInfo, error) {
	proc, err := p.lookup(id)
	if err != nil {
		return ProcessInfo{}, err
	}
	return proc.Info(), nil
}

// Output returns a bounded output snapshot for one managed process.
func (p *HostProcess) Output(_ context.Context, id string) (ProcessOutput, error) {
	proc, err := p.lookup(id)
	if err != nil {
		return ProcessOutput{}, err
	}
	return proc.Output(), nil
}

// Wait waits for one managed process to exit.
func (p *HostProcess) Wait(ctx context.Context, id string, timeout time.Duration) (ProcessResult, error) {
	proc, err := p.lookup(id)
	if err != nil {
		return ProcessResult{}, err
	}
	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(waitCtx, timeout)
		defer cancel()
	}
	return proc.Wait(waitCtx)
}

// Stop gracefully terminates a managed process.
func (p *HostProcess) Stop(_ context.Context, id string) error {
	proc, err := p.lookup(id)
	if err != nil {
		return err
	}
	proc.cancel()
	terminateCommandProcess(proc.cmd)
	return nil
}

// Kill terminates a managed process.
func (p *HostProcess) Kill(_ context.Context, id string) error {
	proc, err := p.lookup(id)
	if err != nil {
		return err
	}
	proc.cancel()
	killCommandProcess(proc.cmd)
	return nil
}

func (p *HostProcess) lookup(id string) (*managedProcess, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	proc, ok := p.procs[id]
	if ok {
		return proc, nil
	}
	for _, candidate := range p.procs {
		info := candidate.Info()
		if info.Label == id {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("process %q not found", id)
}

type managedProcess struct {
	manager *HostProcess
	id      string
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	infoMu  sync.Mutex
	info    ProcessInfo
	stdout  cappedBuffer
	stderr  cappedBuffer
	events  chan ProcessEvent
	done    chan struct{}
	wg      sync.WaitGroup
	result  ProcessResult
	err     error
}

func (p *managedProcess) ID() string { return p.id }

func (p *managedProcess) Info() ProcessInfo {
	p.infoMu.Lock()
	defer p.infoMu.Unlock()
	info := p.info
	info.Args = append([]string(nil), p.info.Args...)
	info.Tags = append([]string(nil), p.info.Tags...)
	info.Metadata = cloneStringMap(p.info.Metadata)
	return info
}

func (p *managedProcess) Events() <-chan ProcessEvent { return p.events }

func (p *managedProcess) Wait(ctx context.Context) (ProcessResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.done:
		return p.result, p.err
	case <-ctx.Done():
		return ProcessResult{}, ctx.Err()
	}
}

func (p *managedProcess) Output() ProcessOutput {
	p.stdout.mu.Lock()
	stdout := p.stdout.String()
	stdoutTruncated := p.stdout.truncated
	p.stdout.mu.Unlock()
	p.stderr.mu.Lock()
	stderr := p.stderr.String()
	stderrTruncated := p.stderr.truncated
	p.stderr.mu.Unlock()
	return ProcessOutput{ProcessID: p.id, Stdout: stdout, Stderr: stderr, StdoutTruncated: stdoutTruncated, StderrTruncated: stderrTruncated}
}

func (p *managedProcess) copyOutput(reader io.Reader, stream string) {
	defer p.wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			if stream == "stderr" {
				_, _ = p.stderr.Write(buf[:n])
			} else {
				_, _ = p.stdout.Write(buf[:n])
			}
			p.emit(ProcessEvent{ProcessID: p.id, Kind: "output", Stream: stream, Data: chunk, Time: time.Now()})
		}
		if err != nil {
			return
		}
	}
}

func (p *managedProcess) wait(ctx context.Context, start time.Time) {
	err := p.cmd.Wait()
	duration := time.Since(start)
	timedOut := ctx.Err() != nil
	if timedOut {
		killCommandProcess(p.cmd)
	}
	p.wg.Wait()
	exitCode := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
		} else if timedOut {
			exitCode = -1
		}
	}
	out := p.Output()
	p.result = ProcessResult{
		Command: p.info.Command, Args: append([]string(nil), p.info.Args...), Workdir: p.info.Workdir,
		Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: exitCode, TimedOut: timedOut,
		StdoutTruncated: out.StdoutTruncated, StderrTruncated: out.StderrTruncated, Duration: duration,
	}
	p.err = err
	if timedOut {
		p.err = ctx.Err()
	}
	ended := time.Now()
	p.infoMu.Lock()
	p.info.Running = false
	p.info.EndedAt = ended
	p.info.ExitCode = exitCode
	if p.err != nil && !errors.Is(p.err, context.Canceled) {
		p.info.Error = p.err.Error()
	}
	p.infoMu.Unlock()
	p.emit(ProcessEvent{ProcessID: p.id, Kind: "exited", Time: ended, Data: fmt.Sprintf("%d", exitCode)})
	close(p.done)
	close(p.events)
}

func (p *managedProcess) emit(event ProcessEvent) {
	select {
	case p.events <- event:
	default:
	}
}

type cappedBuffer struct {
	bytes.Buffer
	mu        sync.Mutex
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		return len(p), nil
	}
	remaining := b.max - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.Buffer.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.Buffer.Write(p)
	return len(p), nil
}

func positiveOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
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

// BrowserManager owns browser session lifecycle and automation IO.
type BrowserManager interface {
	Open(context.Context, BrowserOpenRequest) (BrowserOpenResult, error)
	Navigate(context.Context, BrowserSessionRequest) (BrowserPageResult, error)
	Click(context.Context, BrowserSelectorRequest) (BrowserPageResult, error)
	Type(context.Context, BrowserTypeRequest) (BrowserPageResult, error)
	Select(context.Context, BrowserSelectRequest) (BrowserPageResult, error)
	Read(context.Context, BrowserReadRequest) (BrowserReadResult, error)
	Screenshot(context.Context, BrowserSessionRequest) (BrowserArtifact, error)
	Evaluate(context.Context, BrowserEvaluateRequest) (BrowserEvaluateResult, error)
	Wait(context.Context, BrowserWaitRequest) (BrowserPageResult, error)
	Scroll(context.Context, BrowserScrollRequest) (BrowserPageResult, error)
	Hover(context.Context, BrowserSelectorRequest) (BrowserPageResult, error)
	Back(context.Context, BrowserSessionRequest) (BrowserPageResult, error)
	Forward(context.Context, BrowserSessionRequest) (BrowserPageResult, error)
	PDF(context.Context, BrowserSessionRequest) (BrowserArtifact, error)
	Close(context.Context, BrowserSessionRequest) error
}

type BrowserOpenRequest struct {
	URL     string
	Width   int
	Height  int
	Timeout time.Duration
}

type BrowserSessionRequest struct {
	SessionID string
	URL       string
	Timeout   time.Duration
}

type BrowserSelectorRequest struct {
	SessionID string
	Selector  string
	Timeout   time.Duration
}

type BrowserTypeRequest struct {
	SessionID string
	Selector  string
	Text      string
	Submit    bool
	Timeout   time.Duration
}

type BrowserSelectRequest struct {
	SessionID string
	Selector  string
	Values    []string
	Timeout   time.Duration
}

type BrowserReadRequest struct {
	SessionID string
	Selector  string
	Timeout   time.Duration
}

type BrowserEvaluateRequest struct {
	SessionID string
	Script    string
	Timeout   time.Duration
}

type BrowserWaitRequest struct {
	SessionID string
	Selector  string
	Duration  time.Duration
	Timeout   time.Duration
}

type BrowserScrollRequest struct {
	SessionID string
	X         int
	Y         int
	Timeout   time.Duration
}

type BrowserOpenResult struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url,omitempty"`
	Title     string `json:"title,omitempty"`
}

type BrowserPageResult struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url,omitempty"`
	Title     string `json:"title,omitempty"`
}

type BrowserReadResult struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url,omitempty"`
	Title     string `json:"title,omitempty"`
	Text      string `json:"text,omitempty"`
	HTML      string `json:"html,omitempty"`
}

type BrowserArtifact struct {
	SessionID   string `json:"session_id"`
	Path        string `json:"path,omitempty"`
	MediaType   string `json:"media_type,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
	Description string `json:"description,omitempty"`
}

type BrowserEvaluateResult struct {
	SessionID string `json:"session_id"`
	Value     any    `json:"value,omitempty"`
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

// Clarifier collects human input through a channel or terminal adapter.
type Clarifier interface {
	Clarify(context.Context, ClarifyRequest) (ClarifyResult, error)
}

type ClarifyRequest struct {
	Prompt   string          `json:"prompt"`
	Schema   json.RawMessage `json:"schema,omitempty"`
	Defaults map[string]any  `json:"defaults,omitempty"`
}

type ClarifyResult struct {
	Answer any `json:"answer,omitempty"`
}
