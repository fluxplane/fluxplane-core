package httpssechannel

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

func TestClientSendsInputThroughHTTPAndSSE(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-input"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.SendInput(ctx, clientapi.Input{Text: "hello"})
	if err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Input == nil || result.Input.Status != session.InputStatusOK {
		t.Fatalf("input result = %#v", result.Input)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "agent: hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
	assertRemoteRunEvent(t, run, clientapi.EventInputCompleted)
}

func TestClientSendsCommandThroughHTTPAndSSE(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-command"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.SendCommand(ctx, command.Invocation{
		Path:  command.Path{"echo"},
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Command == nil || result.Command.Status != session.CommandStatusOK {
		t.Fatalf("command result = %#v", result.Command)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
	assertRemoteRunEvent(t, run, clientapi.EventCommandCompleted)
}

func TestClientListsResumesAndReplaysSessionEvents(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-replay"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.SendCommand(ctx, command.Invocation{Path: command.Path{"echo"}, Input: "hello"})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	summaries, err := client.ListSessions(ctx, clientapi.ListSessionsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries len = %d, want 1", len(summaries))
	}

	resumed, err := client.Resume(ctx, clientapi.ResumeRequest{ThreadID: sessionHandle.Info().Thread.ID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	events, cancel, err := resumed.Events(ctx, clientapi.EventOptions{Buffer: 8, Replay: true})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	defer cancel()

	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind != clientapi.EventOutboundProduced {
				continue
			}
			if !event.Replayed {
				t.Fatalf("event = %#v, want replayed", event)
			}
			if event.Cursor.Sequence == 0 {
				t.Fatalf("cursor = %#v, want sequence", event.Cursor)
			}
			if event.Outbound == nil || event.Outbound.Message == nil || event.Outbound.Message.Content != "hello" {
				t.Fatalf("outbound = %#v", event.Outbound)
			}
			return
		case <-deadline:
			t.Fatal("expected replayed outbound event")
		}
	}
}

func assertRemoteRunEvent(t *testing.T, run clientapi.RunHandle, kind clientapi.EventKind) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event, ok := <-run.Events():
			if !ok {
				t.Fatalf("events closed before %s", kind)
			}
			if event.Kind == kind {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}

func testRemoteClient(t *testing.T) *Client {
	t.Helper()
	service := testRuntime(t)
	server, err := NewServer(ServerConfig{Client: service})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	client, err := NewClient(ClientConfig{BaseURL: httpServer.URL, HTTPClient: httpServer.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func testRuntime(t *testing.T) *agentruntime.Service {
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

	service, err := agentruntime.New(agentruntime.Config{
		Agent:      remoteEchoAgent{},
		Commands:   commands,
		Operations: ops,
		Channel:    channel.Ref{Name: "http"},
		Caller: policy.Caller{
			Kind: policy.CallerUser,
			Principal: policy.Principal{
				Kind: "user",
				ID:   "test-user",
			},
		},
		Trust: policy.Trust{
			Kind:  policy.TrustInvocation,
			Level: policy.TrustVerified,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}

type remoteEchoAgent struct{}

func (remoteEchoAgent) Spec() agent.Spec {
	return agent.Spec{Name: "remote-echo"}
}

func (remoteEchoAgent) Step(_ agent.Context, input agent.StepInput) agent.StepResult {
	var content any
	if len(input.Observations) > 0 {
		content = "agent: " + input.Observations[0].Content.(string)
	}
	return agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: content},
		},
	}
}
