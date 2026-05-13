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

func TestRendererStreamsAllContentDeltas(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	for _, text := range []string{"Hi", ", I", " can", " help."} {
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
	got := out.String()
	for _, want := range []string{"Hi", ", I", " can", " help."} {
		if !strings.Contains(got, want) {
			t.Fatalf("out = %q, want streamed delta %q", got, want)
		}
	}
}

func TestRendererFlushesMarkdownListWithoutTrailingNewline(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamContentDelta,
				Text: "- **two**",
			}},
		},
	})
	renderer.Finish()
	got := out.String()
	if !strings.Contains(got, "two") {
		t.Fatalf("out = %q, want rendered list text", got)
	}
	if strings.Contains(got, "**two**") {
		t.Fatalf("out = %q, want rendered markdown without bold markers", got)
	}
}

func TestRendererStreamsBlockMarkdown(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	for _, text := range []string{"## README", " summary\n\n- **item**"} {
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
	got := out.String()
	if !strings.Contains(got, "README summary") || !strings.Contains(got, "item") {
		t.Fatalf("out = %q, want rendered heading and list", got)
	}
	if strings.Contains(got, "## README") || strings.Contains(got, "**item**") {
		t.Fatalf("out = %q, want rendered markdown without source markers", got)
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

func TestRendererIgnoresThinkingDeltas(t *testing.T) {
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
	if got := out.String() + err.String(); got != "" {
		t.Fatalf("thinking output = %q, want empty", got)
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

func TestRendererDebugRedactsThinkingText(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.RenderDebug(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamThinkingDelta,
				Text: "secret chain of thought",
			}},
		},
	})
	got := err.String()
	if strings.Contains(got, "secret") || strings.Contains(got, "chain") {
		t.Fatalf("debug output leaked thinking text: %q", got)
	}
	if !strings.Contains(got, "thinking_delta") || !strings.Contains(got, "redaction") {
		t.Fatalf("debug output = %q, want redacted thinking metadata", got)
	}
}

func TestRenderMarkdownRendersMarkdown(t *testing.T) {
	var out bytes.Buffer
	if err := RenderMarkdown(&out, "**hello** `world`"); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Fatalf("out = %q, want rendered markdown", got)
	}
	if strings.Contains(got, "**hello**") || strings.Contains(got, "`world`") {
		t.Fatalf("out = %q, want rendered markdown without source markers", got)
	}
}

func TestRenderUsageSnapshotGroupsHumanReadableTotals(t *testing.T) {
	var out bytes.Buffer
	tracker := usage.NewTracker()
	tracker.Add(usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "openai", Name: "gpt-test", ID: "resp_1"},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricLLMInputTokens, Quantity: 2109, Unit: usage.UnitToken, Direction: usage.DirectionInput},
			{Metric: usage.MetricLLMInputTokens, Quantity: 128, Unit: usage.UnitToken, Direction: usage.DirectionInput, Dimensions: map[string]string{"cache_creation": "true"}},
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
		"cache write tokens 128",
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
