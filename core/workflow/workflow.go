package workflow

import (
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/operation"
)

// Name identifies a workflow definition.
type Name string

// RunID identifies one workflow run.
type RunID string

// StepID identifies one workflow step.
type StepID string

// ActionRef is retained as an alias while migration language still refers to
// actions. New code should prefer operation references.
type ActionRef = operation.Ref

// StepKind classifies how a workflow step is dispatched.
type StepKind string

const (
	StepOperation StepKind = "operation"
	StepAgent     StepKind = "agent"
)

// StepKinds returns the stable workflow step dispatch vocabulary.
func StepKinds() []StepKind {
	return []StepKind{StepOperation, StepAgent}
}

// Step describes one node in a workflow graph.
type Step struct {
	ID             StepID            `json:"id"`
	Kind           StepKind          `json:"kind,omitempty"`
	Operation      operation.Ref     `json:"operation,omitempty"`
	Agent          agent.Ref         `json:"agent,omitempty"`
	Input          operation.Value   `json:"input,omitempty"`
	InputMap       map[string]string `json:"input_map,omitempty"`
	DependsOn      []StepID          `json:"depends_on,omitempty"`
	When           Condition         `json:"when,omitempty"`
	Retry          RetryPolicy       `json:"retry,omitempty"`
	Timeout        time.Duration     `json:"timeout,omitempty"`
	ErrorPolicy    StepErrorPolicy   `json:"error_policy,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Raw            map[string]any    `json:"raw,omitempty"`
}

// Condition controls whether a step should run after dependencies complete.
type Condition struct {
	StepID StepID          `json:"step_id,omitempty" yaml:"step_id,omitempty"`
	Equals operation.Value `json:"equals,omitempty" yaml:"equals,omitempty"`
	Exists bool            `json:"exists,omitempty" yaml:"exists,omitempty"`
	Not    bool            `json:"not,omitempty" yaml:"not,omitempty"`
}

// RetryPolicy describes retry attempts for one step.
type RetryPolicy struct {
	MaxAttempts int           `json:"max_attempts,omitempty"`
	Backoff     time.Duration `json:"backoff,omitempty"`
}

// StepErrorPolicy controls how execution handles step failure.
type StepErrorPolicy string

const (
	StepErrorFail     StepErrorPolicy = ""
	StepErrorContinue StepErrorPolicy = "continue"
)

// StepErrorPolicies returns the stable workflow step error policy vocabulary.
func StepErrorPolicies() []StepErrorPolicy {
	return []StepErrorPolicy{StepErrorFail, StepErrorContinue}
}

// Spec is an inert workflow graph.
type Spec struct {
	Name        Name              `json:"name"`
	Description string            `json:"description,omitempty"`
	Version     string            `json:"version,omitempty"`
	Inputs      operation.Type    `json:"inputs,omitempty"`
	Outputs     operation.Type    `json:"outputs,omitempty"`
	Steps       []Step            `json:"steps,omitempty"`
	Raw         map[string]any    `json:"raw,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Validate checks the workflow graph has stable step identities and supported
// dispatch refs. It does not resolve refs or prove DAG acyclicity.
func (s Spec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("workflow: spec name is empty")
	}
	seen := map[StepID]struct{}{}
	for i, step := range s.Steps {
		if strings.TrimSpace(string(step.ID)) == "" {
			return fmt.Errorf("workflow: steps[%d] id is empty", i)
		}
		if _, exists := seen[step.ID]; exists {
			return fmt.Errorf("workflow: duplicate step id %q", step.ID)
		}
		seen[step.ID] = struct{}{}
		kind := step.Kind
		if kind == "" {
			switch {
			case step.Agent.Name != "":
				kind = StepAgent
			default:
				kind = StepOperation
			}
		}
		switch kind {
		case StepOperation:
			if step.Operation.Name == "" {
				return fmt.Errorf("workflow: step %q operation is empty", step.ID)
			}
		case StepAgent:
			if step.Agent.Name == "" {
				return fmt.Errorf("workflow: step %q agent is empty", step.ID)
			}
		default:
			return fmt.Errorf("workflow: step %q kind %q is invalid", step.ID, kind)
		}
	}
	for _, step := range s.Steps {
		for _, dep := range step.DependsOn {
			if _, exists := seen[dep]; !exists {
				return fmt.Errorf("workflow: step %q depends on unknown step %q", step.ID, dep)
			}
		}
	}
	return nil
}

// Status describes the projected state of a workflow run.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

const (
	EventQueuedName        event.Name = "workflow.queued"
	EventStartedName       event.Name = "workflow.started"
	EventStepStartedName   event.Name = "workflow.step_started"
	EventStepCompletedName event.Name = "workflow.step_completed"
	EventStepFailedName    event.Name = "workflow.step_failed"
	EventCompletedName     event.Name = "workflow.completed"
	EventFailedName        event.Name = "workflow.failed"
	EventCanceledName      event.Name = "workflow.canceled"
)

// Queued is emitted when a workflow run is queued.
type Queued struct {
	RunID    RunID           `json:"run_id"`
	Workflow Name            `json:"workflow"`
	Input    operation.Value `json:"input,omitempty"`
}

func (Queued) EventName() event.Name { return EventQueuedName }

// Started is emitted when a workflow run starts.
type Started struct {
	RunID    RunID `json:"run_id"`
	Workflow Name  `json:"workflow"`
}

func (Started) EventName() event.Name { return EventStartedName }

// StepStarted is emitted when a workflow step starts.
type StepStarted struct {
	RunID     RunID           `json:"run_id"`
	Workflow  Name            `json:"workflow"`
	StepID    StepID          `json:"step_id"`
	Kind      StepKind        `json:"kind,omitempty"`
	Operation operation.Ref   `json:"operation,omitempty"`
	Agent     agent.Ref       `json:"agent,omitempty"`
	Input     operation.Value `json:"input,omitempty"`
	Attempt   int             `json:"attempt,omitempty"`
}

func (StepStarted) EventName() event.Name { return EventStepStartedName }

// StepCompleted is emitted when a workflow step succeeds.
type StepCompleted struct {
	RunID     RunID           `json:"run_id"`
	Workflow  Name            `json:"workflow"`
	StepID    StepID          `json:"step_id"`
	Kind      StepKind        `json:"kind,omitempty"`
	Operation operation.Ref   `json:"operation,omitempty"`
	Agent     agent.Ref       `json:"agent,omitempty"`
	Output    operation.Value `json:"output,omitempty"`
	Attempt   int             `json:"attempt,omitempty"`
}

func (StepCompleted) EventName() event.Name { return EventStepCompletedName }

// StepFailed is emitted when a workflow step fails.
type StepFailed struct {
	RunID     RunID            `json:"run_id"`
	Workflow  Name             `json:"workflow"`
	StepID    StepID           `json:"step_id"`
	Kind      StepKind         `json:"kind,omitempty"`
	Operation operation.Ref    `json:"operation,omitempty"`
	Agent     agent.Ref        `json:"agent,omitempty"`
	Error     *operation.Error `json:"error,omitempty"`
	Attempt   int              `json:"attempt,omitempty"`
}

func (StepFailed) EventName() event.Name { return EventStepFailedName }

// Completed is emitted when a workflow run succeeds.
type Completed struct {
	RunID    RunID           `json:"run_id"`
	Workflow Name            `json:"workflow"`
	Output   operation.Value `json:"output,omitempty"`
}

func (Completed) EventName() event.Name { return EventCompletedName }

// Failed is emitted when a workflow run fails.
type Failed struct {
	RunID    RunID            `json:"run_id"`
	Workflow Name             `json:"workflow"`
	Error    *operation.Error `json:"error,omitempty"`
}

func (Failed) EventName() event.Name { return EventFailedName }

// Canceled is emitted when a workflow run is canceled.
type Canceled struct {
	RunID    RunID            `json:"run_id"`
	Workflow Name             `json:"workflow"`
	Error    *operation.Error `json:"error,omitempty"`
}

func (Canceled) EventName() event.Name { return EventCanceledName }
