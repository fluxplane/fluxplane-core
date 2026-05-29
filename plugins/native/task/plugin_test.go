package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/operation"
	coretask "github.com/fluxplane/fluxplane-core/core/task"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
	"github.com/fluxplane/fluxplane-core/orchestration/taskexecutor"
	"github.com/fluxplane/fluxplane-core/runtime/eventstore"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimetask "github.com/fluxplane/fluxplane-core/runtime/task"
	runtimethread "github.com/fluxplane/fluxplane-core/runtime/thread"
	"github.com/fluxplane/fluxplane-event"
)

func TestContributionsIncludeTaskResources(t *testing.T) {
	bundle, err := New().Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Commands) != 2 || bundle.Commands[0].Path.String() != "/task" || bundle.Commands[1].Path.String() != "/plan" {
		t.Fatalf("commands = %#v, want /task and /plan", bundle.Commands)
	}
	if len(bundle.Agents) != 5 || string(bundle.Agents[0].Name) != TaskAgent || string(bundle.Agents[1].Name) != PlanAgent || string(bundle.Agents[2].Name) != WorkerAgent || string(bundle.Agents[3].Name) != ExplorerAgent || string(bundle.Agents[4].Name) != ReviewerAgent {
		t.Fatalf("agents = %#v, want task, planner, worker, explorer, and reviewer agents", bundle.Agents)
	}
	if !operationRefsContain(bundle.Agents[2].Operations, "web_search") || !operationRefsContain(bundle.Agents[2].Operations, "web_request") {
		t.Fatalf("worker operations = %#v, want web_search and web_request", bundle.Agents[2].Operations)
	}
	if !operationRefsContain(bundle.Agents[3].Operations, "web_search") || !operationRefsContain(bundle.Agents[3].Operations, "web_request") {
		t.Fatalf("explorer operations = %#v, want web_search and web_request", bundle.Agents[3].Operations)
	}
	if len(bundle.Sessions) != 5 || string(bundle.Sessions[0].Name) != TaskSession || string(bundle.Sessions[1].Name) != PlanSession || string(bundle.Sessions[2].Name) != WorkerSession || string(bundle.Sessions[3].Name) != ExplorerSession || string(bundle.Sessions[4].Name) != ReviewerSession {
		t.Fatalf("sessions = %#v, want task, planner, worker, explorer, and reviewer sessions", bundle.Sessions)
	}
	if planner := bundle.Agents[1].System; !strings.Contains(planner, "status=draft") || !strings.Contains(planner, "not scheduled yet") || !strings.Contains(planner, "Do not create a second task") || !strings.Contains(planner, "task_run") || !strings.Contains(planner, "scheduler response") {
		t.Fatalf("planner instructions = %q, want draft visibility, approval/refinement, and scheduler feedback guidance", planner)
	}
	if taskAgent := bundle.Agents[0].System; !strings.Contains(taskAgent, "immediate execution") || !strings.Contains(taskAgent, "task_run") || !strings.Contains(taskAgent, "at the same time") || !strings.Contains(taskAgent, "one ready explorer-assigned task per distinct thread") {
		t.Fatalf("task agent instructions = %q, want immediate and parallel execution guidance", taskAgent)
	}
	if len(bundle.AssertionDerivers) != 1 || bundle.AssertionDerivers[0].Name != ParallelIntentDeriver {
		t.Fatalf("assertion derivers = %#v, want parallel intent deriver", bundle.AssertionDerivers)
	}
	if !operationRefsContain(bundle.Sessions[0].Operations, TaskRunOp) || !operationRefsContain(bundle.Sessions[0].Operations, TaskSchedulerStatusOp) {
		t.Fatalf("task session operations = %#v, want task_run and scheduler status", bundle.Sessions[0].Operations)
	}
	if len(bundle.Operations) != 12 {
		t.Fatalf("operations len = %d, want 12", len(bundle.Operations))
	}
	for _, name := range []string{TaskCreateOp, TaskModifyOp, TaskGetOp, TaskListOp, TaskListArtifactsOp, TaskGetArtifactOp, TaskReadArtifactOp, TaskValidateOp, ReviewRequestOp, TaskRunOp, TaskSchedulerStatusOp, TaskSchedulerSetEnabledOp} {
		if !hasOperation(bundle.Operations, name) {
			t.Fatalf("operations = %#v, want %s", bundle.Operations, name)
		}
	}
}

func TestParallelIntentAssertionDeriver(t *testing.T) {
	derivers, err := New().AssertionDerivers(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("AssertionDerivers: %v", err)
	}
	assertions, err := derivers[0].Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{Observations: []coreevidence.Observation{{
		ID:      "obs_1",
		Kind:    "channel.message",
		Scope:   "thread",
		Content: "Work on both things at the same time.",
	}}})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(assertions) != 1 || assertions[0].Kind != AssertionParallelWork || assertions[0].Target != "task-scheduler" || len(assertions[0].ObservationIDs) != 1 {
		t.Fatalf("assertions = %#v, want parallel work assertion", assertions)
	}
	negative, err := derivers[0].Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{Observations: []coreevidence.Observation{{
		ID:      "obs_2",
		Kind:    "channel.message",
		Content: "Please review this one file.",
	}}})
	if err != nil {
		t.Fatalf("Derive negative: %v", err)
	}
	if len(negative) != 0 {
		t.Fatalf("negative assertions = %#v, want none", negative)
	}
}

func TestParallelIntentReactionEnablesTaskOperationSet(t *testing.T) {
	rules, err := New().Reactions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Reactions: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules = %#v, want one parallel intent rule", rules)
	}
	if err := rules[0].Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rules[0].When.Assertion != AssertionParallelWork || rules[0].Actions[0].OperationSet != Name {
		t.Fatalf("rule = %#v, want parallel assertion to enable task operation set", rules[0])
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

func TestTaskCreateRecordsOriginSessionMetadata(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	ctx := operation.NewContext(context.Background(), nil)
	ctx = sessionenv.OperationContext(ctx, sessionenv.Config{
		Thread: corethread.Ref{ID: "thread-origin", BranchID: "feature"},
		RunID:  "run-origin",
	}, "call-1")

	result := ops[0].Run(ctx, coretask.TaskCreateRequest{Title: "Origin task"})
	if result.IsError() {
		t.Fatalf("task_create error = %#v", result.Error)
	}
	out := result.Output.(coretask.TaskCreateResult)
	if out.Task.Metadata[coretask.MetadataOriginThreadID] != "thread-origin" {
		t.Fatalf("metadata = %#v, want origin thread", out.Task.Metadata)
	}
	if out.Task.Metadata[coretask.MetadataOriginBranchID] != "feature" {
		t.Fatalf("metadata = %#v, want origin branch", out.Task.Metadata)
	}
	if out.Task.Metadata[coretask.MetadataOriginRunID] != "run-origin" {
		t.Fatalf("metadata = %#v, want origin run", out.Task.Metadata)
	}
}

func TestTaskListDefaultsToCurrentSessionScope(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctxA := sessionenv.OperationContext(operation.NewContext(context.Background(), nil), sessionenv.Config{
		Thread: corethread.Ref{ID: "thread-a", BranchID: "main"},
		RunID:  "run-a",
	}, "call-a")
	ctxB := sessionenv.OperationContext(operation.NewContext(context.Background(), nil), sessionenv.Config{
		Thread: corethread.Ref{ID: "thread-b", BranchID: "main"},
		RunID:  "run-b",
	}, "call-b")

	if result := byName[TaskCreateOp].Run(ctxA, coretask.TaskCreateRequest{ID: "task_a", Title: "Task A"}); result.IsError() {
		t.Fatalf("task_create a error = %#v", result.Error)
	}
	if result := byName[TaskCreateOp].Run(ctxB, coretask.TaskCreateRequest{ID: "task_b", Title: "Task B"}); result.IsError() {
		t.Fatalf("task_create b error = %#v", result.Error)
	}

	current := byName[TaskListOp].Run(ctxA, coretask.TaskListRequest{})
	if current.IsError() {
		t.Fatalf("task_list current error = %#v", current.Error)
	}
	currentOut := current.Output.(coretask.TaskListResult)
	if len(currentOut.Tasks) != 1 || currentOut.Tasks[0].ID != "task_a" {
		t.Fatalf("current-session tasks = %#v, want task_a only", currentOut.Tasks)
	}

	all := byName[TaskListOp].Run(ctxA, coretask.TaskListRequest{Scope: coretask.TaskListScopeAll})
	if all.IsError() {
		t.Fatalf("task_list all error = %#v", all.Error)
	}
	allOut := all.Output.(coretask.TaskListResult)
	if len(allOut.Tasks) != 2 {
		t.Fatalf("all tasks = %#v, want two tasks", allOut.Tasks)
	}

	outside := byName[TaskListOp].Run(operation.NewContext(context.Background(), nil), coretask.TaskListRequest{})
	if outside.IsError() {
		t.Fatalf("task_list outside error = %#v", outside.Error)
	}
	outsideOut := outside.Output.(coretask.TaskListResult)
	if len(outsideOut.Tasks) != 2 {
		t.Fatalf("outside-session tasks = %#v, want global list", outsideOut.Tasks)
	}
}

func TestTaskCreatedByDelegateUsesParentThreadOrigin(t *testing.T) {
	events := eventstore.NewMemoryStore()
	threadStore, err := runtimethread.NewStore(events)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := threadStore.Create(context.Background(), corethread.CreateParams{ID: "thread-parent"}); err != nil {
		t.Fatalf("create parent thread: %v", err)
	}
	if _, err := threadStore.Create(context.Background(), corethread.CreateParams{
		ID:       "thread-delegate",
		Metadata: map[string]string{"parent_thread_id": "thread-parent"},
	}); err != nil {
		t.Fatalf("create delegate thread: %v", err)
	}
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	delegateCtx := sessionenv.OperationContext(operation.NewContext(context.Background(), nil), sessionenv.Config{
		Thread:      corethread.Ref{ID: "thread-delegate", BranchID: "main"},
		ThreadStore: threadStore,
		RunID:       "run-delegate",
	}, "call-delegate")

	created := byName[TaskCreateOp].Run(delegateCtx, coretask.TaskCreateRequest{ID: "task_delegate", Title: "Delegate task"})
	if created.IsError() {
		t.Fatalf("task_create delegate error = %#v", created.Error)
	}
	task := created.Output.(coretask.TaskCreateResult).Task
	if task.Metadata[coretask.MetadataOriginThreadID] != "thread-parent" {
		t.Fatalf("origin thread = %q, want parent thread", task.Metadata[coretask.MetadataOriginThreadID])
	}
	if task.Metadata[coretask.MetadataOriginDelegateThreadID] != "thread-delegate" {
		t.Fatalf("origin delegate thread = %q, want delegate thread", task.Metadata[coretask.MetadataOriginDelegateThreadID])
	}

	parentCtx := sessionenv.OperationContext(operation.NewContext(context.Background(), nil), sessionenv.Config{
		Thread: corethread.Ref{ID: "thread-parent", BranchID: "main"},
		RunID:  "run-parent",
	}, "call-parent")
	listed := byName[TaskListOp].Run(parentCtx, coretask.TaskListRequest{})
	if listed.IsError() {
		t.Fatalf("task_list parent error = %#v", listed.Error)
	}
	out := listed.Output.(coretask.TaskListResult)
	if len(out.Tasks) != 1 || out.Tasks[0].ID != "task_delegate" {
		t.Fatalf("parent current-session tasks = %#v, want delegate task", out.Tasks)
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
	if text := getArtifact.Output.(coretask.TaskArtifactGetResult).ModelText(); !strings.Contains(text, "preview=done") {
		t.Fatalf("artifact ModelText = %q, want preview", text)
	}
	artifactGetOut := getArtifact.Output.(coretask.TaskArtifactGetResult)
	if artifactGetOut.ValueIncluded || artifactGetOut.Artifact.Artifact.Value != nil {
		t.Fatalf("artifact output = %#v, want value omitted by default", artifactGetOut)
	}
	getArtifact = byName[TaskGetArtifactOp].Run(ctx, coretask.TaskArtifactGetRequest{ID: "task_1", ArtifactID: "answer", IncludeValue: true})
	if getArtifact.IsError() {
		t.Fatalf("task_get_artifact include value error = %#v", getArtifact.Error)
	}
	artifactGetOut = getArtifact.Output.(coretask.TaskArtifactGetResult)
	if !artifactGetOut.ValueIncluded || artifactGetOut.Artifact.Artifact.Value != "done" {
		t.Fatalf("artifact output = %#v, want included value", artifactGetOut)
	}
	readArtifact := byName[TaskReadArtifactOp].Run(ctx, coretask.TaskArtifactReadRequest{ID: "task_1", ArtifactID: "answer"})
	if readArtifact.IsError() {
		t.Fatalf("task_read_artifact error = %#v", readArtifact.Error)
	}
	if got := readArtifact.Output.(coretask.TaskArtifactReadResult); got.Content != "done" || got.Source != "value" {
		t.Fatalf("artifact read = %#v, want inline value", got)
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

func TestReviewRequestCreatesReviewerTask(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{
		ID: "task_subject", Title: "Subject", Objective: "Do the work", Status: coretask.StatusCompleted,
	})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	review := byName[ReviewRequestOp].Run(ctx, coretask.ReviewRequest{TaskID: "task_subject", Instruction: "check evidence"})
	if review.IsError() {
		t.Fatalf("review_request error = %#v", review.Error)
	}
	out := review.Output.(coretask.ReviewRequestResult)
	if out.Task.Assignee != coretask.RoleReviewer || out.Task.Metadata["review_subject_task_id"] != "task_subject" {
		t.Fatalf("review task = %#v, want reviewer task linked to subject", out.Task)
	}
	if len(out.Task.Outputs) != 1 || out.Task.Outputs[0].Kind != coretask.ArtifactReview || !out.Task.Outputs[0].Required {
		t.Fatalf("review outputs = %#v, want required review artifact", out.Task.Outputs)
	}
}

func TestTaskReadArtifactPrefersReplacementRefOverPreviewValue(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	full := strings.Repeat("full-result ", 2048)
	_, replacement, err := operationruntime.ReplaceLargeResult(context.Background(), operation.OK(operation.Rendered{Text: full, Model: full, Data: full}), operationruntime.ReplacementOptions{
		ThresholdBytes: 1024,
		TempDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ReplaceLargeResult: %v", err)
	}
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{ID: "task_1", Title: "Replacement artifact"})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	modified := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID: "task_1",
		Modifications: []coretask.TaskModification{{
			Op: "add_artifact",
			Artifact: coretask.ArtifactSpec{
				ID:    "large",
				Name:  "large",
				Kind:  coretask.ArtifactReference,
				Ref:   replacement.Path,
				Value: "preview-only",
				Metadata: map[string]string{
					"replaced": "true",
				},
			},
		}},
	})
	if modified.IsError() {
		t.Fatalf("task_modify error = %#v", modified.Error)
	}
	read := byName[TaskReadArtifactOp].Run(ctx, coretask.TaskArtifactReadRequest{ID: "task_1", ArtifactID: "large", MaxBytes: int64(len(full) * 2)})
	if read.IsError() {
		t.Fatalf("task_read_artifact error = %#v", read.Error)
	}
	out := read.Output.(coretask.TaskArtifactReadResult)
	if out.Source != "replacement_ref" {
		t.Fatalf("source = %q, want replacement_ref", out.Source)
	}
	if strings.Contains(out.Content, "preview-only") || !strings.Contains(out.Content, "full-result full-result") {
		t.Fatalf("content = %.200q, want full replacement content instead of preview", out.Content)
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

func TestTaskModifyConcurrentUniqueArtifacts(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{ID: "task_1", Title: "Concurrent artifacts"})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan operation.Result, workers)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := byName[TaskModifyOp].Run(operation.NewContext(context.Background(), nil), coretask.TaskModifyRequest{
				ID: "task_1",
				Modifications: []coretask.TaskModification{{
					Op:       "add_artifact",
					Artifact: coretask.ArtifactSpec{ID: fmt.Sprintf("artifact-%02d", i), Name: fmt.Sprintf("artifact-%02d", i), Kind: coretask.ArtifactText},
				}},
			})
			errs <- result
		}()
	}
	wg.Wait()
	close(errs)
	for result := range errs {
		if result.IsError() {
			t.Fatalf("task_modify concurrent error = %#v", result.Error)
		}
	}
	listArtifacts := byName[TaskListArtifactsOp].Run(ctx, coretask.TaskArtifactListRequest{ID: "task_1"})
	if listArtifacts.IsError() {
		t.Fatalf("task_list_artifacts error = %#v", listArtifacts.Error)
	}
	artifactOut := listArtifacts.Output.(coretask.TaskArtifactListResult)
	if len(artifactOut.Artifacts) != workers {
		t.Fatalf("artifacts len = %d, want %d: %#v", len(artifactOut.Artifacts), workers, artifactOut.Artifacts)
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
	if runOut.TaskID != "task_1" || runOut.Status != coretask.StatusRunning || !runOut.Started || !runOut.Running || !runOut.Background || runOut.WaitingForCapacity || !runner.submitted {
		t.Fatalf("task_run output = %#v submitted=%v, want running submitted", runOut, runner.submitted)
	}
	if !strings.Contains(runOut.ModelText(), "scheduled") {
		t.Fatalf("task_run model text = %q, want scheduled feedback", runOut.ModelText())
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

func TestTaskRunReportsWaitingForCapacity(t *testing.T) {
	runner := &fakeTaskRunner{submitResult: taskexecutor.SubmitResult{
		TaskID:  "task_1",
		Status:  coretask.StatusReady,
		Summary: "Task task_1 is ready but waiting for scheduler capacity.",
	}}
	ops, err := NewWithRunner(runner).Operations(context.Background(), pluginhost.Context{EventStore: eventstore.NewMemoryStore()})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := operationsByName(ops)[TaskRunOp].Run(operation.NewContext(context.Background(), nil), coretask.ExecutionRequest{TaskID: "task_1"})
	if result.IsError() {
		t.Fatalf("task_run error = %#v", result.Error)
	}
	out := result.Output.(coretask.ExecutionResult)
	if !out.WaitingForCapacity || out.Started || out.Running || out.Background {
		t.Fatalf("execution result = %#v, want waiting for capacity only", out)
	}
}

func TestTaskRunRecordsCurrentSessionWatcher(t *testing.T) {
	events := eventstore.NewMemoryStore()
	runner := &fakeTaskRunner{}
	ops, err := NewWithRunner(runner).Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	baseCtx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(baseCtx, coretask.TaskCreateRequest{ID: "task_1", Title: "Watch task", Status: coretask.StatusReady})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	runCtx := sessionenv.OperationContext(baseCtx, sessionenv.Config{
		Thread: corethread.Ref{ID: "thread-watch", BranchID: "feature"},
		RunID:  "run-watch",
	}, "call-1")

	result := byName[TaskRunOp].Run(runCtx, coretask.ExecutionRequest{TaskID: "task_1"})
	if result.IsError() {
		t.Fatalf("task_run error = %#v", result.Error)
	}
	store, err := runtimetask.NewStore(events)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	state, err := store.Project(context.Background(), "task_1")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := state.Task.Metadata[coretask.MetadataWatchThreadID]; got != "thread-watch" {
		t.Fatalf("watch thread = %q, want thread-watch; metadata=%#v", got, state.Task.Metadata)
	}
	if got := state.Task.Metadata[coretask.MetadataWatchBranchID]; got != "feature" {
		t.Fatalf("watch branch = %q, want feature; metadata=%#v", got, state.Task.Metadata)
	}
	if got := state.Task.Metadata[coretask.MetadataWatchRunID]; got != "run-watch" {
		t.Fatalf("watch run = %q, want run-watch; metadata=%#v", got, state.Task.Metadata)
	}
}

func TestTaskRunDoesNotRecordWatcherForMissingTask(t *testing.T) {
	events := eventstore.NewMemoryStore()
	runner := &fakeTaskRunner{submitErr: errors.New("task missing")}
	ops, err := NewWithRunner(runner).Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	baseCtx := operation.NewContext(context.Background(), nil)
	runCtx := sessionenv.OperationContext(baseCtx, sessionenv.Config{
		Thread: corethread.Ref{ID: "thread-watch", BranchID: "main"},
		RunID:  "run-watch",
	}, "call-1")

	result := operationsByName(ops)[TaskRunOp].Run(runCtx, coretask.ExecutionRequest{TaskID: "missing_task"})
	if !result.IsError() || result.Error.Code != "task_run_failed" {
		t.Fatalf("task_run result = %#v, want runner failure", result)
	}
	records, err := events.Load(context.Background(), runtimetask.StreamID("missing_task"), event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load missing task stream: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("missing task stream records = %#v, want none", records)
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

func TestTaskModifyRejectsRunningTaskResetToReady(t *testing.T) {
	events := eventstore.NewMemoryStore()
	ops, err := New().Operations(context.Background(), pluginhost.Context{EventStore: events})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	ctx := operation.NewContext(context.Background(), nil)
	created := byName[TaskCreateOp].Run(ctx, coretask.TaskCreateRequest{ID: "task_1", Title: "Running task"})
	if created.IsError() {
		t.Fatalf("task_create error = %#v", created.Error)
	}
	running := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "set_status", Status: coretask.StatusRunning}},
	})
	if running.IsError() {
		t.Fatalf("set running error = %#v", running.Error)
	}
	ready := byName[TaskModifyOp].Run(ctx, coretask.TaskModifyRequest{
		ID:            "task_1",
		Modifications: []coretask.TaskModification{{Op: "set_status", Status: coretask.StatusReady}},
	})
	if !ready.IsError() || !strings.Contains(ready.Error.Message, "task is running") {
		t.Fatalf("set ready result = %#v, want running task reset rejection", ready)
	}
}

type fakeTaskRunner struct {
	status       coretask.SchedulerStatusResult
	submitResult taskexecutor.SubmitResult
	submitErr    error
	submitted    bool
}

func (r *fakeTaskRunner) SubmitTask(context.Context, coretask.ID) (taskexecutor.SubmitResult, error) {
	r.submitted = true
	if r.submitErr != nil {
		return taskexecutor.SubmitResult{}, r.submitErr
	}
	if r.submitResult.TaskID != "" {
		return r.submitResult, nil
	}
	return taskexecutor.SubmitResult{
		TaskID: "task_1", Status: coretask.StatusReady, Started: true, Running: true, Summary: "Task task_1 scheduled and running in background.",
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

func operationRefsContain(refs []operation.Ref, name string) bool {
	for _, ref := range refs {
		if string(ref.Name) == name {
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
