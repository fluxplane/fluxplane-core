package codershell

import (
	"context"
	"fmt"
	"strings"
	"testing"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/operation"
	coreusage "github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestDirectChannelClientCreatesSessionAndConnectionDescription(t *testing.T) {
	service, err := agentruntime.New(agentruntime.Config{})
	if err != nil {
		t.Fatalf("agentruntime.New() error = %v", err)
	}
	client := NewDirectChannelClient(DirectChannelClientOptions{Client: service})
	if got := client.ConnectionDescription(); got != "direct-channel" {
		t.Fatalf("ConnectionDescription() = %q", got)
	}
	info, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "/workspace"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if info.ID == "" {
		t.Fatal("CreateSession() ID is empty")
	}
	if info.CWD != "/workspace" {
		t.Fatalf("CreateSession() CWD = %q", info.CWD)
	}
}
func TestDirectChannelClientSubmitAskUsesChannelClient(t *testing.T) {
	service, err := agentruntime.New(agentruntime.Config{Agent: echoAgent{}})
	if err != nil {
		t.Fatalf("agentruntime.New() error = %v", err)
	}
	client := NewDirectChannelClient(DirectChannelClientOptions{Client: service})
	info, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "/workspace"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	events, err := client.SubmitAsk(context.Background(), info.ID, AskRequest{Text: "hello", CWD: "/workspace"})
	if err != nil {
		t.Fatalf("SubmitAsk() error = %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("events = %#v, want at least two", events)
	}
	if events[0].Kind != EventAskSubmitted {
		t.Fatalf("events[0].Kind = %q", events[0].Kind)
	}
	if events[1].Kind != EventAskOutput || events[1].Summary != "echo: hello" {
		t.Fatalf("events[1] = %#v", events[1])
	}
}

func TestDirectChannelClientSubmitCommandUsesShellExecOperation(t *testing.T) {
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: operation.Ref{Name: "shell_exec"}}, func(_ operation.Context, input operation.Value) operation.Result {
		payload, ok := input.(map[string]any)
		if !ok {
			return operation.Failed("bad_input", "input was not a map", nil)
		}
		if payload["command"] != "go" || payload["workdir"] != "/workspace" {
			return operation.Failed("bad_input", fmt.Sprintf("input = %#v", payload), nil)
		}
		return operation.OK("ran")
	})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	service, err := agentruntime.New(agentruntime.Config{Operations: ops})
	if err != nil {
		t.Fatalf("agentruntime.New() error = %v", err)
	}
	client := NewDirectChannelClient(DirectChannelClientOptions{Client: service})
	info, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "/workspace"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	events, err := client.SubmitCommand(context.Background(), info.ID, CommandRequest{Line: "go test ./apps/coder", CWD: "/workspace"})
	if err != nil {
		t.Fatalf("SubmitCommand() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v, want start output complete", events)
	}
	if events[0].Kind != EventCommandStarted {
		t.Fatalf("events[0] = %#v", events[0])
	}
	if events[1].Kind != EventCommandOutput || events[1].Summary != "ran" {
		t.Fatalf("events[1] = %#v", events[1])
	}
	if events[2].Kind != EventCommandComplete || events[2].Summary != "ok" {
		t.Fatalf("events[2] = %#v", events[2])
	}
}

func TestTranscriptEventsForResultUsesRenderedOutboundText(t *testing.T) {
	events := transcriptEventsForResult("session-1", agentruntime.Result{
		Outbound: &channel.Outbound{Message: &channel.Message{Content: operation.Rendered{
			Text: "Environment\n\nObservers\n  - runtime.baseline",
			Data: map[string]any{"observers": []string{"runtime.baseline"}},
		}}},
	}, EventCommandOutput, EventCommandComplete)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one outbound event", events)
	}
	if events[0].Summary != "Environment\n\nObservers\n  - runtime.baseline" {
		t.Fatalf("summary = %q, want rendered text only", events[0].Summary)
	}
}

func TestTranscriptEventsForRunEventMapsLiveRuntimeSignals(t *testing.T) {
	requestedEvents := transcriptEventsForRunEvent("session-1", clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelRequestedName,
		},
	})
	if len(requestedEvents) != 0 {
		t.Fatalf("model requested events = %#v, want no visible transcript noise", requestedEvents)
	}

	streamEvents := transcriptEventsForRunEvent("session-1", clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamContentDelta,
				Text: "hello",
			}},
		},
	})
	if len(streamEvents) != 1 || streamEvents[0].Kind != EventAskDelta || streamEvents[0].Summary != "hello" {
		t.Fatalf("stream events = %#v, want ask delta", streamEvents)
	}

	processEvents := transcriptEventsForRunEvent("session-1", clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    system.EventProcessOutput,
			Payload: system.ProcessEvent{ProcessID: "proc-1", Kind: "output", Stream: "stdout", Data: "line\n"},
		},
	})
	if len(processEvents) != 1 || processEvents[0].Kind != EventProcessOutput || processEvents[0].Summary != "stdout: line" {
		t.Fatalf("process events = %#v, want process output", processEvents)
	}

	usageEvents := transcriptEventsForRunEvent("session-1", clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: coreusage.EventRecordedName,
			Payload: coreusage.Recorded{
				Subject: coreusage.Subject{Kind: coreusage.SubjectLLM, Provider: "codex", Name: "gpt-5.5"},
				Measurements: []coreusage.Measurement{
					{Metric: coreusage.MetricLLMInputTokens, Quantity: 1200, Unit: coreusage.UnitToken, Direction: coreusage.DirectionInput},
					{Metric: coreusage.MetricLLMOutputTokens, Quantity: 34, Unit: coreusage.UnitToken, Direction: coreusage.DirectionOutput},
					{Metric: coreusage.MetricCost, Quantity: 0.0012, Unit: coreusage.UnitCurrency, Dimensions: map[string]string{"currency": "USD"}},
				},
			},
		},
	})
	if len(usageEvents) != 1 || usageEvents[0].Kind != EventUsageRecorded || usageEvents[0].Data["input_tokens"] != "1200" || usageEvents[0].Data["output_tokens"] != "34" {
		t.Fatalf("usage events = %#v, want usage totals", usageEvents)
	}
	if !strings.Contains(usageEvents[0].Summary, "in 1.2k") || !strings.Contains(usageEvents[0].Summary, "$0.0012") {
		t.Fatalf("usage summary = %q, want compact token and cost summary", usageEvents[0].Summary)
	}
}

func TestTranscriptEventsForRunEventMapsOperationLifecycle(t *testing.T) {
	started := transcriptEventsForRunEvent("session-1", clientapi.Event{
		Kind: clientapi.EventOperationRequested,
		Operation: &clientapi.OperationEvent{
			CallID:    "call-1",
			Operation: operation.Ref{Name: "shell_exec"},
			Input:     map[string]any{"command": "go"},
		},
	})
	if len(started) != 1 || started[0].Kind != EventOperationStarted || started[0].Data["call_id"] != "call-1" {
		t.Fatalf("started = %#v, want operation started", started)
	}

	completed := transcriptEventsForRunEvent("session-1", clientapi.Event{
		Kind: clientapi.EventOperationCompleted,
		Operation: &clientapi.OperationEvent{
			CallID:    "call-1",
			Operation: operation.Ref{Name: "shell_exec"},
			Result:    ptr(operation.OK("done")),
		},
	})
	if len(completed) != 1 || completed[0].Kind != EventOperationComplete || completed[0].Data["call_id"] != "call-1" {
		t.Fatalf("completed = %#v, want operation completed", completed)
	}
}

func TestShellOperationInvocationTargetsShellExecOperation(t *testing.T) {
	inv, err := shellOperationInvocation("go test ./apps/coder", "/workspace")
	if err != nil {
		t.Fatalf("shellOperationInvocation() error = %v", err)
	}
	if inv.Operation.Name != "shell_exec" {
		t.Fatalf("operation = %q, want shell_exec", inv.Operation.Name)
	}
	input, ok := inv.Input.(map[string]any)
	if !ok {
		t.Fatalf("input = %#v, want map", inv.Input)
	}
	if input["command"] != "go" {
		t.Fatalf("command = %#v, want go", input["command"])
	}
	if input["workdir"] != "/workspace" {
		t.Fatalf("workdir = %#v, want /workspace", input["workdir"])
	}
	args, ok := input["args"].([]string)
	if !ok || len(args) != 2 || args[0] != "test" || args[1] != "./apps/coder" {
		t.Fatalf("args = %#v, want test ./apps/coder", input["args"])
	}
}

type echoAgent struct{}

func (echoAgent) Spec() agent.Spec { return agent.Spec{Name: "echo"} }

func (echoAgent) Step(_ agent.Context, input agent.StepInput) agent.StepResult {
	text := input.Goal
	if text == "" && len(input.Observations) > 0 {
		text = fmt.Sprint(input.Observations[0].Content)
	}
	return agent.StepResult{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "echo: " + text}}}
}

func TestShellObjectRecordsConnection(t *testing.T) {
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: NewFakeClient(), CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	active := shell.ActiveTab()
	if active == nil || len(active.Transcript) == 0 {
		t.Fatal("missing active transcript")
	}
	first := active.Transcript[0]
	if first.Kind != EventClientConnected {
		t.Fatalf("first event kind = %q", first.Kind)
	}
	if first.Data["connection"] != "fake" {
		t.Fatalf("connection data = %q", first.Data["connection"])
	}
}

func ptr[T any](value T) *T {
	return &value
}
