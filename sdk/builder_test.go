package sdk

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	coresession "github.com/fluxplane/agentruntime/core/session"
)

func TestBuildAppContribution(t *testing.T) {
	op := BuildOperation("lookup").
		WithDescription("Look up a value.").
		WithInputJSONSchema("LookupInput", "Lookup request.", `{"type":"object"}`).
		WithOutput("LookupOutput").
		WithEffects(operation.EffectReadExternal).
		WithRisk(operation.RiskLow).
		Build()
	agentSpec := BuildAgent("main").
		AsLLMAgent("gpt-test").
		WithSystem("Be useful.").
		WithMaxSteps(50).
		WithMaxContinuations(3).
		WithOperation("lookup").
		Build()

	bundle := NewApp("demo").
		WithModel("openai", "gpt-test", "test").
		WithDefaultAgent(agentSpec).
		WithOperation(op).
		WithCommandForOperation("lookup", op).
		WithDefaultSession(coresession.Spec{Name: "default", Agent: agent.Ref{Name: "main"}}).
		Build()

	if len(bundle.Apps) != 1 || bundle.Apps[0].DefaultAgent.Name != "main" {
		t.Fatalf("apps = %#v, want default agent main", bundle.Apps)
	}
	if len(bundle.Agents) != 1 || bundle.Agents[0].Driver.Kind != "llmagent" {
		t.Fatalf("agents = %#v, want one llmagent", bundle.Agents)
	}
	if bundle.Agents[0].Policy.MaxSteps != 50 || bundle.Agents[0].Policy.MaxContinuations != 3 {
		t.Fatalf("policy = %#v, want max_steps 50 and max_continuations 3", bundle.Agents[0].Policy)
	}
	if len(bundle.Commands) != 1 || bundle.Commands[0].Path.String() != "/lookup" {
		t.Fatalf("commands = %#v, want /lookup", bundle.Commands)
	}
	if got := bundle.Commands[0].Policy.RequiredTrust; got != policy.TrustVerified {
		t.Fatalf("required trust = %q, want verified", got)
	}
	if len(bundle.Sessions) != 1 || bundle.Apps[0].DefaultSession.Name != "default" {
		t.Fatalf("sessions = %#v app = %#v, want default session", bundle.Sessions, bundle.Apps[0])
	}
}
