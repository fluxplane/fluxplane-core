package llm

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/invocation"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-policy"
)

func TestScriptedModelStreamsAndReturnsStructuredResponse(t *testing.T) {
	model := ScriptedModel{
		Tools: []ToolSpec{{
			Name: "inspect",
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "inspect"},
			},
		}},
		Events: []StreamEvent{
			{Kind: StreamThinkingDelta, Text: "hidden"},
			{Kind: StreamContentDelta, Text: "looking"},
			{Kind: StreamToolCallStart, Tool: "inspect", ToolCallID: "call_1"},
			{Kind: StreamToolCallDone, Tool: "inspect", ToolCallID: "call_1", Arguments: `{"path":"README.md"}`, Sensitivity: policy.SensitivityConfidential},
		},
		Response: Response{Message: "done"},
		Redactor: Redactor{
			ExposeToolArgs: true,
		},
	}
	var events []llmagent.StreamEvent
	resp, err := model.Stream(context.Background(), llmagent.Request{}, func(evt llmagent.StreamEvent) {
		events = append(events, evt)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message == nil || resp.Message.Content != "done" {
		t.Fatalf("message = %#v, want done", resp.Message)
	}
	if len(resp.Operations) != 1 || resp.Operations[0].Operation.Name != "inspect" {
		t.Fatalf("operations = %#v, want inspect", resp.Operations)
	}
	if len(events) != 3 {
		t.Fatalf("events len = %d, want content/tool deltas: %#v", len(events), events)
	}
	if events[0].Kind != llmagent.StreamContentDelta {
		t.Fatalf("event[0] = %#v, want content delta", events[0])
	}
	if events[1].Kind != llmagent.StreamToolCallDelta || events[1].Tool != "inspect" {
		t.Fatalf("event[1] = %#v, want tool start delta", events[1])
	}
	if events[2].Kind != llmagent.StreamToolCallDelta || events[2].Text != "" || events[2].Redaction == "" || !events[2].Final {
		t.Fatalf("event[2] = %#v, want redacted final tool delta", events[2])
	}
}

func TestScriptedModelCompleteReturnsMessage(t *testing.T) {
	resp, err := (ScriptedModel{Response: Response{Message: "hello"}}).Complete(context.Background(), llmagent.Request{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message == nil || resp.Message.Content != "hello" {
		t.Fatalf("message = %#v, want hello", resp.Message)
	}
}
