package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

func TestExecuteInboundCommandDispatchesOperation(t *testing.T) {
	opRef := operation.Ref{Name: "echo"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"echo": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"echo"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: opRef,
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}

	s := Session{
		Commands:          commands,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
	}
	result := s.ExecuteInboundCommand(context.Background(), channel.Inbound{
		Kind: channel.InboundCommand,
		Caller: policy.Caller{
			Kind: policy.CallerUser,
		},
		Trust: policy.Trust{
			Kind:  policy.TrustInvocation,
			Level: policy.TrustVerified,
		},
		Command: &command.Invocation{
			Path:  command.Path{"echo"},
			Input: "hello",
		},
	})

	if result.Status != CommandStatusOK {
		t.Fatalf("status = %q, want %q: %#v", result.Status, CommandStatusOK, result)
	}
	if result.Effect == nil || result.Effect.Result.Status != operation.StatusOK {
		t.Fatalf("effect result = %#v, want ok", result.Effect)
	}
}

func TestExecuteInboundCommandPersistsFailedOperationOutboundMessage(t *testing.T) {
	ctx := context.Background()
	opRef := operation.Ref{Name: "fail"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(operation.Context, operation.Value) operation.Result {
		return operation.Failed("boom", "operation failed", nil)
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"fail"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: opRef,
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-1"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	s := Session{
		Commands:          commands,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
		Thread:            corethread.Ref{ID: "thread-1"},
	}

	result := s.ExecuteInboundCommand(ctx, channel.Inbound{
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path: command.Path{"fail"},
		},
	})
	if result.Status != CommandStatusFailed {
		t.Fatalf("status = %q, want failed: %#v", result.Status, result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-1"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	payload, ok := stored.Events[4].Event.Payload.(coresession.OutboundProduced)
	if !ok {
		t.Fatalf("payload = %T, want OutboundProduced", stored.Events[4].Event.Payload)
	}
	if payload.Message.Content != "operation failed" {
		t.Fatalf("outbound content = %#v, want operation failed", payload.Message.Content)
	}
}

func TestExecuteInboundInputDispatchesAgentMessage(t *testing.T) {
	s := Session{
		Agent: fixedAgent{
			result: agent.StepResult{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:    agent.DecisionMessage,
					Message: &agent.Message{Content: "pong"},
				},
			},
		},
	}
	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "ping"},
	})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "pong" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
}

func TestExecuteInboundInputDispatchesMultipleAgentOperations(t *testing.T) {
	var emitted []event.Event
	first := operation.Ref{Name: "first"}
	second := operation.Ref{Name: "second"}
	ops := operation.NewRegistry()
	if err := ops.Register(
		operation.New(operation.Spec{Ref: first}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(map[string]any{"first": input})
		}),
		operation.New(operation.Spec{Ref: second}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(map[string]any{"second": input})
		}),
	); err != nil {
		t.Fatalf("register operations: %v", err)
	}
	s := Session{
		Agent: fixedAgent{
			result: agent.StepResult{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind: agent.DecisionOperation,
					Operations: []agent.OperationRequest{
						{Operation: first, Input: "a"},
						{Operation: second, Input: "b"},
					},
				},
			},
		},
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		Events: event.SinkFunc(func(evt event.Event) {
			emitted = append(emitted, evt)
		}),
	}

	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "run"},
	})

	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(result.Effects) != 2 {
		t.Fatalf("effects len = %d, want 2", len(result.Effects))
	}
	if result.Effect == nil || result.Effect.Result.Output.(map[string]any)["second"] != "b" {
		t.Fatalf("last effect = %#v, want second operation", result.Effect)
	}
	requested := requestedCallIDs(emitted)
	completed := completedCallIDs(emitted)
	if len(requested) != 2 || len(completed) != 2 {
		t.Fatalf("operation call ids requested=%v completed=%v", requested, completed)
	}
	if requested[0] == "" || requested[1] == "" || requested[0] == requested[1] {
		t.Fatalf("requested call ids = %v, want two distinct non-empty ids", requested)
	}
	if requested[0] != completed[0] || requested[1] != completed[1] {
		t.Fatalf("requested call ids = %v, completed call ids = %v", requested, completed)
	}
}

func TestExecuteInboundInputContinuesLLMAgentAfterOperation(t *testing.T) {
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{
			Name:   "llm",
			Driver: agent.DriverSpec{Kind: "llmagent"},
		},
		results: []agent.StepResult{
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:       agent.DecisionOperation,
					Operations: []agent.OperationRequest{{Operation: opRef, Input: "thing"}},
				},
			},
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:    agent.DecisionMessage,
					Message: &agent.Message{Content: "found thing"},
				},
			},
		},
	}
	s := Session{
		Agent:             agentRuntime,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
	}

	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "run"},
	})

	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(result.Effects) != 1 {
		t.Fatalf("effects len = %d, want 1", len(result.Effects))
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "found thing" {
		t.Fatalf("outbound = %#v, want final agent message", result.Outbound)
	}
	if len(agentRuntime.inputs) != 2 {
		t.Fatalf("agent steps = %d, want 2", len(agentRuntime.inputs))
	}
	if len(agentRuntime.inputs[1].Observations) != 2 {
		t.Fatalf("second observations len = %d, want user input and operation result", len(agentRuntime.inputs[1].Observations))
	}
	if got := agentRuntime.inputs[1].Observations[1].Kind; got != "operation.result" {
		t.Fatalf("second observation kind = %q, want operation.result", got)
	}
}

func TestExecuteInboundInputRequiresAgent(t *testing.T) {
	result := (Session{}).ExecuteInboundInput(context.Background(), channel.Inbound{
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "ping"},
	})
	if result.Status != InputStatusFailed {
		t.Fatalf("status = %q, want failed: %#v", result.Status, result)
	}
	if result.Error == nil || result.Error.Code != "agent_missing" {
		t.Fatalf("error = %#v, want agent_missing", result.Error)
	}
}

func TestExecuteInboundInputProjectsThreadHistory(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-history"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{Name: "memory"},
		results: []agent.StepResult{
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:    agent.DecisionMessage,
					Message: &agent.Message{Content: "noted"},
				},
			},
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:    agent.DecisionMessage,
					Message: &agent.Message{Content: "Timo"},
				},
			},
		},
	}
	s := Session{
		Agent:       agentRuntime,
		ThreadStore: threadStore,
		Thread:      corethread.Ref{ID: "thread-history"},
	}

	first := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-1",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "my name is Timo"},
	})
	if first.Status != InputStatusOK {
		t.Fatalf("first status = %q, want ok: %#v", first.Status, first)
	}
	second := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-2",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "what is my name?"},
	})
	if second.Status != InputStatusOK {
		t.Fatalf("second status = %q, want ok: %#v", second.Status, second)
	}
	if len(agentRuntime.inputs) != 2 {
		t.Fatalf("agent inputs len = %d, want 2", len(agentRuntime.inputs))
	}
	if len(agentRuntime.inputs[0].Context) != 0 {
		t.Fatalf("first context = %#v, want none", agentRuntime.inputs[0].Context)
	}
	if len(agentRuntime.inputs[1].Context) != 1 {
		t.Fatalf("second context = %#v, want history block", agentRuntime.inputs[1].Context)
	}
	history := agentRuntime.inputs[1].Context[0].Content
	if !strings.Contains(history, "User: my name is Timo") || !strings.Contains(history, "Agent: noted") {
		t.Fatalf("history = %q, want prior user and agent messages", history)
	}
	if strings.Contains(history, "what is my name") {
		t.Fatalf("history = %q, should not duplicate current input", history)
	}
}

func TestExecuteInboundInputProjectsProviderTranscriptAcrossToolContinuation(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-transcript"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	provider := coreconversation.ProviderIdentity{
		Provider: "openai",
		API:      "openai.responses",
		Family:   "responses",
		Model:    "gpt-test",
	}
	var transcripts []coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		if req.Transcript == nil {
			t.Fatalf("transcript is nil")
		}
		transcripts = append(transcripts, *req.Transcript)
		if len(transcripts) == 1 {
			return llmagent.Response{
				Operations: []agent.OperationRequest{{Operation: opRef, Input: "A100", ProviderCallID: "call_1"}},
				Transcript: coreconversation.Transcript{
					Provider: provider,
					Items: []coreconversation.Item{
						{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "lookup A100"},
						{Provider: provider, Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "lookup", Native: json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"key\":\"A100\"}"}`)},
					},
				},
			}, nil
		}
		return llmagent.Response{
			Message: &agent.Message{Content: "found A100"},
			Transcript: coreconversation.Transcript{
				Provider: provider,
				Items: []coreconversation.Item{
					{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "found A100", Native: json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"found A100"}]}`)},
				},
			},
		}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{
		Agent:             runtimeAgent,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
		Thread:            corethread.Ref{ID: "thread-transcript"},
	}

	result := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-1",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "lookup A100"},
	})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(transcripts) != 2 {
		t.Fatalf("transcripts len = %d, want 2", len(transcripts))
	}
	if len(transcripts[0].Items) != 1 || transcripts[0].Items[0].Content != "lookup A100" {
		t.Fatalf("first transcript = %#v, want current user input", transcripts[0])
	}
	if !hasToolResultCallID(transcripts[1].Items, "call_1") {
		t.Fatalf("second transcript items = %#v, want tool result for provider call", transcripts[1].Items)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-transcript"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if countEvent(stored.Events, coreconversation.EventItemsAppended) != 2 {
		t.Fatalf("stored events = %#v, want two transcript append events", stored.Events)
	}
}

func TestExecuteInboundCommandRejectsInsufficientTrust(t *testing.T) {
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"admin"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "admin"},
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustPrivileged,
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}

	s := Session{Commands: commands}
	result := s.ExecuteInboundCommand(context.Background(), channel.Inbound{
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path: command.Path{"admin"},
		},
	})

	if result.Status != CommandStatusRejected {
		t.Fatalf("status = %q, want %q: %#v", result.Status, CommandStatusRejected, result)
	}
	if result.Policy.Reason != "insufficient_trust" {
		t.Fatalf("policy reason = %q, want insufficient_trust", result.Policy.Reason)
	}
}

type fixedAgent struct {
	result agent.StepResult
}

func (a fixedAgent) Spec() agent.Spec {
	return agent.Spec{Name: "fixed"}
}

func (a fixedAgent) Step(_ agent.Context, _ agent.StepInput) agent.StepResult {
	return a.result
}

type sequenceAgent struct {
	spec    agent.Spec
	results []agent.StepResult
	inputs  []agent.StepInput
}

func (a *sequenceAgent) Spec() agent.Spec {
	return a.spec
}

func (a *sequenceAgent) Step(_ agent.Context, input agent.StepInput) agent.StepResult {
	a.inputs = append(a.inputs, input)
	if len(a.inputs) <= len(a.results) {
		return a.results[len(a.inputs)-1]
	}
	return agent.StepResult{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionWait}}
}

func requestedCallIDs(events []event.Event) []operation.CallID {
	var out []operation.CallID
	for _, emitted := range events {
		if payload, ok := emitted.(coresession.OperationRequested); ok {
			out = append(out, payload.CallID)
		}
	}
	return out
}

func completedCallIDs(events []event.Event) []operation.CallID {
	var out []operation.CallID
	for _, emitted := range events {
		if payload, ok := emitted.(coresession.OperationCompleted); ok {
			out = append(out, payload.CallID)
		}
	}
	return out
}

func hasToolResultCallID(items []coreconversation.Item, callID string) bool {
	for _, item := range items {
		if item.Kind == coreconversation.ItemToolResult && item.CallID == callID {
			return true
		}
	}
	return false
}

func countEvent(records []corethread.Record, name event.Name) int {
	count := 0
	for _, record := range records {
		if record.Event.Name == name {
			count++
		}
	}
	return count
}

func TestExecuteInboundCommandPersistsThreadEvents(t *testing.T) {
	ctx := context.Background()
	opRef := operation.Ref{Name: "echo"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"echo"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: opRef,
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-1"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	s := Session{
		Commands:          commands,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
		Thread:            corethread.Ref{ID: "thread-1"},
	}

	result := s.ExecuteInboundCommand(ctx, channel.Inbound{
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path:  command.Path{"echo"},
			Input: "hello",
		},
	})
	if result.Status != CommandStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-1"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if len(stored.Events) != 5 {
		t.Fatalf("len(events) = %d, want 5", len(stored.Events))
	}
	got := stored.Events[1].Event.Name
	if got != "session.command.received" {
		t.Fatalf("event[1] = %q, want session.command.received", got)
	}
	got = stored.Events[4].Event.Name
	if got != "session.outbound.produced" {
		t.Fatalf("event[4] = %q, want session.outbound.produced", got)
	}
}
