package tool

import (
	"testing"

	"github.com/fluxplane/engine/core/invocation"
	"github.com/fluxplane/engine/core/operation"
)

func TestSetValidateRejectsEmptyName(t *testing.T) {
	err := Set{}.Validate()
	if err == nil {
		t.Fatal("Validate: want error for empty set name")
	}
}

func TestSetValidateNoAction(t *testing.T) {
	err := Set{Name: "browser"}.Validate()
	if err != nil {
		t.Fatalf("Validate: want nil when no Action; got %v", err)
	}
}

func TestActionProjectionValidateRejectsEmptyTool(t *testing.T) {
	err := ActionProjection{
		ActionField: "action",
		Cases: []ActionCase{
			{Action: "do", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "op"}}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want error for empty projection tool name")
	}
}

func TestActionProjectionValidateRejectsEmptyActionField(t *testing.T) {
	err := ActionProjection{
		Tool: "browser",
		Cases: []ActionCase{
			{Action: "do", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "op"}}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want error for empty action field")
	}
}

func TestActionProjectionValidateRejectsNoCases(t *testing.T) {
	err := ActionProjection{
		Tool:        "browser",
		ActionField: "action",
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want error for empty cases")
	}
}

func TestActionProjectionValidateRejectsEmptyActionCase(t *testing.T) {
	err := ActionProjection{
		Tool:        "browser",
		ActionField: "action",
		Cases: []ActionCase{
			{Action: "", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "op"}}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want error for empty action case value")
	}
}

func TestActionProjectionValidateRejectsEmptyOperationName(t *testing.T) {
	err := ActionProjection{
		Tool:        "browser",
		ActionField: "action",
		Cases: []ActionCase{
			{Action: "do", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: ""}}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want error for empty operation name in case")
	}
}

func TestSetValidateWithValidActionProjection(t *testing.T) {
	err := Set{
		Name: "browser",
		Action: &ActionProjection{
			Tool:        "browser",
			ActionField: "action",
			Cases: []ActionCase{
				{Action: "open", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "browser_open"}}},
			},
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate: want nil; got %v", err)
	}
}
