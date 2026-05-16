package workspace

import (
	"context"
	"testing"

	coreworkspace "github.com/fluxplane/agentruntime/core/workspace"
	"github.com/fluxplane/agentruntime/runtime/systemtest"
)

func TestManagerResolveSystemWorkspaceFallsBackToLocalRoot(t *testing.T) {
	sys := systemtest.NewMemory()
	result, err := NewManager().ResolveSystemWorkspace(context.Background(), sys.Workspace(), "")
	if err != nil {
		t.Fatalf("ResolveSystemWorkspace: %v", err)
	}
	want := coreworkspace.ID("workspace:local:/memory-workspace")
	if result.Selection.Active != want {
		t.Fatalf("active = %q, want %q", result.Selection.Active, want)
	}
}

func TestManagerResolveSystemWorkspaceUsesDeclarations(t *testing.T) {
	sys := systemtest.NewMemory()
	parent := coreworkspace.Workspace{ID: "workspace:configured:parent", Members: []coreworkspace.ID{"workspace:configured:child"}}
	child := coreworkspace.Workspace{ID: "workspace:configured:child", ParentID: parent.ID, Roots: []coreworkspace.Root{{Path: "/memory-workspace"}}}

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
	sys := systemtest.NewMemory()
	manager := NewManager()
	initial, err := manager.ResolveSystemWorkspace(context.Background(), sys.Workspace(), "")
	if err != nil {
		t.Fatalf("initial ResolveSystemWorkspace: %v", err)
	}
	if initial.Selection.Active != "workspace:local:/memory-workspace" {
		t.Fatalf("initial active = %q, want local fallback", initial.Selection.Active)
	}
	_, err = sys.Workspace().WriteFile(context.Background(), ".agents/workspaces.json", []byte(`{
		"workspaces": [
			{"id":"workspace:configured:parent","members":["workspace:configured:child"]},
			{"id":"workspace:configured:child","parent_id":"workspace:configured:parent","roots":[{"path":"/memory-workspace"}]}
		]
	}`), 0644, true)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

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
	sys := systemtest.NewMemory()
	_, err := sys.Workspace().WriteFile(context.Background(), ".agents/workspaces.json", []byte(`{"workspaces":[{"id":"workspace:bad","roots":[{}]}]}`), 0644, true)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result, err := NewManager().ResolveSystemWorkspace(context.Background(), sys.Workspace(), "")
	if err != nil {
		t.Fatalf("ResolveSystemWorkspace: %v", err)
	}
	if result.Selection.Active != "workspace:local:/memory-workspace" {
		t.Fatalf("active = %q, want local fallback", result.Selection.Active)
	}
	if len(result.Warnings) == 0 || result.Warnings[0].Code != WarningInvalidDeclaration {
		t.Fatalf("warnings = %#v, want invalid declaration warning", result.Warnings)
	}
}
