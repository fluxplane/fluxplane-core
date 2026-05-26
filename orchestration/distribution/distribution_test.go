package distribution

import "testing"

func TestCloneWorkspaceConfigCopiesMutableSlices(t *testing.T) {
	original := WorkspaceConfig{
		Roots:       []WorkspaceRoot{{Name: "api", Path: "../api", EnvFiles: []string{"api.env"}}},
		ScratchRoot: " tmp ",
		EnvFiles:    []string{".env"},
	}

	clone := CloneWorkspaceConfig(original)
	clone.Roots[0].EnvFiles[0] = "changed.env"
	clone.EnvFiles[0] = "changed"

	if original.Roots[0].EnvFiles[0] != "api.env" {
		t.Fatalf("root env file mutated original: %#v", original.Roots[0].EnvFiles)
	}
	if original.EnvFiles[0] != ".env" {
		t.Fatalf("env file mutated original: %#v", original.EnvFiles)
	}
	if clone.ScratchRoot != "tmp" {
		t.Fatalf("scratch root = %q, want trimmed tmp", clone.ScratchRoot)
	}
}

func TestMergeWorkspaceConfigAppendsOverrides(t *testing.T) {
	merged := MergeWorkspaceConfig(
		WorkspaceConfig{
			Roots:       []WorkspaceRoot{{Name: "api", Path: "../api"}},
			ScratchRoot: "base",
			EnvFiles:    []string{"base.env"},
		},
		WorkspaceConfig{
			Roots:       []WorkspaceRoot{{Name: "web", Path: "../web", EnvFiles: []string{"web.env"}}},
			ScratchRoot: " override ",
			EnvFiles:    []string{"override.env"},
		},
	)

	if len(merged.Roots) != 2 || merged.Roots[1].Name != "web" {
		t.Fatalf("roots = %#v, want base plus override", merged.Roots)
	}
	if got := merged.ScratchRoot; got != "override" {
		t.Fatalf("scratch root = %q, want override", got)
	}
	if len(merged.EnvFiles) != 2 || merged.EnvFiles[0] != "base.env" || merged.EnvFiles[1] != "override.env" {
		t.Fatalf("env files = %#v, want appended files", merged.EnvFiles)
	}
}

func TestTrimStrings(t *testing.T) {
	got := TrimStrings([]string{" .env ", "", "  ", "local.env"})
	if len(got) != 2 || got[0] != ".env" || got[1] != "local.env" {
		t.Fatalf("TrimStrings = %#v, want non-empty trimmed values", got)
	}
}

func TestParseWorkspaceRoots(t *testing.T) {
	roots, err := ParseWorkspaceRoots([]string{"../api", "web=../web"})
	if err != nil {
		t.Fatalf("ParseWorkspaceRoots: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("roots len = %d, want 2", len(roots))
	}
	if roots[0].Name != "api" || roots[0].Path != "../api" || roots[0].Access != "read_write" {
		t.Fatalf("root[0] = %#v", roots[0])
	}
	if roots[1].Name != "web" || roots[1].Path != "../web" || roots[1].Access != "read_write" {
		t.Fatalf("root[1] = %#v", roots[1])
	}
}

func TestParseWorkspaceRootsRejectsInvalidValues(t *testing.T) {
	for _, values := range [][]string{
		{""},
		{"=../api"},
		{"api="},
		{"@api=../api"},
		{"api=../api", "api=../other"},
	} {
		if _, err := ParseWorkspaceRoots(values); err == nil {
			t.Fatalf("ParseWorkspaceRoots(%#v) succeeded, want error", values)
		}
	}
}
