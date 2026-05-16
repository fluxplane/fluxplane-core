package workspace

import (
	"testing"

	coreworkspace "github.com/fluxplane/agentruntime/core/workspace"
)

func TestResolverExplicitWorkspaceWins(t *testing.T) {
	parent := coreworkspace.Workspace{ID: "workspace:configured:my_org", Name: "my_org"}
	child := coreworkspace.Workspace{ID: "workspace:github:my_org/proj_b", Name: "proj_b", ParentID: parent.ID, Roots: []coreworkspace.Root{{Path: "/home/timo/my_org/proj_b"}}}

	result, err := NewResolver().Resolve(ResolveRequest{
		ExplicitWorkspaceID: parent.ID,
		Declarations:        []coreworkspace.Workspace{parent, child},
		Evidence:            Evidence{CurrentPath: "/home/timo/my_org/proj_b"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Selection.Active != parent.ID {
		t.Fatalf("active = %q, want %q", result.Selection.Active, parent.ID)
	}
}

func TestResolverPathInsideChildReturnsAncestor(t *testing.T) {
	parent := coreworkspace.Workspace{ID: "workspace:configured:my_org", Name: "my_org", Members: []coreworkspace.ID{"workspace:github:my_org/proj_b"}}
	child := coreworkspace.Workspace{ID: "workspace:github:my_org/proj_b", Name: "proj_b", ParentID: parent.ID, Roots: []coreworkspace.Root{{Path: "/home/timo/my_org/proj_b"}}}

	result, err := NewResolver().Resolve(ResolveRequest{
		Declarations: []coreworkspace.Workspace{parent, child},
		Evidence:     Evidence{CurrentPath: "/home/timo/my_org/proj_b/apps/api"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Selection.Active != child.ID {
		t.Fatalf("active = %q, want %q", result.Selection.Active, child.ID)
	}
	if len(result.Selection.Ancestors) != 1 || result.Selection.Ancestors[0] != parent.ID {
		t.Fatalf("ancestors = %#v, want [%q]", result.Selection.Ancestors, parent.ID)
	}
}

func TestResolverChoosesLongestDeclaredRoot(t *testing.T) {
	parent := coreworkspace.Workspace{ID: "workspace:configured:my_org", Roots: []coreworkspace.Root{{Path: "/home/timo/my_org"}}}
	child := coreworkspace.Workspace{ID: "workspace:github:my_org/proj_b", ParentID: parent.ID, Roots: []coreworkspace.Root{{Path: "/home/timo/my_org/proj_b"}}}

	result, err := NewResolver().Resolve(ResolveRequest{
		Declarations: []coreworkspace.Workspace{parent, child},
		Evidence:     Evidence{CurrentPath: "/home/timo/my_org/proj_b"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Selection.Active != child.ID {
		t.Fatalf("active = %q, want longest matching child %q", result.Selection.Active, child.ID)
	}
}

func TestResolverInfersGitHubWorkspace(t *testing.T) {
	result, err := NewResolver().Resolve(ResolveRequest{Evidence: Evidence{
		Root: "/home/timo/my_org/proj_a",
		Origins: []coreworkspace.Origin{{
			Kind:    coreworkspace.OriginGitHub,
			Locator: "my_org/proj_a",
		}},
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := coreworkspace.ID("workspace:github:my_org/proj_a")
	if result.Selection.Active != want {
		t.Fatalf("active = %q, want %q", result.Selection.Active, want)
	}
	if len(result.Active.Aliases) != 1 || result.Active.Aliases[0].Kind != coreworkspace.OriginLocal {
		t.Fatalf("aliases = %#v, want local alias", result.Active.Aliases)
	}
	if result.Active.Durability != coreworkspace.DurabilityDurable {
		t.Fatalf("durability = %q, want durable", result.Active.Durability)
	}
}

func TestResolverMultipleOriginsFallsBackToLocal(t *testing.T) {
	result, err := NewResolver().Resolve(ResolveRequest{Evidence: Evidence{
		Root: "/home/timo/my_org/proj_a",
		Origins: []coreworkspace.Origin{
			{Kind: coreworkspace.OriginGitHub, Locator: "my_org/proj_a"},
			{Kind: coreworkspace.OriginGitLab, Locator: "mirror/proj_a"},
		},
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := coreworkspace.ID("workspace:local:/home/timo/my_org/proj_a")
	if result.Selection.Active != want {
		t.Fatalf("active = %q, want %q", result.Selection.Active, want)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != WarningMultipleOriginsNoCanonical {
		t.Fatalf("warnings = %#v, want multiple origin warning", result.Warnings)
	}
}

func TestResolverLocalFallback(t *testing.T) {
	result, err := NewResolver().Resolve(ResolveRequest{Evidence: Evidence{Root: "/home/timo/scratch/foo"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := coreworkspace.ID("workspace:local:/home/timo/scratch/foo")
	if result.Selection.Active != want {
		t.Fatalf("active = %q, want %q", result.Selection.Active, want)
	}
	if result.Active.Durability != coreworkspace.DurabilityEphemeral {
		t.Fatalf("durability = %q, want ephemeral", result.Active.Durability)
	}
}

func TestResolverNoEvidenceReturnsWarning(t *testing.T) {
	result, err := NewResolver().Resolve(ResolveRequest{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Selection.Active != "" {
		t.Fatalf("active = %q, want empty", result.Selection.Active)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != WarningNoEvidence {
		t.Fatalf("warnings = %#v, want no evidence warning", result.Warnings)
	}
}
func TestResolverInvalidDeclarationsWarnAndFallback(t *testing.T) {
	result, err := NewResolver().Resolve(ResolveRequest{
		Declarations: []coreworkspace.Workspace{{ID: "workspace:bad", Roots: []coreworkspace.Root{{}}}},
		Evidence:     Evidence{Root: "/workspace"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Selection.Active != "workspace:local:/workspace" {
		t.Fatalf("active = %q, want local fallback", result.Selection.Active)
	}
	if len(result.Warnings) == 0 || result.Warnings[0].Code != WarningInvalidDeclaration {
		t.Fatalf("warnings = %#v, want invalid declaration warning", result.Warnings)
	}
}

func TestResolverDuplicateDeclarationsWarn(t *testing.T) {
	result, err := NewResolver().Resolve(ResolveRequest{
		Declarations: []coreworkspace.Workspace{
			{ID: "workspace:configured:one", Roots: []coreworkspace.Root{{Path: "/workspace"}}},
			{ID: "workspace:configured:one", Roots: []coreworkspace.Root{{Path: "/workspace/other"}}},
		},
		Evidence: Evidence{Root: "/workspace"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Selection.Active != "workspace:configured:one" {
		t.Fatalf("active = %q, want first declaration", result.Selection.Active)
	}
	if len(result.Warnings) == 0 || result.Warnings[0].Code != WarningDuplicateDeclaration {
		t.Fatalf("warnings = %#v, want duplicate declaration warning", result.Warnings)
	}
}
