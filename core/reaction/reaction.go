package reaction

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/workflow"
)

// Mode controls when a matching rule should fire.
type Mode string

const (
	// ModeOnChange fires when a matching signal appears or changes.
	ModeOnChange Mode = "on_change"
	// ModeEveryTurn fires on every evaluated turn while the signal matches.
	ModeEveryTurn Mode = "every_turn"
)

// Rule maps a normalized environment signal to one or more inert actions.
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

// Matcher selects environment signals. Empty fields are wildcards, but at
// least one matching field must be set.
type Matcher struct {
	Signal string            `json:"signal,omitempty"`
	Target string            `json:"target,omitempty"`
	Scope  string            `json:"scope,omitempty"`
	Source string            `json:"source,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
}

// Validate checks that the matcher is not an accidental match-all rule.
func (m Matcher) Validate() error {
	if strings.TrimSpace(m.Signal) == "" &&
		strings.TrimSpace(m.Target) == "" &&
		strings.TrimSpace(m.Scope) == "" &&
		strings.TrimSpace(m.Source) == "" &&
		len(m.Meta) == 0 {
		return fmt.Errorf("empty matcher")
	}
	return nil
}

// Matches reports whether a signal satisfies the matcher.
func (m Matcher) Matches(signal environment.Signal) bool {
	if strings.TrimSpace(m.Signal) != "" && strings.TrimSpace(m.Signal) != strings.TrimSpace(signal.Kind) {
		return false
	}
	if strings.TrimSpace(m.Target) != "" && strings.TrimSpace(m.Target) != strings.TrimSpace(signal.Target) {
		return false
	}
	if strings.TrimSpace(m.Scope) != "" && strings.TrimSpace(m.Scope) != strings.TrimSpace(signal.Scope) {
		return false
	}
	if strings.TrimSpace(m.Source) != "" && strings.TrimSpace(m.Source) != strings.TrimSpace(signal.Source) {
		return false
	}
	for key, value := range m.Meta {
		if signal.Metadata == nil || signal.Metadata[key] != value {
			return false
		}
	}
	return true
}

// ActionKind identifies the kind of state change or runtime request described
// by an action.
type ActionKind string

const (
	ActionActivateSkill      ActionKind = "activate_skill"
	ActionActivateReference  ActionKind = "activate_reference"
	ActionEnableOperationSet ActionKind = "enable_operation_set"
	ActionEnableDatasource   ActionKind = "enable_datasource"
	ActionEnableContext      ActionKind = "enable_context_provider"
	ActionRunWorkflow        ActionKind = "run_workflow"
	ActionRunOperation       ActionKind = "run_operation"
	ActionRunCommand         ActionKind = "run_command"
)

// Action is an inert reaction result. Runtime/orchestration decides whether and
// how an action is applied.
type Action struct {
	Kind                ActionKind              `json:"kind" yaml:"kind"`
	Skill               skill.Ref               `json:"skill,omitempty" yaml:"skill,omitempty"`
	Reference           ReferenceAction         `json:"reference,omitempty" yaml:"reference,omitempty"`
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
