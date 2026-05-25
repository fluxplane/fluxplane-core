package trigger

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/skill"
)

func TestKinds(t *testing.T) {
	kinds := Kinds()
	if len(kinds) != 2 {
		t.Fatalf("Kinds() returned %d kinds, want 2", len(kinds))
	}
	found := map[Kind]bool{}
	for _, k := range kinds {
		found[k] = true
	}
	if !found[KindStartup] {
		t.Errorf("Kinds() missing KindStartup")
	}
	if !found[KindSchedule] {
		t.Errorf("Kinds() missing KindSchedule")
	}
}

func TestSpecValidate(t *testing.T) {
	skillAction := reaction.Action{
		Kind:  reaction.ActionActivateSkill,
		Skill: skillRef("my-skill"),
	}
	workflowAction := reaction.Action{
		Kind: reaction.ActionRunWorkflow,
		Workflow: reaction.WorkflowAction{
			Name: "my-workflow",
		},
	}
	invalidAction := reaction.Action{Kind: reaction.ActionActivateSkill} // missing skill name

	tests := []struct {
		name    string
		spec    Spec
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid startup trigger",
			spec: Spec{
				Name:    "startup-trigger",
				Kind:    KindStartup,
				Session: "default",
			},
			wantErr: false,
		},
		{
			name: "valid schedule trigger",
			spec: Spec{
				Name:     "scheduled-trigger",
				Kind:     KindSchedule,
				Schedule: Schedule{Every: "1h"},
				Session:  "default",
			},
			wantErr: false,
		},
		{
			name: "schedule trigger missing every",
			spec: Spec{
				Name:    "scheduled-trigger",
				Kind:    KindSchedule,
				Session: "default",
			},
			wantErr: true,
			errMsg:  `trigger "scheduled-trigger": schedule.every is empty`,
		},
		{
			name: "whitespace schedule every",
			spec: Spec{
				Name:     "scheduled-trigger",
				Kind:     KindSchedule,
				Schedule: Schedule{Every: "   "},
				Session:  "default",
			},
			wantErr: true,
			errMsg:  `trigger "scheduled-trigger": schedule.every is empty`,
		},
		{
			name: "empty name",
			spec: Spec{
				Name:    "",
				Kind:    KindStartup,
				Session: "default",
			},
			wantErr: true,
			errMsg:  "trigger: name is empty",
		},
		{
			name: "whitespace name",
			spec: Spec{
				Name:    "  ",
				Kind:    KindStartup,
				Session: "default",
			},
			wantErr: true,
			errMsg:  "trigger: name is empty",
		},
		{
			name: "empty kind",
			spec: Spec{
				Name:    "my-trigger",
				Kind:    "",
				Session: "default",
			},
			wantErr: true,
			errMsg:  `trigger "my-trigger": kind is empty`,
		},
		{
			name: "invalid kind",
			spec: Spec{
				Name:    "my-trigger",
				Kind:    "bogus",
				Session: "default",
			},
			wantErr: true,
			errMsg:  `trigger "my-trigger": kind "bogus" is invalid`,
		},
		{
			name: "empty session",
			spec: Spec{
				Name:    "my-trigger",
				Kind:    KindStartup,
				Session: "",
			},
			wantErr: true,
			errMsg:  `trigger "my-trigger": session is empty`,
		},
		{
			name: "whitespace session",
			spec: Spec{
				Name:    "my-trigger",
				Kind:    KindStartup,
				Session: "   ",
			},
			wantErr: true,
			errMsg:  `trigger "my-trigger": session is empty`,
		},
		{
			name: "valid with actions",
			spec: Spec{
				Name:    "reactive-trigger",
				Kind:    KindStartup,
				Session: "default",
				Actions: []reaction.Action{skillAction, workflowAction},
			},
			wantErr: false,
		},
		{
			name: "action validation error",
			spec: Spec{
				Name:    "bad-action-trigger",
				Kind:    KindStartup,
				Session: "default",
				Actions: []reaction.Action{invalidAction},
			},
			wantErr: true,
			errMsg:  `trigger "bad-action-trigger" actions[0]: activate_skill requires skill.name`,
		},
		{
			name: "valid with disabled",
			spec: Spec{
				Name:     "disabled-trigger",
				Kind:     KindSchedule,
				Schedule: Schedule{Every: "1h"},
				Session:  "default",
				Disabled: true,
			},
			wantErr: false,
		},
		{
			name: "valid with metadata",
			spec: Spec{
				Name:     "meta-trigger",
				Kind:     KindStartup,
				Session:  "default",
				Metadata: map[string]string{"env": "prod", "team": "platform"},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() error = nil, want %q", tt.errMsg)
				}
				if err.Error() != tt.errMsg {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
			}
		})
	}
}

// skillRef is a helper to create a skill.Ref for testing
func skillRef(name string) skill.Ref {
	return skill.Ref{Name: skill.Name(name)}
}
