package activation

import (
	"testing"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/skill"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	"github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-operation"
)

func TestReadModelAppliesFocusAndSurfaceEvents(t *testing.T) {
	var model ReadModel
	if err := model.Apply(FocusDetected{
		Objective: "Troubleshoot backend load",
		Intents:   []string{"troubleshoot"},
		Subjects:  []coreevidence.Subject{{Kind: coreevidence.SubjectIntegration, Name: "slack"}},
		Source:    SourceModelFocus,
	}); err != nil {
		t.Fatalf("Apply focus: %v", err)
	}
	if model.Focus == nil || model.Focus.Objective != "Troubleshoot backend load" {
		t.Fatalf("Focus = %#v", model.Focus)
	}
	if len(model.Focus.Subjects) != 1 || model.Focus.Subjects[0] != "slack" {
		t.Fatalf("Focus subjects = %#v", model.Focus.Subjects)
	}

	if err := model.Apply(SurfacePrepared{
		ActivationSets:   []string{"incident.slack"},
		Operations:       []operation.Ref{{Name: "slack_thread_read"}},
		OperationSets:    []string{"loki.read"},
		ContextProviders: []corecontext.ProviderRef{{Name: "surface.schema"}},
		Datasources:      []datasource.Ref{{Name: "jira"}},
		Skills:           []skill.Ref{{Name: "incident-response"}},
		InlineContexts:   []string{"incident/summary"},
		Lifetime:         LifetimeRun,
		Source:           SourceReaction,
	}); err != nil {
		t.Fatalf("Apply prepared: %v", err)
	}
	if len(model.Active.ActivationSets) != 1 || model.Active.ActivationSets[0] != "incident.slack" {
		t.Fatalf("Active activation sets = %#v", model.Active.ActivationSets)
	}
	if model.Active.Lifetime != LifetimeRun {
		t.Fatalf("Active lifetime = %q", model.Active.Lifetime)
	}

	if err := model.Apply(SurfaceExpired{
		ActivationSets: []string{"incident.slack"},
		Operations:     []operation.Ref{{Name: "slack_thread_read"}},
		OperationSets:  []string{"loki.read"},
		Lifetime:       LifetimeRun,
	}); err != nil {
		t.Fatalf("Apply expired: %v", err)
	}
	if len(model.Active.ActivationSets) != 0 || len(model.Active.Operations) != 0 || len(model.Active.OperationSets) != 0 {
		t.Fatalf("Active after expiry = %#v", model.Active)
	}
	if len(model.Recent) != 3 {
		t.Fatalf("Recent len = %d, want 3", len(model.Recent))
	}
}

func TestReadModelAppliesJSONDecodedPayloads(t *testing.T) {
	var model ReadModel
	payload := map[string]any{
		"activation_sets": []any{"incident.slack"},
		"lifetime":        "run",
		"source":          "user_command",
	}
	if err := model.ApplyNamed(EventSurfacePrepared, payload); err != nil {
		t.Fatalf("ApplyNamed() error = %v", err)
	}
	if len(model.Active.ActivationSets) != 1 || model.Active.ActivationSets[0] != "incident.slack" {
		t.Fatalf("Active activation sets = %#v", model.Active.ActivationSets)
	}
	if model.Active.Lifetime != LifetimeRun {
		t.Fatalf("Active lifetime = %q", model.Active.Lifetime)
	}
}

func TestReadModelApplyNamedCoversResolutionSkippedAndExpiry(t *testing.T) {
	var model ReadModel
	if err := model.Apply(nil); err != nil {
		t.Fatalf("Apply(nil) error = %v", err)
	}
	if err := (*ReadModel)(nil).ApplyNamed(EventFocusDetected, FocusDetected{}); err != nil {
		t.Fatalf("nil ApplyNamed error = %v", err)
	}
	if err := model.ApplyNamed(EventSurfacePrepareRequested, []byte(`{"terms":["go","test"],"objective":"raise coverage","source":"user_directive"}`)); err != nil {
		t.Fatalf("ApplyNamed prepare requested: %v", err)
	}
	if err := model.ApplyNamed(EventSurfaceResolved, SurfaceResolved{
		Resources: []ResolvedResource{
			{Kind: TargetOperation, Name: "go_test"},
			{Kind: TargetSkill, Alias: "assistant"},
		},
		UnmatchedTerms: []string{"missing"},
		Skipped:        []Diagnostic{{Term: "bad", Reason: "disabled"}},
		Diagnostics:    []Diagnostic{{Message: "resolved"}},
	}); err != nil {
		t.Fatalf("ApplyNamed resolved: %v", err)
	}
	if err := model.ApplyNamed(EventSurfacePrepareSkipped, SurfacePrepareSkipped{
		ActivationSet: "set",
		Resource:      "resource",
		Reason:        "policy",
		Source:        SourceReaction,
		Diagnostic:    Diagnostic{Reason: "policy", Message: "skipped"},
	}); err != nil {
		t.Fatalf("ApplyNamed skipped: %v", err)
	}
	prepared := SurfacePrepared{
		ActivationSets:   []string{"set", "set", " other "},
		Operations:       []operation.Ref{{Name: "b"}, {Name: "a"}, {}},
		OperationSets:    []string{"ops"},
		ContextProviders: []corecontext.ProviderRef{{Name: "ctx"}},
		Datasources:      []datasource.Ref{{Name: "ds"}},
		Skills:           []skill.Ref{{Name: "skill"}},
		References:       []ReferenceTarget{{Skill: skill.Ref{Name: "skill"}, Path: "references/a.md"}},
		InlineContexts:   []string{"inline"},
		Lifetime:         LifetimeSession,
		Diagnostics:      []Diagnostic{{Message: "prepared"}},
	}
	if err := model.ApplyNamed(EventSurfacePrepared, prepared); err != nil {
		t.Fatalf("ApplyNamed prepared: %v", err)
	}
	if len(model.Active.Operations) != 2 || model.Active.Operations[0].Name != "a" || model.Active.Operations[1].Name != "b" {
		t.Fatalf("Active operations = %#v", model.Active.Operations)
	}
	if len(model.Active.References) != 1 || model.Active.References[0].Path != "references/a.md" {
		t.Fatalf("Active references = %#v", model.Active.References)
	}
	if len(model.Diagnostics) != 4 {
		t.Fatalf("Diagnostics len = %d, want 4: %#v", len(model.Diagnostics), model.Diagnostics)
	}
	if err := model.ApplyNamed(EventSurfaceExpired, SurfaceExpired{
		ActivationSets:   []string{"set"},
		Operations:       []operation.Ref{{Name: "a"}},
		OperationSets:    []string{"ops"},
		ContextProviders: []corecontext.ProviderRef{{Name: "ctx"}},
		Datasources:      []datasource.Ref{{Name: "ds"}},
		Skills:           []skill.Ref{{Name: "skill"}},
		References:       []ReferenceTarget{{Skill: skill.Ref{Name: "skill"}, Path: "references/a.md"}},
		InlineContexts:   []string{"inline"},
		Lifetime:         LifetimeSession,
		Reason:           "done",
	}); err != nil {
		t.Fatalf("ApplyNamed expired: %v", err)
	}
	if model.Active.Lifetime != "" || len(model.Active.ContextProviders) != 0 || len(model.Active.Datasources) != 0 || len(model.Active.Skills) != 0 || len(model.Active.References) != 0 || len(model.Active.InlineContexts) != 0 {
		t.Fatalf("Active after expiry = %#v", model.Active)
	}
	if len(model.Active.ActivationSets) != 1 || model.Active.ActivationSets[0] != "other" {
		t.Fatalf("ActivationSets after expiry = %#v", model.Active.ActivationSets)
	}
}

func TestReadModelApplyNamedErrorAndTraceBounds(t *testing.T) {
	var model ReadModel
	if err := model.ApplyNamed(EventSurfacePrepared, []byte(`{"activation_sets":`)); err == nil {
		t.Fatal("ApplyNamed invalid JSON error = nil")
	}
	if err := model.ApplyNamed("unknown", map[string]any{"bad": make(chan int)}); err != nil {
		t.Fatalf("unknown event error = %v", err)
	}
	if err := model.ApplyNamed(EventSurfacePrepared, map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("ApplyNamed marshal error = nil")
	}
	for i := 0; i < 40; i++ {
		model.appendTrace(EventSurfacePrepared, "event", SourceModelPrepare)
	}
	if len(model.Recent) != 32 {
		t.Fatalf("Recent len = %d, want 32", len(model.Recent))
	}
}
