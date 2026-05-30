package workspace

import (
	"context"
	"os"

	fpsystem "github.com/fluxplane/fluxplane-system"
)

// Workspace is the selected runtime workspace context. It owns Fluxplane
// workspace identity/path semantics and exposes the scoped fluxplane-system
// used for concrete IO.
type Workspace interface {
	System() fpsystem.System
	Root() string
	Roots() []Root
	ResolveExisting(context.Context, string) (ResolvedPath, error)
	ResolveCreate(context.Context, string) (ResolvedPath, error)
	CreateScratch(context.Context, string) (ScratchDir, error)
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
type ResolvedPath struct {
	Input string `json:"input,omitempty"`
	Abs   string `json:"abs"`
	Rel   string `json:"rel"`
}

// ScratchDir is an isolated temporary directory owned by a Workspace.
type ScratchDir interface {
	Root() string
	WriteFile(context.Context, string, []byte, os.FileMode) (ResolvedPath, error)
	RemoveAll(context.Context) error
}
