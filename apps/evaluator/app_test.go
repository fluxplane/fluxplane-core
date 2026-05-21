package evaluator

import (
	"context"
	"net/http/httptest"
	"testing"

	fluxplane "github.com/fluxplane/engine"
	"github.com/fluxplane/engine/adapters/channels/httpsse"
	"github.com/fluxplane/engine/core/agent"
	"github.com/fluxplane/engine/core/channel"
	coreevent "github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
	coreusage "github.com/fluxplane/engine/core/usage"
	llmagent "github.com/fluxplane/engine/runtime/agent/llmagent"
)

func TestDistributionDeclaresInteractiveSurfaces(t *testing.T) {
	dist := Distribution()
	if dist.Spec.Name != AppName {
		t.Fatalf("distribution name = %q, want %q", dist.Spec.Name, AppName)
	}
	if !dist.Spec.Surfaces.CLI || !dist.Spec.Surfaces.REPL || !dist.Spec.Surfaces.OneShot || !dist.Spec.Surfaces.Serve {
		t.Fatalf("surfaces = %#v, want cli/repl/one-shot/serve", dist.Spec.Surfaces)
	}
	if dist.Spec.DefaultSession.Name != SessionName {
		t.Fatalf("default session = %#v", dist.Spec.DefaultSession)
	}
	if dist.Spec.DefaultModel.Provider != "codex" {
		t.Fatalf("default provider = %q, want codex", dist.Spec.DefaultModel.Provider)
	}
}

func TestBundleDeclaresEvaluatorAgentAndTargetSubmit(t *testing.T) {
	bundle := Bundle()
	if len(bundle.Apps) != 1 || bundle.Apps[0].Name != AppName {
		t.Fatalf("apps = %#v", bundle.Apps)
	}
	if len(bundle.Agents) != 1 || bundle.Agents[0].Name != AgentName {
		t.Fatalf("agents = %#v", bundle.Agents)
	}
	if bundle.Agents[0].Agency.Autonomy != agent.AutonomyAutonomous {
		t.Fatalf("autonomy = %q, want autonomous", bundle.Agents[0].Agency.Autonomy)
	}
	if len(bundle.Operations) != 1 || bundle.Operations[0].Ref.Name != TargetSubmitOperation {
		t.Fatalf("operations = %#v", bundle.Operations)
	}
	if len(bundle.Plugins) != 1 || bundle.Plugins[0].Name != AppName {
		t.Fatalf("plugins = %#v, want evaluator plugin ref", bundle.Plugins)
	}
}

func TestDistributionHasRuntime(t *testing.T) {
	dist := Distribution()
	if dist.Runtime == nil {
		t.Fatalf("distribution runtime is nil")
	}
}

func TestTargetSubmitOperationUsesHTTPSSERemoteClient(t *testing.T) {
	ctx := operation.NewContext(context.Background(), coreevent.Discard())
	service, err := fluxplane.New(fluxplane.Config{
		Agent:   targetEchoAgent{},
		Channel: channel.Ref{Name: "http"},
		Caller:  policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "eval-test"}},
		Trust:   policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("New runtime: %v", err)
	}
	handler, err := httpsse.NewServer(httpsse.ServerConfig{Client: service})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	result := targetSubmit(ctx, TargetSubmitInput{
		BaseURL:      httpServer.URL,
		Session:      "",
		Conversation: "eval-target",
		Prompt:       "hello",
		Timeout:      "5s",
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("targetSubmit status = %s error=%#v output=%#v", result.Status, result.Error, result.Output)
	}
	out, ok := result.Output.(TargetSubmitOutput)
	if !ok {
		t.Fatalf("output = %T", result.Output)
	}
	if out.ThreadID == "" || out.RunID == "" {
		t.Fatalf("target ids missing: %#v", out)
	}
	if out.OutboundText != "target: hello" {
		t.Fatalf("outbound text = %q, want target: hello", out.OutboundText)
	}
	if len(out.Events) == 0 {
		t.Fatalf("events empty")
	}
}

type targetEchoAgent struct{}

func (targetEchoAgent) Spec() agent.Spec { return agent.Spec{Name: "target-echo"} }

func (targetEchoAgent) Step(ctx agent.Context, input agent.StepInput) agent.StepResult {
	var content any = "target:"
	if len(input.Observations) > 0 {
		content = "target: " + input.Observations[0].Content.(string)
	}
	ctx.Events().Emit(coreusage.Recorded{
		Source:       "target-test",
		Subject:      coreusage.Subject{Kind: coreusage.SubjectLLM, Name: "scripted"},
		Measurements: []coreusage.Measurement{{Metric: coreusage.MetricLLMInputTokens, Quantity: 2, Unit: coreusage.UnitToken}},
	})
	ctx.Events().Emit(llmagent.ModelCompleted{Agent: "target-echo", Model: "scripted", Decision: agent.DecisionMessage})
	return agent.StepResult{Status: agent.StatusOK, Decision: agent.Decision{Kind: agent.DecisionMessage, Message: &agent.Message{Content: content}}}
}
