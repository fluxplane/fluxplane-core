package directchannel

import (
	"context"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/harness"
	"github.com/fluxplane/agentruntime/orchestration/session"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
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
	sessionEvents, cancel, err := sessionHandle.Events(ctx, clientapi.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	defer cancel()

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
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
	seenSubmission := false
	for event := range run.Events() {
		if event.Kind == clientapi.EventSubmissionReceived {
			seenSubmission = true
			if event.Submission == nil || event.Submission.Command == nil || event.Submission.Command.Input != "hello" {
				t.Fatalf("submission event = %#v", event)
			}
		}
		if event.Kind == clientapi.EventOutboundProduced {
			seenRunOutbound = true
		}
	}
	if !seenSubmission {
		t.Fatal("expected run submission event")
	}
	if !seenRunOutbound {
		t.Fatal("expected run outbound event")
	}
	deadline := time.After(time.Second)
	for {
		select {
		case published := <-sessionEvents:
			if published.Outbound == nil {
				continue
			}
			if published.Outbound.Message == nil || published.Outbound.Message.Content != "hello" {
				t.Fatalf("published = %#v", published)
			}
			goto sawSessionOutbound
		case <-deadline:
			t.Fatal("expected published outbound")
		}
	}
sawSessionOutbound:
}

func TestSessionSubmitInputReturnsRunHandle(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithText("ping"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Input == nil || result.Input.Status != session.InputStatusOK {
		t.Fatalf("input result = %#v", result.Input)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "pong" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
	seenInputCompleted := false
	seenSubmission := false
	for event := range run.Events() {
		if event.Kind == clientapi.EventSubmissionReceived {
			seenSubmission = true
			if event.Submission == nil || event.Submission.Input == nil || event.Submission.Input.Content != "ping" {
				t.Fatalf("submission event = %#v", event)
			}
		}
		if event.Kind == clientapi.EventInputCompleted {
			seenInputCompleted = true
		}
	}
	if !seenSubmission {
		t.Fatal("expected input submission event")
	}
	if !seenInputCompleted {
		t.Fatal("expected input completed event")
	}
}

func TestRunEventsForwardWhileInputIsExecuting(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{})
	release := make(chan struct{})
	client := testClientWithAgent(t, blockingAgent{started: started, release: release})
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithText("ping"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("agent did not start")
	}
	select {
	case event := <-run.Events():
		if event.Kind != clientapi.EventSubmissionReceived {
			t.Fatalf("first live event = %q, want submission.received", event.Kind)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected run event before input completed")
	}
	close(release)
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	for range run.Events() {
	}
}

func TestSessionEventsCanReplayFromThreadStore(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	events, cancel, err := sessionHandle.Events(ctx, clientapi.EventOptions{Buffer: 8, Replay: true})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	defer cancel()

	deadline := time.After(time.Second)
	seenSubmission := false
	for {
		select {
		case event := <-events:
			if event.Kind == clientapi.EventSubmissionReceived {
				seenSubmission = true
				if event.Submission == nil || event.Submission.Command == nil {
					t.Fatalf("replayed submission = %#v", event)
				}
				if event.Submission.Trust.Level != policy.TrustVerified {
					t.Fatalf("replayed trust = %#v, want verified", event.Submission.Trust)
				}
				continue
			}
			if event.Kind != clientapi.EventOutboundProduced {
				continue
			}
			if !seenSubmission {
				t.Fatal("expected replayed submission before outbound")
			}
			if !event.Replayed {
				t.Fatalf("event = %#v, want replayed", event)
			}
			if event.Cursor.Sequence == 0 {
				t.Fatalf("event cursor = %#v, want sequence", event.Cursor)
			}
			if event.Outbound == nil || event.Outbound.Message == nil || event.Outbound.Message.Content != "hello" {
				t.Fatalf("event outbound = %#v", event.Outbound)
			}
			return
		case <-deadline:
			t.Fatal("expected replayed outbound event")
		}
	}
}

func TestResumedSessionSubmitUsesResumedThread(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	opened, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-1"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	resumed, err := client.Resume(ctx, clientapi.ResumeRequest{ThreadID: opened.Info().Thread.ID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	run, err := resumed.Submit(ctx, clientapi.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Session.Thread.ID != opened.Info().Thread.ID {
		t.Fatalf("result thread = %q, want %q", result.Session.Thread.ID, opened.Info().Thread.ID)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
}

func TestClientReceivesLargeStreamingBurst(t *testing.T) {
	ctx := context.Background()
	const total = clientapi.DefaultRunEventBuffer + 64
	client := testClientWithAgent(t, directStreamingBurstAgent{count: total})
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-streaming-burst"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithText("hello"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	eventsDone := make(chan []clientapi.Event, 1)
	go func() {
		eventsDone <- drainRunEvents(run)
	}()
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	var events []clientapi.Event
	select {
	case events = <-eventsDone:
	case <-time.After(time.Second):
		t.Fatal("timed out draining run events")
	}
	if got := countDirectStreamContentDeltas(events); got != total {
		t.Fatalf("stream deltas = %d, want %d", got, total)
	}
	if len(events) == 0 || events[len(events)-1].Kind != clientapi.EventRunCompleted {
		t.Fatalf("last event = %#v, want run.completed", events[len(events)-1])
	}
}

func testClient(t *testing.T) *Client {
	t.Helper()
	return testClientWithAgent(t, fixedAgent{result: agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: "pong"},
		},
	}})
}

func testClientWithAgent(t *testing.T, agentRuntime agent.Agent) *Client {
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
		Agent:             agentRuntime,
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

type fixedAgent struct {
	result agent.StepResult
}

func (a fixedAgent) Spec() agent.Spec {
	return agent.Spec{Name: "fixed"}
}

func (a fixedAgent) Step(agent.Context, agent.StepInput) agent.StepResult {
	return a.result
}

type directStreamingBurstAgent struct {
	count int
}

func (a directStreamingBurstAgent) Spec() agent.Spec {
	return agent.Spec{Name: "streaming-burst"}
}

func (a directStreamingBurstAgent) Step(ctx agent.Context, input agent.StepInput) agent.StepResult {
	for i := 0; i < a.count; i++ {
		ctx.Events().Emit(llmagent.ModelStreamed{
			Agent: "streaming-burst",
			Model: "fake-model",
			Event: llmagent.StreamEvent{
				Kind: llmagent.StreamContentDelta,
				Text: "x",
			},
		})
	}
	return agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: "done"},
		},
	}
}

func drainRunEvents(run clientapi.RunHandle) []clientapi.Event {
	var events []clientapi.Event
	for event := range run.Events() {
		events = append(events, event)
	}
	return events
}

func countDirectStreamContentDeltas(events []clientapi.Event) int {
	var count int
	for _, event := range events {
		if event.Kind != clientapi.EventRuntimeEmitted || event.Runtime == nil {
			continue
		}
		streamed, ok := event.Runtime.Payload.(llmagent.ModelStreamed)
		if ok && streamed.Event.Kind == llmagent.StreamContentDelta {
			count++
		}
	}
	return count
}

type blockingAgent struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (a blockingAgent) Spec() agent.Spec {
	return agent.Spec{Name: "blocking"}
}

func (a blockingAgent) Step(agent.Context, agent.StepInput) agent.StepResult {
	close(a.started)
	<-a.release
	return agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: "pong"},
		},
	}
}
