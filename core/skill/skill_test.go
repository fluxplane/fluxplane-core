package skill

import "testing"

func TestSpecValidateAllowsSkillMetadata(t *testing.T) {
	spec := Spec{
		Name:        "architecture",
		Description: "Evaluate and design software architecture.",
		Source:      SourceRef{URI: ".agents/skills/architecture", Kind: "agents"},
		Triggers:    []string{"architecture", "system design"},
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
