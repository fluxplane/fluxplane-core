package activation

import (
	"testing"

	corecontext "github.com/fluxplane/engine/core/context"
	"github.com/fluxplane/engine/core/datasource"
	coreevidence "github.com/fluxplane/engine/core/evidence"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/skill"
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
