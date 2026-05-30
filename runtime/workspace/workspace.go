package workspace

import "github.com/fluxplane/fluxplane-core/runtime/system"

// Workspace is the selected runtime workspace context. It owns workspace
// identity/path semantics and exposes the scoped fluxplane-system.System used
// for concrete filesystem IO.
type Workspace = system.Workspace

// Root describes one runtime root exposed by a Workspace.
type Root = system.WorkspaceRoot

// ResolvedPath is a canonical workspace path resolved against a Workspace.
type ResolvedPath = system.ResolvedPath

// ScratchDir is an isolated temporary directory owned by a Workspace.
type ScratchDir = system.ScratchDir
