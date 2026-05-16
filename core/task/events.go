package task

import (
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
)

const (
	EventCreatedName              event.Name = "task.created"
	EventRevisedName              event.Name = "task.revised"
	EventStatusChangedName        event.Name = "task.status_changed"
	EventExecutionStartedName     event.Name = "task.execution_started"
	EventExecutionInterruptedName event.Name = "task.execution_interrupted"
	EventStepDispatchedName       event.Name = "task.step_dispatched"
	EventStepProgressedName       event.Name = "task.step_progressed"
	EventStepCompletedName        event.Name = "task.step_completed"
	EventStepFailedName           event.Name = "task.step_failed"
	EventStepCancelledName        event.Name = "task.step_cancelled"
	EventExecutionCompletedName   event.Name = "task.execution_completed"
	EventExecutionFailedName      event.Name = "task.execution_failed"
	EventExecutionCancelledName   event.Name = "task.execution_cancelled"
)

// Created records a new task.
type Created struct {
	TaskID ID   `json:"task_id"`
	Task   Task `json:"task"`
}

func (Created) EventName() event.Name { return EventCreatedName }

// Revised records replacement of task metadata or steps.
type Revised struct {
	TaskID ID     `json:"task_id"`
	Task   Task   `json:"task"`
	Reason string `json:"reason,omitempty"`
}

func (Revised) EventName() event.Name { return EventRevisedName }

// StatusChanged records a task status transition.
type StatusChanged struct {
	TaskID   ID     `json:"task_id"`
	Previous Status `json:"previous,omitempty"`
	Current  Status `json:"current"`
	Reason   string `json:"reason,omitempty"`
}

func (StatusChanged) EventName() event.Name { return EventStatusChangedName }

// ExecutionStarted records a new task execution attempt.
type ExecutionStarted struct {
	TaskID      ID          `json:"task_id"`
	ExecutionID ExecutionID `json:"execution_id,omitempty"`
	Execution   Execution   `json:"execution"`
}

func (ExecutionStarted) EventName() event.Name { return EventExecutionStartedName }

// ExecutionInterrupted records a resumable execution interruption.
type ExecutionInterrupted struct {
	TaskID      ID          `json:"task_id"`
	ExecutionID ExecutionID `json:"execution_id"`
	Reason      string      `json:"reason,omitempty"`
}

func (ExecutionInterrupted) EventName() event.Name { return EventExecutionInterruptedName }

// StepDispatched records a task step being assigned to an external runner.
type StepDispatched struct {
	TaskID      ID          `json:"task_id"`
	ExecutionID ExecutionID `json:"execution_id"`
	StepID      StepID      `json:"step_id"`
	Title       string      `json:"title,omitempty"`
	Assignee    Role        `json:"assignee,omitempty"`
	Profile     string      `json:"profile,omitempty"`
	ExternalID  string      `json:"external_id,omitempty"`
}

func (StepDispatched) EventName() event.Name { return EventStepDispatchedName }

// StepProgressed records a progress update from a task step runner.
type StepProgressed struct {
	TaskID      ID          `json:"task_id"`
	ExecutionID ExecutionID `json:"execution_id"`
	StepID      StepID      `json:"step_id"`
	Message     string      `json:"message,omitempty"`
}

func (StepProgressed) EventName() event.Name { return EventStepProgressedName }

// StepCompleted records a successful task step.
type StepCompleted struct {
	TaskID      ID              `json:"task_id"`
	ExecutionID ExecutionID     `json:"execution_id"`
	StepID      StepID          `json:"step_id"`
	Output      operation.Value `json:"output,omitempty"`
}

func (StepCompleted) EventName() event.Name { return EventStepCompletedName }

// StepFailed records a failed task step.
type StepFailed struct {
	TaskID      ID               `json:"task_id"`
	ExecutionID ExecutionID      `json:"execution_id"`
	StepID      StepID           `json:"step_id"`
	Error       *operation.Error `json:"error,omitempty"`
}

func (StepFailed) EventName() event.Name { return EventStepFailedName }

// StepCancelled records a cancelled task step.
type StepCancelled struct {
	TaskID      ID          `json:"task_id"`
	ExecutionID ExecutionID `json:"execution_id"`
	StepID      StepID      `json:"step_id"`
	Reason      string      `json:"reason,omitempty"`
}

func (StepCancelled) EventName() event.Name { return EventStepCancelledName }

// ExecutionCompleted records successful task execution completion.
type ExecutionCompleted struct {
	TaskID      ID              `json:"task_id"`
	ExecutionID ExecutionID     `json:"execution_id"`
	Output      operation.Value `json:"output,omitempty"`
}

func (ExecutionCompleted) EventName() event.Name { return EventExecutionCompletedName }

// ExecutionFailed records failed task execution completion.
type ExecutionFailed struct {
	TaskID      ID               `json:"task_id"`
	ExecutionID ExecutionID      `json:"execution_id"`
	Error       *operation.Error `json:"error,omitempty"`
}

func (ExecutionFailed) EventName() event.Name { return EventExecutionFailedName }

// ExecutionCancelled records cancelled task execution completion.
type ExecutionCancelled struct {
	TaskID      ID          `json:"task_id"`
	ExecutionID ExecutionID `json:"execution_id"`
	Reason      string      `json:"reason,omitempty"`
}

func (ExecutionCancelled) EventName() event.Name { return EventExecutionCancelledName }
