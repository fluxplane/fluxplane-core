package workspace_test

import (
	"context"
	"testing"

	coreworkspace "github.com/fluxplane/fluxplane-core/core/workspace"
	. "github.com/fluxplane/fluxplane-core/runtime/workspace"
)

func TestManagerResolveSystemWorkspaceFallsBackToLocalRoot(t *testing.T) {
	sys := newTestSystem(t)
	result, err := NewManager().ResolveSystemWorkspace(context.Background(), sys.Workspace(), "")
	if err != nil {
		t.Fatalf("ResolveSystemWorkspace: %v", err)
	}
	want := coreworkspace.ID("workspace:local:" + sys.Workspace().Root())
	if result.Selection.Active != want {
		t.Fatalf("active = %q, want %q", result.Selection.Active, want)
	}
}

func TestManagerResolveSystemWorkspaceUsesDeclarations(t *testing.T) {
	sys := newTestSystem(t)
	parent := coreworkspace.Workspace{ID: "workspace:configured:parent", Members: []coreworkspace.ID{"workspace:configured:child"}}
	child := coreworkspace.Workspace{ID: "workspace:configured:child", ParentID: parent.ID, Roots: []coreworkspace.Root{{Path: sys.Workspace().Root()}}}

	result, err := NewManager(WithDeclarations(parent, child)).ResolveSystemWorkspace(context.Background(), sys.Workspace(), "")
	if err != nil {
		t.Fatalf("ResolveSystemWorkspace: %v", err)
	}
	if result.Selection.Active != child.ID {
		t.Fatalf("active = %q, want %q", result.Selection.Active, child.ID)
	}
	if len(result.Selection.Ancestors) != 1 || result.Selection.Ancestors[0] != parent.ID {
		t.Fatalf("ancestors = %#v, want [%q]", result.Selection.Ancestors, parent.ID)
	}
}
func TestManagerResolveSystemWorkspaceLoadsDeclarationsLazily(t *testing.T) {
	sys := newTestSystem(t)
	manager := NewManager()
	initial, err := manager.ResolveSystemWorkspace(context.Background(), sys.Workspace(), "")
	if err != nil {
		t.Fatalf("initial ResolveSystemWorkspace: %v", err)
	}
	if initial.Selection.Active != coreworkspace.ID("workspace:local:"+sys.Workspace().Root()) {
		t.Fatalf("initial active = %q, want local fallback", initial.Selection.Active)
	}
	writeWorkspaceDeclaration(t, sys, `{
		"workspaces": [
			{"id":"workspace:configured:parent","members":["workspace:configured:child"]},
			{"id":"workspace:configured:child","parent_id":"workspace:configured:parent","roots":[{"path":"`+sys.Workspace().Root()+`"}]}
		]
	}`)

	result, err := manager.ResolveSystemWorkspace(context.Background(), sys.Workspace(), "")
	if err != nil {
		t.Fatalf("ResolveSystemWorkspace: %v", err)
	}
	if result.Selection.Active != "workspace:configured:child" {
		t.Fatalf("active = %q, want declared child", result.Selection.Active)
	}
	if len(result.Selection.Ancestors) != 1 || result.Selection.Ancestors[0] != "workspace:configured:parent" {
		t.Fatalf("ancestors = %#v, want parent", result.Selection.Ancestors)
	}
}

func TestManagerResolveSystemWorkspaceInvalidDeclarationWarnsAndFallsBack(t *testing.T) {
	sys := newTestSystem(t)
	writeWorkspaceDeclaration(t, sys, `{"workspaces":[{"id":"workspace:bad","roots":[{}]}]}`)
	result, err := NewManager().ResolveSystemWorkspace(context.Background(), sys.Workspace(), "")
	if err != nil {
		t.Fatalf("ResolveSystemWorkspace: %v", err)
	}
	if result.Selection.Active != coreworkspace.ID("workspace:local:"+sys.Workspace().Root()) {
		t.Fatalf("active = %q, want local fallback", result.Selection.Active)
	}
	if len(result.Warnings) == 0 || result.Warnings[0].Code != WarningInvalidDeclaration {
		t.Fatalf("warnings = %#v, want invalid declaration warning", result.Warnings)
	}
}
