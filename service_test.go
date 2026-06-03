package fluxplane_test

import (
	"context"
	"testing"
	"time"

	fluxplane "github.com/fluxplane/fluxplane-core"
	"github.com/fluxplane/fluxplane-core/contrib/echo"
	"github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	coreresource "github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/tool"
	appcomposition "github.com/fluxplane/fluxplane-core/orchestration/app"
	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	llmfluxplane "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-policy"
)

func TestServiceSubmitInputThroughTopLevelAPI(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-input"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("hello"))
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
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "agent: hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
	assertRunEvent(t, run, fluxplane.EventInputCompleted)
}

func TestServiceSubmitCommandThroughTopLevelAPI(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-command"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithCommand(command.Invocation{
		Path:  command.Path{"echo"},
		Input: "hello",
	}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
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
	assertRunEvent(t, run, fluxplane.EventCommandCompleted)
}

func TestServiceOnEventReceivesDefaultSessionEvents(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	events := make(chan fluxplane.Event, 8)
	cancel, err := svc.OnEvent(ctx, func(event fluxplane.Event) {
		events <- event
	})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	defer cancel()

	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-service-events"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithCommand(command.Invocation{
		Path:  command.Path{"echo"},
		Input: "hello",
	}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind == fluxplane.EventCommandCompleted {
				return
			}
		case <-deadline:
			t.Fatal("expected command completion event")
		}
	}
}

func TestServiceProjectsToolsForResolvedInboundTrust(t *testing.T) {
	ctx := context.Background()
	agentInstance := &toolCaptureAgent{}
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := appcomposition.Compose(appcomposition.Config{
		Operations: []operation.Operation{echo},
		Bundles: []coreresource.ContributionBundle{{
			Agents: []agent.Spec{{Name: "main"}},
			Operations: []operation.Spec{{
				Ref: operation.Ref{Name: "echo"},
			}},
			Commands: []command.Spec{{
				Path: command.Path{"echo"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "echo"},
				},
				Policy: policy.InvocationPolicy{
					AllowedCallers: []policy.CallerKind{policy.CallerUser},
					RequiredTrust:  policy.TrustVerified,
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		Agent:   agentInstance,
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "test-user"}},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{Conversation: channel.ConversationRef{ID: "conv-tool-trust"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("hello"))
	if err != nil {
		t.Fatalf("Submit verified: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait verified: %v", err)
	}
	if len(agentInstance.lastTools) != 1 {
		t.Fatalf("verified tools len = %d, want 1", len(agentInstance.lastTools))
	}

	run, err = sessionHandle.Submit(ctx, fluxplane.NewSubmission().
		WithText("hello").
		WithTrustDowngrade(fluxplane.TrustDowngrade{Level: policy.TrustUntrusted}))
	if err != nil {
		t.Fatalf("Submit downgraded: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait downgraded: %v", err)
	}
	if len(agentInstance.lastTools) != 0 {
		t.Fatalf("downgraded tools len = %d, want 0", len(agentInstance.lastTools))
	}
}

func TestServiceRejectsOperationNotProjectedForInboundTrust(t *testing.T) {
	ctx := context.Background()
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := appcomposition.Compose(appcomposition.Config{
		Operations: []operation.Operation{echo},
		Bundles: []coreresource.ContributionBundle{{
			Agents:     []agent.Spec{{Name: "main"}},
			Operations: []operation.Spec{{Ref: operation.Ref{Name: "echo"}}},
			Commands: []command.Spec{{
				Path: command.Path{"echo"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "echo"},
				},
				Policy: policy.InvocationPolicy{
					AllowedCallers: []policy.CallerKind{policy.CallerUser},
					RequiredTrust:  policy.TrustVerified,
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		Agent:   operationAgent{},
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "test-user"}},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{Conversation: channel.ConversationRef{ID: "conv-operation-not-projected"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().
		WithText("hello").
		WithTrustDowngrade(fluxplane.TrustDowngrade{Level: policy.TrustUntrusted}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Input == nil || len(result.Input.Effects) != 1 || result.Input.Effects[0].Result.Error == nil {
		t.Fatalf("input effects = %#v, want failed operation result", result.Input)
	}
	if result.Input.Effects[0].Result.Error.Code != "operation_not_projected" {
		t.Fatalf("operation error = %#v, want operation_not_projected", result.Input.Effects[0].Result.Error)
	}
}

func TestServiceListsAndResumesSessions(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	opened, err := svc.Open(ctx, fluxplane.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-resume"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	summaries, err := svc.ListSessions(ctx, fluxplane.ListSessionsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries len = %d, want 1", len(summaries))
	}
	if summaries[0].Info.Thread.ID != opened.Info().Thread.ID {
		t.Fatalf("listed thread = %q, want %q", summaries[0].Info.Thread.ID, opened.Info().Thread.ID)
	}

	resumed, err := svc.Resume(ctx, fluxplane.ResumeRequest{ThreadID: opened.Info().Thread.ID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Info().Thread.ID != opened.Info().Thread.ID {
		t.Fatalf("resumed thread = %q, want %q", resumed.Info().Thread.ID, opened.Info().Thread.ID)
	}
}

func TestServiceRunsCommandFromResourceComposition(t *testing.T) {
	ctx := context.Background()
	bundle := fluxplane.ResourceBundle{
		Commands: []command.Spec{{
			Path: command.Path{"echo"},
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "echo"},
			},
			Policy: policy.InvocationPolicy{
				AllowedCallers: []policy.CallerKind{policy.CallerUser},
				RequiredTrust:  policy.TrustVerified,
			},
		}},
	}

	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := appcomposition.Compose(appcomposition.Config{
		Agent:      echoAgent{},
		Operations: []operation.Operation{echo},
		Bundles:    []fluxplane.ResourceBundle{bundle},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}

	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{
		Conversation: channel.ConversationRef{ID: "resource-app"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
}

func TestServiceRunsCommandFromPluginResourceComposition(t *testing.T) {
	ctx := context.Background()
	bundle := fluxplane.ResourceBundle{
		Plugins: []coreresource.PluginRef{{Name: "echo"}},
	}
	composition, err := appcomposition.Compose(appcomposition.Config{
		Agent:   echoAgent{},
		Plugins: []contributions.Provider{echo.New()},
		Bundles: []fluxplane.ResourceBundle{bundle},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}

	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{
		Conversation: channel.ConversationRef{ID: "plugin-resource-app"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
}

func TestServiceRunsQualifiedCommandsWithDuplicatePluginOperationNames(t *testing.T) {
	ctx := context.Background()
	composition, err := appcomposition.Compose(appcomposition.Config{
		Agent: echoAgent{},
		Plugins: []contributions.Provider{
			resourceTestPlugin{name: "foo"},
			resourceTestPlugin{name: "bar"},
		},
		Bundles: []fluxplane.ResourceBundle{{
			Plugins: []coreresource.PluginRef{{Name: "foo"}, {Name: "bar"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{
		Conversation: channel.ConversationRef{ID: "duplicate-plugin-ops"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"foo", "run"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit foo/run: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait foo/run: %v", err)
	}
	content, ok := result.Outbound.Message.Content.(map[string]any)
	if !ok {
		t.Fatalf("content = %#v, want map", result.Outbound.Message.Content)
	}
	if content["plugin"] != "foo" {
		t.Fatalf("plugin = %q, want foo", content["plugin"])
	}

	run, err = sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"run"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit run: %v", err)
	}
	result, err = run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait run: %v", err)
	}
	if result.Command == nil || result.Command.Status != session.CommandStatusFailed {
		t.Fatalf("unqualified command result = %#v, want failed ambiguity", result.Command)
	}
	if result.Command.Error == nil || result.Command.Error.Code != "command_resolution_failed" {
		t.Fatalf("unqualified command error = %#v, want command_resolution_failed", result.Command.Error)
	}
}

func TestServiceInstantiatesConfiguredLLMAgentFromComposition(t *testing.T) {
	ctx := context.Background()
	composition, err := appcomposition.Compose(appcomposition.Config{
		Bundles: []fluxplane.ResourceBundle{{
			Source: coreresource.SourceRef{Scope: coreresource.ScopeEmbedded, Location: "apps/demo"},
			Apps: []coreapp.Spec{{
				Name:         "demo",
				DefaultAgent: agent.Ref{Name: "main"},
			}},
			Agents: []agent.Spec{{
				Name:   "main",
				Driver: agent.DriverSpec{Kind: "llmagent"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		Channel:  channel.Ref{Name: "local"},
		Caller:   policy.Caller{Kind: policy.CallerUser},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		LLMModel: llmfluxplane.StaticModel{Response: llmfluxplane.MessageResponse("configured agent")},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{
		Session: fluxplane.SessionRef{Name: "default"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("hello"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "configured agent" {
		t.Fatalf("outbound = %#v, want configured agent", result.Outbound)
	}
}

func TestServiceProjectsToolsForConfiguredLLMAgent(t *testing.T) {
	ctx := context.Background()
	echo := operation.New(operation.Spec{
		Ref: operation.Ref{Name: "echo"},
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Risk:        operation.RiskLow,
		},
	}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := appcomposition.Compose(appcomposition.Config{
		Operations: []operation.Operation{echo},
		Bundles: []fluxplane.ResourceBundle{{
			Source: coreresource.SourceRef{Scope: coreresource.ScopeEmbedded, Location: "apps/demo"},
			Apps: []coreapp.Spec{{
				Name:         "demo",
				DefaultAgent: agent.Ref{Name: "main"},
			}},
			Agents: []agent.Spec{{Name: "main"}},
			Commands: []command.Spec{{
				Path: command.Path{"echo"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "echo"},
				},
				Policy: policy.InvocationPolicy{
					AllowedCallers: []policy.CallerKind{policy.CallerUser},
					RequiredTrust:  policy.TrustVerified,
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	var modelRequest llmfluxplane.Request
	svc, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		LLMModel: llmfluxplane.ModelFunc(func(_ context.Context, req llmfluxplane.Request) (llmfluxplane.Response, error) {
			modelRequest = req
			return llmfluxplane.OperationResponse(agent.OperationRequest{
				Operation: operation.Ref{Name: "echo"},
				Input:     "from-agent",
			}), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{
		Session: fluxplane.SessionRef{Name: "default"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("hello"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(modelRequest.Tools) != 1 {
		t.Fatalf("projected tools len = %d, want 1: %#v", len(modelRequest.Tools), modelRequest.Tools)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "from-agent" {
		t.Fatalf("outbound = %#v, want operation result", result.Outbound)
	}
}

func TestServiceSecurityPolicyEnforcedWithDefaultExecutor(t *testing.T) {
	ctx := context.Background()
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := appcomposition.Compose(appcomposition.Config{
		Operations: []operation.Operation{echo},
		Bundles: []fluxplane.ResourceBundle{{
			Apps: []coreapp.Spec{{
				Name: "demo",
				Security: policy.AuthorizationPolicy{Grants: []policy.Grant{{
					Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "someoneelse"}},
					Resources: []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
					Actions:   []policy.Action{policy.ActionOperationInvoke},
				}}},
			}},
			Commands: []command.Spec{{
				Path: command.Path{"echo"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "echo"},
				},
				Policy: policy.InvocationPolicy{
					AllowedCallers: []policy.CallerKind{policy.CallerUser},
					RequiredTrust:  policy.TrustVerified,
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "test-user"}},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := svc.Open(ctx, fluxplane.OpenRequest{Conversation: channel.ConversationRef{ID: "secure-command"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithCommand(command.Invocation{
		Path:  command.Path{"echo"},
		Input: "hello",
	}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Command == nil || result.Command.Status != session.CommandStatusFailed {
		t.Fatalf("command result = %#v, want failed", result.Command)
	}
	if result.Command.Effect == nil || result.Command.Effect.Result.Error == nil || result.Command.Effect.Result.Error.Code != "operation_safety_denied" {
		t.Fatalf("effect = %#v, want operation_safety_denied", result.Command.Effect)
	}
}

func assertRunEvent(t *testing.T, run fluxplane.Run, kind fluxplane.EventKind) {
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

func newTestService(t *testing.T) *fluxplane.Service {
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

	svc, err := fluxplane.New(fluxplane.Config{
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

type toolCaptureAgent struct {
	lastTools []tool.Spec
}

func (a *toolCaptureAgent) Spec() agent.Spec {
	return agent.Spec{Name: "main"}
}

func (a *toolCaptureAgent) Step(agent.Context, agent.StepInput) agent.StepResult {
	return agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: "ok"},
		},
	}
}

func (a *toolCaptureAgent) StepWithTools(_ agent.Context, _ agent.StepInput, tools []tool.Spec) agent.StepResult {
	a.lastTools = append([]tool.Spec(nil), tools...)
	return agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: "ok"},
		},
	}
}

type operationAgent struct{}

func (operationAgent) Spec() agent.Spec {
	return agent.Spec{Name: "main", Turns: agent.TurnPolicy{MaxSteps: 1}}
}

func (operationAgent) Step(agent.Context, agent.StepInput) agent.StepResult {
	return agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind: agent.DecisionOperation,
			Operations: []agent.OperationRequest{{
				Operation: operation.Ref{Name: "echo"},
				Input:     "hello",
			}},
		},
	}
}

type resourceTestPlugin struct {
	name string
}

func (p resourceTestPlugin) Manifest() contributions.Manifest {
	return contributions.Manifest{Name: p.name}
}

func (p resourceTestPlugin) Contributions(context.Context, contributions.Context) (fluxplane.ResourceBundle, error) {
	return fluxplane.ResourceBundle{
		Operations: []operation.Spec{{Ref: operation.Ref{Name: "run"}}},
		Commands: []command.Spec{{
			Path: command.Path{p.name, "run"},
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "run"},
			},
			Policy: policy.InvocationPolicy{
				AllowedCallers: []policy.CallerKind{policy.CallerUser},
				RequiredTrust:  policy.TrustVerified,
			},
		}},
	}, nil
}

func (p resourceTestPlugin) Operations(context.Context, contributions.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operation.New(operation.Spec{Ref: operation.Ref{Name: "run"}}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(map[string]any{"plugin": p.name, "input": input})
		}),
	}, nil
}
