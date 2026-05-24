package workspace

import (
	"context"
	"fmt"

	coreworkspace "github.com/fluxplane/fluxplane-core/core/workspace"
	"github.com/fluxplane/fluxplane-core/runtime/system"
)

// Manager resolves workspace selections for runtime systems.
type Manager struct {
	resolver     Resolver
	loader       DeclarationLoader
	declarations []coreworkspace.Workspace
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithDeclarations configures declared workspaces used during resolution.
func WithDeclarations(declarations ...coreworkspace.Workspace) ManagerOption {
	return func(m *Manager) {
		m.declarations = append(m.declarations, declarations...)
	}
}

// NewManager returns a workspace manager using the default resolver.
func NewManager(options ...ManagerOption) *Manager {
	m := &Manager{resolver: NewResolver(), loader: NewDeclarationLoader()}
	for _, option := range options {
		if option != nil {
			option(m)
		}
	}
	return m
}

// ResolveSystemWorkspace resolves the active workspace from a system workspace
// root, declaration files, configured declarations, and optional explicit
// workspace id.
func (m *Manager) ResolveSystemWorkspace(ctx context.Context, ws system.Workspace, explicit coreworkspace.ID) (ResolveResult, error) {
	if err := ctx.Err(); err != nil {
		return ResolveResult{}, err
	}
	if ws == nil {
		return ResolveResult{}, fmt.Errorf("workspace: system workspace is nil")
	}
	resolver := NewResolver()
	loader := NewDeclarationLoader()
	declarations := []coreworkspace.Workspace(nil)
	if m != nil {
		resolver = m.resolver
		loader = m.loader
		declarations = append(declarations, m.declarations...)
	}
	loaded, warnings, err := loader.Load(ctx, ws, 0)
	if err != nil {
		return ResolveResult{}, err
	}
	declarations = append(declarations, loaded...)
	result, err := resolver.Resolve(ResolveRequest{
		ExplicitWorkspaceID: explicit,
		Declarations:        declarations,
		Evidence:            Evidence{Root: ws.Root()},
	})
	result.Warnings = append(warnings, result.Warnings...)
	return result, err
}
