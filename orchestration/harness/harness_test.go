package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	coreagent "github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	coretask "github.com/fluxplane/agentruntime/core/task"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/session"
	conversationruntime "github.com/fluxplane/agentruntime/runtime/conversation"
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

func TestRuntimeEventSinkPersistsReplayableRuntimeEvents(t *testing.T) {
	ctx := context.Background()
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: operation.Ref{Name: "emit_plan"}}, func(ctx operation.Context, _ operation.Value) operation.Result {
		ctx.Events().Emit(testPlanRuntimeEvent{Value: "persist me"})
		return operation.OK("ok")
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"emit_plan"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "emit_plan"},
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
	service := New(Config{
		Commands:          commands,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
	})
	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-runtime"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if _, err := service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:           "run-runtime",
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-runtime"},
		Caller:       policy.Caller{Kind: policy.CallerUser},
		Trust:        policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:         channel.InboundCommand,
		Command:      &command.Invocation{Path: command.Path{"emit_plan"}},
	}); err != nil {
		t.Fatalf("HandleSessionInbound: %v", err)
	}
	replayed, err := service.replayEvents(ctx, info.Thread.ID, clientapi.EventOptions{Replay: true})
	if err != nil {
		t.Fatalf("replayEvents: %v", err)
	}
	for _, event := range replayed {
		if event.Kind == clientapi.EventRuntimeEmitted && event.Runtime != nil && event.Runtime.Name == "plan.test" {
			payload, ok := event.Runtime.Payload.(testPlanRuntimeEvent)
			if !ok || payload.Value != "persist me" {
				t.Fatalf("runtime payload = %#v, want testPlanRuntimeEvent", event.Runtime.Payload)
			}
			return
		}
	}
	t.Fatalf("replayed events missing persisted runtime event: %#v", replayed)
}

func TestRuntimeEventSinkRetriesRuntimeEventAppendConflict(t *testing.T) {
	ctx := context.Background()
	baseStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	threadStore := &appendFaultThreadStore{
		Store: baseStore,
		failures: []error{coreevent.AppendConflict{
			Stream:   "thread:thread-runtime",
			Expected: 1,
			Actual:   2,
		}},
	}
	service := New(Config{ThreadStore: threadStore})
	info, err := service.OpenSession(ctx, OpenSessionRequest{ThreadID: "thread-runtime"})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := service.persistRuntimeEvent(ctx, info, "run-runtime", testPlanRuntimeEvent{Value: "retry me"}); err != nil {
		t.Fatalf("persistRuntimeEvent: %v", err)
	}
	if threadStore.appendCalls != 2 {
		t.Fatalf("append calls = %d, want 2", threadStore.appendCalls)
	}
	snapshot, err := baseStore.Read(ctx, corethread.ReadParams{ID: info.Thread.ID})
	if err != nil {
		t.Fatalf("Read thread: %v", err)
	}
	for _, record := range snapshot.Events {
		runtimeEvent, ok := record.Event.Payload.(coresession.RuntimeEmitted)
		if ok && runtimeEvent.Name == "plan.test" {
			return
		}
	}
	t.Fatalf("thread events missing retried runtime event: %#v", snapshot.Events)
}

func TestRuntimeEventSinkPersistsTaskEvents(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	service := New(Config{ThreadStore: threadStore})
	info, err := service.OpenSession(ctx, OpenSessionRequest{ThreadID: "thread-task-runtime"})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	payload := coretask.Created{
		TaskID: "task_1",
		Task:   coretask.Task{ID: "task_1", Title: "Inspect runtime task events", Status: coretask.StatusReady},
	}
	if err := service.persistRuntimeEvent(ctx, info, "run-task", payload); err != nil {
		t.Fatalf("persistRuntimeEvent: %v", err)
	}
	replayed, err := service.replayEvents(ctx, info.Thread.ID, clientapi.EventOptions{Replay: true})
	if err != nil {
		t.Fatalf("replayEvents: %v", err)
	}
	for _, event := range replayed {
		if event.Kind != clientapi.EventRuntimeEmitted || event.Runtime == nil || event.Runtime.Name != coretask.EventCreatedName {
			continue
		}
		got, ok := event.Runtime.Payload.(coretask.Created)
		if !ok || got.TaskID != "task_1" {
			t.Fatalf("runtime payload = %#v, want task created", event.Runtime.Payload)
		}
		return
	}
	t.Fatalf("replayed events missing persisted task event: %#v", replayed)
}

func TestPublishRuntimeEventPersistsAndPublishesToThreadSubscribers(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	service := New(Config{ThreadStore: threadStore})
	info, err := service.OpenSession(ctx, OpenSessionRequest{ThreadID: "thread-task-live"})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	events, cancel, err := service.Subscribe(ctx, info.Thread.ID, clientapi.EventOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	payload := coretask.StepCompleted{TaskID: "task_1", ExecutionID: "exec_1", StepID: "step_1"}
	if err := service.PublishRuntimeEvent(ctx, info.Thread, "run-task", payload); err != nil {
		t.Fatalf("PublishRuntimeEvent: %v", err)
	}
	select {
	case event := <-events:
		if event.Kind != clientapi.EventRuntimeEmitted || event.Runtime == nil || event.Runtime.Name != coretask.EventStepCompletedName {
			t.Fatalf("event = %#v, want task runtime event", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published task runtime event")
	}
	replayed, err := service.replayEvents(ctx, info.Thread.ID, clientapi.EventOptions{Replay: true})
	if err != nil {
		t.Fatalf("replayEvents: %v", err)
	}
	for _, event := range replayed {
		if event.Kind == clientapi.EventRuntimeEmitted && event.Runtime != nil && event.Runtime.Name == coretask.EventStepCompletedName {
			return
		}
	}
	t.Fatalf("replayed events missing published task runtime event: %#v", replayed)
}

func TestRuntimeEventSinkPublishesRunFailureOnRuntimeEventAppendError(t *testing.T) {
	ctx := context.Background()
	baseStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	threadStore := &appendFaultThreadStore{
		Store:    baseStore,
		failures: []error{fmt.Errorf("append failed")},
	}
	service := New(Config{ThreadStore: threadStore})
	info, err := service.OpenSession(ctx, OpenSessionRequest{ThreadID: "thread-runtime"})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	events, cancel, err := service.Subscribe(ctx, info.Thread.ID, clientapi.EventOptions{Buffer: 1})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()

	service.runtimeEventSink(ctx, info, "run-runtime").Emit(testPlanRuntimeEvent{Value: "fail me"})

	select {
	case event := <-events:
		if event.Kind != clientapi.EventRunFailed {
			t.Fatalf("event kind = %s, want %s", event.Kind, clientapi.EventRunFailed)
		}
		if event.RunID != "run-runtime" {
			t.Fatalf("run id = %q, want run-runtime", event.RunID)
		}
		if event.Error == nil || !strings.Contains(event.Error.Error(), "append failed") {
			t.Fatalf("event error = %v, want append failed", event.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run failure")
	}
}

func TestHandleSessionInboundReturnsRuntimeEventAppendError(t *testing.T) {
	ctx := context.Background()
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: operation.Ref{Name: "emit_plan"}}, func(ctx operation.Context, _ operation.Value) operation.Result {
		ctx.Events().Emit(testPlanRuntimeEvent{Value: "fail command"})
		return operation.OK("ok")
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"emit_plan"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "emit_plan"},
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	baseStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	threadStore := &appendFaultThreadStore{
		Store:    baseStore,
		failures: []error{fmt.Errorf("append failed")},
		failWhen: appendContainsRuntimeEmitted,
	}
	service := New(Config{
		Commands:          commands,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
	})
	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-runtime-error"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	events, cancel, err := service.Subscribe(ctx, info.Thread.ID, clientapi.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()

	_, err = service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:           "run-runtime-error",
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-runtime-error"},
		Caller:       policy.Caller{Kind: policy.CallerUser},
		Trust:        policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:         channel.InboundCommand,
		Command:      &command.Invocation{Path: command.Path{"emit_plan"}},
	})
	if err == nil || !strings.Contains(err.Error(), "persist runtime event") {
		t.Fatalf("HandleSessionInbound error = %v, want runtime persistence error", err)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind == clientapi.EventRunFailed {
				if event.Error == nil || !strings.Contains(event.Error.Error(), "append failed") {
					t.Fatalf("run failure error = %v, want append failed", event.Error)
				}
				return
			}
			if event.Kind == clientapi.EventRunCompleted {
				t.Fatal("received run.completed after runtime persistence failure")
			}
		case <-deadline:
			t.Fatal("timed out waiting for run failure")
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

func TestSubscribeDoesNotDropBurstEvents(t *testing.T) {
	ctx := context.Background()
	service, _ := testService(t)
	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	events, cancel, err := service.Subscribe(ctx, info.Thread.ID, clientapi.EventOptions{Buffer: 1})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()

	const total = 64
	published := make(chan struct{})
	go func() {
		defer close(published)
		for i := 0; i < total; i++ {
			service.publish(info.Thread.ID, clientapi.Event{
				Kind:  clientapi.EventRuntimeEmitted,
				RunID: clientapi.RunID("run"),
			})
		}
	}()

	deadline := time.After(time.Second)
	for i := 0; i < total; i++ {
		select {
		case _, ok := <-events:
			if !ok {
				t.Fatalf("events closed after %d events, want %d", i, total)
			}
		case <-deadline:
			t.Fatalf("timed out after %d events, want %d", i, total)
		}
	}
	select {
	case <-published:
	case <-deadline:
		t.Fatal("publisher did not finish")
	}
}

func TestSubscriberCloseUnblocksBlockedSend(t *testing.T) {
	sub := newSubscriber(0, 0)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sub.send(clientapi.Event{Kind: clientapi.EventRuntimeEmitted})
		sub.send(clientapi.Event{Kind: clientapi.EventRunCompleted})
	}()

	select {
	case <-done:
		t.Fatal("send finished before subscriber close; want blocked send")
	case <-time.After(20 * time.Millisecond):
	}
	sub.close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not unblock sender")
	}
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

func TestEffectiveProfileRestrictsChildCommands(t *testing.T) {
	ctx := context.Background()
	service, _ := testService(t)
	if err := service.operations.Register(operation.New(operation.Spec{Ref: operation.Ref{Name: "secret"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})); err != nil {
		t.Fatalf("register secret operation: %v", err)
	}
	if err := service.commands.Register(command.Spec{
		Path: command.Path{"secret"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "secret"},
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
	}); err != nil {
		t.Fatalf("register secret command: %v", err)
	}
	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Profile: coresession.Spec{
			Name:     "worker",
			Commands: []command.Path{{"echo"}},
		},
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "child"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	allowed, err := service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:      "run-allowed",
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:    channel.InboundCommand,
		Command: &command.Invocation{Path: command.Path{"echo"}, Input: "ok"},
	})
	if err != nil {
		t.Fatalf("HandleSessionInbound allowed: %v", err)
	}
	if allowed.Command.Status != session.CommandStatusOK {
		t.Fatalf("allowed status = %s, error = %+v", allowed.Command.Status, allowed.Command.Error)
	}

	denied, err := service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:      "run-denied",
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:    channel.InboundCommand,
		Command: &command.Invocation{Path: command.Path{"secret"}, Input: "no"},
	})
	if err != nil {
		t.Fatalf("HandleSessionInbound denied: %v", err)
	}
	if denied.Command.Status != session.CommandStatusFailed || denied.Command.Error == nil || denied.Command.Error.Code != "command_not_found" {
		t.Fatalf("denied command = %#v, want command_not_found failure", denied.Command)
	}
}

func TestOpenSessionApproverIsUsedByChildSession(t *testing.T) {
	ctx := context.Background()
	// Register a side-effecting operation that requires approval and a command
	// that dispatches it.
	ops := operation.NewRegistry()
	ranOp := false
	if err := ops.Register(operation.New(operation.Spec{
		Ref: operation.Ref{Name: "risky"},
		Semantics: operation.Semantics{
			Risk:        operation.RiskHigh,
			Effects:     operation.EffectSet{operation.EffectNone},
			Idempotency: operation.IdempotencyIdempotent,
			Determinism: operation.DeterminismDeterministic,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		ranOp = true
		return operation.OK("ok")
	})); err != nil {
		t.Fatalf("register risky operation: %v", err)
	}
	cmds := command.NewRegistry()
	if err := cmds.Register(command.Spec{
		Path: command.Path{"risky"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "risky"},
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
	}); err != nil {
		t.Fatalf("register risky command: %v", err)
	}
	// Base executor has no approval gate — would block without an override.
	executor := operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
		AllowPure:      true,
		MaxCommandRisk: operation.RiskMedium, // RiskHigh exceeds this, normally denied
	}))
	service := New(Config{
		Commands:          cmds,
		Operations:        ops,
		OperationExecutor: executor,
	})
	// Open a session with AutoApprover — simulates what --yolo does for a child.
	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "approver-test"},
		Approver:     operationruntime.AutoApprover{},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	result, err := service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:      "run-risky",
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:    channel.InboundCommand,
		Command: &command.Invocation{Path: command.Path{"risky"}},
	})
	if err != nil {
		t.Fatalf("HandleSessionInbound: %v", err)
	}
	if result.Command.Status != session.CommandStatusOK {
		t.Fatalf("command status = %s error = %#v, want ok (approver should have approved)", result.Command.Status, result.Command.Error)
	}
	_ = ranOp // operation ran if approval succeeded
}

func TestHandleInboundContextCommandUsesAgentProvider(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	provider := harnessContextProvider{
		spec:   corecontext.ProviderSpec{Name: "docs"},
		blocks: []corecontext.Block{{ID: "docs/current", Content: "provider context"}},
	}
	service := New(Config{
		AgentProvider: harnessAgentProviderFunc(func(context.Context, coresession.Spec) (coreagent.Agent, error) {
			return harnessContextAgent{providers: []corecontext.Provider{provider}}, nil
		}),
		ThreadStore: threadStore,
	})
	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Session: coresession.Ref{Name: "coder"},
		Profile: coresession.Spec{
			Name:  "coder",
			Agent: coreagent.Ref{Name: "coder"},
		},
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-context"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	result, err := service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:           "run-context",
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-context"},
		Caller:       policy.Caller{Kind: policy.CallerUser},
		Trust:        policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:         channel.InboundCommand,
		Command:      &command.Invocation{Path: command.Path{"context"}},
	})
	if err != nil {
		t.Fatalf("HandleSessionInbound: %v", err)
	}
	if result.Command.Status != session.CommandStatusOK {
		t.Fatalf("status = %s, error = %#v", result.Command.Status, result.Command.Error)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || !strings.Contains(fmt.Sprint(result.Outbound.Message.Content), "provider context") {
		t.Fatalf("outbound = %#v, want provider context", result.Outbound)
	}
}

func TestHandleInboundPromptCommandUsesAgentProvider(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"review"},
		Target: invocation.Target{
			Kind:   invocation.TargetPrompt,
			Prompt: "Review {{ .Argument }}.",
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	var providerCalls int
	agentRuntime := &harnessPromptAgent{response: "reviewed"}
	service := New(Config{
		AgentProvider: harnessAgentProviderFunc(func(context.Context, coresession.Spec) (coreagent.Agent, error) {
			providerCalls++
			return agentRuntime, nil
		}),
		Commands:    commands,
		ThreadStore: threadStore,
	})
	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Session: coresession.Ref{Name: "coder"},
		Profile: coresession.Spec{
			Name:  "coder",
			Agent: coreagent.Ref{Name: "coder"},
		},
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-prompt"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	result, err := service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:           "run-prompt",
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-prompt"},
		Trust:        policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:         channel.InboundCommand,
		Command:      &command.Invocation{Path: command.Path{"review"}, Args: []string{"diff"}},
	})
	if err != nil {
		t.Fatalf("HandleSessionInbound: %v", err)
	}
	if providerCalls != 1 {
		t.Fatalf("agent provider calls = %d, want 1", providerCalls)
	}
	if result.Command.Status != session.CommandStatusOK {
		t.Fatalf("status = %s, error = %#v", result.Command.Status, result.Command.Error)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "reviewed" {
		t.Fatalf("outbound = %#v, want reviewed", result.Outbound)
	}
	if len(agentRuntime.inputs) != 1 || agentRuntime.inputs[0].Observations[0].Content != "Review diff." {
		t.Fatalf("agent inputs = %#v, want rendered prompt", agentRuntime.inputs)
	}
}

func TestHandleInboundCompactCommandUsesAgentProvider(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	service := New(Config{
		AgentProvider: harnessAgentProviderFunc(func(context.Context, coresession.Spec) (coreagent.Agent, error) {
			return harnessProviderAgent{provider: provider}, nil
		}),
		ThreadStore: threadStore,
	})
	info, err := service.OpenSession(ctx, OpenSessionRequest{
		Session: coresession.Ref{Name: "coder"},
		Profile: coresession.Spec{
			Name:  "coder",
			Agent: coreagent.Ref{Name: "coder"},
		},
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-compact"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := conversationruntime.Append(ctx, threadStore, info.Thread, "turn-1", provider, []coreconversation.Item{{
		Provider: provider,
		Kind:     coreconversation.ItemToolResult,
		CallID:   "call_1",
		Name:     "file_read",
		Content:  strings.Repeat("large tool result ", 40000),
	}}); err != nil {
		t.Fatalf("Append transcript: %v", err)
	}
	result, err := service.HandleSessionInbound(ctx, info, channel.Inbound{
		ID:           "run-compact",
		Channel:      channel.Ref{Name: "local"},
		Conversation: channel.ConversationRef{ID: "conv-compact"},
		Caller:       policy.Caller{Kind: policy.CallerUser},
		Trust:        policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Kind:         channel.InboundCommand,
		Command:      &command.Invocation{Path: command.Path{"compact"}},
	})
	if err != nil {
		t.Fatalf("HandleSessionInbound: %v", err)
	}
	if result.Command.Status != session.CommandStatusOK {
		t.Fatalf("status = %s, error = %#v", result.Command.Status, result.Command.Error)
	}
	snapshot, err := threadStore.Read(ctx, corethread.ReadParams{ID: info.Thread.ID})
	if err != nil {
		t.Fatalf("Read thread: %v", err)
	}
	var checkpoint coreconversation.CompactionStored
	for _, record := range snapshot.Events {
		if payload, ok := record.Event.Payload.(coreconversation.CompactionStored); ok {
			checkpoint = payload
		}
	}
	if checkpoint.Provider != provider {
		t.Fatalf("checkpoint provider = %#v, want %#v", checkpoint.Provider, provider)
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

type testPlanRuntimeEvent struct {
	Value string `json:"value,omitempty"`
}

func (testPlanRuntimeEvent) EventName() coreevent.Name { return "plan.test" }

type appendFaultThreadStore struct {
	corethread.Store

	mu          sync.Mutex
	failures    []error
	failWhen    func(...corethread.AppendRecord) bool
	appendCalls int
}

func (s *appendFaultThreadStore) Append(ctx context.Context, ref corethread.Ref, records ...corethread.AppendRecord) ([]corethread.Record, error) {
	s.mu.Lock()
	s.appendCalls++
	shouldFail := s.failWhen == nil || s.failWhen(records...)
	if shouldFail && len(s.failures) > 0 {
		err := s.failures[0]
		s.failures = s.failures[1:]
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	return s.Store.Append(ctx, ref, records...)
}

func appendContainsRuntimeEmitted(records ...corethread.AppendRecord) bool {
	for _, record := range records {
		if record.Event.Name == coresession.EventRuntimeEmitted {
			return true
		}
	}
	return false
}

type harnessAgentProviderFunc func(context.Context, coresession.Spec) (coreagent.Agent, error)

func (f harnessAgentProviderFunc) AgentForSession(ctx context.Context, spec coresession.Spec) (coreagent.Agent, error) {
	return f(ctx, spec)
}

type harnessContextAgent struct {
	providers []corecontext.Provider
}

func (a harnessContextAgent) Spec() coreagent.Spec {
	return coreagent.Spec{Name: "coder"}
}

func (a harnessContextAgent) Step(coreagent.Context, coreagent.StepInput) coreagent.StepResult {
	return coreagent.StepResult{Status: coreagent.StatusOK, Decision: coreagent.Decision{Kind: coreagent.DecisionWait}}
}

func (a harnessContextAgent) ContextProviders() []corecontext.Provider {
	return append([]corecontext.Provider(nil), a.providers...)
}

type harnessProviderAgent struct {
	provider coreconversation.ProviderIdentity
}

func (a harnessProviderAgent) Spec() coreagent.Spec {
	return coreagent.Spec{
		Name: "coder",
		Inference: coreagent.InferenceSpec{
			Model: a.provider.Model,
			Annotations: map[string]string{
				"provider": a.provider.Provider,
				"api":      a.provider.API,
				"family":   a.provider.Family,
			},
		},
	}
}

func (a harnessProviderAgent) Step(coreagent.Context, coreagent.StepInput) coreagent.StepResult {
	return coreagent.StepResult{Status: coreagent.StatusOK, Decision: coreagent.Decision{Kind: coreagent.DecisionWait}}
}

type harnessPromptAgent struct {
	response string
	inputs   []coreagent.StepInput
}

func (a *harnessPromptAgent) Spec() coreagent.Spec {
	return coreagent.Spec{Name: "coder"}
}

func (a *harnessPromptAgent) Step(_ coreagent.Context, input coreagent.StepInput) coreagent.StepResult {
	a.inputs = append(a.inputs, input)
	return coreagent.StepResult{
		Status: coreagent.StatusOK,
		Decision: coreagent.Decision{
			Kind:    coreagent.DecisionMessage,
			Message: &coreagent.Message{Content: a.response},
		},
	}
}

type harnessContextProvider struct {
	spec   corecontext.ProviderSpec
	blocks []corecontext.Block
}

func (p harnessContextProvider) Spec() corecontext.ProviderSpec { return p.spec }

func (p harnessContextProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	return append([]corecontext.Block(nil), p.blocks...), nil
}
