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
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
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
			if event.Session.Thread.ID != info.Thread.ID {
				t.Fatalf("event session thread = %q, want %q", event.Session.Thread.ID, info.Thread.ID)
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

func TestHandleInboundCommandPublishesOperationLifecycle(t *testing.T) {
	ctx := context.Background()
	service, _ := testService(t)

	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	events, cancel, err := service.Subscribe(ctx, info.Thread.ID, clientapi.EventOptions{Buffer: 16})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()

	_, err = service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:           "run-1",
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-1"},
		Caller:       policy.Caller{Kind: policy.CallerUser},
		Trust:        policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:         channel.InboundCommand,
		Command:      &command.Invocation{Path: command.Path{"echo"}, Input: "hello"},
	})
	if err != nil {
		t.Fatalf("HandleSessionInbound: %v", err)
	}

	var requested, completed bool
	var callID operation.CallID
	deadline := time.After(time.Second)
	for !requested || !completed {
		select {
		case event := <-events:
			if event.RunID != "run-1" || event.Operation == nil {
				continue
			}
			switch event.Kind {
			case clientapi.EventOperationRequested:
				requested = true
				callID = event.Operation.CallID
				if callID == "" || event.Operation.Operation.Name != "echo" || event.Operation.Input != "hello" {
					t.Fatalf("operation requested = %#v", event.Operation)
				}
			case clientapi.EventOperationCompleted:
				completed = true
				if event.Operation.CallID == "" || event.Operation.CallID != callID || event.Operation.Operation.Name != "echo" || event.Operation.Result == nil || event.Operation.Result.Output != "hello" {
					t.Fatalf("operation completed = %#v", event.Operation)
				}
			}
		case <-deadline:
			t.Fatalf("operation lifecycle requested=%v completed=%v", requested, completed)
		}
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

func TestHandleSessionInboundUsesExplicitSessionThread(t *testing.T) {
	ctx := context.Background()
	service, threadStore := testService(t)

	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	other, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-2"},
	})
	if err != nil {
		t.Fatalf("OpenSession other: %v", err)
	}
	otherBefore, err := threadStore.Read(ctx, corethread.ReadParams{ID: other.Thread.ID})
	if err != nil {
		t.Fatalf("Read other thread before: %v", err)
	}

	result, err := service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:      "run-1",
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:    channel.InboundCommand,
		Command: &command.Invocation{Path: command.Path{"echo"}, Input: "hello"},
	})
	if err != nil {
		t.Fatalf("HandleSessionInbound: %v", err)
	}
	if result.Session.Thread.ID != info.Thread.ID {
		t.Fatalf("thread id = %q, want %q", result.Session.Thread.ID, info.Thread.ID)
	}

	snapshot, err := threadStore.Read(ctx, corethread.ReadParams{ID: info.Thread.ID})
	if err != nil {
		t.Fatalf("Read explicit thread: %v", err)
	}
	if len(snapshot.Events) == 0 {
		t.Fatal("expected explicit thread events")
	}
	otherSnapshot, err := threadStore.Read(ctx, corethread.ReadParams{ID: other.Thread.ID})
	if err != nil {
		t.Fatalf("Read other thread: %v", err)
	}
	if len(otherSnapshot.Events) != len(otherBefore.Events) {
		t.Fatalf("other thread event count = %d, want %d", len(otherSnapshot.Events), len(otherBefore.Events))
	}
}

func TestHandleSessionInboundPreservesInboundRoutingWhenSessionIsThreadOnly(t *testing.T) {
	ctx := context.Background()
	service, _ := testService(t)

	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	threadOnly := SessionInfo{Thread: info.Thread}

	result, err := service.HandleSessionInbound(ctx, threadOnly, channel.Inbound{
		ID:           "run-1",
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-1"},
		Caller:       policy.Caller{Kind: policy.CallerUser},
		Trust:        policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:         channel.InboundCommand,
		Command:      &command.Invocation{Path: command.Path{"echo"}, Input: "hello"},
	})
	if err != nil {
		t.Fatalf("HandleSessionInbound: %v", err)
	}
	if result.Session.Channel.Name != "local" {
		t.Fatalf("result channel = %q, want local", result.Session.Channel.Name)
	}
	if result.Session.Conversation.ID != "conv-1" {
		t.Fatalf("result conversation = %q, want conv-1", result.Session.Conversation.ID)
	}
	if result.Outbound == nil || result.Outbound.Channel.Name != "local" || result.Outbound.Conversation.ID != "conv-1" {
		t.Fatalf("outbound routing = %#v", result.Outbound)
	}
}

func TestSubscribeCancelDoesNotRacePublish(t *testing.T) {
	ctx := context.Background()
	service, _ := testService(t)
	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	const iterations = 200
	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		events, cancel, err := service.Subscribe(ctx, info.Thread.ID, clientapi.EventOptions{Buffer: 1})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		wg.Add(2)
		go func() {
			defer wg.Done()
			cancel()
		}()
		go func(i int) {
			defer wg.Done()
			service.publish(info.Thread.ID, clientapi.Event{
				Kind:  clientapi.EventRunCompleted,
				RunID: clientapi.RunID("run"),
			})
			for range events {
			}
		}(i)
	}
	wg.Wait()
}

func TestOpenSessionAppliesConfiguredSessionDefaults(t *testing.T) {
	ctx := context.Background()
	service, _ := testService(t)
	service.sessionCatalog = session.SessionCatalog{
		"embedded:apps/demo:coder": {
			ID: resource.ResourceID{
				Kind:      "session",
				Origin:    "embedded",
				Namespace: resource.NewNamespace("apps/demo"),
				Name:      "coder",
			},
			Spec: coresession.Spec{
				Name:         "coder",
				Channel:      channel.Ref{Name: "local"},
				Conversation: channel.ConversationRef{ID: "devclient"},
				Metadata:     map[string]string{"profile": "coder", "default": "yes"},
			},
		},
	}

	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Session:  coresession.Ref{Name: "demo:coder"},
		Metadata: map[string]string{"default": "override"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if info.Session.Name != "embedded:apps/demo:coder" {
		t.Fatalf("session ref = %q, want embedded:apps/demo:coder", info.Session.Name)
	}
	if info.Channel.Name != "local" {
		t.Fatalf("channel = %q, want local", info.Channel.Name)
	}
	if info.Conversation.ID != "devclient" {
		t.Fatalf("conversation = %q, want devclient", info.Conversation.ID)
	}
	if info.Metadata["profile"] != "coder" || info.Metadata["default"] != "override" {
		t.Fatalf("metadata = %#v", info.Metadata)
	}
}

func TestOpenSessionProfilesSeparateSameConversation(t *testing.T) {
	ctx := context.Background()
	service, _ := testService(t)
	service.sessionCatalog = session.SessionCatalog{
		"embedded:apps/demo:coder": {
			ID: resource.ResourceID{
				Kind:      "session",
				Origin:    "embedded",
				Namespace: resource.NewNamespace("apps/demo"),
				Name:      "coder",
			},
			Spec: coresession.Spec{
				Name:         "coder",
				Channel:      channel.Ref{Name: "local"},
				Conversation: channel.ConversationRef{ID: "devclient"},
			},
		},
		"embedded:apps/demo:reviewer": {
			ID: resource.ResourceID{
				Kind:      "session",
				Origin:    "embedded",
				Namespace: resource.NewNamespace("apps/demo"),
				Name:      "reviewer",
			},
			Spec: coresession.Spec{
				Name:         "reviewer",
				Channel:      channel.Ref{Name: "local"},
				Conversation: channel.ConversationRef{ID: "devclient"},
			},
		},
	}

	coder, err := service.OpenSession(ctx, OpenSessionRequest{Session: coresession.Ref{Name: "coder"}})
	if err != nil {
		t.Fatalf("OpenSession coder: %v", err)
	}
	reviewer, err := service.OpenSession(ctx, OpenSessionRequest{Session: coresession.Ref{Name: "reviewer"}})
	if err != nil {
		t.Fatalf("OpenSession reviewer: %v", err)
	}
	coderAgain, err := service.OpenSession(ctx, OpenSessionRequest{Session: coresession.Ref{Name: "coder"}})
	if err != nil {
		t.Fatalf("OpenSession coder again: %v", err)
	}
	coderQualified, err := service.OpenSession(ctx, OpenSessionRequest{Session: coresession.Ref{Name: "demo:coder"}})
	if err != nil {
		t.Fatalf("OpenSession coder qualified: %v", err)
	}

	if coder.Thread.ID == reviewer.Thread.ID {
		t.Fatalf("coder and reviewer share thread %q", coder.Thread.ID)
	}
	if coder.Thread.ID != coderAgain.Thread.ID {
		t.Fatalf("coder thread = %q, want resumed %q", coderAgain.Thread.ID, coder.Thread.ID)
	}
	if coder.Thread.ID != coderQualified.Thread.ID {
		t.Fatalf("qualified coder thread = %q, want resumed %q", coderQualified.Thread.ID, coder.Thread.ID)
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
