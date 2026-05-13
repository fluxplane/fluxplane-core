package skill

import "testing"

func TestSpecValidateAllowsSkillMetadata(t *testing.T) {
	spec := Spec{
		Name:        "architecture",
		Description: "Evaluate and design software architecture.",
		Source:      SourceRef{URI: ".agents/skills/architecture", Kind: "agents"},
		Triggers:    []string{"architecture", "system design"},
		References: []ReferenceSpec{{
			Path:     "references/tradeoffs.md",
			Triggers: []string{"tradeoffs"},
		}},
	}

	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSpecValidateRejectsInvalidReferencePath(t *testing.T) {
	tests := []string{
		"",
		"/references/review.md",
		"../references/review.md",
		"references/../review.md",
		"SKILL.md",
		"notes.md",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			err := Spec{Name: "architecture", References: []ReferenceSpec{{Path: path}}}.Validate()
			if err == nil {
				t.Fatal("Validate error is nil, want invalid reference path error")
			}
		})
	}
}

func TestSpecValidateRejectsEmptyName(t *testing.T) {
	err := Spec{}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want empty name error")
	}
}
