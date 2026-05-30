package workspace

import (
	"fmt"

	fpsystem "github.com/fluxplane/fluxplane-system"
)

// Workspace is the selected runtime workspace context. It owns Fluxplane
// workspace identity/path semantics and exposes the scoped fluxplane-system
// used for concrete IO.
type Workspace interface {
	fpsystem.PathResolver
	fpsystem.ScratchProvider
	fpsystem.BoundedFileReader
	System() fpsystem.System
	fpsystem.BoundedFileCopier
	fpsystem.BoundedFileMover
	Root() string
	Roots() []Root
}

// Root describes one runtime root exposed by a Workspace.
type Root struct {
	Name    string `json:"name,omitempty"`
	Path    string `json:"path"`
	Rel     string `json:"rel,omitempty"`
	Read    bool   `json:"read"`
	Write   bool   `json:"write"`
	Scratch bool   `json:"scratch,omitempty"`
}

// ResolvedPath is a canonical workspace path resolved against a Workspace.
type ResolvedPath = fpsystem.ResolvedPath

// ScratchDir is an isolated temporary directory owned by a Workspace.
type ScratchDir = fpsystem.ScratchDir

// PathName returns the scoped filesystem name for resolved.
func PathName(resolved ResolvedPath) string { return fpsystem.PathName(resolved) }

// FileSystem returns the scoped filesystem exposed by ws.
func FileSystem(ws Workspace) (fpsystem.FileSystem, error) {
	if ws == nil || ws.System() == nil || ws.System().FileSystem() == nil {
		return nil, fmt.Errorf("workspace filesystem is nil")
	}
	return ws.System().FileSystem(), nil
}
