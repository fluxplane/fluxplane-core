package terminalui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
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

func TestApproverAcceptsYes(t *testing.T) {
	var out bytes.Buffer
	approver := Approver{In: strings.NewReader("y\n"), Out: &out}

	err := approver.Approve(operation.NewContext(context.Background(), nil), operationruntime.ApprovalRequest{
		Spec:  operation.Spec{Ref: operation.Ref{Name: "git_commit"}},
		Input: map[string]any{"message": "docs"},
		Risk:  operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "needs review", RequiresApproval: true},
	})

	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	text := out.String()
	for _, want := range []string{"approval required: git_commit", "risk: high", "reason: needs review", `"message":"docs"`, "Approve? [y/N]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("approval prompt missing %q:\n%s", want, text)
		}
	}
}

func TestApproverDeniesNoAndEmptyInput(t *testing.T) {
	for _, input := range []string{"n\n", "\n"} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			approver := Approver{In: strings.NewReader(input), Out: &bytes.Buffer{}}

			err := approver.Approve(operation.NewContext(context.Background(), nil), operationruntime.ApprovalRequest{
				Spec: operation.Spec{Ref: operation.Ref{Name: "git_commit"}},
				Risk: operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "needs review", RequiresApproval: true},
			})

			if err == nil {
				t.Fatal("Approve error is nil, want denial")
			}
		})
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

func TestRendererIgnoresUntypedRuntimeStreamPayload(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: map[string]any{
				"event": map[string]any{
					"kind": string(llmagent.StreamContentDelta),
					"text": "hello",
				},
			},
		},
	})
	renderer.Finish()
	if got := out.String() + err.String(); got != "" {
		t.Fatalf("untyped runtime output = %q, want empty", got)
	}
}

func TestRendererRendersTypedPlanPayloadAndIgnoresUntypedPlanPayload(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    planexecplugin.EventStepProgressed,
			Payload: planexecplugin.StepProgressed{StepID: "step_1", Message: "working"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    planexecplugin.EventStepProgressed,
			Payload: map[string]any{"step_id": "step_2", "message": "ignored"},
		},
	})
	renderer.Finish()
	got := out.String() + err.String()
	if !strings.Contains(got, "step_1") || !strings.Contains(got, "working") {
		t.Fatalf("plan output = %q, want typed plan progress", got)
	}
	if strings.Contains(got, "step_2") || strings.Contains(got, "ignored") {
		t.Fatalf("plan output = %q, want untyped plan payload ignored", got)
	}
}

func TestRendererSummarizesPlanToolStart(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationRequested,
		Operation: &clientapi.OperationEvent{
			Operation: operation.Ref{Name: "plan"},
			Input: map[string]any{"actions": []map[string]any{{
				"action": "create",
				"steps":  []map[string]any{{"id": "one"}, {"id": "two"}},
			}}},
		},
	})

	got := err.String()
	if !strings.Contains(got, "●") || !strings.Contains(got, "plan") || !strings.Contains(got, "  ↳ create 2 steps") {
		t.Fatalf("tool start = %q, want summarized plan call block", got)
	}
	if strings.Contains(got, `"actions"`) {
		t.Fatalf("tool start = %q, want no raw JSON actions", got)
	}
}

func TestRendererRendersToolTimeline(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationRequested,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "image_generate"},
			Input:     map[string]any{"prompt": "minimal fluxplane logo", "size": "1024x1024"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationCompleted,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "image_generate"},
			Result:    &operation.Result{Status: operation.StatusOK, Output: operation.Rendered{Text: "created image"}},
		},
	})

	got := err.String()
	for _, want := range []string{"●", "image_generate", `  ↳ prompt="minimal fluxplane logo" size=1024x1024`, "✓", "created image"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool block = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "tool start:") || strings.Contains(got, "tool end:") || strings.Contains(got, `{"prompt"`) || strings.Contains(got, "  >") {
		t.Fatalf("tool block = %q, did not want old start/end wording", got)
	}
}

func TestRendererRendersToolTimelineFailure(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationRequested,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "git_commit"},
			Input:     map[string]any{"message": "fix timeline UI"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationCompleted,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "git_commit"},
			Result: &operation.Result{
				Status: operation.StatusRejected,
				Error:  &operation.Error{Code: "approval_required", Message: "approval required"},
			},
		},
	})

	got := err.String()
	for _, want := range []string{"●", "git_commit", `  ↳ message="fix timeline UI"`, "✕", "rejected", `reason="approval required"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool block = %q, missing %q", got, want)
		}
	}
}

func TestRendererFiltersNoisyPlanProgress(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    planexecplugin.EventStepProgressed,
			Payload: planexecplugin.StepProgressed{StepID: "step_1", Message: "llmagent.model_streamed"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    planexecplugin.EventStepProgressed,
			Payload: planexecplugin.StepProgressed{StepID: "step_1", Message: "completed file_read"},
		},
	})
	renderer.Finish()

	got := out.String() + err.String()
	if strings.Contains(got, "llmagent.model_streamed") {
		t.Fatalf("plan progress = %q, want noisy model progress filtered", got)
	}
	if !strings.Contains(got, "completed file_read") {
		t.Fatalf("plan progress = %q, want operation progress retained", got)
	}
}

func TestRendererRerendersPlanWithStatusMarkers(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: planexecplugin.EventPlanCreated,
			Payload: planexecplugin.PlanCreated{
				PlanID: "plan_1",
				Spec: planexecplugin.PlanSpec{Title: "T", Steps: []planexecplugin.StepSpec{
					{ID: "one", Title: "One", Profile: "explorer"},
					{ID: "two", Title: "Two", Profile: "worker"},
				}},
			},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    planexecplugin.EventStepDispatched,
			Payload: planexecplugin.StepDispatched{PlanID: "plan_1", StepID: "one", Profile: "explorer"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    planexecplugin.EventStepCompleted,
			Payload: planexecplugin.StepCompleted{PlanID: "plan_1", StepID: "one", Output: "done"},
		},
	})
	renderer.Finish()

	got := out.String() + err.String()
	if strings.Count(got, "plan:") != 3 {
		t.Fatalf("plan output = %q, want create, dispatched, and completed renders", got)
	}
	if !strings.Contains(got, ansiGreen+"●"+ansiReset+" One") {
		t.Fatalf("plan output = %q, want completed green marker for One", got)
	}
	if !strings.Contains(got, ansiDim+"◌"+ansiReset+" Two") {
		t.Fatalf("plan output = %q, want waiting marker for Two", got)
	}
}

func TestRendererSuppressesPlanOwnedDelegateNoise(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    subagent.EventStarted,
			Payload: subagent.Started{Causation: subagent.Causation{WorkerID: "plan_1:step_1", Profile: coresession.Ref{Name: "worker"}}},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    subagent.EventCompleted,
			Payload: subagent.Completed{Causation: subagent.Causation{WorkerID: "manual_1"}, Output: "done"},
		},
	})
	renderer.Finish()

	got := out.String() + err.String()
	if strings.Contains(got, "plan_1:step_1") {
		t.Fatalf("delegate output = %q, want plan-owned worker suppressed", got)
	}
	if !strings.Contains(got, "delegate done:") || !strings.Contains(got, "manual_1") {
		t.Fatalf("delegate output = %q, want manual delegate rendered", got)
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
