package task

import (
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coretask "github.com/fluxplane/agentruntime/core/task"
)

func TestProjectBuildsPlanexecShapedExecutionState(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	records := []event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{
			ID:        "task_1",
			Title:     "Feature",
			Objective: "Ship it.",
			Status:    coretask.StatusDraft,
			Steps: []coretask.Step{
				{ID: "inspect", Title: "Inspect", Profile: "explorer"},
				{ID: "patch", Title: "Patch", Profile: "worker", DependsOn: []coretask.StepID{"inspect"}},
			},
		}}},
		{Time: base.Add(time.Second), Payload: coretask.ExecutionStarted{TaskID: "task_1", Execution: coretask.Execution{ID: "exec_1", TaskID: "task_1"}}},
		{Time: base.Add(2 * time.Second), Payload: coretask.StepDispatched{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", Profile: "explorer", ExternalID: "worker_1"}},
		{Time: base.Add(2500 * time.Millisecond), Payload: coretask.StepProgressed{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", Message: "reading files"}},
		{Time: base.Add(3 * time.Second), Payload: coretask.StepCompleted{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", Output: "found context"}},
	}

	state := Project(records)
	if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != "exec_1" {
		t.Fatalf("state = %#v, want running current execution", state)
	}
	exec := state.Executions["exec_1"]
	if exec.Steps["inspect"].Status != coretask.StepStatusCompleted || exec.Steps["inspect"].Output != operation.Value("found context") {
		t.Fatalf("inspect exec = %#v, want completed output", exec.Steps["inspect"])
	}
	if exec.Steps["inspect"].LastProgress != "reading files" {
		t.Fatalf("inspect progress = %q, want latest progress", exec.Steps["inspect"].LastProgress)
	}
	ready := ReadySteps(state)
	if len(ready) != 1 || ready[0].ID != "patch" {
		t.Fatalf("ready = %#v, want patch", ready)
	}
}

func TestCancelWaitingDependents(t *testing.T) {
	state := State{
		Task: coretask.Task{
			ID: "task_1",
			Steps: []coretask.Step{
				{ID: "inspect"},
				{ID: "patch", DependsOn: []coretask.StepID{"inspect"}},
				{ID: "test", DependsOn: []coretask.StepID{"patch"}},
			},
		},
		CurrentExecution: "exec_1",
		Executions: map[coretask.ExecutionID]coretask.Execution{
			"exec_1": {
				ID:     "exec_1",
				TaskID: "task_1",
				Status: coretask.StatusRunning,
				Steps: map[coretask.StepID]coretask.StepExecution{
					"inspect": {StepID: "inspect", Status: coretask.StepStatusFailed},
					"patch":   {StepID: "patch", Status: coretask.StepStatusWaiting},
					"test":    {StepID: "test", Status: coretask.StepStatusWaiting},
				},
			},
		},
	}

	state = CancelWaitingDependents(state, "", time.Now())
	exec := state.Executions["exec_1"]
	if exec.Steps["patch"].Status != coretask.StepStatusCancelled || exec.Steps["test"].Status != coretask.StepStatusCancelled {
		t.Fatalf("exec steps = %#v, want dependent cancellation", exec.Steps)
	}
}

func TestMarkInterruptedOnlyChangesRunningExecution(t *testing.T) {
	state := State{
		Task:             coretask.Task{ID: "task_1", Status: coretask.StatusRunning},
		CurrentExecution: "exec_1",
		Executions: map[coretask.ExecutionID]coretask.Execution{
			"exec_1": {ID: "exec_1", TaskID: "task_1", Status: coretask.StatusRunning},
		},
	}

	state = MarkInterrupted(state, "runner missing", time.Now())
	if state.Task.Status != coretask.StatusInterrupted {
		t.Fatalf("task status = %q, want interrupted", state.Task.Status)
	}
	exec := state.Executions["exec_1"]
	if exec.Status != coretask.StatusInterrupted || exec.Error == nil || exec.Error.Message != "runner missing" {
		t.Fatalf("execution = %#v, want interrupted error", exec)
	}
}

func TestProjectAppliesArtifactAdded(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{ID: "task_1", Title: "Review"}}},
		{Time: base.Add(time.Second), Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_1", Execution: coretask.Execution{TaskID: "task_1"}}},
		{Time: base.Add(2 * time.Second), Payload: coretask.ArtifactAdded{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", Artifact: coretask.ArtifactSpec{Name: "report", Kind: coretask.ArtifactReport}}},
	})
	if len(state.Task.Artifacts) != 0 {
		t.Fatalf("task artifacts = %#v, want none for step artifact", state.Task.Artifacts)
	}
	exec := state.Executions["exec_1"]
	if len(exec.Artifacts) != 0 {
		t.Fatalf("execution artifacts = %#v, want none for step artifact", exec.Artifacts)
	}
	step := exec.Steps["inspect"]
	if len(step.Artifacts) != 1 || step.Artifacts[0].Kind != coretask.ArtifactReport {
		t.Fatalf("step artifacts = %#v, want report", step.Artifacts)
	}
}

func TestProjectAppliesExecutionArtifactAdded(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{ID: "task_1", Title: "Review"}}},
		{Time: base.Add(time.Second), Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_1", Execution: coretask.Execution{TaskID: "task_1"}}},
		{Time: base.Add(2 * time.Second), Payload: coretask.ArtifactAdded{TaskID: "task_1", ExecutionID: "exec_1", Artifact: coretask.ArtifactSpec{Name: "report", Kind: coretask.ArtifactReport}}},
	})
	exec := state.Executions["exec_1"]
	if len(exec.Artifacts) != 1 || exec.Artifacts[0].Name != "report" {
		t.Fatalf("execution artifacts = %#v, want report", exec.Artifacts)
	}
}

func TestProjectAppliesSchedulerDiagnostic(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{ID: "task_1", Title: "Review", Steps: []coretask.Step{{ID: "inspect"}}}}},
		{Time: base.Add(time.Second), Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_1", Execution: coretask.Execution{TaskID: "task_1"}}},
		{Time: base.Add(2 * time.Second), Payload: coretask.SchedulerDiagnostic{
			TaskID:      "task_1",
			ExecutionID: "exec_1",
			StepID:      "inspect",
			Diagnostic:  coretask.Diagnostic{Code: "task_stale_step_result_ignored", Message: "ignored"},
		}},
		{Time: base.Add(3 * time.Second), Payload: coretask.SchedulerDiagnostic{
			TaskID:     "task_1",
			Diagnostic: coretask.Diagnostic{Code: "task_append_conflict", Message: "conflict"},
		}},
	})
	step := state.Executions["exec_1"].Steps["inspect"]
	if len(step.Diagnostics) != 1 || step.Diagnostics[0].Code != "task_stale_step_result_ignored" {
		t.Fatalf("step diagnostics = %#v, want stale result diagnostic", step.Diagnostics)
	}
	if len(state.Executions["exec_1"].Diagnostics) != 1 || state.Executions["exec_1"].Diagnostics[0].Code != "task_append_conflict" {
		t.Fatalf("execution diagnostics = %#v, want conflict diagnostic", state.Executions["exec_1"].Diagnostics)
	}
}

func TestProjectSchedulerDiagnosticDoesNotChangeCurrentExecution(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{ID: "task_1", Title: "Review", Steps: []coretask.Step{{ID: "run"}}}}},
		{Time: base.Add(time.Second), Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_old", Execution: coretask.Execution{TaskID: "task_1"}}},
		{Time: base.Add(2 * time.Second), Payload: coretask.ExecutionInterrupted{TaskID: "task_1", ExecutionID: "exec_old", Reason: "blocked"}},
		{Time: base.Add(3 * time.Second), Payload: coretask.StatusChanged{TaskID: "task_1", Previous: coretask.StatusInterrupted, Current: coretask.StatusReady, Reason: "reopened"}},
		{Time: base.Add(4 * time.Second), Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_new", Execution: coretask.Execution{TaskID: "task_1"}}},
		{Time: base.Add(5 * time.Second), Payload: coretask.SchedulerDiagnostic{
			TaskID:      "task_1",
			ExecutionID: "exec_old",
			StepID:      "run",
			Diagnostic:  coretask.Diagnostic{Code: "task_stale_step_result_ignored", Message: "old worker output ignored"},
		}},
	})
	if state.CurrentExecution != "exec_new" {
		t.Fatalf("current execution = %s, want exec_new", state.CurrentExecution)
	}
	oldStep := state.Executions["exec_old"].Steps["run"]
	if len(oldStep.Diagnostics) != 1 || oldStep.Diagnostics[0].Code != "task_stale_step_result_ignored" {
		t.Fatalf("old diagnostics = %#v, want stale diagnostic on old execution", oldStep.Diagnostics)
	}
}

func TestProjectAppliesArtifactUpdatedAndRemoved(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{ID: "task_1", Title: "Review"}}},
		{Time: base.Add(time.Second), Payload: coretask.ArtifactAdded{TaskID: "task_1", Artifact: coretask.ArtifactSpec{ID: "report", Name: "draft", Kind: coretask.ArtifactReport}}},
		{Time: base.Add(2 * time.Second), Payload: coretask.ArtifactUpdated{TaskID: "task_1", ArtifactID: "report", Artifact: coretask.ArtifactSpec{Name: "final", Kind: coretask.ArtifactReport}}},
		{Time: base.Add(3 * time.Second), Payload: coretask.ArtifactRemoved{TaskID: "task_1", ArtifactID: "report"}},
	})
	if len(state.Task.Artifacts) != 0 {
		t.Fatalf("task artifacts = %#v, want removed artifact", state.Task.Artifacts)
	}
}

func TestProjectAppliesStepArtifactUpdatedAndRemoved(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{ID: "task_1", Title: "Review"}}},
		{Time: base.Add(time.Second), Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_1", Execution: coretask.Execution{TaskID: "task_1"}}},
		{Time: base.Add(2 * time.Second), Payload: coretask.ArtifactAdded{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", Artifact: coretask.ArtifactSpec{ID: "note", Name: "draft", Kind: coretask.ArtifactText}}},
		{Time: base.Add(3 * time.Second), Payload: coretask.ArtifactUpdated{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", ArtifactID: "note", Artifact: coretask.ArtifactSpec{Name: "final", Kind: coretask.ArtifactText}}},
	})
	step := state.Executions["exec_1"].Steps["inspect"]
	if len(step.Artifacts) != 1 || step.Artifacts[0].ID != "note" || step.Artifacts[0].Name != "final" {
		t.Fatalf("step artifacts = %#v, want updated note", step.Artifacts)
	}
	if len(state.Executions["exec_1"].Artifacts) != 0 {
		t.Fatalf("execution artifacts = %#v, want no duplicated step artifact", state.Executions["exec_1"].Artifacts)
	}
	state = Apply(state, coretask.ArtifactRemoved{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", ArtifactID: "note"}, base.Add(4*time.Second))
	if len(state.Executions["exec_1"].Steps["inspect"].Artifacts) != 0 {
		t.Fatalf("step artifacts = %#v, want removed note", state.Executions["exec_1"].Steps["inspect"].Artifacts)
	}
}

func TestProjectAppliesStepStatusChanged(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{ID: "task_1", Title: "Review", Steps: []coretask.Step{{ID: "inspect"}}}}},
		{Time: base.Add(time.Second), Payload: coretask.StepStatusChanged{TaskID: "task_1", StepID: "inspect", Current: coretask.StepStatusCompleted, Output: "done"}},
	})
	if state.CurrentExecution != "manual" {
		t.Fatalf("current execution = %q, want manual", state.CurrentExecution)
	}
	step := state.Executions["manual"].Steps["inspect"]
	if step.Status != coretask.StepStatusCompleted || step.Output != operation.Value("done") {
		t.Fatalf("step = %#v, want completed output", step)
	}
}

func TestProjectStepStatusChangedClearsTerminalMetadataWhenReopened(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{ID: "task_1", Title: "Review", Steps: []coretask.Step{{ID: "inspect"}}}}},
		{Time: base.Add(time.Second), Payload: coretask.StepStatusChanged{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", Current: coretask.StepStatusCompleted, Output: "done"}},
		{Time: base.Add(2 * time.Second), Payload: coretask.StepStatusChanged{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", Current: coretask.StepStatusRunning}},
	})
	step := state.Executions["exec_1"].Steps["inspect"]
	if step.Status != coretask.StepStatusRunning {
		t.Fatalf("step status = %q, want running", step.Status)
	}
	if !step.CompletedAt.IsZero() || step.Output != nil || step.Error != nil {
		t.Fatalf("step = %#v, want cleared terminal metadata", step)
	}
}

func TestProjectStepStatusChangedClearsFailureMetadataWhenReopened(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{ID: "task_1", Title: "Review", Steps: []coretask.Step{{ID: "inspect"}}}}},
		{Time: base.Add(time.Second), Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_1", Execution: coretask.Execution{TaskID: "task_1"}}},
		{Time: base.Add(2 * time.Second), Payload: coretask.StepFailed{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", Error: &operation.Error{Code: "failed", Message: "failed"}}},
		{Time: base.Add(3 * time.Second), Payload: coretask.StepStatusChanged{TaskID: "task_1", ExecutionID: "exec_1", StepID: "inspect", Current: coretask.StepStatusWaiting}},
	})
	step := state.Executions["exec_1"].Steps["inspect"]
	if step.Status != coretask.StepStatusWaiting {
		t.Fatalf("step status = %q, want waiting", step.Status)
	}
	if !step.CompletedAt.IsZero() || step.Output != nil || step.Error != nil {
		t.Fatalf("step = %#v, want cleared terminal metadata", step)
	}
}

func TestAllStepsTerminal(t *testing.T) {
	state := State{
		Task:             coretask.Task{Steps: []coretask.Step{{ID: "a"}, {ID: "b"}}},
		CurrentExecution: "exec_1",
		Executions: map[coretask.ExecutionID]coretask.Execution{
			"exec_1": {ID: "exec_1", Steps: map[coretask.StepID]coretask.StepExecution{
				"a": {Status: coretask.StepStatusCompleted},
				"b": {Status: coretask.StepStatusCancelled},
			}},
		},
	}
	if !AllStepsTerminal(state) {
		t.Fatal("AllStepsTerminal = false, want true")
	}
}

func TestAllStepsTerminalRequiresDeclaredTaskSteps(t *testing.T) {
	state := State{
		Task:             coretask.Task{Steps: []coretask.Step{{ID: "a"}, {ID: "b"}}},
		CurrentExecution: "exec_1",
		Executions: map[coretask.ExecutionID]coretask.Execution{
			"exec_1": {ID: "exec_1", Steps: map[coretask.StepID]coretask.StepExecution{
				"a": {Status: coretask.StepStatusCompleted},
			}},
		},
	}
	if AllStepsTerminal(state) {
		t.Fatal("AllStepsTerminal = true, want false for missing declared step")
	}
}

func TestProjectReconcilesExecutionStepsOnRevision(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	state := Project([]event.Record{
		{Time: base, Payload: coretask.Created{TaskID: "task_1", Task: coretask.Task{
			ID: "task_1", Title: "Feature", Steps: []coretask.Step{{ID: "a"}, {ID: "old"}},
		}}},
		{Time: base.Add(time.Second), Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_1", Execution: coretask.Execution{TaskID: "task_1"}}},
		{Time: base.Add(2 * time.Second), Payload: coretask.StepCompleted{TaskID: "task_1", ExecutionID: "exec_1", StepID: "a"}},
		{Time: base.Add(3 * time.Second), Payload: coretask.Revised{TaskID: "task_1", Task: coretask.Task{
			ID: "task_1", Title: "Feature", Steps: []coretask.Step{{ID: "a"}, {ID: "new"}},
		}}},
	})

	exec := state.Executions["exec_1"]
	if _, ok := exec.Steps["old"]; ok {
		t.Fatalf("execution steps = %#v, want removed step dropped", exec.Steps)
	}
	if exec.Steps["new"].Status != coretask.StepStatusWaiting {
		t.Fatalf("new step = %#v, want waiting", exec.Steps["new"])
	}
	if AllStepsTerminal(state) {
		t.Fatal("AllStepsTerminal = true, want false after new waiting step")
	}
}

func TestApplyClonesNonNilEmptyExecutionMap(t *testing.T) {
	original := State{Executions: map[coretask.ExecutionID]coretask.Execution{}}
	next := Apply(original, coretask.ExecutionStarted{
		TaskID:      "task_1",
		ExecutionID: "exec_1",
		Execution:   coretask.Execution{TaskID: "task_1"},
	}, time.Now())
	if len(original.Executions) != 0 {
		t.Fatalf("original executions = %#v, want unchanged empty map", original.Executions)
	}
	if _, ok := next.Executions["exec_1"]; !ok {
		t.Fatalf("next executions = %#v, want exec_1", next.Executions)
	}
}

func TestApplyIgnoresExecutionStartedWithoutID(t *testing.T) {
	state := Apply(State{}, coretask.ExecutionStarted{TaskID: "task_1", Execution: coretask.Execution{TaskID: "task_1"}}, time.Now())
	if state.CurrentExecution != "" || len(state.Executions) != 0 {
		t.Fatalf("state = %#v, want malformed execution start ignored", state)
	}
}
