package loop

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	coreoperation "github.com/fluxplane/fluxplane-core/core/operation"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-core/orchestration/sessioncontrol"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionrun"
)

func TestSessionCommandsContributesLoopHandler(t *testing.T) {
	bindings, err := (Plugin{}).SessionCommands(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("SessionCommands: %v", err)
	}
	if len(bindings) != 1 || bindings[0].Spec.Path.String() != "/loop" || bindings[0].Handler == nil {
		t.Fatalf("bindings = %#v, want loop handler", bindings)
	}
	if !strings.Contains(bindings[0].Spec.Annotations[command.CompletionFlagsAnnotation], "count") {
		t.Fatalf("completion flags = %q, want count", bindings[0].Spec.Annotations[command.CompletionFlagsAnnotation])
	}
}

func TestExecuteCommandRunsCountWithFreshConversations(t *testing.T) {
	client := &loopFakeClient{outputs: []string{"first output", "second output"}}
	s := session.Session{
		Profile:     coresession.Spec{Name: "assistant"},
		Thread:      corethread.Ref{ID: "parent-thread"},
		SessionRuns: sessionrun.New(sessionrun.Config{Client: client}),
	}
	result := ExecuteCommand(s, context.Background(), loopInbound("run-loop", []string{"do", "work"}, map[string]any{"count": 2}), CommandSpec(), sessioncontrol.PolicyEvaluation{})
	if result.Status != session.CommandStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(client.opens) != 2 {
		t.Fatalf("opens = %d, want 2", len(client.opens))
	}
	if client.opens[0].Conversation.ID != "run-loop:loop:1" || client.opens[1].Conversation.ID != "run-loop:loop:2" {
		t.Fatalf("conversations = %q/%q, want fresh loop ids", client.opens[0].Conversation.ID, client.opens[1].Conversation.ID)
	}
	if client.inputs[0] != "do work" || client.inputs[1] != "do work" {
		t.Fatalf("inputs = %#v, want repeated prompt", client.inputs)
	}
	rendered, ok := result.Output.(coreoperation.Rendered)
	if !ok {
		t.Fatalf("output = %T, want operation.Rendered", result.Output)
	}
	output, ok := rendered.Data.(Output)
	if !ok {
		t.Fatalf("rendered data = %T, want loop.Output", rendered.Data)
	}
	if output.Success != 2 || output.Failed != 0 {
		t.Fatalf("output counts = %#v, want 2 success", output)
	}
	if !strings.Contains(rendered.Text, "child-thread-1") || !strings.Contains(rendered.Text, "child-thread-2") {
		t.Fatalf("rendered text = %q, want child thread summaries", rendered.Text)
	}
}

func TestExecuteCommandRequiresCountOrForever(t *testing.T) {
	result := ExecuteCommand(session.Session{SessionRuns: sessionrun.New(sessionrun.Config{Client: &loopFakeClient{}})}, context.Background(), loopInbound("run-loop", []string{"do", "work"}, nil), CommandSpec(), sessioncontrol.PolicyEvaluation{})
	if result.Status != session.CommandStatusFailed || result.Error == nil || !strings.Contains(result.Error.Message, "either --count N or --forever") {
		t.Fatalf("result = %#v, want missing count failure", result)
	}
}

func TestExecuteCommandStopsOnFirstError(t *testing.T) {
	client := &loopFakeClient{failAt: map[int]error{1: errors.New("iteration failed")}}
	s := session.Session{
		Profile:     coresession.Spec{Name: "assistant"},
		Thread:      corethread.Ref{ID: "parent-thread"},
		SessionRuns: sessionrun.New(sessionrun.Config{Client: client}),
	}
	result := ExecuteCommand(s, context.Background(), loopInbound("run-loop", []string{"do", "work"}, map[string]any{"count": 3}), CommandSpec(), sessioncontrol.PolicyEvaluation{})
	if result.Status != session.CommandStatusFailed || result.Error == nil || !strings.Contains(result.Error.Message, "iteration 1 failed") {
		t.Fatalf("result = %#v, want iteration failure", result)
	}
	if len(client.opens) != 1 {
		t.Fatalf("opens = %d, want stop after first failure", len(client.opens))
	}
}

func TestExecuteCommandRequiresDelegationForDifferentSession(t *testing.T) {
	client := &loopFakeClient{}
	s := session.Session{
		Profile:     coresession.Spec{Name: "assistant"},
		SessionRuns: sessionrun.New(sessionrun.Config{Client: client}),
	}
	result := ExecuteCommand(s, context.Background(), loopInbound("run-loop", []string{"do", "work"}, map[string]any{"count": 1, "session": "worker"}), CommandSpec(), sessioncontrol.PolicyEvaluation{})
	if result.Status != session.CommandStatusFailed || result.Error == nil || !strings.Contains(result.Error.Message, "delegation policy") {
		t.Fatalf("result = %#v, want delegation failure", result)
	}
	if len(client.opens) != 0 {
		t.Fatalf("opens = %d, want no helper session opened", len(client.opens))
	}
}

func loopInbound(id string, args []string, input map[string]any) channel.Inbound {
	var commandInput any
	if len(input) > 0 {
		commandInput = input
	}
	return channel.Inbound{
		ID:   id,
		Kind: channel.InboundCommand,
		Command: &command.Invocation{
			Path:  command.Path{Command},
			Args:  append([]string(nil), args...),
			Input: commandInput,
		},
	}
}

type loopFakeClient struct {
	opens   []sessionrun.OpenRequest
	inputs  []string
	outputs []string
	failAt  map[int]error
}

func (c *loopFakeClient) Open(_ context.Context, req sessionrun.OpenRequest) (sessionrun.Session, error) {
	c.opens = append(c.opens, req)
	return loopFakeSession{client: c, index: len(c.opens)}, nil
}

type loopFakeSession struct {
	client *loopFakeClient
	index  int
}

func (s loopFakeSession) Info() sessionrun.SessionInfo {
	return sessionrun.SessionInfo{Thread: corethread.Ref{ID: corethread.ID("child-thread-" + strconv.Itoa(s.index))}}
}

func (s loopFakeSession) SendInput(_ context.Context, input sessionrun.Input) (sessionrun.Run, error) {
	s.client.inputs = append(s.client.inputs, input.Text)
	events := make(chan sessionrun.RunEvent)
	close(events)
	return loopFakeRun{client: s.client, index: s.index, events: events}, nil
}

type loopFakeRun struct {
	client *loopFakeClient
	index  int
	events <-chan sessionrun.RunEvent
}

func (r loopFakeRun) ID() string { return "child-run-" + strconv.Itoa(r.index) }

func (r loopFakeRun) Events() <-chan sessionrun.RunEvent { return r.events }

func (r loopFakeRun) Wait(context.Context) (sessionrun.RunResult, error) {
	if r.client.failAt != nil {
		if err := r.client.failAt[r.index]; err != nil {
			return sessionrun.RunResult{}, err
		}
	}
	index := r.index - 1
	if index >= 0 && index < len(r.client.outputs) {
		return sessionrun.RunResult{Text: r.client.outputs[index]}, nil
	}
	return sessionrun.RunResult{Text: "output"}, nil
}
