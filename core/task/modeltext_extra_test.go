package task

import (
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/operation"
)

func TestTaskResultModelTextSummaries(t *testing.T) {
	if got := (TaskCreateResult{}).ModelText(); got != "Task created." {
		t.Fatalf("empty create result = %q", got)
	}
	created := TaskCreateResult{Task: Task{ID: "task_1", Title: "Ship feature", Status: StatusReady}}
	if got, want := created.ModelText(), "Created task task_1: Ship feature (status: ready)"; got != want {
		t.Fatalf("create result = %q, want %q", got, want)
	}

	modified := TaskModifyResult{
		Task:      Task{ID: "task_1", Objective: "Fix bug"},
		Artifacts: []ScopedArtifact{{TaskID: "task_1", Artifact: ArtifactSpec{ID: "diff"}}},
	}
	if got, want := modified.ModelText(), "Modified task task_1: Fix bug (status: draft, artifacts: 1)"; got != want {
		t.Fatalf("modify result = %q, want %q", got, want)
	}

	review := ReviewRequestResult{Task: Task{ID: "review_1", Status: StatusDraft, Metadata: map[string]string{"review_subject_task_id": "task_1"}}}
	if got, want := review.ModelText(), "Review task review_1 created for task_1 (status: draft)"; got != want {
		t.Fatalf("review result = %q, want %q", got, want)
	}
}

func TestTaskGetResultModelTextFull(t *testing.T) {
	result := TaskGetResult{
		Task: Task{
			ID:                 "task_1",
			Title:              "Ship feature",
			Objective:          "Deliver it",
			Description:        "More detail",
			Status:             StatusRunning,
			Priority:           PriorityHigh,
			Assignee:           RoleDeveloper,
			AcceptanceCriteria: []string{"tests pass"},
			Outputs:            []ArtifactSpec{{ID: "patch", Kind: ArtifactPatch, Required: true}},
			Artifacts:          []ArtifactSpec{{ID: "task-note", Kind: ArtifactText, Value: operation.Value("task artifact")}},
			Diagnostics:        []Diagnostic{{Code: "task_warn", Message: "task warning"}},
			Steps: []Step{{
				ID:          "inspect",
				Title:       "Inspect",
				DependsOn:   []StepID{"setup"},
				Inputs:      []ArtifactSpec{{Name: "brief", Kind: ArtifactReference}},
				Outputs:     []ArtifactSpec{{Name: "notes", Kind: ArtifactText}},
				Description: "fallback description",
			}},
		},
		CurrentExecution: "exec_1",
		Executions: map[ExecutionID]Execution{
			"exec_1": {
				Diagnostics: []Diagnostic{{Message: "exec note"}},
				Artifacts:   []ArtifactSpec{{ID: "exec-log", Kind: ArtifactReport, Ref: "log.txt"}},
				Steps: map[StepID]StepExecution{
					"inspect": {
						Status:      StepStatusCompleted,
						Artifacts:   []ArtifactSpec{{ID: "step-artifact", Kind: ArtifactJSON, Value: operation.Value(map[string]string{"ok": "true"})}},
						Diagnostics: []Diagnostic{{Code: "step_note", Message: "step done"}},
					},
				},
			},
		},
	}

	got := result.ModelText()
	for _, want := range []string{
		"Task task_1: Ship feature",
		"Status: running Priority: high Assignee: developer",
		"Objective: Deliver it",
		"Description: More detail",
		"Acceptance criteria:\n- tests pass",
		"Expected outputs:\n- patch [patch] required",
		"- inspect: Inspect (status: completed)",
		"depends_on: setup",
		"inputs: brief [reference]",
		"outputs: notes [text]",
		"artifacts: step-artifact [json]",
		"diagnostic: step_note: step done",
		"Diagnostics:\n- task_warn: task warning\n- exec note",
		"Artifacts:",
		"- task: task-note [text]",
		"- execution:exec_1: exec-log [report]",
		"- execution:exec_1/step:inspect: step-artifact [json]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ModelText missing %q in:\n%s", want, got)
		}
	}

	summary := result
	summary.View = ViewSummary
	if got, want := summary.ModelText(), "Task task_1: Ship feature (status: running)"; got != want {
		t.Fatalf("summary ModelText = %q, want %q", got, want)
	}
	if got := (TaskGetResult{}).ModelText(); got != "Task not found." {
		t.Fatalf("empty get result = %q", got)
	}
}

func TestTaskListAndArtifactModelText(t *testing.T) {
	if got := (TaskListResult{}).ModelText(); got != "No tasks found." {
		t.Fatalf("empty list = %q", got)
	}
	list := TaskListResult{Truncated: true, Tasks: []TaskSummary{{ID: "task_1", Title: "One", Status: StatusReady}, {ID: "task_2", Objective: "Two"}}}
	if got := list.ModelText(); !strings.Contains(got, "Tasks: 2 (truncated)") || !strings.Contains(got, "task_2: Two (status: draft)") {
		t.Fatalf("list ModelText = %q", got)
	}

	if got := (TaskArtifactListResult{}).ModelText(); got != "No task artifacts found." {
		t.Fatalf("empty artifact list = %q", got)
	}
	artifacts := TaskArtifactListResult{Artifacts: []ScopedArtifact{
		{TaskID: "task_1", Artifact: ArtifactSpec{ID: "task-file", Kind: ArtifactFile}},
		{TaskID: "task_1", ExecutionID: "exec_1", StepID: "step_1", Artifact: ArtifactSpec{Name: "patch", Kind: ArtifactPatch}},
	}}
	got := artifacts.ModelText()
	if !strings.Contains(got, "Task artifacts: 2") || !strings.Contains(got, "execution:exec_1/step:step_1: patch [patch]") {
		t.Fatalf("artifact list ModelText = %q", got)
	}

	missing := TaskArtifactGetResult{}
	if got := missing.ModelText(); got != "Task artifact not found." {
		t.Fatalf("missing artifact = %q", got)
	}
	get := TaskArtifactGetResult{
		Artifact:     ScopedArtifact{TaskID: "task_1", Artifact: ArtifactSpec{ID: "report", Kind: ArtifactReport, Description: "summary", Ref: "report.md", Value: operation.Value([]byte("inline")), Metadata: map[string]string{"b": "2", "a": "1"}}},
		ValuePreview: "preview",
		OmittedBytes: 12,
	}
	got = get.ModelText()
	for _, want := range []string{"Task artifact: task: report [report]", "description=summary", "ref=report.md", "value=inline", "metadata={a=1, b=2}", "preview=preview", "omitted_bytes=12"} {
		if !strings.Contains(got, want) {
			t.Fatalf("artifact get missing %q in %q", want, got)
		}
	}

	read := TaskArtifactReadResult{Artifact: ScopedArtifact{TaskID: "task_1", Artifact: ArtifactSpec{Name: "log"}}}
	if got := read.ModelText(); !strings.Contains(got, "content unavailable") {
		t.Fatalf("empty read = %q", got)
	}
	read.Content = "hello"
	read.Truncated = true
	if got := read.ModelText(); !strings.Contains(got, "hello\n[truncated]") {
		t.Fatalf("read result = %q", got)
	}
}

func TestValidationExecutionAndSchedulerModelText(t *testing.T) {
	validation := TaskValidationResult{TaskID: "task_1", Completable: true, Checks: []TaskCheck{{Code: "required_output", Message: "patch present", OK: true}, {Code: "steps_terminal", Message: "step waiting"}}}
	if got := validation.ModelText(); !strings.Contains(got, "Task task_1 is completable.") || !strings.Contains(got, "required_output: patch present (ok)") || !strings.Contains(got, "steps_terminal: step waiting (missing)") {
		t.Fatalf("validation ModelText = %q", got)
	}

	cases := []struct {
		name string
		in   ExecutionResult
		want string
	}{
		{"summary", ExecutionResult{Summary: "already handled"}, "already handled"},
		{"empty", ExecutionResult{}, "Task execution request accepted."},
		{"running", ExecutionResult{TaskID: "task_1", Status: StatusRunning}, "Task task_1 scheduled and running."},
		{"ready", ExecutionResult{TaskID: "task_1", Status: StatusReady}, "Task task_1 is ready but not running yet."},
		{"blocked", ExecutionResult{TaskID: "task_1", Status: StatusBlocked}, "Task task_1 is blocked."},
		{"completed", ExecutionResult{TaskID: "task_1", Status: StatusCompleted}, "Task task_1 is already completed."},
		{"default", ExecutionResult{TaskID: "task_1"}, "Task task_1 execution status: queued"},
	}
	for _, tc := range cases {
		if got := tc.in.ModelText(); got != tc.want {
			t.Fatalf("%s ModelText = %q, want %q", tc.name, got, tc.want)
		}
	}

	expires := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	scheduler := SchedulerStatusResult{
		Enabled:     true,
		Active:      true,
		Capacity:    1,
		MaxParallel: 3,
		Running:     []ID{"task_run"},
		Queued:      []ID{"task_wait"},
		Leases:      []ExecutionLease{{TaskID: "task_run", ExecutionID: "exec_1", WorkerID: "worker_1", LeaseExpiresAt: expires}, {TaskID: "task_old", ExecutionID: "exec_2", Expired: true}},
		Workers:     []WorkerStatus{{WorkerID: "worker_1", Active: true, Capacity: 1, MaxParallel: 2}},
		Diagnostics: []Diagnostic{{Code: "capacity", Message: "near limit"}},
	}
	got := scheduler.ModelText()
	for _, want := range []string{"Task scheduler is enabled and active. Capacity: 1/3.", "running: task_run", "queued: task_wait", "lease task_run/exec_1: active worker=worker_1 expires=2026-05-25T12:00:00Z", "lease task_old/exec_2: expired", "worker worker_1: active capacity=1/2", "capacity: near limit"} {
		if !strings.Contains(got, want) {
			t.Fatalf("scheduler ModelText missing %q in:\n%s", want, got)
		}
	}
}

func TestTaskValidationHelpersAndExecutionValidation(t *testing.T) {
	if err := RegisterEvents(nil); err == nil {
		t.Fatal("RegisterEvents(nil) error is nil")
	}

	checks := []struct {
		name string
		ok   bool
	}{
		{"invalid status", !ValidStatus("bogus")},
		{"valid step status", ValidStepStatus(StepStatusRunning)},
		{"invalid step status", !ValidStepStatus("bogus")},
		{"valid priority", ValidPriority(PriorityUrgent)},
		{"invalid priority", !ValidPriority("soon")},
		{"nonterminal status", !Terminal(StatusRunning)},
		{"terminal failed", Terminal(StatusFailed)},
		{"step nonterminal", !StepTerminal(StepStatusRunning)},
		{"step terminal skipped", StepTerminal(StepStatusSkipped)},
	}
	for _, check := range checks {
		if !check.ok {
			t.Fatalf("%s failed", check.name)
		}
	}

	if err := (Task{Title: "Feature", Priority: "soon"}).Validate(); err == nil {
		t.Fatal("Task.Validate invalid priority error is nil")
	}
	if err := (Task{Title: "Feature", Steps: []Step{{ID: "dup"}, {ID: "dup"}}}).Validate(); err == nil {
		t.Fatal("Task.Validate duplicate step error is nil")
	}
	if err := (Task{Title: "Feature", Steps: []Step{{}}}).Validate(); err == nil {
		t.Fatal("Task.Validate empty step id error is nil")
	}

	if err := (Execution{}).Validate(); err == nil {
		t.Fatal("Execution.Validate empty id error is nil")
	}
	if err := (Execution{ID: "exec_1"}).Validate(); err == nil {
		t.Fatal("Execution.Validate empty task id error is nil")
	}
	if err := (Execution{ID: "exec_1", TaskID: "task_1", Status: "bogus"}).Validate(); err == nil {
		t.Fatal("Execution.Validate invalid status error is nil")
	}
	if err := (Execution{ID: "exec_1", TaskID: "task_1", Steps: map[StepID]StepExecution{"step_1": {Status: "bogus"}}}).Validate(); err == nil {
		t.Fatal("Execution.Validate invalid step status error is nil")
	}
}

func TestArtifactValueTextVariants(t *testing.T) {
	if got, ok := artifactValueText(nil); ok || got != "" {
		t.Fatalf("nil value = %q, %v; want empty false", got, ok)
	}
	if got, ok := artifactValueText(operation.Value("  \t")); ok || got != "" {
		t.Fatalf("blank string value = %q, %v; want empty false", got, ok)
	}
	if got, ok := artifactValueText(operation.Value(map[string]string{"k": "v"})); !ok || got != `{"k":"v"}` {
		t.Fatalf("map value = %q, %v", got, ok)
	}
}
