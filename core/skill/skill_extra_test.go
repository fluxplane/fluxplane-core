package skill

import (
	"testing"
)

func TestSkillEventNames(t *testing.T) {
	activated := SkillActivated{}
	if activated.EventName() != EventSkillActivated {
		t.Fatalf("SkillActivated EventName = %q, want %q", activated.EventName(), EventSkillActivated)
	}
	refActivated := SkillReferenceActivated{}
	if refActivated.EventName() != EventSkillReferenceActivated {
		t.Fatalf("SkillReferenceActivated EventName = %q, want %q", refActivated.EventName(), EventSkillReferenceActivated)
	}
}
