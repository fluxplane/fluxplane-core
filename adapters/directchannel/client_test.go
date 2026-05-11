package directchannel

import (
	"context"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/harness"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

func TestClientSendsCommandThroughHarness(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sessionEvents, cancel, err := sessionHandle.Events(ctx, clientapi.EventOptions{Buffer: 1})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	defer cancel()

	run, err := sessionHandle.SendCommand(ctx, command.Invocation{Path: command.Path{"echo"}, Input: "hello"})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Command == nil {
		t.Fatal("Command is nil")
	}
	if result.Command.Status != session.CommandStatusOK {
		t.Fatalf("status = %s", result.Command.Status)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
	seenRunOutbound := false
	for event := range run.Events() {
		if event.Kind == clientapi.EventOutboundProduced {
			seenRunOutbound = true
		}
	}
	if !seenRunOutbound {
		t.Fatal("expected run outbound event")
	}
	select {
	case published := <-sessionEvents:
		if published.Outbound == nil || published.Outbound.Message == nil || published.Outbound.Message.Content != "hello" {
			t.Fatalf("published = %#v", published)
		}
	case <-time.After(time.Second):
		t.Fatal("expected published outbound")
	}
}

func testClient(t *testing.T) *Client {
	t.Helper()
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}

	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"echo"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "echo"},
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
		t.Fatalf("NewStore: %v", err)
	}
	service := harness.New(harness.Config{
		Commands:          commands,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
	})
	client, err := New(Config{
		Service: service,
		Channel: channel.Ref{Name: "local"},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}
