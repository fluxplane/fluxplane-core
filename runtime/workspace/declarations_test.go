package workspace

import (
	"context"
	"testing"

	coreworkspace "github.com/fluxplane/agentruntime/core/workspace"
	"github.com/fluxplane/agentruntime/runtime/systemtest"
)

func TestParseDeclarationsObject(t *testing.T) {
	decls, err := ParseDeclarations([]byte(`{
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
	decls, err := ParseDeclarations([]byte(`[{"id":"workspace:configured:child","roots":[{"path":"/workspace"}]}]`))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(decls) != 1 || decls[0].Roots[0].Path != "/workspace" {
		t.Fatalf("decls = %#v", decls)
	}
}

func TestParseDeclarationsEmpty(t *testing.T) {
	decls, err := ParseDeclarations([]byte(`  `))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(decls) != 0 {
		t.Fatalf("decls = %#v, want empty", decls)
	}
}

func TestDeclarationsUseWorkspaceShape(t *testing.T) {
	decls, err := ParseDeclarations([]byte(`[{"id":"workspace:configured:child","roots":[{"path":"/workspace","project_ids":["project:."]}]}]`))
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
	sys := systemtest.NewMemory()
	_, err := sys.Workspace().WriteFile(context.Background(), ".agents/workspaces.json", []byte(`[{"id":"workspace:configured:child","roots":[{"path":"/memory-workspace"}]}]`), 0644, true)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	decls, warnings, err := NewDeclarationLoader().Load(context.Background(), sys.Workspace(), 0)
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
	sys := systemtest.NewMemory()
	_, err := sys.Workspace().WriteFile(context.Background(), ".agents/workspaces.json", []byte(`[{"id":"workspace:bad","roots":[{}]}]`), 0644, true)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	decls, warnings, err := NewDeclarationLoader().Load(context.Background(), sys.Workspace(), 0)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(decls) != 0 {
		t.Fatalf("decls = %#v, want none", decls)
	}
	if len(warnings) != 1 || warnings[0].Code != WarningInvalidDeclaration {
		t.Fatalf("warnings = %#v, want invalid declaration", warnings)
	}
}
