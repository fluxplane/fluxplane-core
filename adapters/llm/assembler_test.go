package llm

import (
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/tool"
)

func TestToolCallAssemblerBuildsOperationRequestFromStreamedArguments(t *testing.T) {
	assembler := NewToolCallAssembler([]ToolSpec{{
		Name: "inspect",
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "inspect"},
		},
	}})

	if completed, err := assembler.Apply(StreamEvent{
		Kind:       StreamToolCallStart,
		Tool:       "inspect",
		ToolCallID: "call_1",
		Index:      0,
	}); err != nil || len(completed) != 0 {
		t.Fatalf("start completed = %#v, err = %v", completed, err)
	}
	if completed, err := assembler.Apply(StreamEvent{
		Kind:       StreamToolCallDelta,
		ToolCallID: "call_1",
		Arguments:  `{"path":`,
	}); err != nil || len(completed) != 0 {
		t.Fatalf("delta completed = %#v, err = %v", completed, err)
	}
	completed, err := assembler.Apply(StreamEvent{
		Kind:       StreamToolCallDone,
		ToolCallID: "call_1",
		Arguments:  `"README.md"}`,
	})
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if len(completed) != 1 {
		t.Fatalf("completed len = %d, want 1", len(completed))
	}
	if completed[0].Operation.Name != "inspect" {
		t.Fatalf("operation = %q, want inspect", completed[0].Operation.Name)
	}
	input := completed[0].Input.(map[string]any)
	if input["path"] != "README.md" {
		t.Fatalf("input path = %#v, want README.md", input["path"])
	}
}

func TestToolCallAssemblerRejectsUnknownTool(t *testing.T) {
	assembler := NewToolCallAssembler(nil)
	_, err := assembler.Apply(StreamEvent{
		Kind:      StreamToolCallDone,
		Tool:      "missing",
		Arguments: `{}`,
	})
	if err == nil {
		t.Fatal("Apply error is nil, want unknown tool error")
	}
}

func TestToolCallAssemblerRejectsNonOperationTool(t *testing.T) {
	assembler := NewToolCallAssembler([]ToolSpec{{Name: "prompt", Target: invocation.Target{Kind: invocation.TargetPrompt, Prompt: "hi"}}})
	_, err := assembler.Apply(StreamEvent{
		Kind:      StreamToolCallDone,
		Tool:      "prompt",
		Arguments: `{}`,
	})
	if err == nil {
		t.Fatal("Apply error is nil, want non-operation target error")
	}
}

func TestToolCallAssemblerTreatsEmptyArgumentsAsObject(t *testing.T) {
	assembler := NewToolCallAssembler([]ToolSpec{{
		Name: "inspect",
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "inspect"},
		},
	}})
	completed, err := assembler.Apply(StreamEvent{
		Kind:       StreamToolCallDone,
		Tool:       "inspect",
		ToolCallID: "call_empty",
		Arguments:  "",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(completed) != 1 {
		t.Fatalf("completed = %#v, want one operation", completed)
	}
	if input, ok := completed[0].Input.(map[string]any); !ok || len(input) != 0 {
		t.Fatalf("input = %#v, want empty object", completed[0].Input)
	}
}

func TestToolCallAssemblerDispatchesActionToolSet(t *testing.T) {
	assembler := NewToolCallAssembler([]ToolSpec{{
		Name: "image",
		Dispatch: &tool.Dispatch{
			ActionField: "action",
			Cases: []tool.DispatchCase{
				{Action: "generate", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_generate"}}},
				{Action: "understand", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_understand"}}},
			},
		},
	}})
	completed, err := assembler.Apply(StreamEvent{
		Kind:      StreamToolCallDone,
		Tool:      "image",
		Arguments: `{"action":"understand","images":["screen.png"]}`,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(completed) != 1 || completed[0].Operation.Name != "image_understand" {
		t.Fatalf("completed = %#v, want image_understand", completed)
	}
}

func TestToolCallAssemblerReportsAvailableActions(t *testing.T) {
	assembler := NewToolCallAssembler([]ToolSpec{{
		Name: "image",
		Dispatch: &tool.Dispatch{
			ActionField: "action",
			Cases: []tool.DispatchCase{
				{Action: "generate", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_generate"}}},
			},
		},
	}})
	_, err := assembler.Apply(StreamEvent{
		Kind:      StreamToolCallDone,
		Tool:      "image",
		Arguments: `{"action":"missing"}`,
	})
	if err == nil {
		t.Fatal("Apply error is nil, want unknown action")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "available actions: generate") {
		t.Fatalf("error = %q, want available actions", got)
	}
}

func TestToolsFromCoreRejectsDuplicateNames(t *testing.T) {
	_, err := ToolsFromCore([]tool.Spec{
		{Name: "echo", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "echo"}}},
		{Name: "echo", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "echo2"}}},
	})
	if err == nil {
		t.Fatal("ToolsFromCore error is nil, want duplicate name error")
	}
}
