package agentfactory

import (
	"context"
	"strings"
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
	coreskill "github.com/fluxplane/agentruntime/core/skill"
	appcomposition "github.com/fluxplane/agentruntime/orchestration/app"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	runtimeskill "github.com/fluxplane/agentruntime/runtime/skill"
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

func TestFactorySkillsContextFollowsAgentAllowList(t *testing.T) {
	tests := []struct {
		name        string
		contextRefs []corecontext.ProviderRef
		wantSkills  bool
	}{
		{name: "nil context includes skills", contextRefs: nil, wantSkills: true},
		{name: "explicit context omits skills", contextRefs: []corecontext.ProviderRef{{Name: "docs"}}, wantSkills: false},
		{name: "explicit context includes skills", contextRefs: []corecontext.ProviderRef{{Name: runtimeskill.ContextProviderName}}, wantSkills: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request llmagent.Request
			spec := agent.Spec{
				Name:    "main",
				Skills:  []coreskill.Ref{{Name: "architecture"}},
				Context: tt.contextRefs,
			}
			composition := testCompositionWithSkills(t, []resource.ContributionBundle{{
				Agents: []agent.Spec{spec},
				Skills: []coreskill.Spec{{
					Name:        "architecture",
					Description: "Architecture guidance.",
				}},
			}})
			factory := New(Config{
				Agents:         composition.AgentCatalog,
				Skills:         composition.SkillCatalog,
				Resolver:       composition.Resolver,
				CommandCatalog: composition.CommandCatalog,
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
			gotSkills := hasContextProvider(request.Context, runtimeskill.ContextProviderName)
			if gotSkills != tt.wantSkills {
				t.Fatalf("skills context present = %t, want %t; context = %#v", gotSkills, tt.wantSkills, request.Context)
			}
			if _, ok := runtimeskill.StateFromAgent(runtime); !ok {
				t.Fatalf("skill state missing from runtime")
			}
		})
	}
}

func TestFactoryDuplicateSkillsUseResolverPrecedence(t *testing.T) {
	var request llmagent.Request
	composition := testCompositionWithSkills(t, []resource.ContributionBundle{
		{
			Source: resource.SourceRef{Scope: resource.ScopeUser, Location: "global"},
			Agents: []agent.Spec{{
				Name:   "main",
				Skills: []coreskill.Ref{{Name: "architecture"}},
			}},
			Skills: []coreskill.Spec{{
				Name:        "architecture",
				Description: "User architecture.",
			}},
		},
		{
			Source: resource.SourceRef{Scope: resource.ScopeProject, Location: "/repo/.agents"},
			Skills: []coreskill.Spec{{
				Name:        "architecture",
				Description: "Local architecture.",
			}},
		},
	})
	factory := New(Config{
		Agents:         composition.AgentCatalog,
		Skills:         composition.SkillCatalog,
		Resolver:       composition.Resolver,
		CommandCatalog: composition.CommandCatalog,
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
	var content string
	for _, block := range request.Context {
		content += block.Content
	}
	if !strings.Contains(content, "Local architecture.") || strings.Contains(content, "User architecture.") {
		t.Fatalf("context content = %q, want local skill winner", content)
	}
}

func TestFactoryDuplicateSkillsAmbiguousErrors(t *testing.T) {
	composition := testCompositionWithSkills(t, []resource.ContributionBundle{
		{
			Source: resource.SourceRef{Scope: resource.ScopeProject, Location: "/repo/one/.agents"},
			Agents: []agent.Spec{{
				Name:   "main",
				Skills: []coreskill.Ref{{Name: "architecture"}},
			}},
			Skills: []coreskill.Spec{{Name: "architecture"}},
		},
		{
			Source: resource.SourceRef{Scope: resource.ScopeProject, Location: "/repo/two/.agents"},
			Skills: []coreskill.Spec{{Name: "architecture"}},
		},
	})
	factory := New(Config{
		Agents:         composition.AgentCatalog,
		Skills:         composition.SkillCatalog,
		Resolver:       composition.Resolver,
		CommandCatalog: composition.CommandCatalog,
		Model:          llmagent.StaticModel{Response: llmagent.MessageResponse("ok")},
	})
	_, err := factory.Build(context.Background(), agent.Ref{Name: "main"})
	if err == nil {
		t.Fatal("Build error is nil, want ambiguous skill error")
	}
	if !strings.Contains(err.Error(), "ambiguous skill") {
		t.Fatalf("Build error = %v, want ambiguous skill", err)
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

func testCompositionWithSkills(t *testing.T, bundles []resource.ContributionBundle) appcomposition.Composition {
	t.Helper()
	composition, err := appcomposition.Compose(appcomposition.Config{Bundles: bundles})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return composition
}

func hasContextProvider(blocks []corecontext.Block, name corecontext.ProviderName) bool {
	for _, block := range blocks {
		if block.Provider == name {
			return true
		}
	}
	return false
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
