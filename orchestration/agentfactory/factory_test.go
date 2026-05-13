package agentfactory

import (
	"context"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	appcomposition "github.com/fluxplane/agentruntime/orchestration/app"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

func TestFactoryBuildsLLMAgentWithProjectedTools(t *testing.T) {
	var request llmagent.Request
	composition := testComposition(t, agent.Spec{Name: "main"})
	factory := New(Config{
		Agents:           composition.AgentCatalog,
		Resolver:         composition.Resolver,
		CommandCatalog:   composition.CommandCatalog,
		OperationCatalog: composition.OperationCatalog,
		Model: llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
			request = req
			return llmagent.MessageResponse("ok"), nil
		}),
	})
	runtime, err := factory.Build(context.Background(), agent.Ref{Name: "main"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(request.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(request.Tools))
	}
}

func TestFactoryRespectsAgentToolNarrowing(t *testing.T) {
	var request llmagent.Request
	composition := testComposition(t, agent.Spec{
		Name:     "main",
		Commands: []agent.CommandRef{{Name: "missing"}},
	})
	factory := New(Config{
		Agents:           composition.AgentCatalog,
		Resolver:         composition.Resolver,
		CommandCatalog:   composition.CommandCatalog,
		OperationCatalog: composition.OperationCatalog,
		Model: llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
			request = req
			return llmagent.MessageResponse("ok"), nil
		}),
	})
	runtime, err := factory.Build(context.Background(), agent.Ref{Name: "main"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(request.Tools) != 0 {
		t.Fatalf("tools len = %d, want 0: %#v", len(request.Tools), request.Tools)
	}
}

func TestFactoryAppliesSessionCommandNarrowing(t *testing.T) {
	var request llmagent.Request
	composition := testComposition(t, agent.Spec{Name: "main"})
	factory := New(Config{
		Agents:           composition.AgentCatalog,
		Resolver:         composition.Resolver,
		CommandCatalog:   composition.CommandCatalog,
		OperationCatalog: composition.OperationCatalog,
		Model: llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
			request = req
			return llmagent.MessageResponse("ok"), nil
		}),
	})
	runtime, err := factory.AgentForSession(context.Background(), coresession.Spec{
		Name:     "worker",
		Agent:    agent.Ref{Name: "main"},
		Commands: []command.Path{{"missing"}},
	})
	if err != nil {
		t.Fatalf("AgentForSession: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(request.Tools) != 0 {
		t.Fatalf("tools len = %d, want 0: %#v", len(request.Tools), request.Tools)
	}
}

func TestFactoryAppliesSessionContextNarrowing(t *testing.T) {
	var request llmagent.Request
	composition := testComposition(t, agent.Spec{Name: "main"})
	factory := New(Config{
		Agents:           composition.AgentCatalog,
		Resolver:         composition.Resolver,
		CommandCatalog:   composition.CommandCatalog,
		OperationCatalog: composition.OperationCatalog,
		ContextProviders: []corecontext.Provider{
			testContextProvider{name: "docs"},
			testContextProvider{name: "repo"},
		},
		Model: llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
			request = req
			return llmagent.MessageResponse("ok"), nil
		}),
	})
	runtime, err := factory.AgentForSession(context.Background(), coresession.Spec{
		Name:    "worker",
		Agent:   agent.Ref{Name: "main"},
		Context: []corecontext.ProviderRef{{Name: "docs"}},
	})
	if err != nil {
		t.Fatalf("AgentForSession: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(request.Context) != 1 || request.Context[0].Provider != "docs" {
		t.Fatalf("context = %#v, want docs only", request.Context)
	}
}

func testComposition(t *testing.T, spec agent.Spec) appcomposition.Composition {
	t.Helper()
	echo := operation.New(operation.Spec{
		Ref: operation.Ref{Name: "echo"},
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Risk:        operation.RiskLow,
		},
	}, func(operation.Context, operation.Value) operation.Result {
		return operation.OK(nil)
	})
	composition, err := appcomposition.Compose(appcomposition.Config{
		Operations: []operation.Operation{echo},
		Bundles: []resource.ContributionBundle{{
			Agents: []agent.Spec{spec},
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
	return composition
}

type testAgentContext struct{}

func (testAgentContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (testAgentContext) Done() <-chan struct{} { return nil }

func (testAgentContext) Err() error { return nil }

func (testAgentContext) Value(any) any { return nil }

func (testAgentContext) Events() event.Sink { return event.Discard() }

type testContextProvider struct {
	name corecontext.ProviderName
}

func (p testContextProvider) Spec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{Name: p.name}
}

func (p testContextProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	return []corecontext.Block{{Provider: p.name, Content: string(p.name)}}, nil
}
