// Package task defines inert user-facing task and execution models.
package task

import (
	"encoding/json"
	"fmt"
	"sort"
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

const (
	RoleHuman     Role = "human"
	RoleDeveloper Role = "developer"
	RoleReviewer  Role = "reviewer"
	RoleTester    Role = "tester"
	RoleExplorer  Role = "explorer"
)

// Priority classifies task ordering hints.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

// TaskCreateKind classifies task creation requests from commands or agents.
type TaskCreateKind string

const (
	TaskCreateKindGeneric TaskCreateKind = "generic"
)

const (
	// MetadataOriginThreadID records the session thread that created a task.
	MetadataOriginThreadID = "origin_thread_id"
	// MetadataOriginBranchID records the session branch that created a task.
	MetadataOriginBranchID = "origin_branch_id"
	// MetadataOriginRunID records the session run that created a task.
	MetadataOriginRunID = "origin_run_id"
)

// ArtifactKind classifies task input/output and produced artifacts.
type ArtifactKind string

const (
	ArtifactText       ArtifactKind = "text"
	ArtifactFile       ArtifactKind = "file"
	ArtifactPatch      ArtifactKind = "patch"
	ArtifactDiff       ArtifactKind = "diff"
	ArtifactReport     ArtifactKind = "report"
	ArtifactReview     ArtifactKind = "review"
	ArtifactTestResult ArtifactKind = "test_result"
	ArtifactBuild      ArtifactKind = "build"
	ArtifactReference  ArtifactKind = "reference"
	ArtifactJSON       ArtifactKind = "json"
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
	StepStatusBlocked   StepStatus = "blocked"
	StepStatusSkipped   StepStatus = "skipped"
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
	Inputs             []ArtifactSpec    `json:"inputs,omitempty"`
	Outputs            []ArtifactSpec    `json:"outputs,omitempty"`
	Artifacts          []ArtifactSpec    `json:"artifacts,omitempty"`
	Scope              []string          `json:"scope,omitempty"`
	Constraints        []string          `json:"constraints,omitempty"`
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
	Inputs             []ArtifactSpec    `json:"inputs,omitempty"`
	Outputs            []ArtifactSpec    `json:"outputs,omitempty"`
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
	Artifacts   []ArtifactSpec           `json:"artifacts,omitempty"`
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
	Artifacts    []ArtifactSpec    `json:"artifacts,omitempty"`
	Error        *operation.Error  `json:"error,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ArtifactSpec describes a required, expected, or produced task artifact.
type ArtifactSpec struct {
	ID          string            `json:"id,omitempty" jsonschema:"description=Stable artifact identifier."`
	Name        string            `json:"name,omitempty" jsonschema:"description=Human-readable artifact name."`
	Kind        ArtifactKind      `json:"kind,omitempty" jsonschema:"description=Artifact kind.,enum=text,enum=file,enum=patch,enum=diff,enum=report,enum=review,enum=test_result,enum=build,enum=reference,enum=json"`
	Description string            `json:"description,omitempty" jsonschema:"description=Natural-language description of the required expected or produced artifact."`
	Required    bool              `json:"required,omitempty" jsonschema:"description=Whether the artifact is required for readiness or completion."`
	Schema      operation.Type    `json:"schema,omitempty" jsonschema:"description=Optional structured type contract for artifact values."`
	Ref         string            `json:"ref,omitempty" jsonschema:"description=External or runtime reference for the artifact."`
	Value       operation.Value   `json:"value,omitempty" jsonschema:"description=Inline artifact value when appropriate."`
	Metadata    map[string]string `json:"metadata,omitempty" jsonschema:"description=Additional artifact metadata."`
}

// TaskCreateRequest asks the task runtime to create a durable task.
type TaskCreateRequest struct {
	ID          ID             `json:"id,omitempty" jsonschema:"description=Optional caller-supplied task id. Must be unique when supplied."`
	Kind        TaskCreateKind `json:"kind,omitempty" jsonschema:"description=Task creation kind. Defaults to generic.,enum=generic"`
	Instruction string         `json:"instruction,omitempty" jsonschema:"description=Original user instruction or request text."`
	Intent      string         `json:"intent,omitempty" jsonschema:"description=Short statement of why this task is being created."`
	Objective   string         `json:"objective,omitempty" jsonschema:"description=Concrete objective the task should accomplish."`
	Title       string         `json:"title,omitempty" jsonschema:"description=Short human-readable task title."`
	Description string         `json:"description,omitempty" jsonschema:"description=Additional task details and context."`

	AcceptanceCriteria []string          `json:"acceptance_criteria,omitempty" jsonschema:"description=Conditions that define successful completion."`
	Inputs             []ArtifactSpec    `json:"inputs,omitempty" jsonschema:"description=Inputs required before this task can be executed."`
	Outputs            []ArtifactSpec    `json:"outputs,omitempty" jsonschema:"description=Expected output artifacts or deliverables."`
	Scope              []string          `json:"scope,omitempty" jsonschema:"description=Files packages projects or conceptual boundaries in scope."`
	Constraints        []string          `json:"constraints,omitempty" jsonschema:"description=Constraints the executor must honor."`
	Labels             []string          `json:"labels,omitempty" jsonschema:"description=Free-form labels for grouping and retrieval."`
	Priority           Priority          `json:"priority,omitempty" jsonschema:"description=Task priority.,enum=low,enum=normal,enum=high,enum=urgent"`
	Assignee           Role              `json:"assignee,omitempty" jsonschema:"description=Suggested assignee role or profile. Common values: human developer reviewer tester explorer."`
	Owner              Role              `json:"owner,omitempty" jsonschema:"description=Task owner role or profile. Common values: human developer reviewer tester explorer."`
	WorkspaceID        workspace.ID      `json:"workspace_id,omitempty" jsonschema:"description=Workspace id this task belongs to."`
	ProjectID          project.ID        `json:"project_id,omitempty" jsonschema:"description=Project id this task belongs to."`
	WorkflowRef        workflow.Name     `json:"workflow_ref,omitempty" jsonschema:"description=Optional workflow name associated with this task."`
	SuggestedSteps     []Step            `json:"suggested_steps,omitempty" jsonschema:"description=Optional committed task step DAG. Each step id must be unique and dependencies must be acyclic."`
	Status             Status            `json:"status,omitempty" jsonschema:"description=Initial task status. Defaults to ready.,enum=draft,enum=ready,enum=running,enum=blocked,enum=completed,enum=failed,enum=cancelled,enum=interrupted"`
	Metadata           map[string]string `json:"metadata,omitempty" jsonschema:"description=Additional task metadata."`
}

// TaskCreateResult reports the created task and non-fatal creation notes.
type TaskCreateResult struct {
	Task        Task         `json:"task"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// ModelText returns a compact model-facing creation summary.
func (r TaskCreateResult) ModelText() string {
	if r.Task.ID == "" {
		return "Task created."
	}
	return fmt.Sprintf("Created task %s: %s (status: %s)", r.Task.ID, firstNonEmpty(r.Task.Title, r.Task.Objective), firstNonEmpty(string(r.Task.Status), string(StatusDraft)))
}

// TaskGetRequest asks for the current projected task state.
type TaskGetRequest struct {
	ID   ID   `json:"id" jsonschema:"description=Task id to load.,required"`
	View View `json:"view,omitempty" jsonschema:"description=Task view. Defaults to full.,enum=full,enum=summary"`
}

// View controls how much task detail should be rendered for humans/models.
type View string

const (
	ViewFull    View = "full"
	ViewSummary View = "summary"
)

// TaskGetResult reports current projected task state.
type TaskGetResult struct {
	Task             Task                      `json:"task"`
	CurrentExecution ExecutionID               `json:"current_execution,omitempty"`
	Executions       map[ExecutionID]Execution `json:"executions,omitempty"`
	View             View                      `json:"view,omitempty"`
}

// ModelText returns a compact model-facing state summary.
func (r TaskGetResult) ModelText() string {
	if r.Task.ID == "" {
		return "Task not found."
	}
	if r.View != ViewSummary {
		var b strings.Builder
		fmt.Fprintf(&b, "Task %s: %s\n", r.Task.ID, firstNonEmpty(r.Task.Title, r.Task.Objective))
		fmt.Fprintf(&b, "Status: %s", firstNonEmpty(string(r.Task.Status), string(StatusDraft)))
		if r.Task.Priority != "" {
			fmt.Fprintf(&b, " Priority: %s", r.Task.Priority)
		}
		if r.Task.Assignee != "" {
			fmt.Fprintf(&b, " Assignee: %s", r.Task.Assignee)
		}
		if r.Task.Objective != "" {
			fmt.Fprintf(&b, "\nObjective: %s", r.Task.Objective)
		}
		if r.Task.Description != "" {
			fmt.Fprintf(&b, "\nDescription: %s", r.Task.Description)
		}
		if len(r.Task.AcceptanceCriteria) > 0 {
			b.WriteString("\nAcceptance criteria:")
			for _, criterion := range r.Task.AcceptanceCriteria {
				fmt.Fprintf(&b, "\n- %s", criterion)
			}
		}
		if len(r.Task.Outputs) > 0 {
			b.WriteString("\nExpected outputs:")
			for _, artifact := range r.Task.Outputs {
				fmt.Fprintf(&b, "\n- %s", artifactLabel(artifact))
			}
		}
		if len(r.Task.Steps) > 0 {
			b.WriteString("\nSteps:")
			exec, hasExec := r.Executions[r.CurrentExecution]
			for _, step := range r.Task.Steps {
				fmt.Fprintf(&b, "\n- %s: %s", step.ID, firstNonEmpty(step.Title, step.Objective, step.Description))
				if hasExec {
					if status := exec.Steps[step.ID].Status; status != "" {
						fmt.Fprintf(&b, " (status: %s)", status)
					}
				}
				if len(step.DependsOn) > 0 {
					fmt.Fprintf(&b, "\n  depends_on: %s", stepIDList(step.DependsOn))
				}
				if len(step.Inputs) > 0 {
					b.WriteString("\n  inputs:")
					for _, artifact := range step.Inputs {
						fmt.Fprintf(&b, " %s", artifactLabel(artifact))
					}
				}
				if len(step.Outputs) > 0 {
					b.WriteString("\n  outputs:")
					for _, artifact := range step.Outputs {
						fmt.Fprintf(&b, " %s", artifactLabel(artifact))
					}
				}
				if hasExec && len(exec.Steps[step.ID].Artifacts) > 0 {
					b.WriteString("\n  artifacts:")
					for _, artifact := range exec.Steps[step.ID].Artifacts {
						fmt.Fprintf(&b, " %s", artifactSummary(artifact))
					}
				}
			}
		}
		artifacts := scopedArtifactsForText(r.Task, r.Executions)
		if len(artifacts) > 0 {
			b.WriteString("\nArtifacts:")
			for _, artifact := range artifacts {
				fmt.Fprintf(&b, "\n- %s: %s", artifactScope(artifact), artifactSummary(artifact.Artifact))
			}
		}
		return b.String()
	}
	return fmt.Sprintf("Task %s: %s (status: %s)", r.Task.ID, firstNonEmpty(r.Task.Title, r.Task.Objective), firstNonEmpty(string(r.Task.Status), string(StatusDraft)))
}

// TaskModifyRequest applies one or more modifications to an existing task.
type TaskModifyRequest struct {
	ID            ID                 `json:"id" jsonschema:"description=Task id to modify.,required"`
	Modifications []TaskModification `json:"modifications" jsonschema:"description=Sequential task modifications to apply.,required,minItems=1"`
	Reason        string             `json:"reason,omitempty" jsonschema:"description=Shared reason for the modification batch."`
}

// TaskModification is the broad reflected shape. The operation schema narrows
// array items with oneOf variants.
type TaskModification struct {
	Op                 string            `json:"op" jsonschema:"description=Modification operation discriminator.,required"`
	Title              string            `json:"title,omitempty"`
	Description        string            `json:"description,omitempty"`
	Objective          string            `json:"objective,omitempty"`
	AcceptanceCriteria []string          `json:"acceptance_criteria,omitempty"`
	Inputs             []ArtifactSpec    `json:"inputs,omitempty"`
	Outputs            []ArtifactSpec    `json:"outputs,omitempty"`
	Scope              []string          `json:"scope,omitempty"`
	Constraints        []string          `json:"constraints,omitempty"`
	Labels             []string          `json:"labels,omitempty"`
	Priority           Priority          `json:"priority,omitempty"`
	Assignee           Role              `json:"assignee,omitempty"`
	Owner              Role              `json:"owner,omitempty"`
	WorkspaceID        workspace.ID      `json:"workspace_id,omitempty"`
	ProjectID          project.ID        `json:"project_id,omitempty"`
	WorkflowRef        workflow.Name     `json:"workflow_ref,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Criterion          string            `json:"criterion,omitempty"`
	Step               Step              `json:"step,omitempty"`
	StepID             StepID            `json:"step_id,omitempty"`
	Status             Status            `json:"status,omitempty"`
	StepStatus         StepStatus        `json:"step_status,omitempty"`
	ExecutionID        ExecutionID       `json:"execution_id,omitempty"`
	Artifact           ArtifactSpec      `json:"artifact,omitempty"`
	Artifacts          []ArtifactSpec    `json:"artifacts,omitempty"`
	ArtifactID         string            `json:"artifact_id,omitempty"`
	Output             ArtifactSpec      `json:"output,omitempty"`
	StepOutput         operation.Value   `json:"step_output,omitempty"`
	ForceOverrides     []string          `json:"force_overrides,omitempty"`
	Reason             string            `json:"reason,omitempty"`
}

// TaskModifyResult reports per-modification outcomes and final projected state.
type TaskModifyResult struct {
	Results          []TaskModificationResult  `json:"results"`
	Task             Task                      `json:"task"`
	CurrentExecution ExecutionID               `json:"current_execution,omitempty"`
	Executions       map[ExecutionID]Execution `json:"executions,omitempty"`
	Validation       TaskValidationResult      `json:"validation,omitempty"`
	Artifacts        []ScopedArtifact          `json:"artifacts,omitempty"`
}

// ModelText returns a compact model-facing modification summary.
func (r TaskModifyResult) ModelText() string {
	if r.Task.ID == "" {
		return "Task modified."
	}
	return fmt.Sprintf("Modified task %s: %s (status: %s, artifacts: %d)", r.Task.ID, firstNonEmpty(r.Task.Title, r.Task.Objective), firstNonEmpty(string(r.Task.Status), string(StatusDraft)), len(r.Artifacts))
}

// TaskModificationResult reports one modification outcome.
type TaskModificationResult struct {
	Op    string `json:"op"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// TaskListRequest lists projected task summaries from the task index stream.
type TaskListRequest struct {
	Status      Status       `json:"status,omitempty" jsonschema:"description=Optional task status filter.,enum=draft,enum=ready,enum=running,enum=blocked,enum=completed,enum=failed,enum=cancelled,enum=interrupted"`
	Label       string       `json:"label,omitempty" jsonschema:"description=Optional label filter."`
	ProjectID   project.ID   `json:"project_id,omitempty" jsonschema:"description=Optional project id filter."`
	WorkspaceID workspace.ID `json:"workspace_id,omitempty" jsonschema:"description=Optional workspace id filter."`
	Assignee    Role         `json:"assignee,omitempty" jsonschema:"description=Optional assignee role or profile filter."`
	Owner       Role         `json:"owner,omitempty" jsonschema:"description=Optional owner role or profile filter."`
	Query       string       `json:"query,omitempty" jsonschema:"description=Optional case-insensitive text query over id title objective description labels and metadata."`
	MaxResults  int          `json:"max_results,omitempty" jsonschema:"description=Maximum task summaries returned. Defaults to 50."`
}

// TaskListResult reports indexed task summaries.
type TaskListResult struct {
	Tasks     []TaskSummary `json:"tasks"`
	Truncated bool          `json:"truncated,omitempty"`
}

// ModelText returns a compact model-facing task list.
func (r TaskListResult) ModelText() string {
	if len(r.Tasks) == 0 {
		return "No tasks found."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Tasks: %d", len(r.Tasks))
	if r.Truncated {
		b.WriteString(" (truncated)")
	}
	for _, task := range r.Tasks {
		fmt.Fprintf(&b, "\n- %s: %s (status: %s)", task.ID, firstNonEmpty(task.Title, task.Objective), firstNonEmpty(string(task.Status), string(StatusDraft)))
	}
	return b.String()
}

// TaskSummary is the compact task index projection.
type TaskSummary struct {
	ID          ID                `json:"id"`
	Title       string            `json:"title,omitempty"`
	Objective   string            `json:"objective,omitempty"`
	Description string            `json:"description,omitempty"`
	Status      Status            `json:"status,omitempty"`
	Priority    Priority          `json:"priority,omitempty"`
	Assignee    Role              `json:"assignee,omitempty"`
	Owner       Role              `json:"owner,omitempty"`
	WorkspaceID workspace.ID      `json:"workspace_id,omitempty"`
	ProjectID   project.ID        `json:"project_id,omitempty"`
	Labels      []string          `json:"labels,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// TaskArtifactListRequest lists scoped artifacts for a task.
type TaskArtifactListRequest struct {
	ID ID `json:"id" jsonschema:"description=Task id.,required"`
}

// TaskArtifactGetRequest loads one artifact by id from task/execution/step scopes.
type TaskArtifactGetRequest struct {
	ID         ID     `json:"id" jsonschema:"description=Task id.,required"`
	ArtifactID string `json:"artifact_id" jsonschema:"description=Artifact id.,required"`
}

// TaskArtifactListResult reports scoped artifacts.
type TaskArtifactListResult struct {
	Artifacts []ScopedArtifact `json:"artifacts"`
}

// ModelText returns a compact model-facing artifact list.
func (r TaskArtifactListResult) ModelText() string {
	if len(r.Artifacts) == 0 {
		return "No task artifacts found."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Task artifacts: %d", len(r.Artifacts))
	for _, scoped := range r.Artifacts {
		fmt.Fprintf(&b, "\n- %s: %s", artifactScope(scoped), artifactSummary(scoped.Artifact))
	}
	return b.String()
}

// TaskArtifactGetResult reports one scoped artifact.
type TaskArtifactGetResult struct {
	Artifact ScopedArtifact `json:"artifact"`
}

// ModelText returns a compact model-facing artifact summary.
func (r TaskArtifactGetResult) ModelText() string {
	if r.Artifact.Artifact.ID == "" && r.Artifact.Artifact.Name == "" {
		return "Task artifact not found."
	}
	return "Task artifact: " + artifactScope(r.Artifact) + ": " + artifactDetail(r.Artifact.Artifact)
}

// ScopedArtifact is an artifact with task/execution/step coordinates.
type ScopedArtifact struct {
	TaskID      ID           `json:"task_id"`
	ExecutionID ExecutionID  `json:"execution_id,omitempty"`
	StepID      StepID       `json:"step_id,omitempty"`
	Artifact    ArtifactSpec `json:"artifact"`
}

// TaskValidateRequest validates task completion/readiness conditions.
type TaskValidateRequest struct {
	ID ID `json:"id" jsonschema:"description=Task id to validate.,required"`
}

// TaskValidationResult reports validation checks.
type TaskValidationResult struct {
	TaskID      ID          `json:"task_id,omitempty"`
	Ready       bool        `json:"ready"`
	Completable bool        `json:"completable"`
	Checks      []TaskCheck `json:"checks,omitempty"`
}

// ModelText returns a compact model-facing validation summary.
func (r TaskValidationResult) ModelText() string {
	state := "not completable"
	if r.Completable {
		state = "completable"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s is %s.", r.TaskID, state)
	for _, check := range r.Checks {
		status := "missing"
		if check.OK {
			status = "ok"
		}
		fmt.Fprintf(&b, "\n- %s: %s (%s)", check.Code, check.Message, status)
	}
	return b.String()
}

// TaskCheck is one validation outcome.
type TaskCheck struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	OK      bool   `json:"ok"`
	Target  string `json:"target,omitempty"`
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

// ModelText returns a compact model-facing execution control summary.
func (r ExecutionResult) ModelText() string {
	if r.Summary != "" {
		return r.Summary
	}
	if r.TaskID == "" {
		return "Task execution request accepted."
	}
	status := firstNonEmpty(string(r.Status), "queued")
	return fmt.Sprintf("Task %s execution status: %s", r.TaskID, status)
}

// SchedulerStatusRequest asks for the current task scheduler state.
type SchedulerStatusRequest struct{}

// SchedulerStatusResult reports local scheduler capacity and in-flight tasks.
type SchedulerStatusResult struct {
	Enabled           bool         `json:"enabled"`
	Active            bool         `json:"active"`
	Running           []ID         `json:"running,omitempty"`
	Capacity          int          `json:"capacity"`
	MaxParallel       int          `json:"max_parallel"`
	ReconcileInterval string       `json:"reconcile_interval,omitempty"`
	Diagnostics       []Diagnostic `json:"diagnostics,omitempty"`
}

// ModelText returns a compact model-facing scheduler summary.
func (r SchedulerStatusResult) ModelText() string {
	state := "disabled"
	if r.Enabled {
		state = "enabled"
	}
	active := "inactive"
	if r.Active {
		active = "active"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Task scheduler is %s and %s. Capacity: %d/%d.", state, active, r.Capacity, r.MaxParallel)
	for _, id := range r.Running {
		fmt.Fprintf(&b, "\n- running: %s", id)
	}
	for _, diagnostic := range r.Diagnostics {
		fmt.Fprintf(&b, "\n- %s: %s", diagnostic.Code, diagnostic.Message)
	}
	return b.String()
}

// SchedulerSetEnabledRequest enables or disables automatic task scheduling.
type SchedulerSetEnabledRequest struct {
	Enabled bool `json:"enabled" jsonschema:"description=Whether automatic ready-task scheduling should be enabled.,required"`
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
	case StepStatusWaiting, StepStatusRunning, StepStatusBlocked, StepStatusSkipped, StepStatusCompleted, StepStatusFailed, StepStatusCancelled:
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
	case StepStatusCompleted, StepStatusFailed, StepStatusCancelled, StepStatusSkipped:
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func artifactLabel(artifact ArtifactSpec) string {
	label := firstNonEmpty(artifact.ID, artifact.Name, artifact.Description, artifact.Ref)
	if label == "" {
		label = "artifact"
	}
	if artifact.Kind != "" {
		label += " [" + string(artifact.Kind) + "]"
	}
	if artifact.Required {
		label += " required"
	}
	return label
}

func stepIDList(ids []StepID) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			parts = append(parts, string(id))
		}
	}
	return strings.Join(parts, ", ")
}

func scopedArtifactsForText(task Task, executions map[ExecutionID]Execution) []ScopedArtifact {
	var out []ScopedArtifact
	for _, artifact := range task.Artifacts {
		out = append(out, ScopedArtifact{TaskID: task.ID, Artifact: artifact})
	}
	execIDs := make([]string, 0, len(executions))
	for id := range executions {
		execIDs = append(execIDs, string(id))
	}
	sort.Strings(execIDs)
	for _, rawID := range execIDs {
		id := ExecutionID(rawID)
		exec := executions[id]
		for _, artifact := range exec.Artifacts {
			out = append(out, ScopedArtifact{TaskID: task.ID, ExecutionID: id, Artifact: artifact})
		}
		stepIDs := make([]string, 0, len(exec.Steps))
		for stepID := range exec.Steps {
			stepIDs = append(stepIDs, string(stepID))
		}
		sort.Strings(stepIDs)
		for _, rawStepID := range stepIDs {
			stepID := StepID(rawStepID)
			for _, artifact := range exec.Steps[stepID].Artifacts {
				out = append(out, ScopedArtifact{TaskID: task.ID, ExecutionID: id, StepID: stepID, Artifact: artifact})
			}
		}
	}
	return out
}

func artifactScope(scoped ScopedArtifact) string {
	switch {
	case scoped.ExecutionID != "" && scoped.StepID != "":
		return fmt.Sprintf("execution:%s/step:%s", scoped.ExecutionID, scoped.StepID)
	case scoped.ExecutionID != "":
		return fmt.Sprintf("execution:%s", scoped.ExecutionID)
	default:
		return "task"
	}
}

func artifactSummary(artifact ArtifactSpec) string {
	return artifactLabel(artifact)
}

func artifactDetail(artifact ArtifactSpec) string {
	fields := []string{artifactLabel(artifact)}
	if artifact.Description != "" {
		fields = append(fields, "description="+artifact.Description)
	}
	if artifact.Ref != "" {
		fields = append(fields, "ref="+artifact.Ref)
	}
	if text, ok := artifactValueText(artifact.Value); ok {
		fields = append(fields, "value="+text)
	}
	if len(artifact.Metadata) > 0 {
		keys := make([]string, 0, len(artifact.Metadata))
		for key := range artifact.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		pairs := make([]string, 0, len(keys))
		for _, key := range keys {
			pairs = append(pairs, key+"="+artifact.Metadata[key])
		}
		fields = append(fields, "metadata={"+strings.Join(pairs, ", ")+"}")
	}
	return strings.Join(fields, "; ")
}

func artifactValueText(value operation.Value) (string, bool) {
	if value == nil {
		return "", false
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case []byte:
		text = string(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			text = fmt.Sprint(typed)
		} else {
			text = string(data)
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	const limit = 240
	if len(text) > limit {
		text = text[:limit] + "...[truncated]"
	}
	return text, true
}
