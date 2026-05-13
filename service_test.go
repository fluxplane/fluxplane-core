package agentruntime_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/resourcefs"
	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	coreresource "github.com/fluxplane/agentruntime/core/resource"
	appcomposition "github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/plugins/echoplugin"
	llmagentruntime "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

func TestServiceSubmitInputThroughTopLevelAPI(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	sessionHandle, err := svc.Open(ctx, agentruntime.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-input"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithText("hello"))
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
	assertRunEvent(t, run, agentruntime.EventInputCompleted)
}

func TestServiceSubmitCommandThroughTopLevelAPI(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	sessionHandle, err := svc.Open(ctx, agentruntime.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-command"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithCommand(command.Invocation{
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

func TestServiceRunsCommandFromResourceComposition(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	manifest := []byte(`{
  "commands": [
    {
      "path": ["echo"],
      "operation": "echo",
      "policy": {
        "allowed_callers": ["user"],
        "required_trust": "verified"
      }
    }
  ]
}`)
	if err := os.WriteFile(filepath.Join(dir, resourcefs.DefaultManifestName), manifest, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	bundle, err := resourcefs.LoadDir(ctx, dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := appcomposition.Compose(appcomposition.Config{
		Agent:      echoAgent{},
		Operations: []operation.Operation{echo},
		Bundles:    []agentruntime.ResourceBundle{bundle},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}

	sessionHandle, err := svc.Open(ctx, agentruntime.OpenRequest{
		Conversation: channel.ConversationRef{ID: "resource-app"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
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
	dir := t.TempDir()
	manifest := []byte(`{
  "plugins": [
    {"name": "echo"}
  ]
}`)
	if err := os.WriteFile(filepath.Join(dir, resourcefs.DefaultManifestName), manifest, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	bundle, err := resourcefs.LoadDir(ctx, dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	composition, err := appcomposition.Compose(appcomposition.Config{
		Agent:   echoAgent{},
		Plugins: []pluginhost.Plugin{echoplugin.New()},
		Bundles: []agentruntime.ResourceBundle{bundle},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}

	sessionHandle, err := svc.Open(ctx, agentruntime.OpenRequest{
		Conversation: channel.ConversationRef{ID: "plugin-resource-app"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
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
		Plugins: []pluginhost.Plugin{
			resourceTestPlugin{name: "foo"},
			resourceTestPlugin{name: "bar"},
		},
		Bundles: []agentruntime.ResourceBundle{{
			Plugins: []coreresource.PluginRef{{Name: "foo"}, {Name: "bar"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	svc, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := svc.Open(ctx, agentruntime.OpenRequest{
		Conversation: channel.ConversationRef{ID: "duplicate-plugin-ops"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"foo", "run"}, Input: "hello"}))
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

	run, err = sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"run"}, Input: "hello"}))
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
		Bundles: []agentruntime.ResourceBundle{{
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
	svc, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		Channel:  channel.Ref{Name: "local"},
		Caller:   policy.Caller{Kind: policy.CallerUser},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		LLMModel: llmagentruntime.StaticModel{Response: llmagentruntime.MessageResponse("configured agent")},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := svc.Open(ctx, agentruntime.OpenRequest{
		Session: agentruntime.SessionRef{Name: "default"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithText("hello"))
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
		Bundles: []agentruntime.ResourceBundle{{
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
					AllowedCallers: []policy.CallerKind{policy.CallerAgent},
					RequiredTrust:  policy.TrustVerified,
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	var modelRequest llmagentruntime.Request
	svc, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		Channel: channel.Ref{Name: "local"},
		Caller:  policy.Caller{Kind: policy.CallerUser},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		LLMModel: llmagentruntime.ModelFunc(func(_ context.Context, req llmagentruntime.Request) (llmagentruntime.Response, error) {
			modelRequest = req
			return llmagentruntime.OperationResponse(agent.OperationRequest{
				Operation: operation.Ref{Name: "echo"},
				Input:     "from-agent",
			}), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := svc.Open(ctx, agentruntime.OpenRequest{
		Session: agentruntime.SessionRef{Name: "default"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithText("hello"))
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

type resourceTestPlugin struct {
	name string
}

func (p resourceTestPlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: p.name}
}

func (p resourceTestPlugin) Contributions(context.Context, pluginhost.Context) (agentruntime.ResourceBundle, error) {
	return agentruntime.ResourceBundle{
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

func (p resourceTestPlugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operation.New(operation.Spec{Ref: operation.Ref{Name: "run"}}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(map[string]any{"plugin": p.name, "input": input})
		}),
	}, nil
}
