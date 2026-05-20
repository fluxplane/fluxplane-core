package reaction

import (
	"strings"
	"testing"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/skill"
)

func TestMatcherMatchesSignalFieldsAndMetadata(t *testing.T) {
	matcher := Matcher{
		Signal:  "integration.available",
		Target:  "kubernetes",
		Subject: environment.Subject{Kind: environment.SubjectIntegration, Name: "kubernetes"},
		Scope:   "workspace:/repo",
		Meta:    map[string]string{"namespace": "ai-bots"},
	}
	signal := environment.Signal{
		Kind:     "integration.available",
		Target:   "kubernetes",
		Subject:  environment.Subject{Kind: environment.SubjectIntegration, Name: "kubernetes"},
		Scope:    "workspace:/repo",
		Metadata: map[string]string{"namespace": "ai-bots"},
	}
	if !matcher.Matches(signal) {
		t.Fatal("Matches = false, want true")
	}
	signal.Metadata["namespace"] = "default"
	if matcher.Matches(signal) {
		t.Fatal("Matches = true after metadata changed, want false")
	}
	signal.Metadata["namespace"] = "ai-bots"
	signal.Subject.Name = "aws"
	if matcher.Matches(signal) {
		t.Fatal("Matches = true after subject changed, want false")
	}
}

func TestRuleValidateAcceptsSubjectMatcher(t *testing.T) {
	err := Rule{
		Name: "go-language",
		When: Matcher{Subject: environment.Subject{Kind: environment.SubjectLanguage, Name: "go"}},
		Actions: []Action{{
			Kind:         ActionEnableOperationSet,
			OperationSet: "golang.parser",
		}},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRuleValidateRejectsMatchAll(t *testing.T) {
	err := Rule{
		Name: "bad",
		Actions: []Action{{
			Kind:  ActionActivateSkill,
			Skill: skill.Ref{Name: "go"},
		}},
	}.Validate()
	if err == nil || !strings.Contains(err.Error(), "empty matcher") {
		t.Fatalf("Validate error = %v, want empty matcher", err)
	}
}

func TestRuleValidateAcceptsActivationActions(t *testing.T) {
	err := Rule{
		Name: "kubernetes",
		When: Matcher{Signal: "integration.available", Target: "kubernetes"},
		Actions: []Action{
			{
				Kind:       ActionEnableDatasource,
				Datasource: datasource.Ref{Name: "kubernetes"},
			},
			{
				Kind:            ActionEnableContext,
				ContextProvider: corecontext.ProviderRef{Name: "kubernetes.context"},
			},
			{
				Kind: ActionActivateReference,
				Reference: ReferenceAction{
					Skill: skill.Ref{Name: "kubernetes"},
					Path:  "references/kubectl.md",
				},
			},
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestActionValidateRejectsInvalidReferencePath(t *testing.T) {
	err := Action{
		Kind: ActionActivateReference,
		Reference: ReferenceAction{
			Skill: skill.Ref{Name: "docs"},
			Path:  "../secret.md",
		},
	}.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("Validate error = %v, want invalid reference path", err)
	}
}
