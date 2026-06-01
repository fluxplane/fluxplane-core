package skills

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
	coreskill "github.com/fluxplane/fluxplane-core/core/skill"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeskill "github.com/fluxplane/fluxplane-core/runtime/skill"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	coreevent "github.com/fluxplane/fluxplane-event"
)

func TestContributionsExposeDefaultActivationSet(t *testing.T) {
	bundle, err := New().Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.ActivationSets) != 1 || bundle.ActivationSets[0].Name != Name {
		t.Fatalf("activation sets = %#v, want skills set", bundle.ActivationSets)
	}
}

func TestSkillOperationActivatesSkillAndReference(t *testing.T) {
	state := testSkillState(t)
	var events []coreevent.Event
	ctx := operation.NewContext(
		runtimeskill.ContextWithState(context.Background(), state),
		coreevent.SinkFunc(func(event coreevent.Event) { events = append(events, event) }),
	)
	result := runSkillOperation(ctx, actionInput{Actions: []action{
		{Action: "activate", Skill: "architecture"},
		{Action: "activate", Skill: "architecture", References: []string{"references/tradeoffs.md"}},
	}})
	if result.IsError() {
		t.Fatalf("result = %#v, want ok", result)
	}
	if got := state.Status("architecture"); got != runtimeskill.StatusDynamic {
		t.Fatalf("status = %q, want dynamic", got)
	}
	if refs := state.ActiveReferences("architecture"); len(refs) != 1 || refs[0].Path != "references/tradeoffs.md" {
		t.Fatalf("active refs = %#v", refs)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if _, ok := events[0].(coreskill.SkillActivated); !ok {
		t.Fatalf("first event = %T, want SkillActivated", events[0])
	}
	if _, ok := events[1].(coreskill.SkillReferenceActivated); !ok {
		t.Fatalf("second event = %T, want SkillReferenceActivated", events[1])
	}
}

func TestSkillDatasourceSearchAndGet(t *testing.T) {
	state := testSkillState(t)
	if _, _, err := state.ActivateSkill("architecture"); err != nil {
		t.Fatalf("ActivateSkill: %v", err)
	}
	provider := datasourceProvider{}
	accessor, err := provider.Open(context.Background(), DatasourceSpec())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := runtimeskill.ContextWithState(context.Background(), state)
	searcher := accessor.(coredatasource.Searcher)
	result, err := searcher.Search(ctx, coredatasource.SearchRequest{Entity: SkillEntity, Query: "systems"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "architecture" {
		t.Fatalf("records = %#v, want architecture", result.Records)
	}
	getter := accessor.(coredatasource.Getter)
	record, err := getter.Get(ctx, coredatasource.GetRequest{Entity: ReferenceEntity, ID: "architecture:references/tradeoffs.md"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.Entity != ReferenceEntity || record.Metadata["skill"] != "architecture" {
		t.Fatalf("record = %#v", record)
	}
}

func testSkillState(t *testing.T) *runtimeskill.ActivationState {
	t.Helper()
	repo, err := runtimeskill.NewRepository([]coreskill.Spec{{
		Name:        "architecture",
		Description: "Design systems.",
		Body:        "Architecture guidance.",
		References: []coreskill.ReferenceSpec{{
			Path: "references/tradeoffs.md",
			Body: "Tradeoff reference.",
		}},
	}})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	state, err := runtimeskill.NewActivationState(repo, nil)
	if err != nil {
		t.Fatalf("NewActivationState: %v", err)
	}
	return state
}
