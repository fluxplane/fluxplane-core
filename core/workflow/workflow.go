package workflow

import (
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
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

// Step describes one operation node in a workflow graph.
type Step struct {
	ID             StepID            `json:"id"`
	Operation      operation.Ref     `json:"operation"`
	Input          operation.Value   `json:"input,omitempty"`
	InputMap       map[string]string `json:"input_map,omitempty"`
	DependsOn      []StepID          `json:"depends_on,omitempty"`
	When           Condition         `json:"when,omitempty"`
	Retry          RetryPolicy       `json:"retry,omitempty"`
	Timeout        time.Duration     `json:"timeout,omitempty"`
	ErrorPolicy    StepErrorPolicy   `json:"error_policy,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
}

// Condition controls whether a step should run after dependencies complete.
type Condition struct {
	StepID StepID          `json:"step_id,omitempty"`
	Equals operation.Value `json:"equals,omitempty"`
	Exists bool            `json:"exists,omitempty"`
	Not    bool            `json:"not,omitempty"`
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

// Spec is an inert workflow graph.
type Spec struct {
	Name        Name              `json:"name"`
	Description string            `json:"description,omitempty"`
	Version     string            `json:"version,omitempty"`
	Inputs      operation.Type    `json:"inputs,omitempty"`
	Outputs     operation.Type    `json:"outputs,omitempty"`
	Steps       []Step            `json:"steps,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
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
	RunID     RunID         `json:"run_id"`
	Workflow  Name          `json:"workflow"`
	StepID    StepID        `json:"step_id"`
	Operation operation.Ref `json:"operation"`
	Attempt   int           `json:"attempt,omitempty"`
}

func (StepStarted) EventName() event.Name { return EventStepStartedName }

// StepCompleted is emitted when a workflow step succeeds.
type StepCompleted struct {
	RunID     RunID           `json:"run_id"`
	Workflow  Name            `json:"workflow"`
	StepID    StepID          `json:"step_id"`
	Operation operation.Ref   `json:"operation"`
	Output    operation.Value `json:"output,omitempty"`
	Attempt   int             `json:"attempt,omitempty"`
}

func (StepCompleted) EventName() event.Name { return EventStepCompletedName }

// StepFailed is emitted when a workflow step fails.
type StepFailed struct {
	RunID     RunID            `json:"run_id"`
	Workflow  Name             `json:"workflow"`
	StepID    StepID           `json:"step_id"`
	Operation operation.Ref    `json:"operation"`
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
