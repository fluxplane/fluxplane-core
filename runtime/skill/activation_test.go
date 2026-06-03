package skill

import (
	"context"
	"strings"
	"testing"

	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	coreskill "github.com/fluxplane/fluxplane-skill"
)

func TestActivationStateActivatesSkillsAndReferences(t *testing.T) {
	repo, err := NewRepository([]coreskill.Spec{{
		Name:        "architecture",
		Description: "Design systems.",
		Body:        "Use boundaries.",
		References: []coreskill.ReferenceSpec{{
			Path: "references/tradeoffs.md",
			Body: "Compare options.",
		}},
	}})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	state, err := NewActivationState(repo, []coreskill.Ref{{Name: "architecture"}})
	if err != nil {
		t.Fatalf("NewActivationState: %v", err)
	}
	if got := state.Status("architecture"); got != StatusBase {
		t.Fatalf("status = %q, want %q", got, StatusBase)
	}
	activated, err := state.ActivateReferences("architecture", []string{"references/tradeoffs.md"})
	if err != nil {
		t.Fatalf("ActivateReferences: %v", err)
	}
	if len(activated) != 1 || activated[0] != "references/tradeoffs.md" {
		t.Fatalf("activated refs = %#v", activated)
	}
	provider := NewContextProvider(repo, state)
	blocks, err := provider.Build(context.Background(), corecontext.Request{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var combined string
	for _, block := range blocks {
		combined += block.Content + "\n"
	}
	if !strings.Contains(combined, "Use boundaries.") || !strings.Contains(combined, "Compare options.") {
		t.Fatalf("context content = %q, want active skill and reference", combined)
	}
}

func TestActivationStateRejectsReferenceBeforeSkill(t *testing.T) {
	repo, err := NewRepository([]coreskill.Spec{{
		Name:       "architecture",
		References: []coreskill.ReferenceSpec{{Path: "references/tradeoffs.md"}},
	}})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	state, err := NewActivationState(repo, nil)
	if err != nil {
		t.Fatalf("NewActivationState: %v", err)
	}
	if _, err := state.ActivateReferences("architecture", []string{"references/tradeoffs.md"}); err == nil {
		t.Fatal("ActivateReferences error is nil, want inactive skill error")
	}
}

func TestActivationStateReferenceActivationIsAtomic(t *testing.T) {
	repo, err := NewRepository([]coreskill.Spec{{
		Name: "architecture",
		References: []coreskill.ReferenceSpec{
			{Path: "references/tradeoffs.md"},
			{Path: "references/review.md"},
		},
	}})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	state, err := NewActivationState(repo, []coreskill.Ref{{Name: "architecture"}})
	if err != nil {
		t.Fatalf("NewActivationState: %v", err)
	}
	_, err = state.ActivateReferences("architecture", []string{
		"references/tradeoffs.md",
		"references/missing.md",
	})
	if err == nil {
		t.Fatal("ActivateReferences error is nil, want missing reference error")
	}
	if refs := state.ActiveReferences("architecture"); len(refs) != 0 {
		t.Fatalf("active refs = %#v, want none after failed batch", refs)
	}
}
