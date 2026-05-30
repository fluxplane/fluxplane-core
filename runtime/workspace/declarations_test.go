package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	coreworkspace "github.com/fluxplane/fluxplane-core/core/workspace"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
)

func TestParseDeclarationsObject(t *testing.T) {
	decls, err := runtimeworkspace.ParseDeclarations([]byte(`{
		"workspaces": [{
			"id": "workspace:configured:parent",
			"members": ["workspace:configured:child"]
		}]
	}`))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(decls) != 1 || decls[0].ID != "workspace:configured:parent" {
		t.Fatalf("decls = %#v", decls)
	}
}

func TestParseDeclarationsArray(t *testing.T) {
	decls, err := runtimeworkspace.ParseDeclarations([]byte(`[{"id":"workspace:configured:child","roots":[{"path":"/workspace"}]}]`))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(decls) != 1 || decls[0].Roots[0].Path != "/workspace" {
		t.Fatalf("decls = %#v", decls)
	}
}

func TestParseDeclarationsEmpty(t *testing.T) {
	decls, err := runtimeworkspace.ParseDeclarations([]byte(`  `))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(decls) != 0 {
		t.Fatalf("decls = %#v, want empty", decls)
	}
}

func TestDeclarationsUseWorkspaceShape(t *testing.T) {
	decls, err := runtimeworkspace.ParseDeclarations([]byte(`[{"id":"workspace:configured:child","roots":[{"path":"/workspace","project_ids":["project:."]}]}]`))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if got := decls[0].Roots[0].ProjectIDs; len(got) != 1 || got[0] != "project:." {
		t.Fatalf("project ids = %#v", got)
	}
	if err := (coreworkspace.Workspace{ID: decls[0].ID, Roots: decls[0].Roots}).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDeclarationLoaderDefaultsDeclarationsDurable(t *testing.T) {
	sys := newTestSystem(t)
	writeWorkspaceDeclaration(t, sys, `[{"id":"workspace:configured:child","roots":[{"path":"`+sys.Workspace().Root()+`"}]}]`)
	decls, warnings, err := runtimeworkspace.NewDeclarationLoader().Load(context.Background(), sys.Workspace(), 0)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
	if len(decls) != 1 || decls[0].Durability != coreworkspace.DurabilityDurable {
		t.Fatalf("decls = %#v, want durable declaration", decls)
	}
}

func TestDeclarationLoaderWarnsOnInvalidDeclaration(t *testing.T) {
	sys := newTestSystem(t)
	writeWorkspaceDeclaration(t, sys, `[{"id":"workspace:bad","roots":[{}]}]`)
	decls, warnings, err := runtimeworkspace.NewDeclarationLoader().Load(context.Background(), sys.Workspace(), 0)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(decls) != 0 {
		t.Fatalf("decls = %#v, want none", decls)
	}
	if len(warnings) != 1 || warnings[0].Code != runtimeworkspace.WarningInvalidDeclaration {
		t.Fatalf("warnings = %#v, want invalid declaration", warnings)
	}
}

func newTestSystem(t *testing.T) *runtimeworkspace.Host {
	t.Helper()
	sys, err := runtimeworkspace.NewHost(runtimeworkspace.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return sys
}

func writeWorkspaceDeclaration(t *testing.T, sys *runtimeworkspace.Host, data string) {
	t.Helper()
	dir := filepath.Join(sys.Workspace().Root(), ".agents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspaces.json"), []byte(data), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
