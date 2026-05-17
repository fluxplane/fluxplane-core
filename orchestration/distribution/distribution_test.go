package distribution

import "testing"

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
