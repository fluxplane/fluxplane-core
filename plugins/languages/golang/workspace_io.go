package golang

import (
	"context"
	"fmt"
	"io/fs"
	"time"

	"github.com/fluxplane/fluxplane-core/runtime/system"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

func statWorkspacePath(ctx context.Context, ws system.Workspace, rel string) (fs.FileInfo, system.ResolvedPath, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, system.ResolvedPath{}, err
	}
	fsys, err := system.WorkspaceFileSystem(ws)
	if err != nil {
		return nil, system.ResolvedPath{}, err
	}
	info, err := fsys.Stat(system.WorkspacePathName(resolved))
	return info, resolved, err
}

func readWorkspaceFile(ctx context.Context, ws system.Workspace, rel string, maxBytes int64) ([]byte, bool, system.ResolvedPath, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, false, system.ResolvedPath{}, err
	}
	fsys, err := system.WorkspaceFileSystem(ws)
	if err != nil {
		return nil, false, system.ResolvedPath{}, err
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, fsys, system.WorkspacePathName(resolved), maxBytes)
	return data, truncated, resolved, err
}

type workspaceWalkEntry struct {
	Path    system.ResolvedPath
	Name    string
	Kind    string
	Size    int64
	Mode    string
	ModTime time.Time
	Level   int
}

func walkWorkspace(ctx context.Context, ws system.Workspace, rel string, opts system.WalkOptions) ([]workspaceWalkEntry, system.ResolvedPath, bool, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, system.ResolvedPath{}, false, err
	}
	fsys, err := system.WorkspaceFileSystem(ws)
	if err != nil {
		return nil, system.ResolvedPath{}, false, err
	}
	entries, truncated, err := fpsystem.Walk(ctx, fsys, system.WorkspacePathName(resolved), fpsystem.WalkOptions{
		Depth:         opts.Depth,
		ShowHidden:    opts.ShowHidden,
		MaxEntries:    opts.MaxEntries,
		FilesOnly:     opts.FilesOnly,
		SkipDirs:      opts.SkipDirs,
		FilterPattern: opts.FilterPattern,
	})
	if err != nil {
		return nil, system.ResolvedPath{}, false, err
	}
	out := make([]workspaceWalkEntry, 0, len(entries))
	for _, entry := range entries {
		path, err := ws.ResolveExisting(ctx, entry.Path)
		if err != nil {
			continue
		}
		out = append(out, workspaceWalkEntry{
			Path:    path,
			Name:    entry.Name,
			Kind:    entry.Kind,
			Size:    entry.Size,
			Mode:    entry.Mode,
			ModTime: entry.ModTime,
			Level:   entry.Level,
		})
	}
	if len(out) == 0 && len(entries) > 0 {
		return nil, system.ResolvedPath{}, false, fmt.Errorf("workspace walk resolved no readable entries")
	}
	return out, resolved, truncated, nil
}
