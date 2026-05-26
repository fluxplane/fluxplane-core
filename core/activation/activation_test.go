package activation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/command"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	"github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resourceaddr"
	"github.com/fluxplane/fluxplane-core/core/skill"
	"github.com/fluxplane/fluxplane-core/core/workflow"
)

func TestSetValidateAcceptsSupportedTargets(t *testing.T) {
	set := Set{
		Name:    "incident.slack_prom_jira",
		Aliases: []string{"incident"},
		Targets: []Target{
			{Kind: TargetOperation, Operation: operation.Ref{Name: "slack_thread_read"}},
			{Kind: TargetOperationSet, OperationSet: "loki.read"},
			{Kind: TargetCommand, Command: command.Path{"surface"}},
			{Kind: TargetWorkflow, Workflow: workflow.Name("incident_triage")},
			{Kind: TargetSkill, Skill: skill.Ref{Name: "incident-response"}},
			{Kind: TargetReference, Reference: ReferenceTarget{Skill: skill.Ref{Name: "incident-response"}, Path: "references/slack.md"}},
			{Kind: TargetContextProvider, ContextProvider: corecontext.ProviderRef{Name: "surface.schema"}},
			{Kind: TargetDatasource, Datasource: datasource.Ref{Name: "jira"}},
			{Kind: TargetResource, ResourceAddr: resourceaddr.Address("local:runbook:incident")},
			{Kind: TargetInlineContext, InlineContext: &ContextTarget{ID: "incident/summary", Content: "Use the incident response checklist."}},
		},
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSetValidateRejectsEmptyName(t *testing.T) {
	if err := (Set{Targets: []Target{{Kind: TargetOperation, Operation: operation.Ref{Name: "op"}}}}).Validate(); err == nil {
		t.Fatal("Validate() error = nil, want empty name error")
	}
}

func TestSetValidateRejectsDuplicateAlias(t *testing.T) {
	err := Set{
		Name:    "incident",
		Aliases: []string{"slack", "slack"},
		Targets: []Target{{Kind: TargetOperation, Operation: operation.Ref{Name: "op"}}},
	}.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("Validate() error = %v, want duplicate alias error", err)
	}
}

func TestSetValidateRejectsAliasMatchingName(t *testing.T) {
	err := Set{
		Name:    "incident",
		Aliases: []string{"incident"},
		Targets: []Target{{Kind: TargetOperation, Operation: operation.Ref{Name: "op"}}},
	}.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicates set name") {
		t.Fatalf("Validate() error = %v, want duplicate name alias error", err)
	}
}

func TestSetValidateRejectsEmptyTargets(t *testing.T) {
	if err := (Set{Name: "incident"}).Validate(); err == nil {
		t.Fatal("Validate() error = nil, want empty targets error")
	}
}

func TestTargetValidateRejectsInvalidKind(t *testing.T) {
	err := Target{Kind: TargetKind("unknown"), Operation: operation.Ref{Name: "op"}}.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("Validate() error = %v, want invalid kind error", err)
	}
}

func TestTargetValidateRequiresMatchingSingleRef(t *testing.T) {
	err := Target{Kind: TargetOperationSet, Operation: operation.Ref{Name: "op"}}.Validate()
	if err == nil || !strings.Contains(err.Error(), "requires operation_set ref") {
		t.Fatalf("Validate() error = %v, want mismatched ref error", err)
	}

	err = Target{
		Kind:         TargetOperation,
		Operation:    operation.Ref{Name: "op"},
		OperationSet: "ops",
	}.Validate()
	if err == nil || !strings.Contains(err.Error(), "exactly one populated ref") {
		t.Fatalf("Validate() error = %v, want multi-ref error", err)
	}
}

func TestTargetValidateRejectsMalformedReference(t *testing.T) {
	err := Target{
		Kind:      TargetReference,
		Reference: ReferenceTarget{Skill: skill.Ref{Name: "incident"}, Path: "../secret"},
	}.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("Validate() error = %v, want invalid reference path error", err)
	}
}

func TestTargetValidateRejectsEmptyInlineContextContent(t *testing.T) {
	err := Target{
		Kind:          TargetInlineContext,
		InlineContext: &ContextTarget{ID: "surface/empty"},
	}.Validate()
	if err == nil || !strings.Contains(err.Error(), "content is empty") {
		t.Fatalf("Validate() error = %v, want empty inline context content error", err)
	}
}

func TestSetJSONRoundTrip(t *testing.T) {
	set := Set{
		Name: "assistant.local_editing",
		Targets: []Target{{
			Kind:      TargetOperation,
			Operation: operation.Ref{Name: "file_read"},
		}},
	}
	data, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got Set
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("round-tripped Validate() error = %v", err)
	}
}
