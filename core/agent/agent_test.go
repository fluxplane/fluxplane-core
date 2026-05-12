package agent

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/skill"
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
		Policy: Policy{MaxSteps: 100},
		Tools:  []ToolRef{{Name: "bash"}, {Name: "file_read"}},
		Commands: []CommandRef{
			{Name: "review"},
			{Name: "design"},
		},
		Skills: []skill.Ref{{Name: "architecture"}},
		Stop: StopConditionSpec{
			Type: "max-continuations",
			Max:  3,
		},
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

func TestSpecValidateRejectsEmptyRefs(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
	}{
		{name: "tool", spec: Spec{Name: "main", Tools: []ToolRef{{}}}},
		{name: "command", spec: Spec{Name: "main", Commands: []CommandRef{{}}}},
		{name: "skill", spec: Spec{Name: "main", Skills: []skill.Ref{{}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.spec.Validate(); err == nil {
				t.Fatal("Validate error is nil, want ref error")
			}
		})
	}
}
