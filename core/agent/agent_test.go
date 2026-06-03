package agent

import (
	"testing"

	"github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-skill"
)

func TestSpecValidateAllowsEngineerAgentShape(t *testing.T) {
	spec := Spec{
		Name:        "main",
		Description: "Senior software engineer.",
		System:      "You are a senior software engineer.",
		Inference: InferenceSpec{
			Model:           "claude-sonnet-4-20250514",
			MaxOutputTokens: 16000,
			Thinking:        "auto",
			ReasoningEffort: "high",
		},
		Turns: TurnPolicy{
			MaxSteps: 100,
			Continuation: ContinuationPolicy{
				StopCondition: StopConditionSpec{
					Type: "max-continuations",
					Max:  3,
				},
			},
		},
		Tools: []ToolRef{{Name: "bash"}, {Name: "file_read"}},
		Commands: []CommandRef{
			{Name: "review"},
			{Name: "design"},
		},
		Skills: []skill.Ref{{Name: "architecture"}},
	}

	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSpecValidateRejectsEmptyName(t *testing.T) {
	err := Spec{}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want empty name error")
	}
}

func TestSpecValidateRejectsNegativeMaxContinuations(t *testing.T) {
	err := Spec{Name: "main", Turns: TurnPolicy{Continuation: ContinuationPolicy{MaxContinuations: -1}}}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want negative max continuations error")
	}
}

func TestSpecValidateRejectsMaxContinuationsWithoutStopCondition(t *testing.T) {
	err := Spec{Name: "main", Turns: TurnPolicy{Continuation: ContinuationPolicy{MaxContinuations: 3}}}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want stop condition requirement")
	}
}

func TestSpecValidateRejectsEmptyRefs(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
	}{
		{name: "tool", spec: Spec{Name: "main", Tools: []ToolRef{{}}}},
		{name: "command", spec: Spec{Name: "main", Commands: []CommandRef{{}}}},
		{name: "datasource", spec: Spec{Name: "main", Datasources: []datasource.Ref{{}}}},
		{name: "skill", spec: Spec{Name: "main", Skills: []skill.Ref{{}}}},
		{name: "activation set", spec: Spec{Name: "main", ActivationSets: []string{""}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.spec.Validate(); err == nil {
				t.Fatal("Validate error is nil, want ref error")
			}
		})
	}
}
