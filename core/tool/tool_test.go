package tool

import (
	"testing"

	"github.com/fluxplane/engine/core/invocation"
	"github.com/fluxplane/engine/core/operation"
)

func TestSpecValidateRejectsEmptyName(t *testing.T) {
	err := Spec{}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want empty name error")
	}
}

func TestSpecValidateRejectsToolWithoutTargetOrDispatch(t *testing.T) {
	err := Spec{Name: "image"}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want missing target error")
	}
}

func TestSpecValidateAllowsDispatchOnlyTool(t *testing.T) {
	err := Spec{
		Name: "image",
		Dispatch: &Dispatch{
			ActionField: "action",
			Cases: []DispatchCase{
				{Action: "generate", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_generate"}}},
			},
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate error = %v, want nil", err)
	}
}

func TestSetValidateRejectsDuplicateActionProjectionCase(t *testing.T) {
	err := Set{
		Name: "image",
		Action: &ActionProjection{
			Tool:        "image",
			ActionField: "action",
			Cases: []ActionCase{
				{Action: "generate", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_generate"}}},
				{Action: "generate", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_generate"}}},
			},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want duplicate action error")
	}
}

func TestSetValidateRejectsActionProjectionNonOperationTarget(t *testing.T) {
	err := Set{
		Name: "image",
		Action: &ActionProjection{
			Tool:        "image",
			ActionField: "action",
			Cases: []ActionCase{
				{Action: "prompt", Target: invocation.Target{Kind: invocation.TargetPrompt, Prompt: "hi"}},
			},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want non-operation target error")
	}
}
