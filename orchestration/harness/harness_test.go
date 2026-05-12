package harness

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

func TestHandleInboundCommandBindsChannelConversationAndPublishesOutbound(t *testing.T) {
	ctx := context.Background()
	service, threadStore := testService(t)

	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	events, cancel, err := service.Subscribe(ctx, info.Thread.ID, clientapi.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()

	result, err := service.HandleInbound(ctx, channel.Inbound{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-1"},
		Caller:       policy.Caller{Kind: policy.CallerUser},
		Trust:        policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:         channel.InboundCommand,
		Command:      &command.Invocation{Path: command.Path{"echo"}, Input: "hello"},
	})
	if err != nil {
		t.Fatalf("HandleInbound: %v", err)
	}
	if result.Session.Thread.ID != info.Thread.ID {
		t.Fatalf("thread id = %q, want %q", result.Session.Thread.ID, info.Thread.ID)
	}
	if result.Command.Status != session.CommandStatusOK {
		t.Fatalf("status = %s, error = %+v", result.Command.Status, result.Command.Error)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Outbound == nil {
				continue
			}
			if event.Outbound.Message == nil || event.Outbound.Message.Content != "hello" {
				t.Fatalf("published event = %#v", event)
			}
			goto sawOutbound
		case <-deadline:
			t.Fatal("expected published outbound")
		}
	}
sawOutbound:

	snapshot, err := threadStore.Read(ctx, corethread.ReadParams{ID: info.Thread.ID})
	if err != nil {
		t.Fatalf("Read thread: %v", err)
	}
	if len(snapshot.Events) < 4 {
		t.Fatalf("thread event count = %d, want at least command lifecycle events", len(snapshot.Events))
	}
}

func TestOpenSessionConcurrentSameConversationUsesOneThread(t *testing.T) {
	ctx := context.Background()
	service, _ := testService(t)

	const workers = 32
	results := make(chan corethread.ID, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := service.OpenSession(ctx, OpenSessionRequest{
				Channel:      channel.Ref{Name: "local"},
				Conversation: channel.ConversationRef{ID: "conv-1"},
			})
			if err != nil {
				errs <- err
				return
			}
			results <- info.Thread.ID
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("OpenSession: %v", err)
	}
	var first corethread.ID
	for id := range results {
		if first == "" {
			first = id
			continue
		}
		if id != first {
			t.Fatalf("thread id = %q, want %q", id, first)
		}
	}
}

func testService(t *testing.T) (*Service, corethread.Store) {
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
	return New(Config{
		Commands:          commands,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
	}), threadStore
}
