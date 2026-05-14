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
	if bundle.Agents[0].Turns.MaxSteps != 50 {
		t.Fatalf("turns = %#v, want max_steps 50", bundle.Agents[0].Turns)
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

func TestBuildAgentContinuationHelpersProduceValidSpecs(t *testing.T) {
	capped := BuildAgent("main").
		WithMaxContinuations(3).
		Build()
	if err := capped.Validate(); err != nil {
		t.Fatalf("capped Validate: %v", err)
	}
	if capped.Turns.Continuation.StopCondition.Type != "max-continuations" || capped.Turns.Continuation.StopCondition.Max != 3 {
		t.Fatalf("stop condition = %#v, want max-continuations 3", capped.Turns.Continuation.StopCondition)
	}

	prompt := BuildAgent("reviewer").
		WithMaxContinuations(5).
		WithPromptStopCondition("Stop when reviewed.").
		WithContinuationContextPolicy("summary").
		Build()
	if err := prompt.Validate(); err != nil {
		t.Fatalf("prompt Validate: %v", err)
	}
	if prompt.Turns.Continuation.StopCondition.Type != "prompt" || prompt.Turns.Continuation.StopCondition.Prompt != "Stop when reviewed." {
		t.Fatalf("stop condition = %#v, want prompt", prompt.Turns.Continuation.StopCondition)
	}
	if prompt.Turns.Continuation.ContextPolicy != "summary" {
		t.Fatalf("context policy = %q, want summary", prompt.Turns.Continuation.ContextPolicy)
	}
}
