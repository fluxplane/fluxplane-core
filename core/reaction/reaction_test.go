package reaction

import (
	"reflect"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/command"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/skill"
	"github.com/fluxplane/fluxplane-core/core/workflow"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	"github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-operation"
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
				Kind:          ActionEnableActivationSet,
				ActivationSet: "incident.slack_thread",
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

func TestModesAndActionKinds(t *testing.T) {
	if got, want := Modes(), []Mode{ModeOnChange, ModeEveryTurn}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Modes() = %#v, want %#v", got, want)
	}
	if got, want := ActionKinds(), []ActionKind{
		ActionActivateSkill, ActionActivateReference, ActionEnableActivationSet,
		ActionEnableOperationSet, ActionEnableDatasource, ActionEnableContext,
		ActionRunWorkflow, ActionRunOperation, ActionRunCommand,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ActionKinds() = %#v, want %#v", got, want)
	}
}

func TestRuleValidateRejectsInvalidShape(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		want string
	}{
		{name: "empty name", rule: Rule{When: Matcher{Assertion: "ready"}, Actions: []Action{{Kind: ActionActivateSkill, Skill: skill.Ref{Name: "go"}}}}, want: "reaction: rule name is empty"},
		{name: "bad mode", rule: Rule{Name: "bad", Mode: "sometimes", When: Matcher{Assertion: "ready"}, Actions: []Action{{Kind: ActionActivateSkill, Skill: skill.Ref{Name: "go"}}}}, want: `reaction: rule "bad" mode "sometimes" is invalid`},
		{name: "no actions", rule: Rule{Name: "bad", When: Matcher{Assertion: "ready"}}, want: `reaction: rule "bad" has no actions`},
		{name: "bad action", rule: Rule{Name: "bad", When: Matcher{Assertion: "ready"}, Actions: []Action{{Kind: ActionEnableContext}}}, want: `reaction: rule "bad" actions[0]: enable_context_provider requires context_provider.name`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestMatcherMatchesRejectsMismatchedFields(t *testing.T) {
	assertion := coreevidence.Assertion{
		Kind:     "integration.available",
		Target:   "kubernetes",
		Subject:  coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: "kubernetes", ID: "cluster-a"},
		Scope:    "workspace:/repo",
		Source:   "scanner",
		Metadata: map[string]string{"namespace": "ai-bots"},
	}
	tests := []struct {
		name    string
		matcher Matcher
	}{
		{name: "assertion", matcher: Matcher{Assertion: "other"}},
		{name: "target", matcher: Matcher{Target: "aws"}},
		{name: "subject kind", matcher: Matcher{Subject: coreevidence.Subject{Kind: coreevidence.SubjectLanguage}}},
		{name: "subject id", matcher: Matcher{Subject: coreevidence.Subject{ID: "cluster-b"}}},
		{name: "scope", matcher: Matcher{Scope: "workspace:/other"}},
		{name: "source", matcher: Matcher{Source: "manual"}},
		{name: "missing metadata", matcher: Matcher{Meta: map[string]string{"team": "platform"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.matcher.Matches(assertion) {
				t.Fatal("Matches() = true, want false")
			}
		})
	}
}

func TestActionValidateKinds(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		want   string
	}{
		{name: "skill missing", action: Action{Kind: ActionActivateSkill}, want: "activate_skill requires skill.name"},
		{name: "reference missing skill", action: Action{Kind: ActionActivateReference, Reference: ReferenceAction{Path: "references/doc.md"}}, want: "activate_reference requires reference.skill.name"},
		{name: "activation set missing", action: Action{Kind: ActionEnableActivationSet}, want: "enable_activation_set requires activation_set"},
		{name: "operation set missing", action: Action{Kind: ActionEnableOperationSet}, want: "enable_operation_set requires operation_set"},
		{name: "datasource missing", action: Action{Kind: ActionEnableDatasource}, want: "enable_datasource requires datasource.name"},
		{name: "context missing", action: Action{Kind: ActionEnableContext}, want: "enable_context_provider requires context_provider.name"},
		{name: "workflow missing", action: Action{Kind: ActionRunWorkflow}, want: "run_workflow requires workflow.name"},
		{name: "operation missing", action: Action{Kind: ActionRunOperation}, want: "run_operation requires operation"},
		{name: "command invalid", action: Action{Kind: ActionRunCommand}, want: "run_command: command: invocation path is empty"},
		{name: "invalid kind", action: Action{Kind: "bogus"}, want: `kind "bogus" is invalid`},
		{name: "run workflow", action: Action{Kind: ActionRunWorkflow, Workflow: WorkflowAction{Name: workflow.Name("deploy")}}},
		{name: "run operation", action: Action{Kind: ActionRunOperation, Operation: OperationAction{Operation: operation.Ref{Name: "deploy"}}}},
		{name: "run command", action: Action{Kind: ActionRunCommand, Command: command.Invocation{Path: []string{"deploy"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.action.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}
