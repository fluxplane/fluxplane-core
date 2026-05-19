package codershell

import (
	"context"
	"fmt"
	"testing"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/agent"
)

func TestDirectChannelClientCreatesSessionAndConnectionDescription(t *testing.T) {
	service, err := agentruntime.New(agentruntime.Config{})
	if err != nil {
		t.Fatalf("agentruntime.New() error = %v", err)
	}
	client := NewDirectChannelClient(DirectChannelClientOptions{Client: service})
	if got := client.ConnectionDescription(); got != "direct-channel" {
		t.Fatalf("ConnectionDescription() = %q", got)
	}
	info, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "/workspace"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if info.ID == "" {
		t.Fatal("CreateSession() ID is empty")
	}
	if info.CWD != "/workspace" {
		t.Fatalf("CreateSession() CWD = %q", info.CWD)
	}
}
func TestDirectChannelClientSubmitAskUsesChannelClient(t *testing.T) {
	service, err := agentruntime.New(agentruntime.Config{Agent: echoAgent{}})
	if err != nil {
		t.Fatalf("agentruntime.New() error = %v", err)
	}
	client := NewDirectChannelClient(DirectChannelClientOptions{Client: service})
	info, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "/workspace"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	events, err := client.SubmitAsk(context.Background(), info.ID, AskRequest{Text: "hello", CWD: "/workspace"})
	if err != nil {
		t.Fatalf("SubmitAsk() error = %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("events = %#v, want at least two", events)
	}
	if events[0].Kind != EventAskSubmitted {
		t.Fatalf("events[0].Kind = %q", events[0].Kind)
	}
	if events[1].Kind != EventAskOutput || events[1].Summary != "echo: hello" {
		t.Fatalf("events[1] = %#v", events[1])
	}
}

func TestParseSlashInvocation(t *testing.T) {
	inv, err := parseSlashInvocation("/echo 'hello world'")
	if err != nil {
		t.Fatalf("parseSlashInvocation() error = %v", err)
	}
	if inv.Path.String() != "/echo" {
		t.Fatalf("path = %q", inv.Path.String())
	}
	if len(inv.Args) != 1 || inv.Args[0] != "hello world" {
		t.Fatalf("args = %#v", inv.Args)
	}
}

func TestShellCommandInvocationTargetsRegisteredCoderCommand(t *testing.T) {
	inv, err := shellCommandInvocation("go test ./apps/coder", "/workspace")
	if err != nil {
		t.Fatalf("shellCommandInvocation() error = %v", err)
	}
	if inv.Path.String() != "/shell/exec" {
		t.Fatalf("path = %q, want /shell/exec", inv.Path.String())
	}
	input, ok := inv.Input.(map[string]any)
	if !ok {
		t.Fatalf("input = %#v, want map", inv.Input)
	}
	if input["command"] != "go" {
		t.Fatalf("command = %#v, want go", input["command"])
	}
	if input["workdir"] != "/workspace" {
		t.Fatalf("workdir = %#v, want /workspace", input["workdir"])
	}
	args, ok := input["args"].([]string)
	if !ok || len(args) != 2 || args[0] != "test" || args[1] != "./apps/coder" {
		t.Fatalf("args = %#v, want test ./apps/coder", input["args"])
	}
}

type echoAgent struct{}

func (echoAgent) Spec() agent.Spec { return agent.Spec{Name: "echo"} }

func (echoAgent) Step(_ agent.Context, input agent.StepInput) agent.StepResult {
	text := input.Goal
	if text == "" && len(input.Observations) > 0 {
		text = fmt.Sprint(input.Observations[0].Content)
	}
	return agent.StepResult{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "echo: " + text}}}
}

func TestShellObjectRecordsConnection(t *testing.T) {
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: NewFakeClient(), CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	active := shell.ActiveTab()
	if active == nil || len(active.Transcript) == 0 {
		t.Fatal("missing active transcript")
	}
	first := active.Transcript[0]
	if first.Kind != EventClientConnected {
		t.Fatalf("first event kind = %q", first.Kind)
	}
	if first.Data["connection"] != "fake" {
		t.Fatalf("connection data = %q", first.Data["connection"])
	}
}
