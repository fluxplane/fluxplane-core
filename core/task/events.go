package task

import (
	"time"

	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-operation"
)

const (
	EventCreateRequestedName       event.Name = "task.create_requested"
	EventCreatedName               event.Name = "task.created"
	EventRevisedName               event.Name = "task.revised"
	EventStatusChangedName         event.Name = "task.status_changed"
	EventArtifactAddedName         event.Name = "task.artifact_added"
	EventArtifactUpdatedName       event.Name = "task.artifact_updated"
	EventArtifactRemovedName       event.Name = "task.artifact_removed"
	EventStepStatusChangedName     event.Name = "task.step_status_changed"
	EventIndexedName               event.Name = "task.indexed"
	EventExecutionStartedName      event.Name = "task.execution_started"
	EventExecutionLeaseRenewedName event.Name = "task.execution_lease_renewed"
	EventExecutionInterruptedName  event.Name = "task.execution_interrupted"
	EventStepDispatchedName        event.Name = "task.step_dispatched"
	EventStepProgressedName        event.Name = "task.step_progressed"
	EventStepCompletedName         event.Name = "task.step_completed"
	EventStepFailedName            event.Name = "task.step_failed"
	EventStepCancelledName         event.Name = "task.step_cancelled"
	EventExecutionCompletedName    event.Name = "task.execution_completed"
	EventExecutionFailedName       event.Name = "task.execution_failed"
	EventExecutionCancelledName    event.Name = "task.execution_cancelled"
	EventSchedulerDiagnosticName   event.Name = "task.scheduler_diagnostic"
	EventWorkerRegisteredName      event.Name = "task.worker_registered"
)

// CreateRequested records the accepted task creation request before defaults
// and validation materialize it as a task.
type CreateRequested struct {
	TaskID  ID                `json:"task_id,omitempty"`
	Request TaskCreateRequest `json:"request"`
}

func (CreateRequested) EventName() event.Name { return EventCreateRequestedName }

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

// ArtifactAdded records a task, execution, or step artifact.
type ArtifactAdded struct {
	TaskID      ID           `json:"task_id"`
	ExecutionID ExecutionID  `json:"execution_id,omitempty"`
	StepID      StepID       `json:"step_id,omitempty"`
	Artifact    ArtifactSpec `json:"artifact"`
}

func (ArtifactAdded) EventName() event.Name { return EventArtifactAddedName }

// ArtifactUpdated records a replacement artifact at the same scope.
type ArtifactUpdated struct {
	TaskID      ID           `json:"task_id"`
	ExecutionID ExecutionID  `json:"execution_id,omitempty"`
	StepID      StepID       `json:"step_id,omitempty"`
	ArtifactID  string       `json:"artifact_id"`
	Artifact    ArtifactSpec `json:"artifact"`
}

func (ArtifactUpdated) EventName() event.Name { return EventArtifactUpdatedName }

// ArtifactRemoved records an artifact removal at the same scope.
type ArtifactRemoved struct {
	TaskID      ID          `json:"task_id"`
	ExecutionID ExecutionID `json:"execution_id,omitempty"`
	StepID      StepID      `json:"step_id,omitempty"`
	ArtifactID  string      `json:"artifact_id"`
	Reason      string      `json:"reason,omitempty"`
}

func (ArtifactRemoved) EventName() event.Name { return EventArtifactRemovedName }

// StepStatusChanged records a manual step status transition.
type StepStatusChanged struct {
	TaskID      ID              `json:"task_id"`
	ExecutionID ExecutionID     `json:"execution_id,omitempty"`
	StepID      StepID          `json:"step_id"`
	Previous    StepStatus      `json:"previous,omitempty"`
	Current     StepStatus      `json:"current"`
	Reason      string          `json:"reason,omitempty"`
	Output      operation.Value `json:"output,omitempty"`
}

func (StepStatusChanged) EventName() event.Name { return EventStepStatusChangedName }

// Indexed records the latest compact task summary in the task index stream.
type Indexed struct {
	TaskID  ID          `json:"task_id"`
	Summary TaskSummary `json:"summary"`
}

func (Indexed) EventName() event.Name { return EventIndexedName }

// ExecutionStarted records a new task execution attempt.
type ExecutionStarted struct {
	TaskID      ID          `json:"task_id"`
	ExecutionID ExecutionID `json:"execution_id,omitempty"`
	Execution   Execution   `json:"execution"`
}

func (ExecutionStarted) EventName() event.Name { return EventExecutionStartedName }

// ExecutionLeaseRenewed records a refreshed scheduler execution lease.
type ExecutionLeaseRenewed struct {
	TaskID         ID          `json:"task_id"`
	ExecutionID    ExecutionID `json:"execution_id"`
	WorkerID       string      `json:"worker_id,omitempty"`
	LeaseID        string      `json:"lease_id,omitempty"`
	LeaseExpiresAt time.Time   `json:"lease_expires_at,omitempty"`
}

func (ExecutionLeaseRenewed) EventName() event.Name { return EventExecutionLeaseRenewedName }

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

// SchedulerDiagnostic records a scheduler-side diagnostic that affected a task
// but does not itself define task lifecycle state.
type SchedulerDiagnostic struct {
	TaskID      ID          `json:"task_id"`
	ExecutionID ExecutionID `json:"execution_id,omitempty"`
	StepID      StepID      `json:"step_id,omitempty"`
	Diagnostic  Diagnostic  `json:"diagnostic"`
}

func (SchedulerDiagnostic) EventName() event.Name { return EventSchedulerDiagnosticName }

// WorkerRegistered records a scheduler worker capacity registration.
type WorkerRegistered struct {
	Worker WorkerStatus `json:"worker"`
}

func (WorkerRegistered) EventName() event.Name { return EventWorkerRegisteredName }
