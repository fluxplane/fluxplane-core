package agentruntime_test

import (
	"context"
	"testing"
	"time"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

func TestServiceSendInputThroughTopLevelAPI(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	sessionHandle, err := svc.Open(ctx, agentruntime.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-input"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.SendInput(ctx, agentruntime.Input{Text: "hello"})
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
	assertRunEvent(t, run, agentruntime.EventInputCompleted)
}

func TestServiceSendCommandThroughTopLevelAPI(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	sessionHandle, err := svc.Open(ctx, agentruntime.OpenRequest{
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
	assertRunEvent(t, run, agentruntime.EventCommandCompleted)
}

func TestServiceListsAndResumesSessions(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	opened, err := svc.Open(ctx, agentruntime.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-resume"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	summaries, err := svc.ListSessions(ctx, agentruntime.ListSessionsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries len = %d, want 1", len(summaries))
	}
	if summaries[0].Info.Thread.ID != opened.Info().Thread.ID {
		t.Fatalf("listed thread = %q, want %q", summaries[0].Info.Thread.ID, opened.Info().Thread.ID)
	}

	resumed, err := svc.Resume(ctx, agentruntime.ResumeRequest{ThreadID: opened.Info().Thread.ID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Info().Thread.ID != opened.Info().Thread.ID {
		t.Fatalf("resumed thread = %q, want %q", resumed.Info().Thread.ID, opened.Info().Thread.ID)
	}
}

func assertRunEvent(t *testing.T, run agentruntime.Run, kind agentruntime.EventKind) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event, ok := <-run.Events():
			if !ok {
				t.Fatalf("run events closed before %s", kind)
			}
			if event.Kind == kind {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}

func newTestService(t *testing.T) *agentruntime.Service {
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

	svc, err := agentruntime.New(agentruntime.Config{
		Agent:      echoAgent{},
		Commands:   commands,
		Operations: ops,
		Channel:    channel.Ref{Name: "local"},
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
	return svc
}

type echoAgent struct{}

func (echoAgent) Spec() agent.Spec {
	return agent.Spec{Name: "echo-agent"}
}

func (echoAgent) Step(_ agent.Context, input agent.StepInput) agent.StepResult {
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
