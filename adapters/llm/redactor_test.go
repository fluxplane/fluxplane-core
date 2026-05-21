package llm

import (
	"testing"

	"github.com/fluxplane/engine/core/policy"
	llmagent "github.com/fluxplane/engine/runtime/agent/llmagent"
)

func TestRedactorHidesThinkingByDefault(t *testing.T) {
	_, ok := (Redactor{}).ToRuntimeStream(StreamEvent{
		Kind: StreamThinkingDelta,
		Text: "hidden",
	})
	if ok {
		t.Fatal("thinking stream emitted by default")
	}
}

func TestRedactorExposesPublicThinkingWhenEnabled(t *testing.T) {
	evt, ok := (Redactor{ExposeThinking: true}).ToRuntimeStream(StreamEvent{
		Kind:        StreamThinkingDelta,
		Text:        "brief reasoning",
		Sensitivity: policy.SensitivityPublic,
	})
	if !ok {
		t.Fatal("thinking stream not emitted")
	}
	if evt.Kind != llmagent.StreamThinkingDelta || evt.Text != "brief reasoning" {
		t.Fatalf("event = %#v, want visible thinking", evt)
	}
}

func TestRedactorRedactsConfidentialToolArguments(t *testing.T) {
	evt, ok := (Redactor{ExposeToolArgs: true}).ToRuntimeStream(StreamEvent{
		Kind:        StreamToolCallDelta,
		Tool:        "write",
		Arguments:   `{"secret":"token"}`,
		Sensitivity: policy.SensitivityConfidential,
	})
	if !ok {
		t.Fatal("tool stream not emitted")
	}
	if evt.Text != "" || evt.Redaction != "tool_arguments_redacted" {
		t.Fatalf("event = %#v, want redacted args", evt)
	}
}

func TestRedactorPreservesZeroStreamIndex(t *testing.T) {
	evt, ok := (Redactor{ExposeToolArgs: true}).ToRuntimeStream(StreamEvent{
		Kind:      StreamToolCallDelta,
		Tool:      "lookup",
		Index:     0,
		Arguments: `{}`,
	})
	if !ok {
		t.Fatal("tool stream not emitted")
	}
	if evt.Index == nil || *evt.Index != 0 {
		t.Fatalf("index = %#v, want explicit zero", evt.Index)
	}
}
