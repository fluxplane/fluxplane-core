package taskplugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	coretask "github.com/fluxplane/agentruntime/core/task"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimetask "github.com/fluxplane/agentruntime/runtime/task"
)

const (
	Name                = "task"
	TaskCreateOp        = "task_create"
	TaskModifyOp        = "task_modify"
	TaskGetOp           = "task_get"
	TaskListOp          = "task_list"
	TaskListArtifactsOp = "task_list_artifacts"
	TaskGetArtifactOp   = "task_get_artifact"
	TaskValidateOp      = "task_validate"
	TaskCommand         = "task"
	TaskAgent           = "task"
	TaskSession         = "task"
	defaultPrefix       = "task_"
	artifactPrefix      = "artifact_"
)

// Plugin contributes task creation resources and operations.
type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns the task plugin.
func New() Plugin { return Plugin{} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Task creation commands, agents, and operations."}
}

// Contributions returns task resources.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := []operation.Spec{taskCreateSpec(), taskModifySpec(), taskGetSpec(), taskListSpec(), taskListArtifactsSpec(), taskGetArtifactSpec(), taskValidateSpec()}
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Task creation and management operations.",
			Operations:  operationRefs(specs),
		}},
		Operations: specs,
		Commands: []command.Spec{{
			Path:        command.Path{TaskCommand},
			Description: "Create a structured task from the request.",
			Target: invocation.Target{
				Kind:    invocation.TargetSession,
				Session: TaskSession,
			},
			Policy: policy.InvocationPolicy{
				AllowedCallers: []policy.CallerKind{policy.CallerUser},
				RequiredTrust:  policy.TrustVerified,
			},
		}},
		Agents: []agent.Spec{taskAgentSpec()},
		Sessions: []coresession.Spec{{
			Name:        TaskSession,
			Description: "Dedicated task creator session.",
			Agent:       agent.Ref{Name: TaskAgent},
			Operations:  []operation.Ref{{Name: TaskCreateOp}, {Name: TaskModifyOp}, {Name: TaskGetOp}, {Name: TaskListOp}, {Name: TaskListArtifactsOp}, {Name: TaskGetArtifactOp}, {Name: TaskValidateOp}, {Name: "clarify"}},
			Metadata:    map[string]string{"role": "task_creator"},
		}},
	}, nil
}

// Operations returns executable task operations.
func (Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	var store runtimetask.Store
	if ctx.EventStore != nil {
		taskStore, err := runtimetask.NewStore(ctx.EventStore)
		if err != nil {
			return nil, err
		}
		store = taskStore
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[coretask.TaskCreateRequest, coretask.TaskCreateResult](taskCreateSpec(), createTask(store)),
		operationruntime.NewTypedResult[coretask.TaskModifyRequest, coretask.TaskModifyResult](taskModifySpec(), modifyTask(store)),
		operationruntime.NewTypedResult[coretask.TaskGetRequest, coretask.TaskGetResult](taskGetSpec(), getTask(store)),
		operationruntime.NewTypedResult[coretask.TaskListRequest, coretask.TaskListResult](taskListSpec(), listTasks(store)),
		operationruntime.NewTypedResult[coretask.TaskArtifactListRequest, coretask.TaskArtifactListResult](taskListArtifactsSpec(), listArtifacts(store)),
		operationruntime.NewTypedResult[coretask.TaskArtifactGetRequest, coretask.TaskArtifactGetResult](taskGetArtifactSpec(), getArtifact(store)),
		operationruntime.NewTypedResult[coretask.TaskValidateRequest, coretask.TaskValidationResult](taskValidateSpec(), validateTask(store)),
	}, nil
}

func operationRefs(specs []operation.Spec) []operation.Ref {
	out := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Ref)
	}
	return out
}

func taskCreateSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskCreateRequest, coretask.TaskCreateResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskCreateOp},
		Description: "Create an event-sourced task and return immediately after creation.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectCreate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskGetSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskGetRequest, coretask.TaskGetResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskGetOp},
		Description: "Load the current projected state for an event-sourced task.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskModifySpec() operation.Spec {
	input := operationruntime.WithArrayItems(
		operationruntime.TypeOf[coretask.TaskModifyRequest]("task_modify_input"),
		"modifications",
		operationruntime.OneOf(
			operationruntime.SchemaFor[modifyMetadata](),
			operationruntime.SchemaFor[modifyAcceptanceCriterion](),
			operationruntime.SchemaFor[modifyOutput](),
			operationruntime.SchemaFor[modifyStep](),
			operationruntime.SchemaFor[modifyStepUpdate](),
			operationruntime.SchemaFor[modifyStepRemove](),
			operationruntime.SchemaFor[modifyStepStatus](),
			operationruntime.SchemaFor[modifyArtifact](),
			operationruntime.SchemaFor[modifyArtifactUpdate](),
			operationruntime.SchemaFor[modifyArtifactRemove](),
			operationruntime.SchemaFor[modifyStatus](),
			operationruntime.SchemaFor[modifyReopen](),
			operationruntime.SchemaFor[modifyStepReopen](),
			operationruntime.SchemaFor[modifyComplete](),
		),
	)
	input.Description = "Batch task modification request. Apply modifications sequentially to one existing task."
	return operation.Spec{
		Ref:         operation.Ref{Name: TaskModifyOp},
		Description: "Apply one or more metadata, step, status, artifact, or completion modifications to an existing task.",
		Input:       input,
		Output:      operationruntime.TypeOf[coretask.TaskModifyResult]("task_modify_output"),
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectUpdate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	}
}

func taskListSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskListRequest, coretask.TaskListResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskListOp},
		Description: "List indexed tasks by status, labels, workspace, project, owner, assignee, or text query.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskListArtifactsSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskArtifactListRequest, coretask.TaskArtifactListResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskListArtifactsOp},
		Description: "List task, execution, and step artifacts for one task.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskGetArtifactSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskArtifactGetRequest, coretask.TaskArtifactGetResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskGetArtifactOp},
		Description: "Get one task artifact by artifact id.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskValidateSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskValidateRequest, coretask.TaskValidationResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskValidateOp},
		Description: "Validate required outputs, artifacts, and step terminal state for one task.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskAgentSpec() agent.Spec {
	return agent.Spec{
		Name:        TaskAgent,
		Description: "Narrow task creator agent that turns requests into structured tasks.",
		System: strings.Join([]string{
			"You create structured tasks only.",
			"Given the user's request and available context, produce a complete task.",
			"Prefer creating a task with explicit inputs, outputs, assumptions, and acceptance criteria over asking clarification.",
			"Ask clarification only when the request cannot be represented as a useful draft or ready task.",
			"When enough information exists, call task_create.",
			"Use task_modify for follow-up changes to an existing task.",
			"After task_create succeeds, send a concise final message with the task id, title, status, and expected outputs.",
		}, " "),
		Driver:     agent.DriverSpec{Kind: "llmagent"},
		Turns:      agent.TurnPolicy{MaxSteps: 10},
		Operations: []operation.Ref{{Name: TaskCreateOp}, {Name: TaskModifyOp}, {Name: TaskGetOp}, {Name: TaskListOp}, {Name: TaskListArtifactsOp}, {Name: TaskGetArtifactOp}, {Name: TaskValidateOp}, {Name: "clarify"}},
	}
}

type modifyMetadata struct {
	Op                 string                  `json:"op" jsonschema:"description=Operation discriminator.,enum=update_metadata,required"`
	Title              string                  `json:"title,omitempty" jsonschema:"description=Replacement title."`
	Description        string                  `json:"description,omitempty" jsonschema:"description=Replacement description."`
	Objective          string                  `json:"objective,omitempty" jsonschema:"description=Replacement objective."`
	AcceptanceCriteria []string                `json:"acceptance_criteria,omitempty" jsonschema:"description=Replacement acceptance criteria."`
	Inputs             []coretask.ArtifactSpec `json:"inputs,omitempty" jsonschema:"description=Replacement input artifact declarations."`
	Outputs            []coretask.ArtifactSpec `json:"outputs,omitempty" jsonschema:"description=Replacement output artifact declarations."`
	Labels             []string                `json:"labels,omitempty" jsonschema:"description=Replacement labels."`
	Priority           coretask.Priority       `json:"priority,omitempty" jsonschema:"description=Replacement priority.,enum=low,enum=normal,enum=high,enum=urgent"`
	Assignee           coretask.Role           `json:"assignee,omitempty" jsonschema:"description=Replacement assignee."`
	Owner              coretask.Role           `json:"owner,omitempty" jsonschema:"description=Replacement owner."`
	Metadata           map[string]string       `json:"metadata,omitempty" jsonschema:"description=Metadata keys to merge into task metadata."`
}

type modifyAcceptanceCriterion struct {
	Op        string `json:"op" jsonschema:"description=Operation discriminator.,enum=add_acceptance_criterion,required"`
	Criterion string `json:"criterion" jsonschema:"description=Acceptance criterion to append.,required"`
}

type modifyOutput struct {
	Op     string                `json:"op" jsonschema:"description=Operation discriminator.,enum=add_output,required"`
	Output coretask.ArtifactSpec `json:"output" jsonschema:"description=Expected output artifact declaration.,required"`
}

type modifyStep struct {
	Op   string        `json:"op" jsonschema:"description=Operation discriminator.,enum=add_step,required"`
	Step coretask.Step `json:"step" jsonschema:"description=Step to append.,required"`
}

type modifyStepUpdate struct {
	Op     string          `json:"op" jsonschema:"description=Operation discriminator.,enum=update_step,required"`
	StepID coretask.StepID `json:"step_id" jsonschema:"description=Step id to replace.,required"`
	Step   coretask.Step   `json:"step" jsonschema:"description=Replacement step.,required"`
}

type modifyStepRemove struct {
	Op     string          `json:"op" jsonschema:"description=Operation discriminator.,enum=remove_step,required"`
	StepID coretask.StepID `json:"step_id" jsonschema:"description=Step id to remove.,required"`
}

type modifyStepStatus struct {
	Op          string               `json:"op" jsonschema:"description=Operation discriminator.,enum=set_step_status,required"`
	StepID      coretask.StepID      `json:"step_id" jsonschema:"description=Step id whose execution status changes.,required"`
	StepStatus  coretask.StepStatus  `json:"step_status" jsonschema:"description=New step status.,enum=waiting,enum=running,enum=blocked,enum=skipped,enum=completed,enum=failed,enum=cancelled,required"`
	ExecutionID coretask.ExecutionID `json:"execution_id,omitempty" jsonschema:"description=Optional execution id. Defaults to current execution or manual."`
	StepOutput  operation.Value      `json:"step_output,omitempty" jsonschema:"description=Optional step output."`
	Reason      string               `json:"reason,omitempty" jsonschema:"description=Reason for status change."`
}

type modifyArtifact struct {
	Op          string                `json:"op" jsonschema:"description=Operation discriminator.,enum=add_artifact,required"`
	ExecutionID coretask.ExecutionID  `json:"execution_id,omitempty" jsonschema:"description=Optional execution id. For step artifacts defaults to current execution or manual."`
	StepID      coretask.StepID       `json:"step_id,omitempty" jsonschema:"description=Optional step id. When set without execution_id the artifact is attached to current execution or manual."`
	Artifact    coretask.ArtifactSpec `json:"artifact" jsonschema:"description=Artifact to add.,required"`
}

type modifyArtifactUpdate struct {
	Op          string                `json:"op" jsonschema:"description=Operation discriminator.,enum=update_artifact,required"`
	ExecutionID coretask.ExecutionID  `json:"execution_id,omitempty" jsonschema:"description=Optional execution id used only to verify the artifact scope."`
	StepID      coretask.StepID       `json:"step_id,omitempty" jsonschema:"description=Optional step id used only to verify the artifact scope."`
	ArtifactID  string                `json:"artifact_id" jsonschema:"description=Artifact id to update.,required"`
	Artifact    coretask.ArtifactSpec `json:"artifact" jsonschema:"description=Replacement artifact.,required"`
}

type modifyArtifactRemove struct {
	Op          string               `json:"op" jsonschema:"description=Operation discriminator.,enum=remove_artifact,required"`
	ExecutionID coretask.ExecutionID `json:"execution_id,omitempty" jsonschema:"description=Optional execution id used only to verify the artifact scope."`
	StepID      coretask.StepID      `json:"step_id,omitempty" jsonschema:"description=Optional step id used only to verify the artifact scope."`
	ArtifactID  string               `json:"artifact_id" jsonschema:"description=Artifact id to remove.,required"`
	Reason      string               `json:"reason,omitempty" jsonschema:"description=Reason for removal."`
}

type modifyStatus struct {
	Op     string          `json:"op" jsonschema:"description=Operation discriminator.,enum=set_status,required"`
	Status coretask.Status `json:"status" jsonschema:"description=New task status.,enum=draft,enum=ready,enum=running,enum=blocked,enum=completed,enum=failed,enum=cancelled,enum=interrupted,required"`
	Reason string          `json:"reason,omitempty" jsonschema:"description=Reason for status change."`
}

type modifyReopen struct {
	Op     string          `json:"op" jsonschema:"description=Operation discriminator.,enum=reopen,required"`
	Status coretask.Status `json:"status,omitempty" jsonschema:"description=Active status after reopening. Defaults to ready.,enum=draft,enum=ready,enum=running,enum=blocked,enum=interrupted"`
	Reason string          `json:"reason,omitempty" jsonschema:"description=Reason for reopening the task."`
}

type modifyStepReopen struct {
	Op          string               `json:"op" jsonschema:"description=Operation discriminator.,enum=reopen_step,required"`
	StepID      coretask.StepID      `json:"step_id" jsonschema:"description=Step id to reopen.,required"`
	StepStatus  coretask.StepStatus  `json:"step_status,omitempty" jsonschema:"description=Active step status after reopening. Defaults to waiting.,enum=waiting,enum=running,enum=blocked"`
	ExecutionID coretask.ExecutionID `json:"execution_id,omitempty" jsonschema:"description=Optional execution id. Defaults to current execution or manual."`
	Reason      string               `json:"reason,omitempty" jsonschema:"description=Reason for reopening the step."`
}

type modifyComplete struct {
	Op             string                  `json:"op" jsonschema:"description=Operation discriminator.,enum=complete,required"`
	ForceOverrides []string                `json:"force_overrides,omitempty" jsonschema:"description=Validation check codes to override explicitly. Supported values include required_output and steps_terminal."`
	Reason         string                  `json:"reason,omitempty" jsonschema:"description=Completion reason."`
	Artifacts      []coretask.ArtifactSpec `json:"artifacts,omitempty" jsonschema:"description=Artifacts to add before completion validation."`
}

func createTask(store runtimetask.Store) func(operation.Context, coretask.TaskCreateRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskCreateRequest) operation.Result {
		if store == nil {
			return operation.Failed("task_store_missing", "task_create requires an event store", nil)
		}
		task := taskFromCreateRequest(req)
		if task.ID == "" {
			task.ID = coretask.ID(newID(defaultPrefix))
		}
		if task.Status == "" {
			task.Status = coretask.StatusReady
		}
		if err := task.Validate(); err != nil {
			return operation.Failed("task_invalid", err.Error(), nil)
		}
		req.ID = task.ID
		if req.Status == "" {
			req.Status = task.Status
		}
		events := []event.Event{
			coretask.CreateRequested{TaskID: task.ID, Request: req},
			coretask.Created{TaskID: task.ID, Task: task},
		}
		if err := store.Create(ctx, task.ID, events...); err != nil {
			if isAppendConflict(err) {
				return operation.Failed("task_already_exists", "task id already exists", map[string]any{"task_id": task.ID})
			}
			return operation.Failed("task_store_append_failed", err.Error(), map[string]any{"task_id": task.ID})
		}
		if err := store.Index(ctx, taskSummary(task)); err != nil {
			return operation.Failed("task_index_append_failed", err.Error(), map[string]any{"task_id": task.ID})
		}
		for _, payload := range events {
			ctx.Events().Emit(payload)
		}
		return operation.OK(coretask.TaskCreateResult{Task: task})
	}
}

func isAppendConflict(err error) bool {
	var conflict event.AppendConflict
	return errors.As(err, &conflict)
}

func getTask(store runtimetask.Store) func(operation.Context, coretask.TaskGetRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskGetRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		out := taskGetResult(state)
		out.View = defaultView(req.View)
		return operation.OK(out)
	}
}

func modifyTask(store runtimetask.Store) func(operation.Context, coretask.TaskModifyRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskModifyRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		if len(req.Modifications) == 0 {
			return operation.Failed("task_modify_empty", "task_modify requires at least one modification", nil)
		}
		var events []event.Event
		results := make([]coretask.TaskModificationResult, 0, len(req.Modifications))
		for _, item := range req.Modifications {
			modEvents, err := applyModification(req.ID, req.Reason, item, &state)
			results = append(results, coretask.TaskModificationResult{Op: item.Op, OK: err == nil, Error: errorString(err)})
			if err != nil {
				return operation.Failed("task_modify_invalid", err.Error(), map[string]any{"op": item.Op})
			}
			events = append(events, modEvents...)
		}
		if len(events) == 0 {
			return operation.Failed("task_modify_noop", "task_modify did not produce any changes", nil)
		}
		if err := store.Append(ctx, req.ID, events...); err != nil {
			return operation.Failed("task_store_append_failed", err.Error(), map[string]any{"task_id": req.ID})
		}
		if err := store.Index(ctx, taskSummary(state.Task)); err != nil {
			return operation.Failed("task_index_append_failed", err.Error(), map[string]any{"task_id": req.ID})
		}
		for _, payload := range events {
			ctx.Events().Emit(payload)
		}
		projected, err := store.Project(ctx, req.ID)
		if err != nil {
			return operation.Failed("task_store_project_failed", err.Error(), map[string]any{"task_id": req.ID})
		}
		return operation.OK(coretask.TaskModifyResult{
			Results:          results,
			Task:             projected.Task,
			CurrentExecution: projected.CurrentExecution,
			Executions:       projected.Executions,
			Validation:       validateState(projected),
			Artifacts:        scopedArtifacts(projected),
		})
	}
}

func listTasks(store runtimetask.Store) func(operation.Context, coretask.TaskListRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskListRequest) operation.Result {
		if store == nil {
			return operation.Failed("task_store_missing", "task_list requires an event store", nil)
		}
		summaries, err := store.List(ctx)
		if err != nil {
			return operation.Failed("task_index_load_failed", err.Error(), nil)
		}
		out, truncated := filterSummaries(summaries, req)
		return operation.OK(coretask.TaskListResult{Tasks: out, Truncated: truncated})
	}
}

func listArtifacts(store runtimetask.Store) func(operation.Context, coretask.TaskArtifactListRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskArtifactListRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		return operation.OK(coretask.TaskArtifactListResult{Artifacts: scopedArtifacts(state)})
	}
}

func getArtifact(store runtimetask.Store) func(operation.Context, coretask.TaskArtifactGetRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskArtifactGetRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		for _, artifact := range scopedArtifacts(state) {
			if artifact.Artifact.ID == req.ArtifactID {
				return operation.OK(coretask.TaskArtifactGetResult{Artifact: artifact})
			}
		}
		return operation.Failed("task_artifact_not_found", "artifact was not found", map[string]any{"task_id": req.ID, "artifact_id": req.ArtifactID})
	}
}

func validateTask(store runtimetask.Store) func(operation.Context, coretask.TaskValidateRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskValidateRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		return operation.OK(validateState(state))
	}
}

func loadTask(ctx context.Context, store runtimetask.Store, id coretask.ID) (runtimetask.State, bool, operation.Result) {
	if store == nil {
		return runtimetask.State{}, false, operation.Failed("task_store_missing", "task operation requires an event store", nil)
	}
	if strings.TrimSpace(string(id)) == "" {
		return runtimetask.State{}, false, operation.Failed("task_id_required", "task id is required", nil)
	}
	records, err := store.Load(ctx, id)
	if err != nil {
		return runtimetask.State{}, false, operation.Failed("task_store_load_failed", err.Error(), map[string]any{"task_id": id})
	}
	if len(records) == 0 {
		return runtimetask.State{}, false, operation.Failed("task_not_found", "task was not found", map[string]any{"task_id": id})
	}
	state := runtimetask.Project(records)
	if state.Task.ID == "" {
		return runtimetask.State{}, false, operation.Failed("task_not_found", "task was not found", map[string]any{"task_id": id})
	}
	return state, true, operation.Result{}
}

func taskGetResult(state runtimetask.State) coretask.TaskGetResult {
	return coretask.TaskGetResult{
		Task:             state.Task,
		CurrentExecution: state.CurrentExecution,
		Executions:       state.Executions,
	}
}

func taskSummary(task coretask.Task) coretask.TaskSummary {
	return coretask.TaskSummary{
		ID:          task.ID,
		Title:       task.Title,
		Objective:   task.Objective,
		Description: task.Description,
		Status:      task.Status,
		Priority:    task.Priority,
		Assignee:    task.Assignee,
		Owner:       task.Owner,
		WorkspaceID: task.WorkspaceID,
		ProjectID:   task.ProjectID,
		Labels:      append([]string(nil), task.Labels...),
		Metadata:    cloneStringMap(task.Metadata),
	}
}

func filterSummaries(in []coretask.TaskSummary, req coretask.TaskListRequest) ([]coretask.TaskSummary, bool) {
	out := make([]coretask.TaskSummary, 0, len(in))
	query := strings.ToLower(strings.TrimSpace(req.Query))
	for _, summary := range in {
		if req.Status != "" && summary.Status != req.Status {
			continue
		}
		if req.Label != "" && !stringSliceContains(summary.Labels, req.Label) {
			continue
		}
		if req.ProjectID != "" && summary.ProjectID != req.ProjectID {
			continue
		}
		if req.WorkspaceID != "" && summary.WorkspaceID != req.WorkspaceID {
			continue
		}
		if req.Assignee != "" && summary.Assignee != req.Assignee {
			continue
		}
		if req.Owner != "" && summary.Owner != req.Owner {
			continue
		}
		if query != "" && !summaryMatches(summary, query) {
			continue
		}
		out = append(out, summary)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return string(out[i].ID) < string(out[j].ID)
	})
	limit := req.MaxResults
	if limit <= 0 {
		limit = 50
	}
	if len(out) > limit {
		return out[:limit], true
	}
	return out, false
}

func summaryMatches(summary coretask.TaskSummary, query string) bool {
	fields := []string{string(summary.ID), summary.Title, summary.Objective, summary.Description, string(summary.Status), string(summary.Assignee), string(summary.Owner)}
	for _, value := range fields {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	for _, label := range summary.Labels {
		if strings.Contains(strings.ToLower(label), query) {
			return true
		}
	}
	for k, v := range summary.Metadata {
		if strings.Contains(strings.ToLower(k), query) || strings.Contains(strings.ToLower(v), query) {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func scopedArtifacts(state runtimetask.State) []coretask.ScopedArtifact {
	var out []coretask.ScopedArtifact
	for _, artifact := range state.Task.Artifacts {
		out = append(out, coretask.ScopedArtifact{TaskID: state.Task.ID, Artifact: artifact})
	}
	execIDs := make([]string, 0, len(state.Executions))
	for id := range state.Executions {
		execIDs = append(execIDs, string(id))
	}
	sort.Strings(execIDs)
	for _, rawID := range execIDs {
		id := coretask.ExecutionID(rawID)
		exec := state.Executions[id]
		for _, artifact := range exec.Artifacts {
			out = append(out, coretask.ScopedArtifact{TaskID: state.Task.ID, ExecutionID: id, Artifact: artifact})
		}
		stepIDs := make([]string, 0, len(exec.Steps))
		for stepID := range exec.Steps {
			stepIDs = append(stepIDs, string(stepID))
		}
		sort.Strings(stepIDs)
		for _, rawStepID := range stepIDs {
			stepID := coretask.StepID(rawStepID)
			for _, artifact := range exec.Steps[stepID].Artifacts {
				out = append(out, coretask.ScopedArtifact{TaskID: state.Task.ID, ExecutionID: id, StepID: stepID, Artifact: artifact})
			}
		}
	}
	return out
}

func validateState(state runtimetask.State) coretask.TaskValidationResult {
	result := coretask.TaskValidationResult{TaskID: state.Task.ID, Ready: state.Task.Status == coretask.StatusReady || state.Task.Status == coretask.StatusRunning}
	produced := scopedArtifacts(state)
	completable := true
	for _, output := range state.Task.Outputs {
		if !output.Required {
			continue
		}
		ok := artifactSatisfied(output, produced)
		if !ok {
			completable = false
		}
		result.Checks = append(result.Checks, coretask.TaskCheck{
			Code:    "required_output",
			Message: fmt.Sprintf("required output %s", firstNonEmpty(output.ID, output.Name, output.Description, output.Ref)),
			OK:      ok,
			Target:  output.ID,
		})
	}
	if len(state.Task.Steps) > 0 {
		ok := runtimetask.AllStepsTerminal(state)
		if !ok {
			completable = false
		}
		result.Checks = append(result.Checks, coretask.TaskCheck{
			Code:    "steps_terminal",
			Message: "all declared task steps have terminal execution state",
			OK:      ok,
		})
	}
	if len(state.Task.AcceptanceCriteria) > 0 {
		result.Checks = append(result.Checks, coretask.TaskCheck{
			Code:    "acceptance_manual",
			Message: "acceptance criteria require reviewer or caller judgment",
			OK:      true,
		})
	}
	if len(result.Checks) == 0 {
		result.Checks = append(result.Checks, coretask.TaskCheck{Code: "task_shape", Message: "task has no required outputs or steps", OK: true})
	}
	result.Completable = completable
	return result
}

func completionAllowed(validation coretask.TaskValidationResult, overrides []string) bool {
	if validation.Completable {
		return true
	}
	allowed := map[string]bool{}
	for _, code := range overrides {
		allowed[strings.TrimSpace(code)] = true
	}
	for _, check := range validation.Checks {
		if check.OK {
			continue
		}
		if !allowed[check.Code] {
			return false
		}
	}
	return len(allowed) > 0
}

func artifactSatisfied(required coretask.ArtifactSpec, produced []coretask.ScopedArtifact) bool {
	for _, scoped := range produced {
		artifact := scoped.Artifact
		if required.ID != "" && artifact.ID == required.ID {
			return true
		}
		if required.Name != "" && strings.EqualFold(artifact.Name, required.Name) {
			return true
		}
	}
	return false
}

func activeTaskStatus(status coretask.Status) bool {
	switch status {
	case coretask.StatusDraft, coretask.StatusReady, coretask.StatusRunning, coretask.StatusBlocked, coretask.StatusInterrupted:
		return true
	default:
		return false
	}
}

func activeStepStatus(status coretask.StepStatus) bool {
	switch status {
	case coretask.StepStatusWaiting, coretask.StepStatusRunning, coretask.StepStatusBlocked:
		return true
	default:
		return false
	}
}

func stepExists(steps []coretask.Step, id coretask.StepID) bool {
	for _, step := range steps {
		if step.ID == id {
			return true
		}
	}
	return false
}

func stepHasExecutionState(state runtimetask.State, id coretask.StepID) bool {
	for _, exec := range state.Executions {
		if _, ok := exec.Steps[id]; ok {
			return true
		}
	}
	return false
}

func artifactScopeForID(state runtimetask.State, artifactID string) (coretask.ScopedArtifact, bool) {
	for _, scoped := range scopedArtifacts(state) {
		if scoped.Artifact.ID == artifactID {
			return scoped, true
		}
	}
	return coretask.ScopedArtifact{}, false
}

func normalizeArtifactScope(state runtimetask.State, executionID coretask.ExecutionID, stepID coretask.StepID) (coretask.ExecutionID, coretask.StepID) {
	if stepID == "" {
		return executionID, stepID
	}
	if executionID != "" {
		return executionID, stepID
	}
	if state.CurrentExecution != "" {
		return state.CurrentExecution, stepID
	}
	return coretask.ExecutionID("manual"), stepID
}

func ensureArtifactID(state runtimetask.State, artifact *coretask.ArtifactSpec) {
	for artifact.ID == "" {
		id := newID(artifactPrefix)
		if artifactIDExists(state, id) {
			continue
		}
		artifact.ID = id
	}
}

func artifactIDExists(state runtimetask.State, artifactID string) bool {
	if artifactID == "" {
		return false
	}
	_, ok := artifactScopeForID(state, artifactID)
	return ok
}

func applyModification(taskID coretask.ID, batchReason string, item coretask.TaskModification, state *runtimetask.State) ([]event.Event, error) {
	reason := firstNonEmpty(item.Reason, batchReason)
	switch strings.TrimSpace(item.Op) {
	case "update_metadata":
		next := cloneTask(state.Task)
		if item.Title != "" {
			next.Title = strings.TrimSpace(item.Title)
		}
		if item.Description != "" {
			next.Description = strings.TrimSpace(item.Description)
		}
		if item.Objective != "" {
			next.Objective = strings.TrimSpace(item.Objective)
		}
		if len(item.AcceptanceCriteria) > 0 {
			next.AcceptanceCriteria = append([]string(nil), item.AcceptanceCriteria...)
		}
		if len(item.Inputs) > 0 {
			next.Inputs = cloneArtifacts(item.Inputs)
		}
		if len(item.Outputs) > 0 {
			next.Outputs = cloneArtifacts(item.Outputs)
		}
		if len(item.Scope) > 0 {
			next.Scope = append([]string(nil), item.Scope...)
		}
		if len(item.Constraints) > 0 {
			next.Constraints = append([]string(nil), item.Constraints...)
		}
		if len(item.Labels) > 0 {
			next.Labels = append([]string(nil), item.Labels...)
		}
		if item.Priority != "" {
			next.Priority = item.Priority
		}
		if item.Assignee != "" {
			next.Assignee = item.Assignee
		}
		if item.Owner != "" {
			next.Owner = item.Owner
		}
		if item.WorkspaceID != "" {
			next.WorkspaceID = item.WorkspaceID
		}
		if item.ProjectID != "" {
			next.ProjectID = item.ProjectID
		}
		if item.WorkflowRef != "" {
			next.WorkflowRef = item.WorkflowRef
		}
		if len(item.Metadata) > 0 {
			if next.Metadata == nil {
				next.Metadata = map[string]string{}
			}
			for k, v := range item.Metadata {
				next.Metadata[k] = v
			}
		}
		return revise(taskID, next, reason, state)
	case "add_acceptance_criterion":
		if strings.TrimSpace(item.Criterion) == "" {
			return nil, fmt.Errorf("acceptance criterion is required")
		}
		next := cloneTask(state.Task)
		next.AcceptanceCriteria = append(append([]string(nil), next.AcceptanceCriteria...), strings.TrimSpace(item.Criterion))
		return revise(taskID, next, reason, state)
	case "add_output":
		if err := validateArtifact(item.Output); err != nil {
			return nil, err
		}
		next := cloneTask(state.Task)
		artifact := item.Output
		if artifact.ID == "" {
			artifact.ID = newID(artifactPrefix)
		}
		next.Outputs = append(append([]coretask.ArtifactSpec(nil), next.Outputs...), artifact)
		return revise(taskID, next, reason, state)
	case "add_step":
		if strings.TrimSpace(string(item.Step.ID)) == "" {
			return nil, fmt.Errorf("step id is required")
		}
		next := cloneTask(state.Task)
		next.Steps = append(cloneSteps(next.Steps), cloneStep(item.Step))
		return revise(taskID, next, reason, state)
	case "update_step":
		if item.StepID == "" {
			return nil, fmt.Errorf("step_id is required")
		}
		next := cloneTask(state.Task)
		steps := cloneSteps(next.Steps)
		found := false
		step := cloneStep(item.Step)
		if step.ID == "" {
			step.ID = item.StepID
		}
		for i := range steps {
			if steps[i].ID == item.StepID {
				steps[i] = step
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("step %q not found", item.StepID)
		}
		next.Steps = steps
		return revise(taskID, next, reason, state)
	case "remove_step":
		if item.StepID == "" {
			return nil, fmt.Errorf("step_id is required")
		}
		if stepHasExecutionState(*state, item.StepID) {
			return nil, fmt.Errorf("step %q has execution state", item.StepID)
		}
		next := cloneTask(state.Task)
		steps := make([]coretask.Step, 0, len(next.Steps))
		found := false
		for _, step := range next.Steps {
			if step.ID == item.StepID {
				found = true
				continue
			}
			steps = append(steps, cloneStep(step))
		}
		if !found {
			return nil, fmt.Errorf("step %q not found", item.StepID)
		}
		next.Steps = steps
		return revise(taskID, next, reason, state)
	case "set_step_status":
		if item.StepID == "" {
			return nil, fmt.Errorf("step_id is required")
		}
		if !stepExists(state.Task.Steps, item.StepID) {
			return nil, fmt.Errorf("step %q not found", item.StepID)
		}
		if item.StepStatus == "" || !coretask.ValidStepStatus(item.StepStatus) {
			return nil, fmt.Errorf("step status %q is invalid", item.StepStatus)
		}
		executionID := item.ExecutionID
		if executionID == "" {
			executionID = state.CurrentExecution
		}
		if executionID == "" {
			executionID = coretask.ExecutionID("manual")
		}
		previous := coretask.StepStatus("")
		if exec, ok := state.Executions[executionID]; ok {
			previous = exec.Steps[item.StepID].Status
		}
		if coretask.StepTerminal(previous) && activeStepStatus(item.StepStatus) {
			return nil, fmt.Errorf("step %q is terminal; use reopen_step", item.StepID)
		}
		payload := coretask.StepStatusChanged{TaskID: taskID, ExecutionID: executionID, StepID: item.StepID, Previous: previous, Current: item.StepStatus, Reason: reason, Output: item.StepOutput}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "add_artifact":
		executionID, stepID := normalizeArtifactScope(*state, item.ExecutionID, item.StepID)
		if stepID != "" && !stepExists(state.Task.Steps, stepID) {
			return nil, fmt.Errorf("step %q not found", stepID)
		}
		if err := validateArtifact(item.Artifact); err != nil {
			return nil, err
		}
		artifact := item.Artifact
		if artifact.ID != "" && artifactIDExists(*state, artifact.ID) {
			return nil, fmt.Errorf("artifact %q already exists", artifact.ID)
		}
		ensureArtifactID(*state, &artifact)
		payload := coretask.ArtifactAdded{TaskID: taskID, ExecutionID: executionID, StepID: stepID, Artifact: artifact}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "update_artifact":
		if item.ArtifactID == "" {
			return nil, fmt.Errorf("artifact_id is required")
		}
		scoped, found := artifactScopeForID(*state, item.ArtifactID)
		if !found {
			return nil, fmt.Errorf("artifact %q not found", item.ArtifactID)
		}
		if item.ExecutionID != "" && item.ExecutionID != scoped.ExecutionID {
			return nil, fmt.Errorf("artifact %q not found in execution %q", item.ArtifactID, item.ExecutionID)
		}
		if item.StepID != "" && item.StepID != scoped.StepID {
			return nil, fmt.Errorf("artifact %q not found in step %q", item.ArtifactID, item.StepID)
		}
		if err := validateArtifact(item.Artifact); err != nil {
			return nil, err
		}
		artifact := item.Artifact
		if artifact.ID == "" {
			artifact.ID = item.ArtifactID
		} else if artifact.ID != item.ArtifactID && artifactIDExists(*state, artifact.ID) {
			return nil, fmt.Errorf("artifact %q already exists", artifact.ID)
		}
		payload := coretask.ArtifactUpdated{TaskID: taskID, ExecutionID: scoped.ExecutionID, StepID: scoped.StepID, ArtifactID: item.ArtifactID, Artifact: artifact}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "remove_artifact":
		if item.ArtifactID == "" {
			return nil, fmt.Errorf("artifact_id is required")
		}
		scoped, found := artifactScopeForID(*state, item.ArtifactID)
		if !found {
			return nil, fmt.Errorf("artifact %q not found", item.ArtifactID)
		}
		if item.ExecutionID != "" && item.ExecutionID != scoped.ExecutionID {
			return nil, fmt.Errorf("artifact %q not found in execution %q", item.ArtifactID, item.ExecutionID)
		}
		if item.StepID != "" && item.StepID != scoped.StepID {
			return nil, fmt.Errorf("artifact %q not found in step %q", item.ArtifactID, item.StepID)
		}
		payload := coretask.ArtifactRemoved{TaskID: taskID, ExecutionID: scoped.ExecutionID, StepID: scoped.StepID, ArtifactID: item.ArtifactID, Reason: reason}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "set_status":
		if item.Status == "" || !coretask.ValidStatus(item.Status) {
			return nil, fmt.Errorf("task status %q is invalid", item.Status)
		}
		if state.Task.Status == item.Status {
			return nil, nil
		}
		if coretask.Terminal(state.Task.Status) && activeTaskStatus(item.Status) {
			return nil, fmt.Errorf("task is terminal; use reopen")
		}
		payload := coretask.StatusChanged{TaskID: taskID, Previous: state.Task.Status, Current: item.Status, Reason: reason}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "reopen":
		if !coretask.Terminal(state.Task.Status) {
			return nil, fmt.Errorf("task is not terminal")
		}
		status := item.Status
		if status == "" {
			status = coretask.StatusReady
		}
		if !activeTaskStatus(status) {
			return nil, fmt.Errorf("reopen status %q is invalid", status)
		}
		payload := coretask.StatusChanged{TaskID: taskID, Previous: state.Task.Status, Current: status, Reason: reason}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "reopen_step":
		if item.StepID == "" {
			return nil, fmt.Errorf("step_id is required")
		}
		if !stepExists(state.Task.Steps, item.StepID) {
			return nil, fmt.Errorf("step %q not found", item.StepID)
		}
		executionID := item.ExecutionID
		if executionID == "" {
			executionID = state.CurrentExecution
		}
		if executionID == "" {
			executionID = coretask.ExecutionID("manual")
		}
		previous := coretask.StepStatus("")
		if exec, ok := state.Executions[executionID]; ok {
			previous = exec.Steps[item.StepID].Status
		}
		if !coretask.StepTerminal(previous) {
			return nil, fmt.Errorf("step %q is not terminal", item.StepID)
		}
		status := item.StepStatus
		if status == "" {
			status = coretask.StepStatusWaiting
		}
		if !activeStepStatus(status) {
			return nil, fmt.Errorf("reopen step status %q is invalid", status)
		}
		payload := coretask.StepStatusChanged{TaskID: taskID, ExecutionID: executionID, StepID: item.StepID, Previous: previous, Current: status, Reason: reason}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "complete":
		events := make([]event.Event, 0, len(artifactList(item))+1)
		for _, artifact := range artifactList(item) {
			if err := validateArtifact(artifact); err != nil {
				return nil, err
			}
			if artifact.ID != "" && artifactIDExists(*state, artifact.ID) {
				return nil, fmt.Errorf("artifact %q already exists", artifact.ID)
			}
			ensureArtifactID(*state, &artifact)
			payload := coretask.ArtifactAdded{TaskID: taskID, Artifact: artifact}
			*state = runtimetask.Apply(*state, payload, timeNow())
			events = append(events, payload)
		}
		validation := validateState(*state)
		if !completionAllowed(validation, item.ForceOverrides) {
			return nil, fmt.Errorf("task is not completable")
		}
		if state.Task.Status != coretask.StatusCompleted {
			payload := coretask.StatusChanged{TaskID: taskID, Previous: state.Task.Status, Current: coretask.StatusCompleted, Reason: reason}
			*state = runtimetask.Apply(*state, payload, timeNow())
			events = append(events, payload)
		}
		return events, nil
	default:
		return nil, fmt.Errorf("unsupported task modification %q", item.Op)
	}
}

func artifactList(m coretask.TaskModification) []coretask.ArtifactSpec {
	if len(m.Artifacts) > 0 {
		return m.Artifacts
	}
	if len(m.Outputs) > 0 {
		return m.Outputs
	}
	if m.Artifact.Name != "" || m.Artifact.Description != "" || m.Artifact.Ref != "" || m.Artifact.ID != "" {
		return []coretask.ArtifactSpec{m.Artifact}
	}
	return nil
}

func revise(taskID coretask.ID, next coretask.Task, reason string, state *runtimetask.State) ([]event.Event, error) {
	if next.ID == "" {
		next.ID = taskID
	}
	if next.ID != taskID {
		return nil, fmt.Errorf("task id mismatch: %s != %s", next.ID, taskID)
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	payload := coretask.Revised{TaskID: taskID, Task: next, Reason: reason}
	*state = runtimetask.Apply(*state, payload, timeNow())
	return []event.Event{payload}, nil
}

func validateArtifact(artifact coretask.ArtifactSpec) error {
	if strings.TrimSpace(artifact.Name) == "" && strings.TrimSpace(artifact.Description) == "" && strings.TrimSpace(artifact.Ref) == "" {
		return fmt.Errorf("artifact requires name, description, or ref")
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

func timeNow() time.Time {
	return time.Now().UTC()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func defaultView(view coretask.View) coretask.View {
	if view == coretask.ViewSummary {
		return view
	}
	return coretask.ViewFull
}

func taskFromCreateRequest(req coretask.TaskCreateRequest) coretask.Task {
	title := strings.TrimSpace(req.Title)
	objective := strings.TrimSpace(req.Objective)
	if title == "" && objective == "" {
		objective = strings.TrimSpace(req.Instruction)
	}
	if title == "" {
		title = summaryTitle(objective)
	}
	if objective == "" {
		objective = title
	}
	return coretask.Task{
		ID:                 req.ID,
		Title:              title,
		Description:        strings.TrimSpace(req.Description),
		Objective:          objective,
		AcceptanceCriteria: append([]string(nil), req.AcceptanceCriteria...),
		Inputs:             cloneArtifacts(req.Inputs),
		Outputs:            cloneArtifacts(req.Outputs),
		Scope:              append([]string(nil), req.Scope...),
		Constraints:        append([]string(nil), req.Constraints...),
		Status:             req.Status,
		Priority:           req.Priority,
		Assignee:           req.Assignee,
		Owner:              req.Owner,
		WorkspaceID:        req.WorkspaceID,
		ProjectID:          req.ProjectID,
		WorkflowRef:        req.WorkflowRef,
		Steps:              cloneSteps(req.SuggestedSteps),
		Labels:             append([]string(nil), req.Labels...),
		Metadata:           cloneStringMap(req.Metadata),
	}
}

func summaryTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if len(text) <= 80 {
		return text
	}
	return strings.TrimSpace(text[:80])
}

func cloneSteps(in []coretask.Step) []coretask.Step {
	if len(in) == 0 {
		return nil
	}
	out := make([]coretask.Step, len(in))
	for i, step := range in {
		out[i] = cloneStep(step)
	}
	return out
}

func cloneTask(task coretask.Task) coretask.Task {
	out := task
	out.AcceptanceCriteria = append([]string(nil), task.AcceptanceCriteria...)
	out.Inputs = cloneArtifacts(task.Inputs)
	out.Outputs = cloneArtifacts(task.Outputs)
	out.Artifacts = cloneArtifacts(task.Artifacts)
	out.Scope = append([]string(nil), task.Scope...)
	out.Constraints = append([]string(nil), task.Constraints...)
	out.Steps = cloneSteps(task.Steps)
	out.Labels = append([]string(nil), task.Labels...)
	out.Metadata = cloneStringMap(task.Metadata)
	return out
}

func cloneStep(step coretask.Step) coretask.Step {
	out := step
	out.AcceptanceCriteria = append([]string(nil), step.AcceptanceCriteria...)
	out.Inputs = cloneArtifacts(step.Inputs)
	out.Outputs = cloneArtifacts(step.Outputs)
	out.DependsOn = append([]coretask.StepID(nil), step.DependsOn...)
	out.Scope = append([]string(nil), step.Scope...)
	out.Metadata = cloneStringMap(step.Metadata)
	return out
}

func cloneArtifacts(in []coretask.ArtifactSpec) []coretask.ArtifactSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]coretask.ArtifactSpec, len(in))
	for i, artifact := range in {
		out[i] = artifact
		out[i].Metadata = cloneStringMap(artifact.Metadata)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s%x", prefix, strings.ReplaceAll(err.Error(), " ", "_"))
	}
	return prefix + hex.EncodeToString(b[:])
}
