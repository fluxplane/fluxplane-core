package workflow

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-operation"
)

func TestSpecValidateAllowsAgentWorkflow(t *testing.T) {
	spec := Spec{
		Name:        "feature",
		Description: "Analyze then implement.",
		Steps: []Step{
			{ID: "analyze", Agent: agent.Ref{Name: "analyst"}},
			{ID: "implement", Agent: agent.Ref{Name: "implementer"}, DependsOn: []StepID{"analyze"}},
		},
		Raw: map[string]any{"name": "feature"},
	}

	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSpecValidateAllowsOperationWorkflow(t *testing.T) {
	spec := Spec{
		Name: "ops",
		Steps: []Step{{
			ID:        "echo",
			Operation: operation.Ref{Name: "echo"},
		}},
	}

	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSpecValidateRejectsUnknownDependency(t *testing.T) {
	err := Spec{
		Name: "feature",
		Steps: []Step{{
			ID:        "implement",
			Agent:     agent.Ref{Name: "implementer"},
			DependsOn: []StepID{"analyze"},
		}},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want dependency error")
	}
}

func TestSpecValidateRejectsDuplicateStep(t *testing.T) {
	err := Spec{
		Name: "feature",
		Steps: []Step{
			{ID: "analyze", Agent: agent.Ref{Name: "analyst"}},
			{ID: "analyze", Agent: agent.Ref{Name: "analyst"}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want duplicate step error")
	}
}
