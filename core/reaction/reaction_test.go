package reaction

import (
	"strings"
	"testing"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/datasource"
	coreevidence "github.com/fluxplane/agentruntime/core/evidence"
	"github.com/fluxplane/agentruntime/core/skill"
)

func TestMatcherMatchesAssertionFieldsAndMetadata(t *testing.T) {
	matcher := Matcher{
		Assertion: "integration.available",
		Target:    "kubernetes",
		Subject:   coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: "kubernetes"},
		Scope:     "workspace:/repo",
		Meta:      map[string]string{"namespace": "ai-bots"},
	}
	assertion := coreevidence.Assertion{
		Kind:     "integration.available",
		Target:   "kubernetes",
		Subject:  coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: "kubernetes"},
		Scope:    "workspace:/repo",
		Metadata: map[string]string{"namespace": "ai-bots"},
	}
	if !matcher.Matches(assertion) {
		t.Fatal("Matches = false, want true")
	}
	assertion.Metadata["namespace"] = "default"
	if matcher.Matches(assertion) {
		t.Fatal("Matches = true after metadata changed, want false")
	}
	assertion.Metadata["namespace"] = "ai-bots"
	assertion.Subject.Name = "aws"
	if matcher.Matches(assertion) {
		t.Fatal("Matches = true after subject changed, want false")
	}
}

func TestRuleValidateAcceptsSubjectMatcher(t *testing.T) {
	err := Rule{
		Name: "go-language",
		When: Matcher{Subject: coreevidence.Subject{Kind: coreevidence.SubjectLanguage, Name: "go"}},
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
		When: Matcher{Assertion: "integration.available", Target: "kubernetes"},
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
