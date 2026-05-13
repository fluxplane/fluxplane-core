package agentdir

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fluxplane/agentruntime/core/resource"
)

func TestLoadFSLoadsEmbeddedSkillsAndReferences(t *testing.T) {
	fsys := fstest.MapFS{
		".agents/agents/main.md": {
			Data: []byte("---\nname: main\nskills: [architecture]\n---\nUse skills.\n"),
		},
		".agents/skills/architecture/SKILL.md": {
			Data: []byte("---\nname: architecture\ndescription: Architecture help\ntriggers: [design]\n---\nArchitecture body.\n"),
		},
		".agents/skills/architecture/references/tradeoffs.md": {
			Data: []byte("---\ntrigger: tradeoffs, choices\n---\nReference body.\n"),
		},
	}
	bundle, err := LoadFS(context.Background(), fsys, ".agents", resource.SourceRef{
		ID:       "embedded:test",
		Scope:    resource.ScopeEmbedded,
		Location: ".agents",
	})
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	if got, want := len(bundle.Agents), 1; got != want {
		t.Fatalf("agents len = %d, want %d", got, want)
	}
	if got, want := len(bundle.Skills), 1; got != want {
		t.Fatalf("skills len = %d, want %d", got, want)
	}
	skill := bundle.Skills[0]
	if skill.Body != "Architecture body." {
		t.Fatalf("skill body = %q", skill.Body)
	}
	if len(skill.References) != 1 || skill.References[0].Path != "references/tradeoffs.md" {
		t.Fatalf("references = %#v", skill.References)
	}
	if got := skill.References[0].Triggers; len(got) != 2 || got[0] != "tradeoffs" || got[1] != "choices" {
		t.Fatalf("reference triggers = %#v", got)
	}
}
