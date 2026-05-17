package taskplugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coretask "github.com/fluxplane/agentruntime/core/task"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/taskexecutor"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	runtimetask "github.com/fluxplane/agentruntime/runtime/task"
)

func TestContributionsIncludeTaskResources(t *testing.T) {
	bundle, err := New().Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Commands) != 2 || bundle.Commands[0].Path.String() != "/task" || bundle.Commands[1].Path.String() != "/plan" {
		t.Fatalf("commands = %#v, want /task and /plan", bundle.Commands)
	}
	if len(bundle.Agents) != 2 || string(bundle.Agents[0].Name) != TaskAgent || string(bundle.Agents[1].Name) != PlanAgent {
		t.Fatalf("agents = %#v, want task and planner agents", bundle.Agents)
	}
	if len(bundle.Sessions) != 2 || string(bundle.Sessions[0].Name) != TaskSession || string(bundle.Sessions[1].Name) != PlanSession {
		t.Fatalf("sessions = %#v, want task and planner sessions", bundle.Sessions)
	}
	if len(bundle.Operations) != 10 {
		t.Fatalf("operations len = %d, want 10", len(bundle.Operations))
	}
	for _, name := range []string{TaskCreateOp, TaskModifyOp, TaskGetOp, TaskListOp, TaskListArtifactsOp, TaskGetArtifactOp, TaskValidateOp, TaskRunOp, TaskSchedulerStatusOp, TaskSchedulerSetEnabledOp} {
		if !hasOperation(bundle.Operations, name) {
			t.Fatalf("operations = %#v, want %s", bundle.Operations, name)
		}
	}
}

func TestTaskCreatePersistsEventsAndDefaultsReady(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	var emitted []event.Event
	result := ops[0].Run(operation.NewContext(context.Background(), event.SinkFunc(func(payload event.Event) {
		emitted = append(emitted, payload)
	})), coretask.TaskCreateRequest{
		Instruction:        "Review core/task",
		AcceptanceCriteria: []string{"Findings include evidence."},
		Outputs:            []coretask.ArtifactSpec{{Name: "report", Kind: coretask.ArtifactReport, Required: true}},
	})
	if result.IsError() {
		t.Fatalf("task_create error = %#v", result.Error)
	}
	out, ok := result.Output.(coretask.TaskCreateResult)
	if !ok {
		t.Fatalf("output = %T, want TaskCreateResult", result.Output)
	}
	if out.Task.ID == "" || out.Task.Status != coretask.StatusReady {
		t.Fatalf("task = %#v, want generated ready task", out.Task)
	}
	if len(emitted) != 2 {
		t.Fatalf("emitted len = %d, want create_requested and created", len(emitted))
	}
	store, err := runtimetask.NewStore(events)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	state, err := store.Project(context.Background(), out.Task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.ID != out.Task.ID || state.Task.Title == "" {
		t.Fatalf("projected task = %#v, want created task", state.Task)
	}
}

func TestTaskCreateRejectsDuplicateID(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	ctx := operation.NewContext(context.Background(), nil)
	first := ops[0].Run(ctx, coretask.TaskCreateRequest{ID: "task_fixed", Title: "Original"})
	if first.IsError() {
		t.Fatalf("first task_create error = %#v", first.Error)
	}
	second := ops[0].Run(ctx, coretask.TaskCreateRequest{ID: "task_fixed", Title: "Replacement"})
	if !second.IsError() || second.Error.Code != "task_already_exists" {
		t.Fatalf("second result = %#v, want task_already_exists", second)
	}
	store, err := runtimetask.NewStore(events)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	state, err := store.Project(context.Background(), "task_fixed")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Title != "Original" {
		t.Fatalf("title = %q, want Original", state.Task.Title)
	}
}

func TestTaskCreateRespectsDraft(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[0].Run(operation.NewContext(context.Background(), nil), coretask.TaskCreateRequest{
		Title:  "Draft task",
		Status: coretask.StatusDraft,
	})
	if result.IsError() {
		t.Fatalf("task_create error = %#v", result.Error)
	}
	out := result.Output.(coretask.TaskCreateResult)
	if out.Task.Status != coretask.StatusDraft {
		t.Fatalf("status = %q, want draft", out.Task.Status)
	}
}

func TestTaskManagementOperations(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{ID: "task_1", Title: "Original"})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	modified := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID: "task_1",
		Modifications: []coretask.TaskModification{
			{Op: "set_status", Status: coretask.StatusBlocked, Reason: "waiting for input"},
			{Op: "add_artifact", Artifact: coretask.ArtifactSpec{ID: "answer", Name: "answer", Kind: coretask.ArtifactText, Value: "done"}},
		},
	})
	if modified.IsError() {
		t.Fatalf("task_modify error = %#v", modified.Error)
	}
	modifyOut := modified.Output.(coretask.TaskModifyResult)
	if modifyOut.Task.Status != coretask.StatusBlocked || len(modifyOut.Task.Artifacts) != 1 {
		t.Fatalf("modified = %#v, want blocked task with artifact", modifyOut)
	}
	get := byName[TaskGetOp].Run(ctx, coretask.TaskGetRequest{ID: "task_1"})
	if get.IsError() {
		t.Fatalf("task_get error = %#v", get.Error)
	}
	getOut := get.Output.(coretask.TaskGetResult)
	if len(getOut.Task.Artifacts) != 1 || getOut.Task.Artifacts[0].Name != "answer" {
		t.Fatalf("artifacts = %#v, want answer", getOut.Task.Artifacts)
	}
	if getOut.View != coretask.ViewFull || !strings.Contains(getOut.ModelText(), "Artifacts:") {
		t.Fatalf("get ModelText = %q, want full artifact detail", getOut.ModelText())
	}
	listArtifacts := byName[TaskListArtifactsOp].Run(ctx, coretask.TaskArtifactListRequest{ID: "task_1"})
	if listArtifacts.IsError() {
		t.Fatalf("task_list_artifacts error = %#v", listArtifacts.Error)
	}
	artifactOut := listArtifacts.Output.(coretask.TaskArtifactListResult)
	if len(artifactOut.Artifacts) != 1 || artifactOut.Artifacts[0].Artifact.ID != "answer" {
		t.Fatalf("artifact list = %#v, want answer", artifactOut.Artifacts)
	}
	getArtifact := byName[TaskGetArtifactOp].Run(ctx, coretask.TaskArtifactGetRequest{ID: "task_1", ArtifactID: "answer"})
	if getArtifact.IsError() {
		t.Fatalf("task_get_artifact error = %#v", getArtifact.Error)
	}
	if text := getArtifact.Output.(coretask.TaskArtifactGetResult).ModelText(); !strings.Contains(text, "value=done") {
		t.Fatalf("artifact ModelText = %q, want value", text)
	}
	list := byName[TaskListOp].Run(ctx, coretask.TaskListRequest{Status: coretask.StatusBlocked})
	if list.IsError() {
		t.Fatalf("task_list error = %#v", list.Error)
	}
	listOut := list.Output.(coretask.TaskListResult)
	if len(listOut.Tasks) != 1 || listOut.Tasks[0].ID != "task_1" {
		t.Fatalf("task list = %#v, want task_1", listOut.Tasks)
	}
}

func TestTaskModifyStepArtifactDefaultsToManualExecution(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{
		ID:             "task_1",
		Title:          "Step artifacts",
		SuggestedSteps: []coretask.Step{{ID: "inspect", Title: "Inspect"}},
	})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	modified := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID: "task_1",
		Modifications: []coretask.TaskModification{{
			Op:       "add_artifact",
			StepID:   "inspect",
			Artifact: coretask.ArtifactSpec{ID: "note", Name: "note", Kind: coretask.ArtifactText, Value: "step evidence"},
		}},
	})
	if modified.IsError() {
		t.Fatalf("task_modify error = %#v", modified.Error)
	}
	out := modified.Output.(coretask.TaskModifyResult)
	step := out.Executions["manual"].Steps["inspect"]
	if len(step.Artifacts) != 1 || step.Artifacts[0].ID != "note" {
		t.Fatalf("manual step artifacts = %#v, want note", step.Artifacts)
	}
	if len(out.Executions["manual"].Artifacts) != 0 {
		t.Fatalf("manual execution artifacts = %#v, want no duplicated step artifact", out.Executions["manual"].Artifacts)
	}
	listArtifacts := byName[TaskListArtifactsOp].Run(ctx, coretask.TaskArtifactListRequest{ID: "task_1"})
	if listArtifacts.IsError() {
		t.Fatalf("task_list_artifacts error = %#v", listArtifacts.Error)
	}
	artifactOut := listArtifacts.Output.(coretask.TaskArtifactListResult)
	if len(artifactOut.Artifacts) != 1 || artifactOut.Artifacts[0].ExecutionID != "manual" || artifactOut.Artifacts[0].StepID != "inspect" {
		t.Fatalf("artifact list = %#v, want one manual step artifact", artifactOut.Artifacts)
	}
	if text := artifactOut.ModelText(); !strings.Contains(text, "execution:manual/step:inspect") {
		t.Fatalf("artifact list ModelText = %q, want scoped step label", text)
	}
	get := byName[TaskGetOp].Run(ctx, coretask.TaskGetRequest{ID: "task_1"})
	if get.IsError() {
		t.Fatalf("task_get error = %#v", get.Error)
	}
	if text := get.Output.(coretask.TaskGetResult).ModelText(); !strings.Contains(text, "execution:manual/step:inspect") {
		t.Fatalf("task_get ModelText = %q, want scoped step artifact", text)
	}
}

func TestTaskModifyStepArtifactUsesCurrentExecution(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{
		ID:             "task_1",
		Title:          "Step artifacts",
		SuggestedSteps: []coretask.Step{{ID: "inspect", Title: "Inspect"}},
	})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	modified := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID: "task_1",
		Modifications: []coretask.TaskModification{
			{Op: "set_step_status", ExecutionID: "exec_live", StepID: "inspect", StepStatus: coretask.StepStatusRunning},
			{Op: "add_artifact", StepID: "inspect", Artifact: coretask.ArtifactSpec{ID: "note", Name: "note", Kind: coretask.ArtifactText}},
		},
	})
	if modified.IsError() {
		t.Fatalf("task_modify error = %#v", modified.Error)
	}
	out := modified.Output.(coretask.TaskModifyResult)
	if len(out.Executions["exec_live"].Steps["inspect"].Artifacts) != 1 {
		t.Fatalf("exec_live step artifacts = %#v, want one artifact", out.Executions["exec_live"].Steps["inspect"].Artifacts)
	}
}

func TestTaskModifyRejectsDuplicateArtifactIDsAcrossScopes(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{
		ID:             "task_1",
		Title:          "Duplicate artifacts",
		SuggestedSteps: []coretask.Step{{ID: "inspect", Title: "Inspect"}},
	})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	first := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "add_artifact", Artifact: coretask.ArtifactSpec{ID: "same", Name: "task", Kind: coretask.ArtifactText}}},
	})
	if first.IsError() {
		t.Fatalf("first task_modify error = %#v", first.Error)
	}
	second := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "add_artifact", StepID: "inspect", Artifact: coretask.ArtifactSpec{ID: "same", Name: "step", Kind: coretask.ArtifactText}}},
	})
	if !second.IsError() || second.Error.Code != "task_modify_invalid" {
		t.Fatalf("second task_modify = %#v, want duplicate artifact failure", second)
	}
	listArtifacts := byName[TaskListArtifactsOp].Run(ctx, coretask.TaskArtifactListRequest{ID: "task_1"})
	if listArtifacts.IsError() {
		t.Fatalf("task_list_artifacts error = %#v", listArtifacts.Error)
	}
	if got := len(listArtifacts.Output.(coretask.TaskArtifactListResult).Artifacts); got != 1 {
		t.Fatalf("artifacts len = %d, want 1 after rejected duplicate", got)
	}
}

func TestTaskModifyValidatesCompletion(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{
		ID:      "task_1",
		Title:   "Needs report",
		Outputs: []coretask.ArtifactSpec{{ID: "report", Name: "report", Kind: coretask.ArtifactReport, Required: true}},
	})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	complete := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "complete"}},
	})
	if !complete.IsError() || complete.Error.Code != "task_modify_invalid" {
		t.Fatalf("complete = %#v, want validation error", complete)
	}
	complete = byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID: "task_1",
		Modifications: []coretask.TaskModification{{
			Op:        "complete",
			Artifacts: []coretask.ArtifactSpec{{ID: "report", Name: "report", Kind: coretask.ArtifactReport}},
		}},
	})
	if complete.IsError() {
		t.Fatalf("complete with artifact error = %#v", complete.Error)
	}
	out := complete.Output.(coretask.TaskModifyResult)
	if out.Task.Status != coretask.StatusCompleted || !out.Validation.Completable {
		t.Fatalf("complete output = %#v, want completed completable", out)
	}
	validate := byName[TaskValidateOp].Run(ctx, coretask.TaskValidateRequest{ID: "task_1"})
	if validate.IsError() {
		t.Fatalf("task_validate error = %#v", validate.Error)
	}
	if !validate.Output.(coretask.TaskValidationResult).Completable {
		t.Fatalf("validation = %#v, want completable", validate.Output)
	}
}

func TestTaskRunAndSchedulerControlsUseRunner(t *testing.T) {
	runner := &fakeTaskRunner{status: coretask.SchedulerStatusResult{
		Enabled: true, Active: true, Capacity: 1, MaxParallel: 2,
	}}
	ops, err := NewWithRunner(runner).Operations(context.Background(), pluginhost.Context{EventStore: eventstore.NewMemoryStore()})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	run := byName[TaskRunOp].Run(ctx, coretask.ExecutionRequest{TaskID: "task_1"})
	if run.IsError() {
		t.Fatalf("task_run error = %#v", run.Error)
	}
	runOut := run.Output.(coretask.ExecutionResult)
	if runOut.TaskID != "task_1" || runOut.Status != coretask.StatusRunning || !runner.submitted {
		t.Fatalf("task_run output = %#v submitted=%v, want running submitted", runOut, runner.submitted)
	}
	status := byName[TaskSchedulerStatusOp].Run(ctx, coretask.SchedulerStatusRequest{})
	if status.IsError() {
		t.Fatalf("task_scheduler_status error = %#v", status.Error)
	}
	if got := status.Output.(coretask.SchedulerStatusResult); !got.Enabled || got.Capacity != 1 {
		t.Fatalf("status = %#v, want enabled capacity 1", got)
	}
	disabled := byName[TaskSchedulerSetEnabledOp].Run(ctx, coretask.SchedulerSetEnabledRequest{Enabled: false})
	if disabled.IsError() {
		t.Fatalf("task_scheduler_set_enabled error = %#v", disabled.Error)
	}
	if got := disabled.Output.(coretask.SchedulerStatusResult); got.Enabled {
		t.Fatalf("enabled = true, want false")
	}
}

func TestTaskRunFailsWithoutScheduler(t *testing.T) {
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: eventstore.NewMemoryStore()})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := operationsByName(ops)[TaskRunOp].Run(operation.NewContext(context.Background(), nil), coretask.ExecutionRequest{TaskID: "task_1"})
	if !result.IsError() || result.Error.Code != "task_scheduler_missing" {
		t.Fatalf("task_run = %#v, want task_scheduler_missing", result)
	}
}

func TestTaskModifyRequiresExplicitTaskReopen(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{ID: "task_1", Title: "Reopen task"})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	complete := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "complete"}},
	})
	if complete.IsError() {
		t.Fatalf("complete error = %#v", complete.Error)
	}
	regress := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "set_status", Status: coretask.StatusRunning}},
	})
	if !regress.IsError() || !strings.Contains(regress.Error.Message, "use reopen") {
		t.Fatalf("set_status result = %#v, want reopen error", regress)
	}
	reopen := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "reopen", Status: coretask.StatusRunning}},
	})
	if reopen.IsError() {
		t.Fatalf("reopen error = %#v", reopen.Error)
	}
	if got := reopen.Output.(coretask.TaskModifyResult).Task.Status; got != coretask.StatusRunning {
		t.Fatalf("status = %q, want running", got)
	}
}

type fakeTaskRunner struct {
	status    coretask.SchedulerStatusResult
	submitted bool
}

func (r *fakeTaskRunner) SubmitTask(context.Context, coretask.ID) (taskexecutor.SubmitResult, error) {
	r.submitted = true
	return taskexecutor.SubmitResult{
		TaskID: "task_1", Status: coretask.StatusReady, Started: true, Running: true, Summary: "Task task_1 scheduled.",
	}, nil
}

func (r *fakeTaskRunner) Status() coretask.SchedulerStatusResult {
	return r.status
}

func (r *fakeTaskRunner) SetEnabled(enabled bool) coretask.SchedulerStatusResult {
	r.status.Enabled = enabled
	return r.status
}

func TestTaskModifyRequiresExplicitStepReopen(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{
		ID:             "task_1",
		Title:          "Reopen step",
		SuggestedSteps: []coretask.Step{{ID: "inspect", Title: "Inspect"}},
	})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	completeStep := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "set_step_status", StepID: "inspect", StepStatus: coretask.StepStatusCompleted, StepOutput: "done"}},
	})
	if completeStep.IsError() {
		t.Fatalf("complete step error = %#v", completeStep.Error)
	}
	regress := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "set_step_status", StepID: "inspect", StepStatus: coretask.StepStatusRunning}},
	})
	if !regress.IsError() || !strings.Contains(regress.Error.Message, "use reopen_step") {
		t.Fatalf("set_step_status result = %#v, want reopen_step error", regress)
	}
	reopen := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "reopen_step", StepID: "inspect", StepStatus: coretask.StepStatusRunning}},
	})
	if reopen.IsError() {
		t.Fatalf("reopen_step error = %#v", reopen.Error)
	}
	step := reopen.Output.(coretask.TaskModifyResult).Executions["manual"].Steps["inspect"]
	if step.Status != coretask.StepStatusRunning || !step.CompletedAt.IsZero() || step.Output != nil || step.Error != nil {
		t.Fatalf("step = %#v, want reopened running with cleared metadata", step)
	}
}

func TestTaskModifyRejectsRemovingStepWithExecutionState(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{
		ID:             "task_1",
		Title:          "Remove step",
		SuggestedSteps: []coretask.Step{{ID: "inspect", Title: "Inspect"}},
	})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	started := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "set_step_status", StepID: "inspect", StepStatus: coretask.StepStatusRunning}},
	})
	if started.IsError() {
		t.Fatalf("set_step_status error = %#v", started.Error)
	}
	removed := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "remove_step", StepID: "inspect"}},
	})
	if !removed.IsError() || !strings.Contains(removed.Error.Message, "has execution state") {
		t.Fatalf("remove_step result = %#v, want execution state error", removed)
	}
}

func TestTaskModifyCompleteRequiresExplicitForceOverrides(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{
		ID:             "task_1",
		Title:          "Forced complete",
		Outputs:        []coretask.ArtifactSpec{{ID: "report", Name: "report", Kind: coretask.ArtifactReport, Required: true}},
		SuggestedSteps: []coretask.Step{{ID: "inspect", Title: "Inspect"}},
	})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	partial := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "complete", ForceOverrides: []string{"required_output"}}},
	})
	if !partial.IsError() || partial.Error.Code != "task_modify_invalid" {
		t.Fatalf("partial force = %#v, want task_modify_invalid", partial)
	}
	forced := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "complete", ForceOverrides: []string{"required_output", "steps_terminal"}}},
	})
	if forced.IsError() {
		t.Fatalf("forced complete error = %#v", forced.Error)
	}
	out := forced.Output.(coretask.TaskModifyResult)
	if out.Task.Status != coretask.StatusCompleted || out.Validation.Completable {
		t.Fatalf("forced output = %#v, want completed with validation still incomplete", out)
	}
}

func TestTaskCreateFailsWithoutEventStore(t *testing.T) {
	ops, err := New().Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[0].Run(operation.NewContext(context.Background(), nil), coretask.TaskCreateRequest{Title: "Task"})
	if !result.IsError() || result.Error.Code != "task_store_missing" {
		t.Fatalf("result = %#v, want task_store_missing", result)
	}
}

func TestTaskCreateSchemaIncludesEnums(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(taskCreateSpec().Input.Schema.Data, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	raw := string(taskCreateSpec().Input.Schema.Data)
	for _, want := range []string{`"status"`, `"draft"`, `"ready"`, `"priority"`, `"urgent"`, `"kind"`, `"generic"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("schema = %s, want %s", raw, want)
		}
	}
	modifyRaw := string(taskModifySpec().Input.Schema.Data)
	for _, want := range []string{`"oneOf"`, `"update_metadata"`, `"add_artifact"`, `"set_step_status"`, `"reopen"`, `"reopen_step"`, `"complete"`, `"force_overrides"`, `"blocked"`, `"skipped"`} {
		if !strings.Contains(modifyRaw, want) {
			t.Fatalf("task_modify schema = %s, want %s", modifyRaw, want)
		}
	}
}

func hasOperation(specs []operation.Spec, name string) bool {
	for _, spec := range specs {
		if string(spec.Ref.Name) == name {
			return true
		}
	}
	return false
}

func operationsByName(ops []operation.Operation) map[string]operation.Operation {
	out := map[string]operation.Operation{}
	for _, op := range ops {
		out[string(op.Spec().Ref.Name)] = op
	}
	return out
}
