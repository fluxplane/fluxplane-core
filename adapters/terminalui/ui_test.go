package terminalui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

func TestRendererStreamsMarkdownContent(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamContentDelta,
				Text: "**hello** `world`",
			}},
		},
	})
	renderer.Finish()
	if !renderer.HasStreamedContent() {
		t.Fatalf("HasStreamedContent = false, want true")
	}
	if !strings.Contains(out.String(), "hello") || !strings.Contains(out.String(), "world") {
		t.Fatalf("out = %q", out.String())
	}
	if strings.Contains(out.String(), "**hello**") || strings.Contains(out.String(), "`world`") {
		t.Fatalf("out = %q, want rendered markdown without source markers", out.String())
	}
}

func TestRendererStreamsContentBeforeFinish(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamContentDelta,
				Text: "hello",
			}},
		},
	})
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("out before Finish = %q, want streamed content", out.String())
	}
}

func TestRendererDoesNotReplayContentDeltas(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	for _, text := range []string{"hello", " world"} {
		renderer.Render(clientapi.Event{
			Kind: clientapi.EventRuntimeEmitted,
			Runtime: &clientapi.RuntimeEvent{
				Name: llmagent.EventModelStreamedName,
				Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
					Kind: llmagent.StreamContentDelta,
					Text: text,
				}},
			},
		})
	}
	renderer.Finish()
	if count := strings.Count(out.String(), "hello"); count != 1 {
		t.Fatalf("out = %q, hello count = %d, want 1", out.String(), count)
	}
}

func TestRendererRendersThinkingAsMarkdown(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamThinkingDelta,
				Text: "**checking** `state`",
			}},
		},
	})
	renderer.Finish()
	got := err.String()
	if !strings.Contains(got, "checking") || !strings.Contains(got, "state") {
		t.Fatalf("thinking output = %q, want rendered text", got)
	}
	if strings.Contains(got, "**checking**") || strings.Contains(got, "`state`") {
		t.Fatalf("thinking output = %q, want markdown rendered without source markers", got)
	}
}

func TestRendererRendersDebugAsMarkdownFence(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.RenderDebug(clientapi.Event{Kind: clientapi.EventRunCompleted})
	if got := err.String(); !strings.Contains(got, "run.completed") {
		t.Fatalf("debug output = %q, want event JSON", got)
	}
}

func TestRenderUsageSnapshotGroupsHumanReadableTotals(t *testing.T) {
	var out bytes.Buffer
	tracker := usage.NewTracker()
	tracker.Add(usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "openai", Name: "gpt-test", ID: "resp_1"},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricLLMInputTokens, Quantity: 2109, Unit: usage.UnitToken, Direction: usage.DirectionInput},
			{Metric: usage.MetricLLMCachedTokens, Quantity: 1536, Unit: usage.UnitToken, Direction: usage.DirectionCached},
			{Metric: usage.MetricLLMOutputTokens, Quantity: 11, Unit: usage.UnitToken, Direction: usage.DirectionOutput},
			{Metric: usage.MetricCost, Quantity: 0.0031, Unit: usage.UnitCurrency, Dimensions: map[string]string{"currency": "USD", "estimated": "true"}},
		},
	})
	tracker.Add(usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectNetwork, Provider: "codex", Name: "gpt-test"},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricNetworkBytes, Quantity: 18628, Unit: usage.UnitByte, Direction: usage.DirectionUpload},
			{Metric: usage.MetricNetworkBytes, Quantity: 61881, Unit: usage.UnitByte, Direction: usage.DirectionDownload},
		},
	})

	RenderUsageSnapshot(&out, tracker.Snapshot())
	got := out.String()
	for _, want := range []string{
		"Total usage",
		"openai/gpt-test",
		"input tokens 2,109",
		"cached input tokens 1,536",
		"output tokens 11",
		"estimated cost $0.0031",
		"codex/gpt-test",
		"uploaded 18.2 KB",
		"downloaded 60.4 KB",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage output = %q, want %q", got, want)
		}
	}
}

func TestRenderUsageSnapshotEmpty(t *testing.T) {
	var out bytes.Buffer
	RenderUsageSnapshot(&out, usage.Snapshot{})
	if out.Len() != 0 {
		t.Fatalf("usage output = %q, want empty", out.String())
	}
}
