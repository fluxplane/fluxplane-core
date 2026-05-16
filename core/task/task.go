// Package task defines inert user-facing task and execution models.
package task

import (
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/project"
	"github.com/fluxplane/agentruntime/core/workflow"
	"github.com/fluxplane/agentruntime/core/workspace"
)

// ID identifies one user-facing task.
type ID string

// ExecutionID identifies one attempt to execute a task.
type ExecutionID string

// StepID identifies one task execution step.
type StepID string

// Role identifies a user, agent profile, team, or external owner role.
type Role string

// Priority classifies task ordering hints.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

// Status describes user-facing task state.
type Status string

const (
	StatusDraft       Status = "draft"
	StatusReady       Status = "ready"
	StatusRunning     Status = "running"
	StatusBlocked     Status = "blocked"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusInterrupted Status = "interrupted"
)

// StepStatus describes execution state for one task step.
type StepStatus string

const (
	StepStatusWaiting   StepStatus = "waiting"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusCancelled StepStatus = "cancelled"
)

// Ref points at a task.
type Ref struct {
	ID ID `json:"id"`
}

// Task is the durable, user-facing work item. It describes what should be
// achieved; execution state belongs in Execution.
type Task struct {
	ID                 ID                `json:"id,omitempty"`
	Title              string            `json:"title,omitempty"`
	Description        string            `json:"description,omitempty"`
	Objective          string            `json:"objective,omitempty"`
	AcceptanceCriteria []string          `json:"acceptance_criteria,omitempty"`
	Status             Status            `json:"status,omitempty"`
	Priority           Priority          `json:"priority,omitempty"`
	Assignee           Role              `json:"assignee,omitempty"`
	Creator            Role              `json:"creator,omitempty"`
	Owner              Role              `json:"owner,omitempty"`
	WorkspaceID        workspace.ID      `json:"workspace_id,omitempty"`
	ProjectID          project.ID        `json:"project_id,omitempty"`
	WorkflowRef        workflow.Name     `json:"workflow_ref,omitempty"`
	Workflow           *workflow.Spec    `json:"workflow,omitempty"`
	Steps              []Step            `json:"steps,omitempty"`
	Labels             []string          `json:"labels,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	CreatedAt          time.Time         `json:"created_at,omitempty"`
	UpdatedAt          time.Time         `json:"updated_at,omitempty"`
}

// Step is one user-facing unit of work inside a task.
type Step struct {
	ID                 StepID            `json:"id"`
	Title              string            `json:"title,omitempty"`
	Description        string            `json:"description,omitempty"`
	Objective          string            `json:"objective,omitempty"`
	AcceptanceCriteria []string          `json:"acceptance_criteria,omitempty"`
	DependsOn          []StepID          `json:"depends_on,omitempty"`
	Assignee           Role              `json:"assignee,omitempty"`
	Profile            string            `json:"profile,omitempty"`
	Scope              []string          `json:"scope,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// Execution records one attempt to execute a task.
type Execution struct {
	ID          ExecutionID              `json:"id,omitempty"`
	TaskID      ID                       `json:"task_id,omitempty"`
	Status      Status                   `json:"status,omitempty"`
	Steps       map[StepID]StepExecution `json:"steps,omitempty"`
	WorkflowRef workflow.Name            `json:"workflow_ref,omitempty"`
	Workflow    *workflow.Spec           `json:"workflow,omitempty"`
	StartedAt   time.Time                `json:"started_at,omitempty"`
	CompletedAt time.Time                `json:"completed_at,omitempty"`
	Output      operation.Value          `json:"output,omitempty"`
	Error       *operation.Error         `json:"error,omitempty"`
	Metadata    map[string]string        `json:"metadata,omitempty"`
}

// StepExecution records execution state for one task step.
type StepExecution struct {
	StepID       StepID            `json:"step_id,omitempty"`
	Status       StepStatus        `json:"status,omitempty"`
	Assignee     Role              `json:"assignee,omitempty"`
	Profile      string            `json:"profile,omitempty"`
	ExternalID   string            `json:"external_id,omitempty"`
	StartedAt    time.Time         `json:"started_at,omitempty"`
	UpdatedAt    time.Time         `json:"updated_at,omitempty"`
	CompletedAt  time.Time         `json:"completed_at,omitempty"`
	LastProgress string            `json:"last_progress,omitempty"`
	Output       operation.Value   `json:"output,omitempty"`
	Error        *operation.Error  `json:"error,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ExecutionAction classifies a requested execution control action.
type ExecutionAction string

const (
	ExecutionActionRun      ExecutionAction = "run"
	ExecutionActionContinue ExecutionAction = "continue"
	ExecutionActionCancel   ExecutionAction = "cancel"
)

// ExecutionRequest asks runtime/orchestration to control task execution.
type ExecutionRequest struct {
	TaskID      ID              `json:"task_id,omitempty"`
	ExecutionID ExecutionID     `json:"execution_id,omitempty"`
	Action      ExecutionAction `json:"action,omitempty"`
	WorkflowRef workflow.Name   `json:"workflow_ref,omitempty"`
	Workflow    *workflow.Spec  `json:"workflow,omitempty"`
	Input       operation.Value `json:"input,omitempty"`
	DryRun      bool            `json:"dry_run,omitempty"`
	Reason      string          `json:"reason,omitempty"`
}

// ExecutionResult reports an execution control outcome.
type ExecutionResult struct {
	TaskID      ID               `json:"task_id,omitempty"`
	ExecutionID ExecutionID      `json:"execution_id,omitempty"`
	Status      Status           `json:"status,omitempty"`
	Output      operation.Value  `json:"output,omitempty"`
	Summary     string           `json:"summary,omitempty"`
	Error       *operation.Error `json:"error,omitempty"`
	Diagnostics []Diagnostic     `json:"diagnostics,omitempty"`
	StartedAt   time.Time        `json:"started_at,omitempty"`
	CompletedAt time.Time        `json:"completed_at,omitempty"`
}

// Diagnostic records a non-fatal task execution note.
type Diagnostic struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Target  string `json:"target,omitempty"`
}

// Validate checks task structure without resolving owners or workflow refs.
func (t Task) Validate() error {
	if strings.TrimSpace(t.Title) == "" && strings.TrimSpace(t.Objective) == "" {
		return fmt.Errorf("task: title or objective is required")
	}
	if t.Status != "" && !ValidStatus(t.Status) {
		return fmt.Errorf("task: status %q is invalid", t.Status)
	}
	if t.Priority != "" && !ValidPriority(t.Priority) {
		return fmt.Errorf("task: priority %q is invalid", t.Priority)
	}
	seen := map[StepID]bool{}
	for i, step := range t.Steps {
		if strings.TrimSpace(string(step.ID)) == "" {
			return fmt.Errorf("task: steps[%d] id is empty", i)
		}
		if seen[step.ID] {
			return fmt.Errorf("task: duplicate step id %q", step.ID)
		}
		seen[step.ID] = true
	}
	for _, step := range t.Steps {
		for _, dep := range step.DependsOn {
			if !seen[dep] {
				return fmt.Errorf("task: step %q depends on unknown step %q", step.ID, dep)
			}
		}
	}
	if err := validateAcyclic(t.Steps); err != nil {
		return err
	}
	if t.Workflow != nil {
		if err := t.Workflow.Validate(); err != nil {
			return fmt.Errorf("task: workflow: %w", err)
		}
	}
	return nil
}

// Validate checks execution structure without resolving runners.
func (e Execution) Validate() error {
	if strings.TrimSpace(string(e.ID)) == "" {
		return fmt.Errorf("task: execution id is empty")
	}
	if strings.TrimSpace(string(e.TaskID)) == "" {
		return fmt.Errorf("task: execution task_id is empty")
	}
	if e.Status != "" && !ValidStatus(e.Status) {
		return fmt.Errorf("task: execution status %q is invalid", e.Status)
	}
	for id, step := range e.Steps {
		if step.StepID != "" && step.StepID != id {
			return fmt.Errorf("task: execution step key %q does not match step_id %q", id, step.StepID)
		}
		if step.Status != "" && !ValidStepStatus(step.Status) {
			return fmt.Errorf("task: execution step %q status %q is invalid", id, step.Status)
		}
	}
	if e.Workflow != nil {
		if err := e.Workflow.Validate(); err != nil {
			return fmt.Errorf("task: execution workflow: %w", err)
		}
	}
	return nil
}

// ValidStatus reports whether status is known.
func ValidStatus(status Status) bool {
	switch status {
	case StatusDraft, StatusReady, StatusRunning, StatusBlocked, StatusCompleted, StatusFailed, StatusCancelled, StatusInterrupted:
		return true
	default:
		return false
	}
}

// ValidStepStatus reports whether status is known for an execution step.
func ValidStepStatus(status StepStatus) bool {
	switch status {
	case StepStatusWaiting, StepStatusRunning, StepStatusCompleted, StepStatusFailed, StepStatusCancelled:
		return true
	default:
		return false
	}
}

// ValidPriority reports whether priority is known.
func ValidPriority(priority Priority) bool {
	switch priority {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent:
		return true
	default:
		return false
	}
}

// Terminal reports whether a task/execution status cannot make more progress.
func Terminal(status Status) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// StepTerminal reports whether a step status cannot make more progress.
func StepTerminal(status StepStatus) bool {
	switch status {
	case StepStatusCompleted, StepStatusFailed, StepStatusCancelled:
		return true
	default:
		return false
	}
}

func validateAcyclic(steps []Step) error {
	deps := map[StepID][]StepID{}
	for _, step := range steps {
		deps[step.ID] = append([]StepID(nil), step.DependsOn...)
	}
	visiting := map[StepID]bool{}
	visited := map[StepID]bool{}
	var visit func(StepID) error
	visit = func(id StepID) error {
		if visiting[id] {
			return fmt.Errorf("task: step dependency cycle includes %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range deps[id] {
			if dep == id {
				return fmt.Errorf("task: step %q depends on itself", id)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for _, step := range steps {
		if err := visit(step.ID); err != nil {
			return err
		}
	}
	return nil
}
