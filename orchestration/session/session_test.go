package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	coretask "github.com/fluxplane/agentruntime/core/task"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/core/user"
	coreworkflow "github.com/fluxplane/agentruntime/core/workflow"
	"github.com/fluxplane/agentruntime/orchestration/resourcecatalog"
	"github.com/fluxplane/agentruntime/orchestration/sessionagent"
	"github.com/fluxplane/agentruntime/orchestration/sessioncontrol"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	conversationruntime "github.com/fluxplane/agentruntime/runtime/conversation"
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

func TestExecuteInboundCommandLeavesOversizedOperationResult(t *testing.T) {
	large := strings.Repeat("large command result ", 800)
	opRef := operation.Ref{Name: "echo"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"echo": input, "content": large})
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

	result := (Session{
		Commands:          commands,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
	}).ExecuteInboundCommand(context.Background(), channel.Inbound{
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
	if result.Effect == nil {
		t.Fatalf("effect = nil, want result")
	}
	if _, ok := toolResultReplacement(result.Effect.Result); ok {
		t.Fatalf("command operation result was replaced: %#v", result.Effect.Result)
	}
	output, ok := result.Effect.Result.Output.(map[string]any)
	if !ok || output["content"] != large {
		t.Fatalf("output = %#v, want original large command result", result.Effect.Result.Output)
	}
}

func TestExecuteInboundCommandDispatchesWorkflow(t *testing.T) {
	opRef := operation.Ref{Name: "echo"}
	op := operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"echo": input})
	})
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"feature"},
		Target: invocation.Target{
			Kind:     invocation.TargetWorkflow,
			Workflow: "feature",
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	workflowID := resource.ResourceID{Kind: "workflow", Origin: "project", Name: "feature"}
	operationID := resource.ResourceID{Kind: "operation", Origin: "project", Name: "echo"}
	index := resource.NewResourceIndex()
	index.Add(workflowID)
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(context.Background(), corethread.CreateParams{ID: "thread-workflow"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	var emitted []event.Event
	s := Session{
		Commands: commands,
		Resolver: resource.NewResolver(resource.ResolverConfig{Index: index}),
		WorkflowCatalog: resourcecatalog.WorkflowCatalog{
			workflowID.Address(): {
				ID: workflowID,
				Spec: coreworkflow.Spec{
					Name: "feature",
					Steps: []coreworkflow.Step{{
						ID:        "run",
						Operation: opRef,
					}},
				},
			},
		},
		OperationCatalog: OperationCatalog{
			operationID.Address(): {ID: operationID, Operation: op},
		},
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
		Thread:            corethread.Ref{ID: "thread-workflow"},
		Events: event.SinkFunc(func(payload event.Event) {
			emitted = append(emitted, payload)
		}),
	}
	result := s.ExecuteInboundCommand(context.Background(), channel.Inbound{
		ID:     "run-1",
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path:  command.Path{"feature"},
			Input: "hello",
		},
	})

	if result.Status != CommandStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if result.Output == nil {
		t.Fatalf("output is nil, want workflow output")
	}
	if !hasEvent(emitted, coreworkflow.EventCompletedName) {
		t.Fatalf("emitted events = %#v, want workflow completion", eventNames(emitted))
	}
	stored, err := threadStore.Read(context.Background(), corethread.ReadParams{ID: "thread-workflow"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if countEvent(stored.Events, coresession.EventOutboundProduced) != 1 {
		t.Fatalf("outbound event count = %d, want 1", countEvent(stored.Events, coresession.EventOutboundProduced))
	}
}

func TestNormalizeProviderModelRecognizesClaudeCodePrefix(t *testing.T) {
	provider, model := normalizeProviderModel("", "claudecode/claude-sonnet-4-6")
	if provider != "claudecode" || model != "claude-sonnet-4-6" {
		t.Fatalf("provider/model = %q/%q, want claudecode/claude-sonnet-4-6", provider, model)
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

func TestExecuteInboundCommandDispatchesPromptCommandAsInput(t *testing.T) {
	ctx := context.Background()
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"review"},
		Target: invocation.Target{
			Kind:   invocation.TargetPrompt,
			Prompt: "Review the requested change.",
		},
		Policy: policy.InvocationPolicy{AllowedCallers: []policy.CallerKind{policy.CallerUser}},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-prompt"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	runtimeAgent := &sequenceAgent{
		results: []agent.StepResult{{
			Status:   agent.StatusOK,
			Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "reviewed"}},
		}},
	}
	s := Session{
		Agent:       runtimeAgent,
		Commands:    commands,
		ThreadStore: threadStore,
		Thread:      corethread.Ref{ID: "thread-prompt"},
	}

	result := s.ExecuteInboundCommand(ctx, channel.Inbound{
		ID:     "cmd-1",
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path: command.Path{"review"},
			Args: []string{"CHANGELOG.md"},
		},
	})
	if result.Status != CommandStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if result.Output != "reviewed" {
		t.Fatalf("output = %#v, want reviewed", result.Output)
	}
	if len(runtimeAgent.inputs) != 1 {
		t.Fatalf("agent inputs len = %d, want 1", len(runtimeAgent.inputs))
	}
	observation := runtimeAgent.inputs[0].Observations[0]
	content, ok := observation.Content.(string)
	if !ok || !strings.Contains(content, "Review the requested change.") || !strings.Contains(content, "CHANGELOG.md") {
		t.Fatalf("prompt content = %#v, want prompt plus command args", observation.Content)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-prompt"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if countEvent(stored.Events, coresession.EventCommandReceived) != 1 {
		t.Fatalf("command received events = %d, want 1", countEvent(stored.Events, coresession.EventCommandReceived))
	}
	if countEvent(stored.Events, coresession.EventInputReceived) != 1 {
		t.Fatalf("input received events = %d, want 1", countEvent(stored.Events, coresession.EventInputReceived))
	}
	if countEvent(stored.Events, coresession.EventOutboundProduced) != 1 {
		t.Fatalf("outbound events = %d, want 1", countEvent(stored.Events, coresession.EventOutboundProduced))
	}
}

func TestExecuteInboundCommandDispatchesSessionTargetToSessionAgent(t *testing.T) {
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"task"},
		Target: invocation.Target{
			Kind:    invocation.TargetSession,
			Session: "task",
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	client := &sessionTargetClient{output: "created task"}
	var emitted []event.Event
	s := Session{
		Commands: commands,
		SessionAgents: sessionagent.New(sessionagent.Config{
			Client:      client,
			MaxParallel: 1,
		}),
		Events: event.SinkFunc(func(payload event.Event) {
			emitted = append(emitted, payload)
		}),
		Delegation: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "task"}},
		},
	}
	result := s.ExecuteInboundCommand(context.Background(), channel.Inbound{
		ID:     "run-task",
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path: command.Path{"task"},
			Args: []string{"review", "core/task"},
		},
	})
	if result.Status != CommandStatusOK || result.Output != "created task" {
		t.Fatalf("result = %#v, want session output", result)
	}
	if client.input != "review core/task" {
		t.Fatalf("child input = %q, want joined command args", client.input)
	}
	if !hasEvent(emitted, sessionagent.EventStarted) || !hasEvent(emitted, sessionagent.EventCompleted) {
		t.Fatalf("emitted events = %#v, want session-agent lifecycle", eventNames(emitted))
	}
	if hasEventPrefix(emitted, "subagent.") {
		t.Fatalf("emitted events = %#v, did not expect legacy delegation lifecycle", eventNames(emitted))
	}
}

func TestExecuteInboundCommandDispatchesPlanTargetToTaskPlannerProfile(t *testing.T) {
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"plan"},
		Target: invocation.Target{
			Kind:    invocation.TargetSession,
			Session: "task-planner",
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	client := &sessionTargetClient{output: "drafted task"}
	s := Session{
		Commands: commands,
		SessionAgents: sessionagent.New(sessionagent.Config{
			Client:      client,
			MaxParallel: 1,
		}),
		Delegation: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}, {Name: "explorer"}, {Name: "reviewer"}, {Name: "task"}, {Name: "task-planner"}},
		},
	}
	result := s.ExecuteInboundCommand(context.Background(), channel.Inbound{
		ID:     "run-plan",
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path: command.Path{"plan"},
			Args: []string{"create a demo golang http server in ./tmp/demo-app"},
		},
	})
	if result.Status != CommandStatusOK || result.Output != "drafted task" {
		t.Fatalf("result = %#v, want planner session output", result)
	}
	if client.input != "create a demo golang http server in ./tmp/demo-app" {
		t.Fatalf("child input = %q, want command args", client.input)
	}
}

func TestSessionTargetCreatedOutputFallsBackToTaskEvent(t *testing.T) {
	output := sessionTargetCreatedOutput([]coretask.Created{{
		TaskID: "task_1",
		Task: coretask.Task{
			Title:  "Find first name",
			Status: coretask.StatusReady,
		},
	}})
	if output != "Created task task_1: Find first name (status: ready)" {
		t.Fatalf("output = %q, want task creation summary", output)
	}
}

func TestExecuteInboundCommandRendersPromptCommandTemplate(t *testing.T) {
	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"release"},
		Target: invocation.Target{
			Kind:   invocation.TargetPrompt,
			Prompt: "Write notes for {{ .Argument }} with {{ .Input.tone }} tone.",
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	runtimeAgent := &sequenceAgent{
		results: []agent.StepResult{{
			Status:   agent.StatusOK,
			Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "done"}},
		}},
	}
	s := Session{Agent: runtimeAgent, Commands: commands}

	result := s.ExecuteInboundCommand(context.Background(), channel.Inbound{
		ID:    "cmd-template",
		Kind:  channel.InboundCommand,
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path:  command.Path{"release"},
			Args:  []string{"v1.2.3"},
			Input: map[string]any{"tone": "plain"},
		},
	})
	if result.Status != CommandStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	got := runtimeAgent.inputs[0].Observations[0].Content
	if got != "Write notes for v1.2.3 with plain tone." {
		t.Fatalf("prompt = %#v", got)
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

func TestExecuteInboundInputDispatchesMultipleAgentOperations(t *testing.T) {
	var emitted []event.Event
	first := operation.Ref{Name: "first"}
	second := operation.Ref{Name: "second"}
	ops := operation.NewRegistry()
	if err := ops.Register(
		operation.New(operation.Spec{Ref: first}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(map[string]any{"first": input})
		}),
		operation.New(operation.Spec{Ref: second}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(map[string]any{"second": input})
		}),
	); err != nil {
		t.Fatalf("register operations: %v", err)
	}
	s := Session{
		Agent: fixedAgent{
			result: agent.StepResult{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind: agent.DecisionOperation,
					Operations: []agent.OperationRequest{
						{Operation: first, Input: "a"},
						{Operation: second, Input: "b"},
					},
				},
			},
		},
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		Events: event.SinkFunc(func(evt event.Event) {
			emitted = append(emitted, evt)
		}),
	}

	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "run"},
	})

	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(result.Effects) != 2 {
		t.Fatalf("effects len = %d, want 2", len(result.Effects))
	}
	if result.Effect == nil || result.Effect.Result.Output.(map[string]any)["second"] != "b" {
		t.Fatalf("last effect = %#v, want second operation", result.Effect)
	}
	requested := requestedCallIDs(emitted)
	completed := completedCallIDs(emitted)
	if len(requested) != 2 || len(completed) != 2 {
		t.Fatalf("operation call ids requested=%v completed=%v", requested, completed)
	}
	if requested[0] == "" || requested[1] == "" || requested[0] == requested[1] {
		t.Fatalf("requested call ids = %v, want two distinct non-empty ids", requested)
	}
	if requested[0] != completed[0] || requested[1] != completed[1] {
		t.Fatalf("requested call ids = %v, completed call ids = %v", requested, completed)
	}
}

func TestExecuteInboundInputContinuesLLMAgentAfterOperation(t *testing.T) {
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{
			Name:   "llm",
			Driver: agent.DriverSpec{Kind: "llmagent"},
		},
		results: []agent.StepResult{
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:       agent.DecisionOperation,
					Operations: []agent.OperationRequest{{Operation: opRef, Input: "thing"}},
				},
			},
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:    agent.DecisionMessage,
					Message: &agent.Message{Content: "found thing"},
				},
			},
		},
	}
	s := Session{
		Agent:             agentRuntime,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
	}

	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "run"},
	})

	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(result.Effects) != 1 {
		t.Fatalf("effects len = %d, want 1", len(result.Effects))
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "found thing" {
		t.Fatalf("outbound = %#v, want final agent message", result.Outbound)
	}
	if len(agentRuntime.inputs) != 2 {
		t.Fatalf("agent steps = %d, want 2", len(agentRuntime.inputs))
	}
	if len(agentRuntime.inputs[1].Observations) != 2 {
		t.Fatalf("second observations len = %d, want user input and operation result", len(agentRuntime.inputs[1].Observations))
	}
	if got := agentRuntime.inputs[1].Observations[1].Kind; got != "operation.result" {
		t.Fatalf("second observation kind = %q, want operation.result", got)
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

func TestExecuteInboundInputProjectsThreadHistory(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-history"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{Name: "memory"},
		results: []agent.StepResult{
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:    agent.DecisionMessage,
					Message: &agent.Message{Content: "noted"},
				},
			},
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:    agent.DecisionMessage,
					Message: &agent.Message{Content: "Timo"},
				},
			},
		},
	}
	s := Session{
		Agent:       agentRuntime,
		ThreadStore: threadStore,
		Thread:      corethread.Ref{ID: "thread-history"},
	}

	first := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-1",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "my name is Timo"},
	})
	if first.Status != InputStatusOK {
		t.Fatalf("first status = %q, want ok: %#v", first.Status, first)
	}
	second := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-2",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "what is my name?"},
	})
	if second.Status != InputStatusOK {
		t.Fatalf("second status = %q, want ok: %#v", second.Status, second)
	}
	if len(agentRuntime.inputs) != 2 {
		t.Fatalf("agent inputs len = %d, want 2", len(agentRuntime.inputs))
	}
	if len(agentRuntime.inputs[0].Context) != 0 {
		t.Fatalf("first context = %#v, want none", agentRuntime.inputs[0].Context)
	}
	if len(agentRuntime.inputs[1].Context) != 1 {
		t.Fatalf("second context = %#v, want history block", agentRuntime.inputs[1].Context)
	}
	history := agentRuntime.inputs[1].Context[0].Content
	if !strings.Contains(history, "User: my name is Timo") || !strings.Contains(history, "Agent: noted") {
		t.Fatalf("history = %q, want prior user and agent messages", history)
	}
	if strings.Contains(history, "what is my name") {
		t.Fatalf("history = %q, should not duplicate current input", history)
	}
}

func TestExecuteInboundInputProjectsProviderTranscriptAcrossToolContinuation(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-transcript"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	provider := coreconversation.ProviderIdentity{
		Provider: "openai",
		API:      "openai.responses",
		Family:   "responses",
		Model:    "gpt-test",
	}
	var transcripts []coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		if req.Transcript == nil {
			t.Fatalf("transcript is nil")
		}
		transcripts = append(transcripts, *req.Transcript)
		if len(transcripts) == 1 {
			return llmagent.Response{
				Operations: []agent.OperationRequest{{Operation: opRef, Input: "A100", ProviderCallID: "call_1"}},
				Transcript: coreconversation.Transcript{
					Provider: provider,
					Items: []coreconversation.Item{
						{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "lookup A100"},
						{Provider: provider, Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "lookup", Native: json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"key\":\"A100\"}"}`)},
					},
				},
			}, nil
		}
		return llmagent.Response{
			Message: &agent.Message{Content: "found A100"},
			Transcript: coreconversation.Transcript{
				Provider: provider,
				Items: []coreconversation.Item{
					{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "found A100", Native: json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"found A100"}]}`)},
				},
			},
		}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{
		Agent:             runtimeAgent,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
		Thread:            corethread.Ref{ID: "thread-transcript"},
	}

	result := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-1",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "lookup A100"},
	})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(transcripts) != 2 {
		t.Fatalf("transcripts len = %d, want 2", len(transcripts))
	}
	if !hasTranscriptUserContent(transcripts[0].Items, "lookup A100") {
		t.Fatalf("first transcript = %#v, want current user input", transcripts[0])
	}
	if !hasToolResultCallID(transcripts[1].Items, "call_1") {
		t.Fatalf("second transcript items = %#v, want tool result for provider call", transcripts[1].Items)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-transcript"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if countEvent(stored.Events, coreconversation.EventItemsAppended) != 2 {
		t.Fatalf("stored events = %#v, want two transcript append events", stored.Events)
	}
}

func TestExecuteInboundInputReplacesOversizedToolResult(t *testing.T) {
	ctx := context.Background()
	opRef := operation.Ref{Name: "lookup"}
	large := strings.Repeat("large tool result ", 800)
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input, "content": large})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	provider := coreconversation.ProviderIdentity{
		Provider: "openai",
		API:      "openai.responses",
		Family:   "responses",
		Model:    "gpt-test",
	}
	var transcripts []coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		if req.Transcript == nil {
			t.Fatalf("transcript is nil")
		}
		transcripts = append(transcripts, *req.Transcript)
		if len(transcripts) == 1 {
			return llmagent.Response{
				Operations: []agent.OperationRequest{{Operation: opRef, Input: "A100", ProviderCallID: "call_1"}},
				Transcript: coreconversation.Transcript{
					Provider: provider,
					Items: []coreconversation.Item{
						{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "lookup A100"},
						{Provider: provider, Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "lookup"},
					},
				},
			}, nil
		}
		return llmagent.Response{Message: &agent.Message{Content: "done"}}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{
		Agent:             runtimeAgent,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
	}

	result := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-1",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "lookup A100"},
	})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(result.Effects) != 1 {
		t.Fatalf("effects len = %d, want 1", len(result.Effects))
	}
	replacement, ok := toolResultReplacement(result.Effects[0].Result)
	if !ok {
		t.Fatalf("result = %#v, want replacement", result.Effects[0].Result)
	}
	if !strings.HasPrefix(replacement.Path, os.TempDir()) {
		t.Fatalf("replacement path = %q, want under temp dir %q", replacement.Path, os.TempDir())
	}
	data, err := os.ReadFile(replacement.Path)
	if err != nil {
		t.Fatalf("read replacement: %v", err)
	}
	if !strings.Contains(string(data), large) {
		t.Fatalf("replacement file missing original result")
	}
	if len(transcripts) != 2 {
		t.Fatalf("transcripts len = %d, want 2", len(transcripts))
	}
	item, ok := toolResultByCallID(transcripts[1].Items, "call_1")
	if !ok {
		t.Fatalf("second transcript items = %#v, want tool result", transcripts[1].Items)
	}
	if item.Metadata["replaced"] != "true" || item.Metadata["replacement_path"] != replacement.Path {
		t.Fatalf("tool result metadata = %#v, want replacement metadata", item.Metadata)
	}
	if strings.Contains(valueText(item.Content), large) {
		t.Fatalf("tool result still contains original content")
	}
	if !strings.Contains(valueText(item.Content), replacement.Path) {
		t.Fatalf("tool result content = %#v, want replacement path", item.Content)
	}
}

func TestExecuteInboundInputRendersContextOnlyWhenChanged(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-context-diff"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	providerID := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	contextProvider := &scriptedContextProvider{
		spec: corecontext.ProviderSpec{Name: "docs"},
		blocks: []corecontext.Block{{
			ID:      "docs/current",
			Content: "project rules",
		}},
	}
	var transcripts []coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		if req.Transcript == nil {
			t.Fatalf("transcript is nil")
		}
		transcripts = append(transcripts, *req.Transcript)
		items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
		items = append(items, coreconversation.Item{Provider: providerID, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "ok"})
		return llmagent.Response{
			Message: &agent.Message{Content: "ok"},
			Transcript: coreconversation.Transcript{
				Provider: providerID,
				Items:    items,
			},
		}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
	}, model, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-context-diff"}}

	first := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "first"}})
	if first.Status != InputStatusOK {
		t.Fatalf("first status = %q: %#v", first.Status, first)
	}
	second := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-2", Kind: channel.InboundMessage, Message: &channel.Message{Content: "second"}})
	if second.Status != InputStatusOK {
		t.Fatalf("second status = %q: %#v", second.Status, second)
	}
	if len(transcripts) != 2 {
		t.Fatalf("transcripts len = %d, want 2", len(transcripts))
	}
	if len(transcripts[0].NewItems) != 2 ||
		!strings.Contains(valueText(transcripts[0].NewItems[0].Content), "agent.self") ||
		!strings.Contains(valueText(transcripts[0].NewItems[1].Content), "project rules") {
		t.Fatalf("first new items = %#v, want self system context plus context-prefixed user input", transcripts[0].NewItems)
	}
	if len(transcripts[1].NewItems) != 1 || strings.Contains(valueText(transcripts[1].NewItems[0].Content), "<system-context>") {
		t.Fatalf("second new items = %#v, want unchanged context omitted", transcripts[1].NewItems)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-context-diff"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if countEvent(stored.Events, corecontext.EventRenderCommitted) != 1 {
		t.Fatalf("context render commits = %d, want 1", countEvent(stored.Events, corecontext.EventRenderCommitted))
	}
}

func TestExecuteInboundInputRendersSystemPlacementAsSystemTranscriptItem(t *testing.T) {
	ctx := context.Background()
	contextProvider := &scriptedContextProvider{
		spec: corecontext.ProviderSpec{Name: "agents_md", DefaultPlacement: corecontext.PlacementSystem},
		blocks: []corecontext.Block{{
			ID:      "agents_md/root",
			Content: "AGENTS.md rules",
		}},
	}
	var got coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		if req.Transcript == nil {
			t.Fatalf("transcript is nil")
		}
		got = *req.Transcript
		items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
		items = append(items, coreconversation.Item{Kind: coreconversation.ItemOutput, Role: "assistant", Content: "ok"})
		return llmagent.Response{Message: &agent.Message{Content: "ok"}, Transcript: coreconversation.Transcript{Items: items}}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:   "coder",
		Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
	}, model, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent}
	result := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-system", Kind: channel.InboundMessage, Message: &channel.Message{Content: "work"}})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q: %#v", result.Status, result)
	}
	if len(got.NewItems) != 2 {
		t.Fatalf("new items = %#v, want system context plus user input", got.NewItems)
	}
	if got.NewItems[0].Role != "system" || !strings.Contains(valueText(got.NewItems[0].Content), "AGENTS.md rules") {
		t.Fatalf("first new item = %#v, want system context", got.NewItems[0])
	}
	if got.NewItems[1].Role != "user" || strings.Contains(valueText(got.NewItems[1].Content), "<system-context>") {
		t.Fatalf("second new item = %#v, want plain user input", got.NewItems[1])
	}
}

func TestExecuteInboundInputRendersDeveloperPlacementAsDeveloperTranscriptItem(t *testing.T) {
	ctx := context.Background()
	contextProvider := &scriptedContextProvider{
		spec: corecontext.ProviderSpec{Name: "developer_notes", DefaultPlacement: corecontext.PlacementDeveloper},
		blocks: []corecontext.Block{{
			ID:      "developer_notes/current",
			Content: "developer scoped rules",
		}},
	}
	var got coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		got = *req.Transcript
		return llmagent.Response{
			Message:    &agent.Message{Content: "ok"},
			Transcript: coreconversation.Transcript{Items: append([]coreconversation.Item(nil), req.Transcript.NewItems...)},
		}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, model, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	result := (Session{Agent: runtimeAgent}).ExecuteInboundInput(ctx, channel.Inbound{ID: "run-dev", Kind: channel.InboundMessage, Message: &channel.Message{Content: "work"}})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q: %#v", result.Status, result)
	}
	if len(got.NewItems) != 3 {
		t.Fatalf("new items = %#v, want self, developer context, and user input", got.NewItems)
	}
	if got.NewItems[0].Role != "system" || !strings.Contains(valueText(got.NewItems[0].Content), "agent.self") {
		t.Fatalf("first new item = %#v, want self system context", got.NewItems[0])
	}
	if got.NewItems[1].Role != "developer" || !strings.Contains(valueText(got.NewItems[1].Content), "developer scoped rules") {
		t.Fatalf("second new item = %#v, want developer context", got.NewItems[1])
	}
	if got.NewItems[2].Role != "user" || strings.Contains(valueText(got.NewItems[2].Content), "<system-context>") {
		t.Fatalf("third new item = %#v, want plain user input", got.NewItems[2])
	}
}

func TestExecuteInboundInputPersistsContextUpdatesAndRemovals(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-context-events"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	contextProvider := &scriptedContextProvider{
		spec:   corecontext.ProviderSpec{Name: "docs"},
		blocks: []corecontext.Block{{ID: "docs/current", Content: "rules v1"}},
	}
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
		items = append(items, coreconversation.Item{Kind: coreconversation.ItemOutput, Role: "assistant", Content: "ok"})
		return llmagent.Response{Message: &agent.Message{Content: "ok"}, Transcript: coreconversation.Transcript{Items: items}}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, model, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-context-events"}}
	if result := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "first"}}); result.Status != InputStatusOK {
		t.Fatalf("first status = %q: %#v", result.Status, result)
	}
	contextProvider.blocks[0].Content = "rules v2"
	if result := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-2", Kind: channel.InboundMessage, Message: &channel.Message{Content: "second"}}); result.Status != InputStatusOK {
		t.Fatalf("second status = %q: %#v", result.Status, result)
	}
	contextProvider.blocks = nil
	if result := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-3", Kind: channel.InboundMessage, Message: &channel.Message{Content: "third"}}); result.Status != InputStatusOK {
		t.Fatalf("third status = %q: %#v", result.Status, result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-context-events"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := countEvent(stored.Events, corecontext.EventBlockRecorded); got != 3 {
		t.Fatalf("block recorded events = %d, want self add plus docs add and update", got)
	}
	if got := countEvent(stored.Events, corecontext.EventBlockRemoved); got != 1 {
		t.Fatalf("block removed events = %d, want one removal", got)
	}
	if got := countEvent(stored.Events, corecontext.EventRenderCommitted); got != 3 {
		t.Fatalf("render committed events = %d, want 3 changed renders", got)
	}
}

func TestExecuteInboundInputDoesNotCommitContextWhenModelFails(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-context-fail"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	contextProvider := &scriptedContextProvider{
		spec:   corecontext.ProviderSpec{Name: "docs"},
		blocks: []corecontext.Block{{ID: "docs/current", Content: "rules"}},
	}
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, llmagent.ModelFunc(func(context.Context, llmagent.Request) (llmagent.Response, error) {
		return llmagent.Response{}, errors.New("model down")
	}), llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	result := (Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-context-fail"}}).ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "work"}})
	if result.Status != InputStatusFailed {
		t.Fatalf("status = %q, want failed: %#v", result.Status, result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-context-fail"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := countEvent(stored.Events, corecontext.EventRenderCommitted); got != 0 {
		t.Fatalf("render committed events = %d, want none", got)
	}
}

func TestExecuteInboundInputLoadsPriorContextRecordsAcrossSessionInstances(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-context-resume"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	contextProvider := &scriptedContextProvider{
		spec:   corecontext.ProviderSpec{Name: "docs"},
		blocks: []corecontext.Block{{ID: "docs/current", Content: "rules"}},
	}
	var transcripts []coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		transcripts = append(transcripts, *req.Transcript)
		items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
		items = append(items, coreconversation.Item{Kind: coreconversation.ItemOutput, Role: "assistant", Content: "ok"})
		return llmagent.Response{Message: &agent.Message{Content: "ok"}, Transcript: coreconversation.Transcript{Items: items}}, nil
	})
	firstAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, model, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new first agent: %v", err)
	}
	if result := (Session{Agent: firstAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-context-resume"}}).ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "first"}}); result.Status != InputStatusOK {
		t.Fatalf("first status = %q: %#v", result.Status, result)
	}
	secondAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, model, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new second agent: %v", err)
	}
	if result := (Session{Agent: secondAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-context-resume"}}).ExecuteInboundInput(ctx, channel.Inbound{ID: "run-2", Kind: channel.InboundMessage, Message: &channel.Message{Content: "second"}}); result.Status != InputStatusOK {
		t.Fatalf("second status = %q: %#v", result.Status, result)
	}
	if len(transcripts) != 2 {
		t.Fatalf("transcripts len = %d, want 2", len(transcripts))
	}
	if !strings.Contains(valueText(transcripts[0].NewItems[0].Content), "<system-context>") {
		t.Fatalf("first new items = %#v, want context", transcripts[0].NewItems)
	}
	if strings.Contains(valueText(transcripts[1].NewItems[0].Content), "<system-context>") {
		t.Fatalf("second new items = %#v, want prior records to suppress context", transcripts[1].NewItems)
	}
}

func TestExecuteInboundInputContextRecordsAreBranchScoped(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-context-branches"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	contextProvider := &scriptedContextProvider{
		spec:   corecontext.ProviderSpec{Name: "docs"},
		blocks: []corecontext.Block{{ID: "docs/current", Content: "main rules"}},
	}
	var mainTranscripts []coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		if req.Transcript != nil {
			mainTranscripts = append(mainTranscripts, *req.Transcript)
		}
		items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
		items = append(items, coreconversation.Item{Kind: coreconversation.ItemOutput, Role: "assistant", Content: "ok"})
		return llmagent.Response{Message: &agent.Message{Content: "ok"}, Transcript: coreconversation.Transcript{Items: items}}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, model, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	mainSession := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-context-branches", BranchID: corethread.MainBranch}}
	if result := mainSession.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-main-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "main"}}); result.Status != InputStatusOK {
		t.Fatalf("main first status = %q: %#v", result.Status, result)
	}
	if _, err := threadStore.Fork(ctx, corethread.ForkParams{ID: "thread-context-branches", FromBranchID: corethread.MainBranch, ToBranchID: "feature"}); err != nil {
		t.Fatalf("fork: %v", err)
	}
	contextProvider.blocks[0].Content = "feature rules"
	featureSession := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-context-branches", BranchID: "feature"}}
	if result := featureSession.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-feature", Kind: channel.InboundMessage, Message: &channel.Message{Content: "feature"}}); result.Status != InputStatusOK {
		t.Fatalf("feature status = %q: %#v", result.Status, result)
	}
	contextProvider.blocks[0].Content = "main rules"
	if result := mainSession.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-main-2", Kind: channel.InboundMessage, Message: &channel.Message{Content: "main again"}}); result.Status != InputStatusOK {
		t.Fatalf("main second status = %q: %#v", result.Status, result)
	}
	if len(mainTranscripts) != 3 {
		t.Fatalf("transcripts len = %d, want 3", len(mainTranscripts))
	}
	if strings.Contains(valueText(mainTranscripts[2].NewItems[0].Content), "<system-context>") {
		t.Fatalf("main second new items = %#v, want feature branch context not to leak", mainTranscripts[2].NewItems)
	}
}

func TestExecuteInboundInputToolFollowupContextUsesUpdatedObservations(t *testing.T) {
	ctx := context.Background()
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	var detected []int
	contextProvider := &scriptedContextProvider{
		spec: corecontext.ProviderSpec{Name: "detect"},
		build: func(ctx context.Context, _ corecontext.Request) ([]corecontext.Block, error) {
			input, _ := coredatasource.DetectionInputFromContext(ctx)
			detected = append(detected, len(input.Sources))
			return []corecontext.Block{{ID: "detect/current", Content: fmt.Sprintf("sources:%d", len(input.Sources))}}, nil
		},
	}
	calls := 0
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		calls++
		if calls == 1 {
			return llmagent.Response{
				Operations: []agent.OperationRequest{{Operation: opRef, Input: "A100", ProviderCallID: "call_1"}},
				Transcript: coreconversation.Transcript{Items: append([]coreconversation.Item(nil), req.Transcript.NewItems...)},
			}, nil
		}
		return llmagent.Response{
			Message:    &agent.Message{Content: "done"},
			Transcript: coreconversation.Transcript{Items: append([]coreconversation.Item(nil), req.Transcript.NewItems...)},
		}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}, Turns: agent.TurnPolicy{MaxSteps: 2}}, model, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	result := (Session{Agent: runtimeAgent, Operations: ops, OperationExecutor: operationruntime.NewExecutor()}).ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "lookup A100"}})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q: %#v", result.Status, result)
	}
	if len(detected) != 2 {
		t.Fatalf("detected calls = %v, want two context builds", detected)
	}
	if detected[1] <= detected[0] {
		t.Fatalf("detected sources = %v, want tool followup to include updated observations", detected)
	}
}

func TestExecuteInboundCommandContextPreviewIsDryRun(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-context-command"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	contextProvider := &scriptedContextProvider{
		spec:   corecontext.ProviderSpec{Name: "docs"},
		blocks: []corecontext.Block{{ID: "docs/current", Content: "rules"}},
	}
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, llmagent.StaticModel{Response: llmagent.MessageResponse("ok")}, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-context-command"}}
	inbound := channel.Inbound{
		ID:      "cmd-1",
		Kind:    channel.InboundCommand,
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{Path: command.Path{"context"}},
	}
	first := s.ExecuteInboundCommand(ctx, inbound)
	if first.Status != CommandStatusOK || first.Effect == nil || !strings.Contains(fmt.Sprint(first.Effect.Result.Output), "<system-context>") {
		t.Fatalf("first context command = %#v, want rendered context", first)
	}
	second := s.ExecuteInboundCommand(ctx, inbound)
	if second.Status != CommandStatusOK || second.Effect == nil || !strings.Contains(fmt.Sprint(second.Effect.Result.Output), "<system-context>") {
		t.Fatalf("second context command = %#v, want dry-run to render again", second)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-context-command"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := countEvent(stored.Events, corecontext.EventRenderCommitted); got != 0 {
		t.Fatalf("render committed events = %d, want none for dry-run", got)
	}
}

func TestExecuteInboundCommandContextPreviewPassesIdentityScope(t *testing.T) {
	ctx := context.Background()
	var gotScope map[string]string
	contextProvider := &scriptedContextProvider{
		spec: corecontext.ProviderSpec{Name: "identity.current"},
		build: func(_ context.Context, req corecontext.Request) ([]corecontext.Block, error) {
			gotScope = req.Scope
			if req.Scope["user.id"] == "" {
				return nil, nil
			}
			return []corecontext.Block{{
				ID:        "identity.current",
				Provider:  "identity.current",
				Placement: corecontext.PlacementSystem,
				Content:   "Current user: " + req.Scope["user.id"],
			}}, nil
		},
	}
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, llmagent.StaticModel{Response: llmagent.MessageResponse("ok")}, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, Thread: corethread.Ref{ID: "thread-context-identity"}}
	inbound := channel.Inbound{
		ID:     "cmd-identity",
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "timo@localhost"}, Source: "local"},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
		Actor: &user.Actor{
			User:       user.User{ID: "timo@localhost", Username: "timo@localhost"},
			Identity:   user.Identity{Provider: "local", ProviderID: "timo"},
			Resolution: user.ResolutionResolved,
		},
		Command: &command.Invocation{Path: command.Path{"context"}, Input: map[string]any{"fresh": true, "key": "identity.current"}},
	}
	result := s.ExecuteInboundCommand(ctx, inbound)
	if result.Status != CommandStatusOK || result.Effect == nil {
		t.Fatalf("context command = %#v, want ok", result)
	}
	output := fmt.Sprint(result.Effect.Result.Output)
	if !strings.Contains(output, "Current user: timo@localhost") {
		t.Fatalf("output = %q, want current user context", output)
	}
	if gotScope["user.id"] != "timo@localhost" || gotScope["user.resolution"] != "resolved" || gotScope["trust.level"] != "privileged" {
		t.Fatalf("scope = %#v, want identity scope", gotScope)
	}
}

func TestExecuteInboundCommandWhoamiShowsResolvedIdentity(t *testing.T) {
	ctx := context.Background()
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, llmagent.StaticModel{Response: llmagent.MessageResponse("ok")})
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	result := (Session{Agent: runtimeAgent}).ExecuteInboundCommand(ctx, channel.Inbound{
		ID:     "cmd-whoami",
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "slack_user", ID: "U123"}, Source: "slack:main"},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Actor: &user.Actor{
			User:       user.User{ID: "timo@company.org", Username: "timo@company.org", Groups: []user.ID{"admins"}},
			Identity:   user.Identity{Provider: "slack", ProviderID: "U123"},
			Groups:     []user.Group{{ID: "admins", Trust: user.TrustOperator}},
			Resolution: user.ResolutionResolved,
		},
		Command: &command.Invocation{Path: command.Path{"whoami"}},
	})
	if result.Status != CommandStatusOK || result.Effect == nil {
		t.Fatalf("whoami command = %#v, want ok", result)
	}
	output := fmt.Sprint(result.Effect.Result.Output)
	for _, want := range []string{"user: timo@company.org", "identity: slack:U123", "groups: admins", "trust: verified", "user:timo@company.org", "group:admins", "agent:coder"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %q", output, want)
		}
	}
}

func TestExecuteInboundInputPassesResolvedIdentityToContextProviders(t *testing.T) {
	ctx := context.Background()
	var gotScope map[string]string
	contextProvider := &scriptedContextProvider{
		spec: corecontext.ProviderSpec{Name: "identity"},
		build: func(_ context.Context, req corecontext.Request) ([]corecontext.Block, error) {
			gotScope = req.Scope
			return []corecontext.Block{{ID: "identity", Content: "identity"}}, nil
		},
	}
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, llmagent.StaticModel{Response: llmagent.MessageResponse("ok")}, llmagent.WithContextProviders(contextProvider))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	result := (Session{Agent: runtimeAgent}).ExecuteInboundInput(ctx, channel.Inbound{
		ID:     "run-identity",
		Kind:   channel.InboundMessage,
		Caller: policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "slack_user", ID: "U123"}},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Actor: &user.Actor{
			User:       user.User{ID: "timo@company.org", Username: "timo@company.org", Identities: []user.Identity{{Provider: "gitlab/main", ProviderID: "tfriedl"}}},
			Identity:   user.Identity{Provider: "slack", ProviderID: "U123"},
			Identities: []user.Identity{{Provider: "slack", ProviderID: "U123"}, {Provider: "gitlab/main", ProviderID: "tfriedl"}},
			Resolution: user.ResolutionResolved,
		},
		Message: &channel.Message{Content: "hello"},
	})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if gotScope["user.id"] != "timo@company.org" || gotScope["identity.provider"] != "slack" || gotScope["trust.level"] != "verified" || gotScope["user.resolution"] != "resolved" {
		t.Fatalf("scope = %#v, want resolved user, identity, and trust", gotScope)
	}
	if gotScope["identity.all"] != "slack:U123;gitlab/main:tfriedl" {
		t.Fatalf("identity.all = %q, want Slack and GitLab identities", gotScope["identity.all"])
	}
}

func TestInputObservationMetadataUsesSafeIdentityScalars(t *testing.T) {
	metadata := inputObservationMetadata(channel.Inbound{
		Channel:      channel.Ref{Name: "slack-main"},
		Conversation: channel.ConversationRef{ID: "C123/T123"},
		Caller: policy.Caller{
			Kind:      policy.CallerUser,
			Principal: policy.Principal{Kind: "slack_user", ID: "U123"},
			Source:    "slack:slack-main",
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustUntrusted},
		Actor: &user.Actor{
			User:       user.User{ID: "slack_user:U123", Username: "slack_user:U123"},
			Identity:   user.Identity{Provider: "slack_user", ProviderID: "U123", Claims: map[string]string{"is_admin": "true"}},
			Resolution: user.ResolutionUnresolved,
		},
		Message: &channel.Message{
			Content:  "hello",
			Metadata: map[string]any{"is_admin": true},
		},
	})
	for _, key := range []string{"channel", "conversation", "caller.kind", "caller.principal.kind", "caller.principal.id", "caller.source", "trust.level", "trust.kind", "user.resolution", "user.id", "user.username", "identity.provider", "identity.provider_id", "identity.all"} {
		if metadata[key] == nil {
			t.Fatalf("metadata = %#v, want key %q", metadata, key)
		}
	}
	for _, key := range []string{"caller", "trust", "user", "identity", "groups", "is_admin"} {
		if _, ok := metadata[key]; ok {
			t.Fatalf("metadata = %#v, want no raw key %q", metadata, key)
		}
	}
}

func TestExecuteInboundCommandContextPreviewFreshAndKey(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-context-command-key"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	docs := &scriptedContextProvider{spec: corecontext.ProviderSpec{Name: "docs"}, blocks: []corecontext.Block{{ID: "docs/current", Content: "docs rules"}}}
	skills := &scriptedContextProvider{spec: corecontext.ProviderSpec{Name: "skills"}, blocks: []corecontext.Block{{ID: "skills/current", Content: "skill rules"}}}
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		return llmagent.Response{Message: &agent.Message{Content: "ok"}, Transcript: coreconversation.Transcript{Items: append([]coreconversation.Item(nil), req.Transcript.NewItems...)}}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{Name: "coder", Driver: agent.DriverSpec{Kind: llmagent.DriverKind}}, model, llmagent.WithContextProviders(docs, skills))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-context-command-key"}}
	if result := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "commit"}}); result.Status != InputStatusOK {
		t.Fatalf("input status = %q: %#v", result.Status, result)
	}
	cmd := channel.Inbound{
		ID:      "cmd-1",
		Kind:    channel.InboundCommand,
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{Path: command.Path{"context"}, Input: map[string]any{"fresh": true, "key": "skills"}},
	}
	result := s.ExecuteInboundCommand(ctx, cmd)
	if result.Status != CommandStatusOK || result.Effect == nil {
		t.Fatalf("context command = %#v, want ok", result)
	}
	output := fmt.Sprint(result.Effect.Result.Output)
	if !strings.Contains(output, "skill rules") || strings.Contains(output, "docs rules") {
		t.Fatalf("output = %q, want only skills provider", output)
	}
	cmd.Command.Input = map[string]any{"key": "missing"}
	missing := s.ExecuteInboundCommand(ctx, cmd)
	if missing.Status != CommandStatusFailed || missing.Error == nil || missing.Error.Code != "context_provider_not_found" {
		t.Fatalf("missing provider result = %#v, want context_provider_not_found", missing)
	}
}

func TestExecuteInboundCommandCompactDryRunDoesNotPersistCheckpoint(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-compact-dry-run"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	s := Session{
		Agent:       compactTestAgent(t, provider),
		ThreadStore: threadStore,
		Thread:      corethread.Ref{ID: "thread-compact-dry-run"},
	}
	if err := s.appendConversation(ctx, "turn-1", provider, compactLargeToolResult(provider, "call_1")); err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	result := s.ExecuteInboundCommand(ctx, compactInbound("cmd-1", map[string]any{"dry-run": true}))
	if result.Status != CommandStatusOK || result.Effect == nil {
		t.Fatalf("compact dry-run = %#v, want ok", result)
	}
	output := fmt.Sprint(result.Effect.Result.Output)
	if !strings.Contains(output, "Compaction dry run") || !strings.Contains(output, "Checkpoint: would be persisted by /compact") {
		t.Fatalf("output = %q, want dry-run checkpoint report", output)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-compact-dry-run"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := countEvent(stored.Events, coreconversation.EventCompactionStored); got != 0 {
		t.Fatalf("compaction events = %d, want none for dry-run", got)
	}
}

func TestExecuteInboundCommandCompactPersistsCheckpoint(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-compact-run"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	s := Session{
		Agent:       compactTestAgent(t, provider),
		ThreadStore: threadStore,
		Thread:      corethread.Ref{ID: "thread-compact-run"},
	}
	if err := s.appendConversation(ctx, "turn-1", provider, compactLargeToolResult(provider, "call_1")); err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	result := s.ExecuteInboundCommand(ctx, compactInbound("cmd-1", nil))
	if result.Status != CommandStatusOK || result.Effect == nil {
		t.Fatalf("compact = %#v, want ok", result)
	}
	output := fmt.Sprint(result.Effect.Result.Output)
	if !strings.Contains(output, "Compaction complete") || !strings.Contains(output, "Checkpoint: persisted") {
		t.Fatalf("output = %q, want persisted checkpoint report", output)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-compact-run"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	checkpoints := compactionEvents(stored.Events)
	if len(checkpoints) != 1 {
		t.Fatalf("compaction events = %d, want 1", len(checkpoints))
	}
	checkpoint := checkpoints[0]
	if !checkpoint.Stats.CheckpointPersist || checkpoint.Stats.SummarizedItems == 0 {
		t.Fatalf("checkpoint stats = %#v, want persisted summarized checkpoint", checkpoint.Stats)
	}
	if !hasCompactedToolResult(checkpoint.Items) {
		t.Fatalf("checkpoint items = %#v, want compacted tool result", checkpoint.Items)
	}
}

func TestExecuteInboundCommandCompactAffectsNextReplay(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-compact-replay"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	var got coreconversation.Transcript
	model := sessionIdentifiedModel{
		identity: provider,
		complete: func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
			if req.Transcript == nil {
				t.Fatalf("transcript is nil")
			}
			got = *req.Transcript
			items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
			items = append(items, coreconversation.Item{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "ok"})
			return llmagent.Response{Message: &agent.Message{Content: "ok"}, Transcript: coreconversation.Transcript{Provider: provider, Items: items}}, nil
		},
	}
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-compact-replay"}}
	if err := s.appendConversation(ctx, "turn-1", provider, compactLargeToolResult(provider, "call_1")); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	if result := s.ExecuteInboundCommand(ctx, compactInbound("cmd-1", nil)); result.Status != CommandStatusOK {
		t.Fatalf("compact = %#v, want ok", result)
	}

	input := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "continue"}})
	if input.Status != InputStatusOK {
		t.Fatalf("input = %#v, want ok", input)
	}
	if len(got.Items) < 2 {
		t.Fatalf("transcript items = %#v, want compacted checkpoint plus pending", got.Items)
	}
	if !hasCompactedToolResult(got.Items) {
		t.Fatalf("transcript items = %#v, want compacted checkpoint item", got.Items)
	}
	for _, item := range got.Items {
		if strings.Contains(valueText(item.Content), "large tool result") {
			t.Fatalf("item still contains original large result: %#v", item)
		}
	}
	if !hasTranscriptUserContent(got.Items, "continue") {
		t.Fatalf("transcript items = %#v, want pending user prompt", got.Items)
	}
}

func TestExecuteInboundInputAutoCompactsAfterSuccessfulTurn(t *testing.T) {
	ctx := context.Background()
	threadStore, ref := compactThread(t, ctx, "thread-auto-compact")
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	s := Session{
		Agent:       autoCompactAgent(t, provider, "16000", strings.Repeat("large assistant output ", 4000)),
		ThreadStore: threadStore,
		Thread:      ref,
	}

	result := s.ExecuteInboundInput(ctx, compactMessageInbound("run-1", "hello"))

	if result.Status != InputStatusOK {
		t.Fatalf("input = %#v, want ok", result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	checkpoints := compactionEvents(stored.Events)
	if len(checkpoints) != 1 {
		t.Fatalf("compaction events = %d, want 1", len(checkpoints))
	}
	if !checkpoints[0].Stats.CheckpointPersist || checkpoints[0].Stats.SummarizedItems == 0 {
		t.Fatalf("checkpoint stats = %#v, want persisted summarized checkpoint", checkpoints[0].Stats)
	}
}

func TestExecuteInboundInputAutoCompactionSkipsBelowThreshold(t *testing.T) {
	ctx := context.Background()
	threadStore, ref := compactThread(t, ctx, "thread-auto-compact-skip")
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	s := Session{
		Agent:       autoCompactAgent(t, provider, "16000", "small response"),
		ThreadStore: threadStore,
		Thread:      ref,
	}

	result := s.ExecuteInboundInput(ctx, compactMessageInbound("run-1", "hello"))

	if result.Status != InputStatusOK {
		t.Fatalf("input = %#v, want ok", result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := len(compactionEvents(stored.Events)); got != 0 {
		t.Fatalf("compaction events = %d, want none", got)
	}
}

func TestExecuteInboundInputAutoCompactionUsesTriggerBoundary(t *testing.T) {
	ctx := context.Background()
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}

	t.Run("at threshold", func(t *testing.T) {
		threadStore, ref := compactThread(t, ctx, "thread-auto-compact-boundary-at")
		s := Session{
			Agent:       autoCompactAgent(t, provider, "16000", ""),
			ThreadStore: threadStore,
			Thread:      ref,
		}
		_, budget := s.compactOptionsAndBudget()
		item := autoCompactAssistantOutput(provider, "at-threshold", exactTokenContent(budget.TriggerTokens))
		if tokens := conversationruntime.EstimateItemsTokens([]coreconversation.Item{item}); tokens != budget.TriggerTokens {
			t.Fatalf("estimated tokens = %d, want trigger %d", tokens, budget.TriggerTokens)
		}
		if err := s.appendConversation(ctx, "turn-1", provider, []coreconversation.Item{item}); err != nil {
			t.Fatalf("append transcript: %v", err)
		}

		if err := s.autoCompactAfterTurn(ctx, "turn-1"); err != nil {
			t.Fatalf("auto compact: %v", err)
		}
		stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: ref.ID})
		if err != nil {
			t.Fatalf("read thread: %v", err)
		}
		if got := len(compactionEvents(stored.Events)); got != 0 {
			t.Fatalf("compaction events = %d, want none at trigger threshold", got)
		}
	})

	t.Run("above threshold", func(t *testing.T) {
		threadStore, ref := compactThread(t, ctx, "thread-auto-compact-boundary-over")
		s := Session{
			Agent:       autoCompactAgent(t, provider, "16000", ""),
			ThreadStore: threadStore,
			Thread:      ref,
		}
		_, budget := s.compactOptionsAndBudget()
		item := autoCompactAssistantOutput(provider, "over-threshold", exactTokenContent(budget.TriggerTokens+1))
		if tokens := conversationruntime.EstimateItemsTokens([]coreconversation.Item{item}); tokens != budget.TriggerTokens+1 {
			t.Fatalf("estimated tokens = %d, want trigger+1 %d", tokens, budget.TriggerTokens+1)
		}
		if err := s.appendConversation(ctx, "turn-1", provider, []coreconversation.Item{item}); err != nil {
			t.Fatalf("append transcript: %v", err)
		}

		if err := s.autoCompactAfterTurn(ctx, "turn-1"); err != nil {
			t.Fatalf("auto compact: %v", err)
		}
		stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: ref.ID})
		if err != nil {
			t.Fatalf("read thread: %v", err)
		}
		if got := len(compactionEvents(stored.Events)); got != 1 {
			t.Fatalf("compaction events = %d, want one above trigger threshold", got)
		}
	})
}

func TestExecuteInboundInputAutoCompactionCapsLargeModelContext(t *testing.T) {
	ctx := context.Background()
	threadStore, ref := compactThread(t, ctx, "thread-auto-compact-cap")
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	s := Session{
		Agent:       autoCompactAgent(t, provider, "1000000", strings.Repeat("large capped-model output ", 40000)),
		ThreadStore: threadStore,
		Thread:      ref,
	}

	result := s.ExecuteInboundInput(ctx, compactMessageInbound("run-1", "hello"))

	if result.Status != InputStatusOK {
		t.Fatalf("input = %#v, want ok", result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := len(compactionEvents(stored.Events)); got != 1 {
		t.Fatalf("compaction events = %d, want cap-triggered checkpoint", got)
	}
}

func TestExecuteInboundInputAutoCompactionUsesDefaultContextWhenMissingAnnotation(t *testing.T) {
	ctx := context.Background()
	threadStore, ref := compactThread(t, ctx, "thread-auto-compact-default-context")
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	s := Session{
		Agent:       autoCompactAgentWithOptions(t, provider, autoCompactAgentOptions{Output: exactTokenContent(30000)}),
		ThreadStore: threadStore,
		Thread:      ref,
	}

	result := s.ExecuteInboundInput(ctx, compactMessageInbound("run-1", "hello"))

	if result.Status != InputStatusOK {
		t.Fatalf("input = %#v, want ok", result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := len(compactionEvents(stored.Events)); got != 0 {
		t.Fatalf("compaction events = %d, want none with default context budget", got)
	}
}

func TestExecuteInboundInputAutoCompactionUsesOutputReserve(t *testing.T) {
	ctx := context.Background()
	threadStore, ref := compactThread(t, ctx, "thread-auto-compact-output-reserve")
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	s := Session{
		Agent: autoCompactAgentWithOptions(t, provider, autoCompactAgentOptions{
			ContextTokens:   "16000",
			MaxOutputTokens: 12000,
			Output:          exactTokenContent(5000),
		}),
		ThreadStore: threadStore,
		Thread:      ref,
	}
	_, budget := s.compactOptionsAndBudget()
	if budget.OutputReserve != 12000 || budget.TriggerTokens != compactLargeItemTokens {
		t.Fatalf("budget = %#v, want output reserve to reduce trigger to large item floor", budget)
	}

	result := s.ExecuteInboundInput(ctx, compactMessageInbound("run-1", "hello"))

	if result.Status != InputStatusOK {
		t.Fatalf("input = %#v, want ok", result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := len(compactionEvents(stored.Events)); got != 1 {
		t.Fatalf("compaction events = %d, want reserve-triggered checkpoint", got)
	}
}

func TestExecuteInboundInputAutoCompactionFailureDoesNotFailTurn(t *testing.T) {
	ctx := context.Background()
	base, ref := compactThread(t, ctx, "thread-auto-compact-fail")
	threadStore := failCompactionStore{Store: base}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	s := Session{
		Agent:       autoCompactAgent(t, provider, "16000", strings.Repeat("large assistant output ", 4000)),
		ThreadStore: threadStore,
		Thread:      ref,
	}

	result := s.ExecuteInboundInput(ctx, compactMessageInbound("run-1", "hello"))

	if result.Status != InputStatusOK {
		t.Fatalf("input = %#v, want ok despite compaction append failure", result)
	}
	stored, err := base.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := len(compactionEvents(stored.Events)); got != 0 {
		t.Fatalf("compaction events = %d, want failed append omitted", got)
	}
}

func TestExecuteInboundInputAutoCompactionSkipsFailedTurn(t *testing.T) {
	ctx := context.Background()
	threadStore, ref := compactThread(t, ctx, "thread-auto-compact-skip-failed")
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:   "coder",
		Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{
			Model: provider.Model,
			Annotations: map[string]string{
				"provider":           provider.Provider,
				"api":                provider.API,
				"family":             provider.Family,
				"llm.context_tokens": "16000",
			},
		},
	}, llmagent.ModelFunc(func(context.Context, llmagent.Request) (llmagent.Response, error) {
		return llmagent.Response{}, errors.New("model down")
	}))
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: ref}
	if err := s.appendConversation(ctx, "turn-0", provider, []coreconversation.Item{
		autoCompactAssistantOutput(provider, "preexisting", strings.Repeat("large prior output ", 4000)),
	}); err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	result := s.ExecuteInboundInput(ctx, compactMessageInbound("run-1", "hello"))

	if result.Status != InputStatusFailed {
		t.Fatalf("input = %#v, want failed", result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if got := len(compactionEvents(stored.Events)); got != 0 {
		t.Fatalf("compaction events = %d, want none after failed turn", got)
	}
}

func TestExecuteInboundInputAutoCompactionAffectsNextReplay(t *testing.T) {
	ctx := context.Background()
	threadStore, ref := compactThread(t, ctx, "thread-auto-compact-replay")
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	const original = "large replay assistant output "
	var got coreconversation.Transcript
	calls := 0
	model := sessionIdentifiedModel{
		identity: provider,
		complete: func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
			calls++
			items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
			if calls == 2 {
				got = *req.Transcript
				items = append(items, autoCompactAssistantOutput(provider, "small", "ok"))
			} else {
				items = append(items, autoCompactAssistantOutput(provider, "large", strings.Repeat(original, 4000)))
			}
			return llmagent.Response{
				Message:    &agent.Message{Content: "ok"},
				Transcript: coreconversation.Transcript{Provider: provider, Items: items},
			}, nil
		},
	}
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:   "coder",
		Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{
			Model: provider.Model,
			Annotations: map[string]string{
				"provider":           provider.Provider,
				"api":                provider.API,
				"family":             provider.Family,
				"llm.context_tokens": "16000",
			},
		},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: ref}

	first := s.ExecuteInboundInput(ctx, compactMessageInbound("run-1", "hello"))
	if first.Status != InputStatusOK {
		t.Fatalf("first input = %#v, want ok", first)
	}
	second := s.ExecuteInboundInput(ctx, compactMessageInbound("run-2", "continue"))
	if second.Status != InputStatusOK {
		t.Fatalf("second input = %#v, want ok", second)
	}
	if !hasCompactedAssistantOutput(got.Items) {
		t.Fatalf("transcript items = %#v, want compacted assistant checkpoint", got.Items)
	}
	for _, item := range got.Items {
		if strings.Contains(valueText(item.Content), original) {
			t.Fatalf("item still contains original large output: %#v", item)
		}
	}
	if !hasTranscriptUserContent(got.Items, "continue") {
		t.Fatalf("transcript items = %#v, want pending user prompt", got.Items)
	}
}

func TestExecuteInboundInputPersistsToolResultBeforeStepLimit(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-limit-repair"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	calls := 0
	model := llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
		calls++
		callID := "call_1"
		input := "A100"
		if calls == 2 {
			callID = "call_2"
			input = "A200"
		}
		return llmagent.Response{
			Operations: []agent.OperationRequest{{Operation: opRef, Input: input, ProviderCallID: callID}},
			Transcript: coreconversation.Transcript{
				Provider: provider,
				Items: []coreconversation.Item{
					{Provider: provider, Kind: coreconversation.ItemOutput, CallID: callID, Name: "lookup", Native: json.RawMessage(`{"type":"function_call","name":"lookup","arguments":"{}"}`)},
				},
			},
		}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
		Turns:     agent.TurnPolicy{MaxSteps: 2},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, Operations: ops, OperationExecutor: operationruntime.NewExecutor(), ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-limit-repair"}}

	result := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "lookup A100"}})
	if result.Status != InputStatusFailed {
		t.Fatalf("status = %q, want failed after step limit: %#v", result.Status, result)
	}
	if result.Error == nil || result.Error.Code != "step_limit_exceeded" {
		t.Fatalf("error = %#v, want step_limit_exceeded", result.Error)
	}
	if result.Error.Message != "inner loop reached turns.max_steps=2 model decision calls" {
		t.Fatalf("error message = %q, want inner max_steps detail", result.Error.Message)
	}
	if result.Error.Details["loop"] != "inner" || result.Error.Details["max_steps"] != 2 {
		t.Fatalf("error details = %#v, want inner max_steps detail", result.Error.Details)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-limit-repair"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if !threadHasToolResultCallID(stored.Events, "call_2") {
		t.Fatalf("stored events = %#v, want repaired tool result for call_2", stored.Events)
	}
}

func TestExecuteInboundInputPersistsToolResultWhenContinuationModelFails(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-failure-repair"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	calls := 0
	model := llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
		calls++
		if calls == 1 {
			return llmagent.Response{
				Operations: []agent.OperationRequest{{Operation: opRef, Input: "A100", ProviderCallID: "call_1"}},
				Transcript: coreconversation.Transcript{
					Provider: provider,
					Items: []coreconversation.Item{
						{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "lookup A100"},
						{Provider: provider, Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "lookup", Native: json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}`)},
					},
				},
			}, nil
		}
		return llmagent.Response{}, errors.New("continuation transport failed")
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
		Turns: agent.TurnPolicy{Continuation: agent.ContinuationPolicy{
			MaxContinuations: 2,
			StopCondition:    agent.StopConditionSpec{Type: "max-continuations", Max: 2},
		}},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, Operations: ops, OperationExecutor: operationruntime.NewExecutor(), ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-failure-repair"}}

	result := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "lookup A100"}})
	if result.Status != InputStatusFailed {
		t.Fatalf("status = %q, want failed: %#v", result.Status, result)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-failure-repair"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if !threadHasToolResultCallID(stored.Events, "call_1") {
		t.Fatalf("stored events = %#v, want repaired tool result for call_1", stored.Events)
	}
}

func TestAppendThreadEventsRetriesAppendConflict(t *testing.T) {
	ctx := context.Background()
	events := &conflictingSessionEventStore{inner: eventstore.NewMemoryStore(), threadID: "thread-append-conflict"}
	threadStore, err := runtimethread.NewStore(events)
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-append-conflict"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	s := Session{ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-append-conflict"}}

	err = s.appendThreadEvents(ctx, coresession.InputReceived{
		RunID:   "run-1",
		Message: channel.Message{Content: "hello"},
	})
	if err != nil {
		t.Fatalf("appendThreadEvents returned error: %v", err)
	}
	if !events.injected {
		t.Fatal("conflict was not injected")
	}
}

func TestAppendConversationRetriesAppendConflict(t *testing.T) {
	ctx := context.Background()
	events := &conflictingSessionEventStore{inner: eventstore.NewMemoryStore(), threadID: "thread-conversation-conflict"}
	threadStore, err := runtimethread.NewStore(events)
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-conversation-conflict"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	s := Session{ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-conversation-conflict"}}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}

	err = s.appendConversation(ctx, "turn-1", provider, []coreconversation.Item{{
		Provider: provider,
		Kind:     coreconversation.ItemToolResult,
		CallID:   "call_1",
		Content:  "ok",
	}})
	if err != nil {
		t.Fatalf("appendConversation returned error: %v", err)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-conversation-conflict"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if !threadHasToolResultCallID(stored.Events, "call_1") {
		t.Fatalf("stored events = %#v, want tool result for call_1", stored.Events)
	}
}

func TestStepAndContinuationLimitsUseLLMDefaultsAndExplicitOverrides(t *testing.T) {
	implicitLLMAgent, err := llmagent.New(agent.Spec{Name: "main"}, llmagent.StaticModel{Response: llmagent.MessageResponse("ok")})
	if err != nil {
		t.Fatalf("new implicit llm agent: %v", err)
	}
	implicitLLMSession := Session{Agent: implicitLLMAgent}
	if got := implicitLLMSession.maxSteps(); got != 50 {
		t.Fatalf("implicit llm maxSteps = %d, want 50", got)
	}
	if got := implicitLLMSession.maxContinuations(); got != 3 {
		t.Fatalf("implicit llm maxContinuations = %d, want 3", got)
	}
	if !implicitLLMSession.failOnStepLimit() {
		t.Fatal("implicit llm failOnStepLimit = false, want true")
	}

	defaultSession := Session{Agent: &sequenceAgent{spec: agent.Spec{
		Name:   "main",
		Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
	}}}
	if got := defaultSession.maxSteps(); got != 50 {
		t.Fatalf("default maxSteps = %d, want 50", got)
	}
	if got := defaultSession.maxContinuations(); got != 3 {
		t.Fatalf("default maxContinuations = %d, want 3", got)
	}

	overrideSession := Session{Agent: &sequenceAgent{spec: agent.Spec{
		Name:   "main",
		Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
		Turns: agent.TurnPolicy{MaxSteps: 8, Continuation: agent.ContinuationPolicy{
			MaxContinuations: 50,
			StopCondition:    agent.StopConditionSpec{Type: "max-continuations", Max: 50},
		}},
	}}}
	if got := overrideSession.maxSteps(); got != 8 {
		t.Fatalf("override maxSteps = %d, want 8", got)
	}
	if got := overrideSession.maxContinuations(); got != 50 {
		t.Fatalf("override maxContinuations = %d, want 50", got)
	}
}

func TestExecuteInboundInputMaxContinuationsDoesNotLimitInnerToolLoop(t *testing.T) {
	ctx := context.Background()
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	calls := 0
	model := llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
		calls++
		if calls <= 2 {
			callID := "call_1"
			if calls == 2 {
				callID = "call_2"
			}
			return llmagent.Response{
				Operations: []agent.OperationRequest{{Operation: opRef, Input: callID, ProviderCallID: callID}},
				Transcript: coreconversation.Transcript{
					Provider: provider,
					Items: []coreconversation.Item{
						{Provider: provider, Kind: coreconversation.ItemOutput, CallID: callID, Name: "lookup", Native: json.RawMessage(`{"type":"function_call","name":"lookup","arguments":"{}"}`)},
					},
				},
			}, nil
		}
		return llmagent.Response{Message: &agent.Message{Content: "done"}}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
		Turns:     agent.TurnPolicy{MaxSteps: 3, Continuation: agent.ContinuationPolicy{MaxContinuations: 0}},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, Operations: ops, OperationExecutor: operationruntime.NewExecutor()}

	result := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "lookup twice"}})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestExecuteInboundInputNoStopConditionDoesNotOuterContinue(t *testing.T) {
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{
			Name:   "main",
			Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
			Turns:  agent.TurnPolicy{MaxSteps: 50},
		},
		results: []agent.StepResult{
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "done"}}},
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "continued"}}},
		},
	}
	s := Session{Agent: agentRuntime}

	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "work"}})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(agentRuntime.inputs) != 1 {
		t.Fatalf("steps = %d, want 1", len(agentRuntime.inputs))
	}
}

func TestExecuteInboundInputStopConditionMaxContinuationsOuterContinues(t *testing.T) {
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{
			Name:   "main",
			Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
			Turns: agent.TurnPolicy{MaxSteps: 50, Continuation: agent.ContinuationPolicy{
				MaxContinuations: 2,
				StopCondition:    agent.StopConditionSpec{Type: "max-continuations", Max: 5},
			}},
		},
		results: []agent.StepResult{
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "first"}}},
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "second"}}},
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "third"}}},
		},
	}
	s := Session{Agent: agentRuntime}

	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "work"}})
	if result.Status != InputStatusFailed {
		t.Fatalf("status = %q, want failed after continuation limit: %#v", result.Status, result)
	}
	if len(agentRuntime.inputs) != 3 {
		t.Fatalf("steps = %d, want first turn plus two continuations", len(agentRuntime.inputs))
	}
	if agentRuntime.inputs[1].Observations[0].Kind != "session.continuation" {
		t.Fatalf("second observations = %#v, want continuation", agentRuntime.inputs[1].Observations)
	}
	if result.Error == nil || result.Error.Code != "continuation_limit_exceeded" {
		t.Fatalf("error = %#v, want continuation_limit_exceeded", result.Error)
	}
}

func TestExecuteInboundInputPromptStopConditionUsesEvaluatorInstruction(t *testing.T) {
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{
			Name:   "main",
			Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
			Turns: agent.TurnPolicy{MaxSteps: 50, Continuation: agent.ContinuationPolicy{
				MaxContinuations: 2,
				StopCondition:    agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when the task is complete."},
			}},
		},
		results: []agent.StepResult{
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "first"}}},
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "done"}}},
		},
	}
	evaluator := &sequenceStopEvaluator{evaluations: []StopEvaluation{
		{Action: "continue", Reason: "more work remains", ContinueInstruction: "Add tests for parser errors."},
		{Action: "stop", Reason: "task complete"},
	}}
	s := Session{Agent: agentRuntime, StopEvaluator: evaluator}

	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "improve coverage"}})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(agentRuntime.inputs) != 2 {
		t.Fatalf("steps = %d, want first turn plus one continuation", len(agentRuntime.inputs))
	}
	if got := agentRuntime.inputs[1].Observations[0].Content; got != "Add tests for parser errors." {
		t.Fatalf("continuation content = %#v, want evaluator instruction", got)
	}
	if len(evaluator.inputs) != 2 || evaluator.inputs[0].Condition.Prompt != "Stop when the task is complete." {
		t.Fatalf("evaluator inputs = %#v, want prompt condition", evaluator.inputs)
	}
	if result.Outbound == nil || result.Outbound.Message.Content != "done" {
		t.Fatalf("outbound = %#v, want final continuation message", result.Outbound)
	}
}

func TestExecuteInboundCommandGoalUsesPromptContinuation(t *testing.T) {
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{
			Name:   "coder",
			Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
			Turns:  agent.TurnPolicy{MaxSteps: 1},
		},
		results: []agent.StepResult{
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "first"}}},
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "done"}}},
		},
	}
	evaluator := &sequenceStopEvaluator{evaluations: []StopEvaluation{
		{Action: StopActionContinue, ContinueInstruction: "keep going", Reason: "not done"},
		{Action: StopActionStop, Reason: "done"},
	}}
	s := Session{Agent: agentRuntime, StopEvaluator: evaluator}
	result := s.ExecuteInboundCommand(context.Background(), channel.Inbound{
		ID:   "run-goal",
		Kind: channel.InboundCommand,
		Caller: policy.Caller{
			Kind: policy.CallerUser,
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path: command.Path{"goal"},
			Args: []string{"Test coverage has increased to 90%"},
			Input: map[string]any{
				"max": 2,
			},
		},
	})

	if result.Status != CommandStatusOK {
		t.Fatalf("status = %q error = %#v, want ok", result.Status, result.Error)
	}
	if result.Output != "done" {
		t.Fatalf("output = %#v, want final goal output", result.Output)
	}
	if len(agentRuntime.inputs) != 2 {
		t.Fatalf("agent inputs = %d, want initial plus continuation", len(agentRuntime.inputs))
	}
	for i, input := range agentRuntime.inputs {
		if input.Goal != "Test coverage has increased to 90%" {
			t.Fatalf("input[%d].Goal = %q, want goal", i, input.Goal)
		}
	}
	if len(evaluator.inputs) != 2 {
		t.Fatalf("evaluator inputs = %d, want two continuation decisions", len(evaluator.inputs))
	}
	if evaluator.inputs[0].MaxContinuations != 2 || !strings.Contains(evaluator.inputs[0].Condition.Prompt, "Test coverage has increased to 90%") {
		t.Fatalf("evaluator input = %#v, want goal prompt and cap", evaluator.inputs[0])
	}
}

func TestExecuteInboundCommandGoalContinuesAfterInnerStepLimit(t *testing.T) {
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{
			Name:  "coder",
			Turns: agent.TurnPolicy{MaxSteps: 1},
		},
		results: []agent.StepResult{
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:       agent.DecisionOperation,
					Operations: []agent.OperationRequest{{Operation: opRef, Input: "coverage"}},
				},
			},
			{
				Status: agent.StatusOK,
				Decision: agent.Decision{
					Kind:    agent.DecisionMessage,
					Message: &agent.Message{Content: "done"},
				},
			},
		},
	}
	evaluator := &sequenceStopEvaluator{evaluations: []StopEvaluation{
		{Action: StopActionContinue, ContinueInstruction: "finish the task", Reason: "tool result needs synthesis"},
		{Action: StopActionStop, Reason: "done"},
	}}
	s := Session{Agent: agentRuntime, Operations: ops, OperationExecutor: operationruntime.NewExecutor(), StopEvaluator: evaluator}
	result := s.ExecuteInboundCommand(context.Background(), channel.Inbound{
		ID:   "run-goal",
		Kind: channel.InboundCommand,
		Caller: policy.Caller{
			Kind: policy.CallerUser,
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path: command.Path{"goal"},
			Args: []string{"Complete the coverage task"},
			Input: map[string]any{
				"max": 2,
			},
		},
	})

	if result.Status != CommandStatusOK {
		t.Fatalf("status = %q error = %#v, want ok", result.Status, result.Error)
	}
	if result.Output != "done" {
		t.Fatalf("output = %#v, want final goal output", result.Output)
	}
	if len(agentRuntime.inputs) != 2 {
		t.Fatalf("agent inputs = %d, want initial plus continuation", len(agentRuntime.inputs))
	}
	if len(evaluator.inputs) != 2 {
		t.Fatalf("evaluator inputs = %d, want two continuation decisions", len(evaluator.inputs))
	}
	if result.Error != nil {
		t.Fatalf("error = %#v, want nil", result.Error)
	}
}

func TestParseGoalCommandInputDefaultsMaxContinuations(t *testing.T) {
	input, err := parseGoalCommandInput(command.Invocation{
		Path: command.Path{"goal"},
		Args: []string{"Test coverage has increased to 90%"},
	})
	if err != nil {
		t.Fatalf("parseGoalCommandInput: %v", err)
	}
	if input.Goal != "Test coverage has increased to 90%" || input.MaxContinuations != 10 {
		t.Fatalf("input = %#v, want goal and default cap 10", input)
	}
}

func TestParseGoalCommandInputRejectsExplicitZeroMax(t *testing.T) {
	_, err := parseGoalCommandInput(command.Invocation{
		Path: command.Path{"goal"},
		Args: []string{"Test coverage has increased to 90%"},
		Input: map[string]any{
			"max": 0,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "max-continuations must be > 0") {
		t.Fatalf("parseGoalCommandInput error = %v, want explicit zero rejected", err)
	}
}

func TestModelStopEvaluatorAcceptsToolDecision(t *testing.T) {
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		if got := req.Agent.Inference.Model; got != "expensive-model" {
			t.Fatalf("evaluator model = %q, want parent model", got)
		}
		if len(req.Tools) != 1 || req.Tools[0].Name != "continuation_decision" {
			t.Fatalf("tools = %#v, want continuation_decision", req.Tools)
		}
		return llmagent.Response{Operations: []agent.OperationRequest{{
			Operation: operation.Ref{Name: "continuation_decision"},
			Input:     StopEvaluation{Action: StopActionContinue, Reason: "coverage remains", ContinueInstruction: "Add parser error tests."},
		}}}, nil
	})

	got, err := (ModelStopEvaluator{Model: model}).EvaluateStopCondition(context.Background(), StopEvaluationInput{
		Agent: agent.Spec{
			Name:      "coder",
			Inference: agent.InferenceSpec{Model: "expensive-model", Thinking: "enabled"},
		},
		Condition: agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when complete."},
	})
	if err != nil {
		t.Fatalf("EvaluateStopCondition: %v", err)
	}
	if got.Action != "continue" || got.ContinueInstruction != "Add parser error tests." {
		t.Fatalf("evaluation = %#v, want continue with instruction", got)
	}
}

func TestModelStopEvaluatorAcceptsStopToolDecision(t *testing.T) {
	model := llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
		return llmagent.Response{Operations: []agent.OperationRequest{{
			Operation: operation.Ref{Name: "continuation_decision"},
			Input:     StopEvaluation{Action: StopActionStop, Reason: "done"},
		}}}, nil
	})

	got, err := (ModelStopEvaluator{Model: model}).EvaluateStopCondition(context.Background(), StopEvaluationInput{
		Agent:     agent.Spec{Name: "coder"},
		Condition: agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when complete."},
	})
	if err != nil {
		t.Fatalf("EvaluateStopCondition: %v", err)
	}
	if got.Action != StopActionStop || got.Reason != "done" {
		t.Fatalf("evaluation = %#v, want stop/done", got)
	}
}

func TestModelStopEvaluatorRejectsTextDecision(t *testing.T) {
	model := llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
		return llmagent.Response{Message: &agent.Message{Content: "done"}}, nil
	})

	_, err := (ModelStopEvaluator{Model: model}).EvaluateStopCondition(context.Background(), StopEvaluationInput{
		Agent:     agent.Spec{Name: "coder"},
		Condition: agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when complete."},
	})
	if err == nil || !strings.Contains(err.Error(), "must call continuation_decision") {
		t.Fatalf("EvaluateStopCondition error = %v, want tool-call requirement", err)
	}
}

func TestModelStopEvaluatorRejectsWrongTool(t *testing.T) {
	model := llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
		return llmagent.Response{Operations: []agent.OperationRequest{{
			Operation: operation.Ref{Name: "other"},
			Input:     StopEvaluation{Action: StopActionStop},
		}}}, nil
	})

	_, err := (ModelStopEvaluator{Model: model}).EvaluateStopCondition(context.Background(), StopEvaluationInput{
		Agent:     agent.Spec{Name: "coder"},
		Condition: agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when complete."},
	})
	if err == nil || !strings.Contains(err.Error(), "want \"continuation_decision\"") {
		t.Fatalf("EvaluateStopCondition error = %v, want wrong-tool failure", err)
	}
}

func TestModelStopEvaluatorRejectsMultipleDecisions(t *testing.T) {
	model := llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
		return llmagent.Response{Operations: []agent.OperationRequest{
			{Operation: operation.Ref{Name: "continuation_decision"}, Input: StopEvaluation{Action: StopActionStop}},
			{Operation: operation.Ref{Name: "continuation_decision"}, Input: StopEvaluation{Action: StopActionContinue}},
		}}, nil
	})

	_, err := (ModelStopEvaluator{Model: model}).EvaluateStopCondition(context.Background(), StopEvaluationInput{
		Agent:     agent.Spec{Name: "coder"},
		Condition: agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when complete."},
	})
	if err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("EvaluateStopCondition error = %v, want exactly-once failure", err)
	}
}

func TestModelStopEvaluatorRejectsInvalidDecisionAction(t *testing.T) {
	model := llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
		return llmagent.Response{Operations: []agent.OperationRequest{{
			Operation: operation.Ref{Name: "continuation_decision"},
			Input:     StopEvaluation{Action: "maybe"},
		}}}, nil
	})

	_, err := (ModelStopEvaluator{Model: model}).EvaluateStopCondition(context.Background(), StopEvaluationInput{
		Agent:     agent.Spec{Name: "coder"},
		Condition: agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when complete."},
	})
	if err == nil || !strings.Contains(err.Error(), "stop evaluation action must be stop or continue") {
		t.Fatalf("EvaluateStopCondition error = %v, want invalid-action failure", err)
	}
}

func TestModelStopEvaluatorRejectsMissingDecisionAction(t *testing.T) {
	model := llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
		return llmagent.Response{Operations: []agent.OperationRequest{{
			Operation: operation.Ref{Name: "continuation_decision"},
			Input:     StopEvaluation{Reason: "missing"},
		}}}, nil
	})

	_, err := (ModelStopEvaluator{Model: model}).EvaluateStopCondition(context.Background(), StopEvaluationInput{
		Agent:     agent.Spec{Name: "coder"},
		Condition: agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when complete."},
	})
	if err == nil || !strings.Contains(err.Error(), "stop evaluation action must be stop or continue") {
		t.Fatalf("EvaluateStopCondition error = %v, want missing-action failure", err)
	}
}

func TestStopEvaluatorContextPolicyControlsEffectDetails(t *testing.T) {
	input := StopEvaluationInput{
		Condition: agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when complete."},
		Inbound:   channel.Inbound{Kind: channel.InboundMessage, Message: &channel.Message{Content: "Do sensitive work."}},
		AgentResult: agent.StepResult{Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: "I ran the operation."},
		}},
		Effects: []environment.EffectResult{{Result: operation.OK("RAW_SECRET_OUTPUT")}},
	}

	summaryInput := input
	summaryInput.Agent = agent.Spec{Name: "coder", Turns: agent.TurnPolicy{Continuation: agent.ContinuationPolicy{ContextPolicy: "summary"}}}
	summaryGoal := sessioncontrol.StopEvaluatorGoal(summaryInput)
	if strings.Contains(summaryGoal, "RAW_SECRET_OUTPUT") {
		t.Fatalf("summary goal leaked raw effect output:\n%s", summaryGoal)
	}
	if !strings.Contains(summaryGoal, "Operation effects observed: 1") {
		t.Fatalf("summary goal = %q, want effect count", summaryGoal)
	}

	inheritInput := input
	inheritInput.Agent = agent.Spec{Name: "coder", Turns: agent.TurnPolicy{Continuation: agent.ContinuationPolicy{ContextPolicy: "inherit"}}}
	inheritGoal := sessioncontrol.StopEvaluatorGoal(inheritInput)
	if !strings.Contains(inheritGoal, "RAW_SECRET_OUTPUT") {
		t.Fatalf("inherit goal = %q, want raw effect output", inheritGoal)
	}
}

func TestExecuteInboundInputAgentStopConditionIsDeferred(t *testing.T) {
	agentRuntime := &sequenceAgent{
		spec: agent.Spec{
			Name:  "main",
			Turns: agent.TurnPolicy{Continuation: agent.ContinuationPolicy{MaxContinuations: 1, StopCondition: agent.StopConditionSpec{Type: "agent", Session: "reviewer", Prompt: "Stop when reviewed."}}},
		},
		results: []agent.StepResult{
			{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: "candidate answer"}}},
		},
	}
	s := Session{Agent: agentRuntime}

	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "answer the user"}})
	if result.Status != InputStatusFailed || result.Error == nil || !strings.Contains(result.Error.Message, "typed session-agent decision tools") {
		t.Fatalf("result = %#v, want deferred agent stop-condition failure", result)
	}
}

func TestExecuteInboundInputUsesStoredProviderContinuation(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-native-continuation"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	provider := coreconversation.ProviderIdentity{
		Provider: "openai",
		API:      "openai.responses",
		Family:   "responses",
		Model:    "gpt-test",
	}
	var transcripts []coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		if req.Transcript == nil {
			t.Fatalf("transcript is nil")
		}
		transcripts = append(transcripts, *req.Transcript)
		if len(transcripts) == 1 {
			return llmagent.Response{
				Message: &agent.Message{Content: "noted"},
				Transcript: coreconversation.Transcript{
					Provider: provider,
					Items: []coreconversation.Item{
						{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "remember alpha"},
						{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", ID: "msg_1", Content: "noted", Native: json.RawMessage(`{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"noted"}]}`)},
					},
					Continuation: &coreconversation.ContinuationHandle{
						Provider:   provider,
						Mode:       coreconversation.ContinuationPreviousResponseID,
						Transport:  coreconversation.TransportHTTPSSE,
						ResponseID: "resp_1",
					},
					Mode: coreconversation.ProjectionNativeContinuation,
				},
			}, nil
		}
		return llmagent.Response{
			Message: &agent.Message{Content: "alpha"},
			Transcript: coreconversation.Transcript{
				Provider: provider,
				Items: []coreconversation.Item{
					{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "what did I say?"},
					{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", ID: "msg_2", Content: "alpha", Native: json.RawMessage(`{"type":"message","id":"msg_2","role":"assistant","content":[{"type":"output_text","text":"alpha"}]}`)},
				},
				Continuation: &coreconversation.ContinuationHandle{
					Provider:   provider,
					Mode:       coreconversation.ContinuationPreviousResponseID,
					Transport:  coreconversation.TransportHTTPSSE,
					ResponseID: "resp_2",
				},
				Mode: coreconversation.ProjectionNativeContinuation,
			},
		}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{
		Agent:       runtimeAgent,
		ThreadStore: threadStore,
		Thread:      corethread.Ref{ID: "thread-native-continuation"},
	}

	first := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-1",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "remember alpha"},
	})
	if first.Status != InputStatusOK {
		t.Fatalf("first status = %q, want ok: %#v", first.Status, first)
	}
	second := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-2",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "what did I say?"},
	})
	if second.Status != InputStatusOK {
		t.Fatalf("second status = %q, want ok: %#v", second.Status, second)
	}
	if len(transcripts) != 2 {
		t.Fatalf("transcripts len = %d, want 2", len(transcripts))
	}
	if transcripts[1].Mode != coreconversation.ProjectionNativeContinuation {
		t.Fatalf("second mode = %q, want native continuation", transcripts[1].Mode)
	}
	if transcripts[1].Continuation == nil || transcripts[1].Continuation.ResponseID != "resp_1" {
		t.Fatalf("second continuation = %#v, want resp_1", transcripts[1].Continuation)
	}
	if len(transcripts[1].Items) != 1 || transcripts[1].Items[0].Content != "what did I say?" {
		t.Fatalf("second items = %#v, want only pending input", transcripts[1].Items)
	}
	stored, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-native-continuation"})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if countEvent(stored.Events, coreconversation.EventContinuationStored) != 2 {
		t.Fatalf("stored events = %#v, want two continuation handles", stored.Events)
	}
}

func TestExecuteInboundInputUsesProviderContinuationWithPrefixedModel(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-prefixed-model-continuation"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	opRef := operation.Ref{Name: "lookup"}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: opRef}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{"found": input})
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}
	provider := coreconversation.ProviderIdentity{
		Provider: "openai",
		API:      "openai.responses",
		Family:   "responses",
		Model:    "gpt-test",
	}
	var transcripts []coreconversation.Transcript
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		if req.Transcript == nil {
			t.Fatalf("transcript is nil")
		}
		transcripts = append(transcripts, *req.Transcript)
		if len(transcripts) == 1 {
			return llmagent.Response{
				Operations: []agent.OperationRequest{{Operation: opRef, Input: "lyse", ProviderCallID: "call_1"}},
				Transcript: coreconversation.Transcript{
					Provider: provider,
					Items: []coreconversation.Item{
						{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "search for lyse"},
						{Provider: provider, Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "lookup", Native: json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"lyse\"}"}`)},
					},
					Continuation: &coreconversation.ContinuationHandle{
						Provider:   provider,
						Mode:       coreconversation.ContinuationPreviousResponseID,
						Transport:  coreconversation.TransportHTTPSSE,
						ResponseID: "resp_tool_call",
					},
					Mode: coreconversation.ProjectionNativeContinuation,
				},
			}, nil
		}
		return llmagent.Response{
			Message: &agent.Message{Content: "found lyse"},
			Transcript: coreconversation.Transcript{
				Provider: provider,
				Items: []coreconversation.Item{
					{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "found lyse", Native: json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"found lyse"}]}`)},
				},
			},
		}, nil
	})
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "openai/gpt-test"},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{
		Agent:             runtimeAgent,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
		Thread:            corethread.Ref{ID: "thread-prefixed-model-continuation"},
	}

	result := s.ExecuteInboundInput(ctx, channel.Inbound{
		ID:      "run-1",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "search for lyse"},
	})
	if result.Status != InputStatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result)
	}
	if len(transcripts) != 2 {
		t.Fatalf("transcripts len = %d, want 2", len(transcripts))
	}
	if transcripts[1].Mode != coreconversation.ProjectionNativeContinuation {
		t.Fatalf("second mode = %q, want native continuation", transcripts[1].Mode)
	}
	if transcripts[1].Continuation == nil || transcripts[1].Continuation.ResponseID != "resp_tool_call" {
		t.Fatalf("second continuation = %#v, want resp_tool_call", transcripts[1].Continuation)
	}
	if !hasToolResultCallID(transcripts[1].Items, "call_1") {
		t.Fatalf("second items = %#v, want tool result for call_1", transcripts[1].Items)
	}
}

func TestExecuteInboundInputUsesConcreteModelIdentityForReplay(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-concrete-model-replay"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	provider := coreconversation.ProviderIdentity{
		Provider: "openrouter",
		API:      "openrouter.responses",
		Family:   "responses",
		Model:    "minimax/minimax-m2.7-20260318",
	}
	var transcripts []coreconversation.Transcript
	model := sessionIdentifiedModel{
		identity: provider,
		complete: func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
			if req.Transcript == nil {
				t.Fatalf("transcript is nil")
			}
			transcripts = append(transcripts, *req.Transcript)
			items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
			if len(transcripts) == 1 {
				items = append(items, coreconversation.Item{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "noted"})
				return llmagent.Response{
					Message:    &agent.Message{Content: "noted"},
					Transcript: coreconversation.Transcript{Provider: provider, Items: items},
				}, nil
			}
			items = append(items, coreconversation.Item{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "still here"})
			return llmagent.Response{
				Message:    &agent.Message{Content: "still here"},
				Transcript: coreconversation.Transcript{Provider: provider, Items: items},
			}, nil
		},
	}
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "openrouter/minimax/minimax-m2.7"},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-concrete-model-replay"}}

	first := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "remember browser failure"}})
	if first.Status != InputStatusOK {
		t.Fatalf("first status = %q, want ok: %#v", first.Status, first)
	}
	second := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-2", Kind: channel.InboundMessage, Message: &channel.Message{Content: "so?"}})
	if second.Status != InputStatusOK {
		t.Fatalf("second status = %q, want ok: %#v", second.Status, second)
	}
	if len(transcripts) != 2 {
		t.Fatalf("transcripts len = %d, want 2", len(transcripts))
	}
	if transcripts[1].Provider != provider {
		t.Fatalf("second provider = %#v, want concrete provider %#v", transcripts[1].Provider, provider)
	}
	if !hasTranscriptUserContent(transcripts[1].Items, "remember browser failure") {
		t.Fatalf("second items = %#v, want first prompt replayed", transcripts[1].Items)
	}
}

func TestExecuteInboundInputPersistsPendingInputWhenModelFails(t *testing.T) {
	ctx := context.Background()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-failed-input-replay"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"}
	calls := 0
	var secondTranscript coreconversation.Transcript
	model := sessionIdentifiedModel{
		identity: provider,
		complete: func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
			calls++
			if calls == 1 {
				return llmagent.Response{}, errors.New("transport failed")
			}
			if req.Transcript == nil {
				t.Fatalf("transcript is nil")
			}
			secondTranscript = *req.Transcript
			items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
			items = append(items, coreconversation.Item{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "I saw it"})
			return llmagent.Response{
				Message:    &agent.Message{Content: "I saw it"},
				Transcript: coreconversation.Transcript{Provider: provider, Items: items},
			}, nil
		},
	}
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "coder",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{Model: "gpt-test"},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	s := Session{Agent: runtimeAgent, ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-failed-input-replay"}}

	first := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-1", Kind: channel.InboundMessage, Message: &channel.Message{Content: "open login page"}})
	if first.Status != InputStatusFailed {
		t.Fatalf("first status = %q, want failed: %#v", first.Status, first)
	}
	second := s.ExecuteInboundInput(ctx, channel.Inbound{ID: "run-2", Kind: channel.InboundMessage, Message: &channel.Message{Content: "so?"}})
	if second.Status != InputStatusOK {
		t.Fatalf("second status = %q, want ok: %#v", second.Status, second)
	}
	if !hasTranscriptUserContent(secondTranscript.Items, "open login page") || !hasTranscriptUserContent(secondTranscript.Items, "so?") {
		t.Fatalf("second transcript = %#v, want failed prompt and follow-up", secondTranscript.Items)
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

type sessionIdentifiedModel struct {
	identity coreconversation.ProviderIdentity
	complete func(context.Context, llmagent.Request) (llmagent.Response, error)
}

func (m sessionIdentifiedModel) Complete(ctx context.Context, req llmagent.Request) (llmagent.Response, error) {
	return m.complete(ctx, req)
}

func (m sessionIdentifiedModel) ProviderIdentity(llmagent.Request) coreconversation.ProviderIdentity {
	return m.identity
}

type sessionTargetClient struct {
	input  string
	output string
}

func (c *sessionTargetClient) Open(context.Context, sessionagent.OpenRequest) (sessionagent.Session, error) {
	return sessionTargetSession{client: c}, nil
}

type sessionTargetSession struct {
	client *sessionTargetClient
}

func (s sessionTargetSession) Info() sessionagent.SessionInfo {
	return sessionagent.SessionInfo{}
}

func (s sessionTargetSession) SendInput(_ context.Context, input sessionagent.Input) (sessionagent.Run, error) {
	s.client.input = input.Text
	events := make(chan sessionagent.RunEvent)
	close(events)
	return sessionTargetRun{output: s.client.output, events: events}, nil
}

type sessionTargetRun struct {
	output string
	events <-chan sessionagent.RunEvent
}

func (r sessionTargetRun) ID() string {
	return "child-run"
}

func (r sessionTargetRun) Events() <-chan sessionagent.RunEvent {
	return r.events
}

func (r sessionTargetRun) Wait(context.Context) (sessionagent.RunResult, error) {
	return sessionagent.RunResult{Text: r.output}, nil
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

type sequenceAgent struct {
	spec    agent.Spec
	results []agent.StepResult
	inputs  []agent.StepInput
}

func (a *sequenceAgent) Spec() agent.Spec {
	return a.spec
}

func (a *sequenceAgent) Step(_ agent.Context, input agent.StepInput) agent.StepResult {
	a.inputs = append(a.inputs, input)
	if len(a.inputs) <= len(a.results) {
		return a.results[len(a.inputs)-1]
	}
	return agent.StepResult{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionWait}}
}

type sequenceStopEvaluator struct {
	evaluations []StopEvaluation
	inputs      []StopEvaluationInput
}

func (e *sequenceStopEvaluator) EvaluateStopCondition(_ context.Context, input StopEvaluationInput) (StopEvaluation, error) {
	e.inputs = append(e.inputs, input)
	if len(e.inputs) <= len(e.evaluations) {
		return e.evaluations[len(e.inputs)-1], nil
	}
	return StopEvaluation{Action: "stop"}, nil
}

type scriptedContextProvider struct {
	spec   corecontext.ProviderSpec
	blocks []corecontext.Block
	build  func(context.Context, corecontext.Request) ([]corecontext.Block, error)
}

func (p *scriptedContextProvider) Spec() corecontext.ProviderSpec { return p.spec }

func (p *scriptedContextProvider) Build(ctx context.Context, req corecontext.Request) ([]corecontext.Block, error) {
	if p.build != nil {
		return p.build(ctx, req)
	}
	return append([]corecontext.Block(nil), p.blocks...), nil
}

func requestedCallIDs(events []event.Event) []operation.CallID {
	var out []operation.CallID
	for _, emitted := range events {
		if payload, ok := emitted.(coresession.OperationRequested); ok {
			out = append(out, payload.CallID)
		}
	}
	return out
}

func completedCallIDs(events []event.Event) []operation.CallID {
	var out []operation.CallID
	for _, emitted := range events {
		if payload, ok := emitted.(coresession.OperationCompleted); ok {
			out = append(out, payload.CallID)
		}
	}
	return out
}

func hasToolResultCallID(items []coreconversation.Item, callID string) bool {
	_, ok := toolResultByCallID(items, callID)
	return ok
}

func toolResultByCallID(items []coreconversation.Item, callID string) (coreconversation.Item, bool) {
	for _, item := range items {
		if item.Kind == coreconversation.ItemToolResult && item.CallID == callID {
			return item, true
		}
	}
	return coreconversation.Item{}, false
}

func hasTranscriptUserContent(items []coreconversation.Item, content string) bool {
	for _, item := range items {
		if item.Kind == coreconversation.ItemInput && item.Role == "user" && valueText(item.Content) == content {
			return true
		}
	}
	return false
}

func hasCompactedToolResult(items []coreconversation.Item) bool {
	for _, item := range items {
		if item.Kind == coreconversation.ItemToolResult && item.Metadata["compaction"] == "tool_result_summary" {
			return true
		}
	}
	return false
}

func hasCompactedAssistantOutput(items []coreconversation.Item) bool {
	for _, item := range items {
		if item.Kind == coreconversation.ItemOutput && item.Metadata["compaction"] == "assistant_output_summary" {
			return true
		}
	}
	return false
}

func compactTestAgent(t *testing.T, provider coreconversation.ProviderIdentity) agent.Agent {
	t.Helper()
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:   "coder",
		Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{
			Model: provider.Model,
			Annotations: map[string]string{
				"provider": provider.Provider,
				"api":      provider.API,
				"family":   provider.Family,
			},
		},
	}, llmagent.StaticModel{Response: llmagent.MessageResponse("ok")})
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	return runtimeAgent
}

type autoCompactAgentOptions struct {
	ContextTokens   string
	MaxOutputTokens int
	Output          string
}

func autoCompactAgent(t *testing.T, provider coreconversation.ProviderIdentity, contextTokens, output string) agent.Agent {
	t.Helper()
	return autoCompactAgentWithOptions(t, provider, autoCompactAgentOptions{
		ContextTokens: contextTokens,
		Output:        output,
	})
}

func autoCompactAgentWithOptions(t *testing.T, provider coreconversation.ProviderIdentity, opts autoCompactAgentOptions) agent.Agent {
	t.Helper()
	model := sessionIdentifiedModel{
		identity: provider,
		complete: func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
			items := append([]coreconversation.Item(nil), req.Transcript.NewItems...)
			items = append(items, autoCompactAssistantOutput(provider, "assistant", opts.Output))
			return llmagent.Response{
				Message:    &agent.Message{Content: "ok"},
				Transcript: coreconversation.Transcript{Provider: provider, Items: items},
			}, nil
		},
	}
	annotations := map[string]string{
		"provider": provider.Provider,
		"api":      provider.API,
		"family":   provider.Family,
	}
	if opts.ContextTokens != "" {
		annotations["llm.context_tokens"] = opts.ContextTokens
	}
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:   "coder",
		Driver: agent.DriverSpec{Kind: llmagent.DriverKind},
		Inference: agent.InferenceSpec{
			Model:           provider.Model,
			MaxOutputTokens: opts.MaxOutputTokens,
			Annotations:     annotations,
		},
	}, model)
	if err != nil {
		t.Fatalf("new llm agent: %v", err)
	}
	return runtimeAgent
}

func autoCompactAssistantOutput(provider coreconversation.ProviderIdentity, name, content string) coreconversation.Item {
	return coreconversation.Item{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Name: name, Content: content}
}

func exactTokenContent(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	return strings.Repeat("x", tokens*4-3)
}

func compactThread(t *testing.T, ctx context.Context, id corethread.ID) (corethread.Store, corethread.Ref) {
	t.Helper()
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: id}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	return threadStore, corethread.Ref{ID: id}
}

func compactLargeToolResult(provider coreconversation.ProviderIdentity, callID string) []coreconversation.Item {
	return []coreconversation.Item{{
		Provider: provider,
		Kind:     coreconversation.ItemToolResult,
		CallID:   callID,
		Name:     "file_read",
		Content:  strings.Repeat("large tool result ", 40000),
	}}
}

func compactMessageInbound(id, content string) channel.Inbound {
	return channel.Inbound{
		ID:      id,
		Kind:    channel.InboundMessage,
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Message: &channel.Message{Content: content},
	}
}

func compactInbound(id string, input any) channel.Inbound {
	return channel.Inbound{
		ID:      id,
		Kind:    channel.InboundCommand,
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{Path: command.Path{"compact"}, Input: input},
	}
}

type failCompactionStore struct {
	corethread.Store
}

func (s failCompactionStore) Append(ctx context.Context, ref corethread.Ref, records ...corethread.AppendRecord) ([]corethread.Record, error) {
	for _, record := range records {
		if _, ok := record.Event.Payload.(coreconversation.CompactionStored); ok {
			return nil, errors.New("compaction append failed")
		}
	}
	return s.Store.Append(ctx, ref, records...)
}

func compactionEvents(records []corethread.Record) []coreconversation.CompactionStored {
	var out []coreconversation.CompactionStored
	for _, record := range records {
		payload, ok := record.Event.Payload.(coreconversation.CompactionStored)
		if ok {
			out = append(out, payload)
		}
	}
	return out
}

func threadHasToolResultCallID(records []corethread.Record, callID string) bool {
	for _, record := range records {
		payload, ok := record.Event.Payload.(coreconversation.ItemsAppended)
		if !ok {
			continue
		}
		if hasToolResultCallID(payload.Items, callID) {
			return true
		}
	}
	return false
}

func countEvent(records []corethread.Record, name event.Name) int {
	count := 0
	for _, record := range records {
		if record.Event.Name == name {
			count++
		}
	}
	return count
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

type conflictingSessionEventStore struct {
	inner    event.Store
	threadID corethread.ID
	injected bool
}

func (s *conflictingSessionEventStore) Append(ctx context.Context, stream event.StreamID, opts event.AppendOptions, records ...event.Record) ([]event.StoredRecord, error) {
	if !s.injected && stream == event.StreamID("thread:"+s.threadID) && opts.CheckExpectedSequence && opts.ExpectedSequence > 0 {
		s.injected = true
		if _, err := s.inner.Append(ctx, stream, event.AppendOptions{}, event.Record{
			Name:    "test.concurrent_append",
			Payload: concurrentAppendEvent{},
			Scope:   event.Scope{ThreadID: string(s.threadID)},
		}); err != nil {
			return nil, err
		}
	}
	return s.inner.Append(ctx, stream, opts, records...)
}

func (s *conflictingSessionEventStore) AppendBatch(ctx context.Context, requests ...event.AppendRequest) ([]event.AppendResult, error) {
	return s.inner.AppendBatch(ctx, requests...)
}

func (s *conflictingSessionEventStore) Load(ctx context.Context, stream event.StreamID, opts event.LoadOptions) ([]event.StoredRecord, error) {
	return s.inner.Load(ctx, stream, opts)
}

type concurrentAppendEvent struct{}

func (concurrentAppendEvent) EventName() event.Name { return "test.concurrent_append" }

func hasEvent(events []event.Event, name event.Name) bool {
	for _, payload := range events {
		if payload != nil && payload.EventName() == name {
			return true
		}
	}
	return false
}

func hasEventPrefix(events []event.Event, prefix string) bool {
	for _, payload := range events {
		if payload != nil && strings.HasPrefix(string(payload.EventName()), prefix) {
			return true
		}
	}
	return false
}

func eventNames(events []event.Event) []event.Name {
	out := make([]event.Name, 0, len(events))
	for _, payload := range events {
		if payload != nil {
			out = append(out, payload.EventName())
		}
	}
	return out
}
