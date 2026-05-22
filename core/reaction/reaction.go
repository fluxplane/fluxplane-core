package reaction

import (
	"fmt"
	"strings"

	"github.com/fluxplane/engine/core/command"
	corecontext "github.com/fluxplane/engine/core/context"
	"github.com/fluxplane/engine/core/datasource"
	coreevidence "github.com/fluxplane/engine/core/evidence"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/skill"
	"github.com/fluxplane/engine/core/workflow"
)

// Mode controls when a matching rule should fire.
type Mode string

const (
	// ModeOnChange fires when a matching assertion appears or changes.
	ModeOnChange Mode = "on_change"
	// ModeEveryTurn fires on every evaluated turn while the assertion matches.
	ModeEveryTurn Mode = "every_turn"
)

// Rule maps a normalized evidence assertion to one or more inert actions.
type Rule struct {
	Name        string            `json:"name"`
	Mode        Mode              `json:"mode,omitempty"`
	When        Matcher           `json:"when"`
	Actions     []Action          `json:"actions,omitempty"`
	Description string            `json:"description,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Validate checks that a rule has a stable identity, matcher, and actions.
func (r Rule) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("reaction: rule name is empty")
	}
	if r.Mode != "" && r.Mode != ModeOnChange && r.Mode != ModeEveryTurn {
		return fmt.Errorf("reaction: rule %q mode %q is invalid", r.Name, r.Mode)
	}
	if err := r.When.Validate(); err != nil {
		return fmt.Errorf("reaction: rule %q matcher: %w", r.Name, err)
	}
	if len(r.Actions) == 0 {
		return fmt.Errorf("reaction: rule %q has no actions", r.Name)
	}
	for i, action := range r.Actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("reaction: rule %q actions[%d]: %w", r.Name, i, err)
		}
	}
	return nil
}

// Matcher selects evidence assertions. Empty fields are wildcards, but at
// least one matching field must be set.
type Matcher struct {
	Assertion string               `json:"assertion,omitempty" yaml:"assertion,omitempty"`
	Target    string               `json:"target,omitempty" yaml:"target,omitempty"`
	Subject   coreevidence.Subject `json:"subject,omitempty" yaml:"subject,omitempty"`
	Scope     string               `json:"scope,omitempty" yaml:"scope,omitempty"`
	Source    string               `json:"source,omitempty" yaml:"source,omitempty"`
	Meta      map[string]string    `json:"meta,omitempty" yaml:"meta,omitempty"`
}

// Validate checks that the matcher is not an accidental match-all rule.
func (m Matcher) Validate() error {
	if strings.TrimSpace(m.Assertion) == "" &&
		strings.TrimSpace(m.Target) == "" &&
		m.Subject.IsZero() &&
		strings.TrimSpace(m.Scope) == "" &&
		strings.TrimSpace(m.Source) == "" &&
		len(m.Meta) == 0 {
		return fmt.Errorf("empty matcher")
	}
	return nil
}

// Matches reports whether an assertion satisfies the matcher.
func (m Matcher) Matches(assertion coreevidence.Assertion) bool {
	if strings.TrimSpace(m.Assertion) != "" && strings.TrimSpace(m.Assertion) != strings.TrimSpace(assertion.Kind) {
		return false
	}
	if strings.TrimSpace(m.Target) != "" && strings.TrimSpace(m.Target) != strings.TrimSpace(assertion.Target) {
		return false
	}
	if !subjectMatches(m.Subject, assertion.Subject) {
		return false
	}
	if strings.TrimSpace(m.Scope) != "" && strings.TrimSpace(m.Scope) != strings.TrimSpace(assertion.Scope) {
		return false
	}
	if strings.TrimSpace(m.Source) != "" && strings.TrimSpace(m.Source) != strings.TrimSpace(assertion.Source) {
		return false
	}
	for key, value := range m.Meta {
		if assertion.Metadata == nil || assertion.Metadata[key] != value {
			return false
		}
	}
	return true
}

func subjectMatches(matcher, assertion coreevidence.Subject) bool {
	if matcher.IsZero() {
		return true
	}
	if strings.TrimSpace(string(matcher.Kind)) != "" && strings.TrimSpace(string(matcher.Kind)) != strings.TrimSpace(string(assertion.Kind)) {
		return false
	}
	if strings.TrimSpace(matcher.Name) != "" && strings.TrimSpace(matcher.Name) != strings.TrimSpace(assertion.Name) {
		return false
	}
	if strings.TrimSpace(matcher.ID) != "" && strings.TrimSpace(matcher.ID) != strings.TrimSpace(assertion.ID) {
		return false
	}
	return true
}

// ActionKind identifies the kind of state change or runtime request described
// by an action.
type ActionKind string

const (
	ActionActivateSkill       ActionKind = "activate_skill"
	ActionActivateReference   ActionKind = "activate_reference"
	ActionEnableActivationSet ActionKind = "enable_activation_set"
	ActionEnableOperationSet  ActionKind = "enable_operation_set"
	ActionEnableDatasource    ActionKind = "enable_datasource"
	ActionEnableContext       ActionKind = "enable_context_provider"
	ActionRunWorkflow         ActionKind = "run_workflow"
	ActionRunOperation        ActionKind = "run_operation"
	ActionRunCommand          ActionKind = "run_command"
)

// Action is an inert reaction result. Runtime/orchestration decides whether and
// how an action is applied.
type Action struct {
	Kind                ActionKind              `json:"kind" yaml:"kind"`
	Skill               skill.Ref               `json:"skill,omitempty" yaml:"skill,omitempty"`
	Reference           ReferenceAction         `json:"reference,omitempty" yaml:"reference,omitempty"`
	ActivationSet       string                  `json:"activation_set,omitempty" yaml:"activation_set,omitempty"`
	OperationSet        string                  `json:"operation_set,omitempty" yaml:"operation_set,omitempty"`
	Datasource          datasource.Ref          `json:"datasource,omitempty" yaml:"datasource,omitempty"`
	ContextProvider     corecontext.ProviderRef `json:"context_provider,omitempty" yaml:"context_provider,omitempty"`
	Workflow            WorkflowAction          `json:"workflow,omitempty" yaml:"workflow,omitempty"`
	Operation           OperationAction         `json:"operation,omitempty" yaml:"operation,omitempty"`
	Command             command.Invocation      `json:"command,omitempty" yaml:"command,omitempty"`
	RequireApproval     bool                    `json:"require_approval,omitempty" yaml:"require_approval,omitempty"`
	IdempotencyFragment string                  `json:"idempotency_fragment,omitempty" yaml:"idempotency_fragment,omitempty"`
	Metadata            map[string]string       `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Validate checks that the action carries the target required by its kind.
func (a Action) Validate() error {
	switch a.Kind {
	case ActionActivateSkill:
		if strings.TrimSpace(string(a.Skill.Name)) == "" {
			return fmt.Errorf("activate_skill requires skill.name")
		}
	case ActionActivateReference:
		if strings.TrimSpace(string(a.Reference.Skill.Name)) == "" {
			return fmt.Errorf("activate_reference requires reference.skill.name")
		}
		if !skill.ValidReferencePath(a.Reference.Path) {
			return fmt.Errorf("activate_reference path %q is invalid", a.Reference.Path)
		}
	case ActionEnableActivationSet:
		if strings.TrimSpace(a.ActivationSet) == "" {
			return fmt.Errorf("enable_activation_set requires activation_set")
		}
	case ActionEnableOperationSet:
		if strings.TrimSpace(a.OperationSet) == "" {
			return fmt.Errorf("enable_operation_set requires operation_set")
		}
	case ActionEnableDatasource:
		if strings.TrimSpace(string(a.Datasource.Name)) == "" {
			return fmt.Errorf("enable_datasource requires datasource.name")
		}
	case ActionEnableContext:
		if strings.TrimSpace(string(a.ContextProvider.Name)) == "" {
			return fmt.Errorf("enable_context_provider requires context_provider.name")
		}
	case ActionRunWorkflow:
		if strings.TrimSpace(string(a.Workflow.Name)) == "" {
			return fmt.Errorf("run_workflow requires workflow.name")
		}
	case ActionRunOperation:
		if a.Operation.Operation.IsZero() {
			return fmt.Errorf("run_operation requires operation")
		}
	case ActionRunCommand:
		if err := a.Command.Validate(); err != nil {
			return fmt.Errorf("run_command: %w", err)
		}
	default:
		return fmt.Errorf("kind %q is invalid", a.Kind)
	}
	return nil
}

// ReferenceAction activates one reference for a skill.
type ReferenceAction struct {
	Skill skill.Ref `json:"skill"`
	Path  string    `json:"path"`
}

// WorkflowAction requests a workflow run.
type WorkflowAction struct {
	Name  workflow.Name   `json:"name"`
	Input operation.Value `json:"input,omitempty"`
}

// OperationAction requests one operation invocation.
type OperationAction struct {
	Operation operation.Ref   `json:"operation"`
	Input     operation.Value `json:"input,omitempty"`
}
