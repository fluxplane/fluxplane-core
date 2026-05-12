package session

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
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
