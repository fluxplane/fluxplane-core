package task

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/workflow"
)

func TestTaskValidateAcceptsPlanexecShapedTask(t *testing.T) {
	task := Task{
		ID:                 "task_1",
		Title:              "Implement feature",
		Objective:          "Ship the requested feature safely.",
		AcceptanceCriteria: []string{"tests pass", "design docs updated"},
		Status:             StatusDraft,
		Steps: []Step{
			{ID: "inspect", Title: "Inspect", Profile: "explorer"},
			{ID: "patch", Title: "Patch", Profile: "worker", DependsOn: []StepID{"inspect"}, AcceptanceCriteria: []string{"focused patch"}},
		},
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestTaskValidateAcceptsWorkflow(t *testing.T) {
	task := Task{
		Title:     "Run workflow",
		Objective: "Execute a workflow-backed task.",
		Workflow: &workflow.Spec{
			Name: "feature",
			Steps: []workflow.Step{{
				ID:    "agent",
				Kind:  workflow.StepAgent,
				Agent: agent.Ref{Name: "worker"},
			}},
		},
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestTaskValidateRejectsMissingTitleAndObjective(t *testing.T) {
	if err := (Task{}).Validate(); err == nil {
		t.Fatal("Validate error is nil, want missing title/objective error")
	}
}

func TestTaskValidateRejectsUnknownDependency(t *testing.T) {
	err := Task{
		Title: "Feature",
		Steps: []Step{{
			ID:        "patch",
			DependsOn: []StepID{"inspect"},
		}},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want dependency error")
	}
}

func TestTaskValidateRejectsSelfDependency(t *testing.T) {
	err := Task{
		Title: "Feature",
		Steps: []Step{{ID: "inspect", DependsOn: []StepID{"inspect"}}},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want self dependency error")
	}
}

func TestTaskValidateRejectsTwoStepCycle(t *testing.T) {
	err := Task{
		Title: "Feature",
		Steps: []Step{
			{ID: "a", DependsOn: []StepID{"b"}},
			{ID: "b", DependsOn: []StepID{"a"}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want cycle error")
	}
}

func TestTaskValidateRejectsLongCycle(t *testing.T) {
	err := Task{
		Title: "Feature",
		Steps: []Step{
			{ID: "a", DependsOn: []StepID{"c"}},
			{ID: "b", DependsOn: []StepID{"a"}},
			{ID: "c", DependsOn: []StepID{"b"}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want cycle error")
	}
}

func TestTaskValidateAcceptsDiamondDependency(t *testing.T) {
	task := Task{
		Title: "Feature",
		Steps: []Step{
			{ID: "a"},
			{ID: "b", DependsOn: []StepID{"a"}},
			{ID: "c", DependsOn: []StepID{"a"}},
			{ID: "d", DependsOn: []StepID{"b", "c"}},
		},
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestTaskValidateRejectsInvalidStatus(t *testing.T) {
	err := Task{Title: "Feature", Status: "bogus"}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want invalid status error")
	}
}

func TestExecutionValidateRejectsMismatchedStepKey(t *testing.T) {
	err := Execution{
		ID:     "exec_1",
		TaskID: "task_1",
		Steps: map[StepID]StepExecution{
			"inspect": {StepID: "patch", Status: StepStatusWaiting},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want mismatched step key error")
	}
}

func TestExecutionValidateAcceptsTerminalOutput(t *testing.T) {
	execution := Execution{
		ID:     "exec_1",
		TaskID: "task_1",
		Status: StatusCompleted,
		Steps: map[StepID]StepExecution{
			"inspect": {StepID: "inspect", Status: StepStatusCompleted, Output: operation.Value("done")},
		},
		Output: operation.Value("ok"),
	}
	if err := execution.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !Terminal(execution.Status) || !StepTerminal(execution.Steps["inspect"].Status) {
		t.Fatalf("terminal helpers returned false")
	}
}
