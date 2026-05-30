package golang

import (
	"context"
	"fmt"
	"io/fs"
	"time"

	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

func statWorkspacePath(ctx context.Context, ws runtimeworkspace.Workspace, rel string) (fs.FileInfo, runtimeworkspace.ResolvedPath, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, err
	}
	fsys, err := runtimeworkspace.FileSystem(ws)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, err
	}
	info, err := fsys.Stat(runtimeworkspace.PathName(resolved))
	return info, resolved, err
}

func readWorkspaceFile(ctx context.Context, ws runtimeworkspace.Workspace, rel string, maxBytes int64) ([]byte, bool, runtimeworkspace.ResolvedPath, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, false, runtimeworkspace.ResolvedPath{}, err
	}
	fsys, err := runtimeworkspace.FileSystem(ws)
	if err != nil {
		return nil, false, runtimeworkspace.ResolvedPath{}, err
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, fsys, runtimeworkspace.PathName(resolved), maxBytes)
	return data, truncated, resolved, err
}

type workspaceWalkEntry struct {
	Path    runtimeworkspace.ResolvedPath
	Name    string
	Kind    string
	Size    int64
	Mode    string
	ModTime time.Time
	Level   int
}

func walkWorkspace(ctx context.Context, ws runtimeworkspace.Workspace, rel string, opts fpsystem.WalkOptions) ([]workspaceWalkEntry, runtimeworkspace.ResolvedPath, bool, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, false, err
	}
	fsys, err := runtimeworkspace.FileSystem(ws)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, false, err
	}
	entries, truncated, err := fpsystem.Walk(ctx, fsys, runtimeworkspace.PathName(resolved), opts)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, false, err
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
		return nil, runtimeworkspace.ResolvedPath{}, false, fmt.Errorf("workspace walk resolved no readable entries")
	}
	return out, resolved, truncated, nil
}
