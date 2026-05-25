package task

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/workflow"
)

func TestTaskValidateAcceptsPlanexecShapedTask(t *testing.T) {
	task := Task{
		ID:                 "task_1",
		Title:              "Implement feature",
		Objective:          "Ship the requested feature safely.",
		AcceptanceCriteria: []string{"tests pass", "design docs updated"},
		Inputs:             []ArtifactSpec{{Name: "design", Kind: ArtifactReference, Required: true}},
		Outputs:            []ArtifactSpec{{Name: "patch", Kind: ArtifactPatch, Required: true}},
		Status:             StatusDraft,
		Steps: []Step{
			{ID: "inspect", Title: "Inspect", Profile: "explorer"},
			{ID: "patch", Title: "Patch", Profile: "worker", DependsOn: []StepID{"inspect"}, AcceptanceCriteria: []string{"focused patch"}, Outputs: []ArtifactSpec{{Name: "diff", Kind: ArtifactDiff}}},
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

// TestArtifactValueTextTruncatesOnRuneBoundary regresses a UTF-8 truncation
// bug: artifactValueText used to cut the string at the 240th byte without
// checking that the cut landed on a rune boundary, leaving an invalid UTF-8
// sequence at the tail. The corrupt suffix then flowed into downstream
// task-detail rendering, logs, and any utf8mb4 column that rejected the
// invalid bytes.
func TestArtifactValueTextTruncatesOnRuneBoundary(t *testing.T) {
	// Pad with ASCII so the 4-byte rune straddles position 240.
	padding := strings.Repeat("a", 238)
	value := operation.Value(padding + "\xF0\x9F\x8C\x8D" + strings.Repeat("b", 100))

	got, ok := artifactValueText(value)
	if !ok {
		t.Fatalf("artifactValueText returned ok=false for non-empty value")
	}
	if !strings.HasSuffix(got, "...[truncated]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	body := strings.TrimSuffix(got, "...[truncated]")
	if !utf8.ValidString(body) {
		t.Fatalf("truncated body is not valid UTF-8: %q (% x)", body, body)
	}
	if got := len(body); got > 240 {
		t.Fatalf("truncated body length = %d, want <= 240", got)
	}
}
