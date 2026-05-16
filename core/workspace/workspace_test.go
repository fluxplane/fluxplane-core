package workspace

import "testing"

func TestWorkspaceValidateRejectsEmptyID(t *testing.T) {
	if err := (Workspace{}).Validate(); err == nil {
		t.Fatal("Validate: want error for empty workspace id")
	}
}

func TestWorkspaceValidateAcceptsSingleRootWorkspace(t *testing.T) {
	err := Workspace{
		ID:   "workspace:github:my_org/proj_a",
		Name: "proj_a",
		Roots: []Root{{
			Path: "/home/timo/my_org/proj_a",
			Kind: RootGitWorktree,
			Origins: []Origin{{
				Kind:    OriginGitHub,
				Locator: "my_org/proj_a",
			}},
		}},
		Aliases: []Alias{{
			Kind:    OriginLocal,
			Locator: "/home/timo/my_org/proj_a",
		}},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestWorkspaceValidateAcceptsMultiRootWorkspace(t *testing.T) {
	err := Workspace{
		ID:      "workspace:configured:my_org",
		Name:    "my_org",
		Members: []ID{"workspace:proj_a", "workspace:proj_b", "workspace:proj_c"},
		Roots: []Root{
			{Path: "/home/timo/my_org/proj_a", Kind: RootGitWorktree},
			{Path: "/home/timo/my_org/proj_b", Kind: RootGitWorktree},
			{Path: "/home/timo/my_org/proj_c", Kind: RootGitWorktree},
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestWorkspaceValidateRejectsInvalidOrigin(t *testing.T) {
	err := Workspace{
		ID: "workspace:bad",
		Origins: []Origin{{
			Kind: OriginGitHub,
		}},
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want invalid origin error")
	}
}

func TestWorkspaceValidateRejectsDuplicateMembers(t *testing.T) {
	err := Workspace{
		ID:      "workspace:configured:my_org",
		Members: []ID{"workspace:proj_a", "workspace:proj_a"},
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want duplicate member error")
	}
}

func TestRootValidateRejectsEmptyRoot(t *testing.T) {
	if err := (Root{}).Validate(); err == nil {
		t.Fatal("Validate: want error for empty root")
	}
}

func TestRootValidateRejectsDuplicateProjectIDs(t *testing.T) {
	err := Root{
		Path:       "/workspace",
		ProjectIDs: []string{"project:go", "project:go"},
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want duplicate project id error")
	}
}

func TestSelectionValidateRejectsEmptyActive(t *testing.T) {
	if err := (Selection{}).Validate(); err == nil {
		t.Fatal("Validate: want error for empty active workspace")
	}
}

func TestSelectionValidateRejectsDuplicateAncestors(t *testing.T) {
	err := Selection{
		Active:    "workspace:proj_b",
		Ancestors: []ID{"workspace:my_org", "workspace:my_org"},
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want duplicate ancestor error")
	}
}
