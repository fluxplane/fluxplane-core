package agentfactory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/command"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	coreskill "github.com/fluxplane/fluxplane-core/core/skill"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/orchestration/agentconfig"
	appcomposition "github.com/fluxplane/fluxplane-core/orchestration/app"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	runtimeskill "github.com/fluxplane/fluxplane-core/runtime/skill"
	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-policy"
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

func TestFactoryAnnotatesResolvedModelSpec(t *testing.T) {
	var request llmagent.Request
	composition := testComposition(t, agent.Spec{Name: "main"})
	factory := New(Config{
		Agents:         composition.AgentCatalog,
		Resolver:       composition.Resolver,
		CommandCatalog: composition.CommandCatalog,
		ModelResolver: testModelResolverWithSpec{
			model: llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
				request = req
				return llmagent.MessageResponse("ok"), nil
			}),
			spec: corellm.ModelSpec{
				Ref:             corellm.ModelRef{Provider: "openai", Name: "gpt-test"},
				ContextTokens:   1000000,
				MaxOutputTokens: 8192,
			},
		},
	})
	runtime, err := factory.Build(context.Background(), agent.Ref{Name: "main"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if request.Agent.Inference.Annotations["llm.context_tokens"] != "1000000" {
		t.Fatalf("annotations = %#v, want context tokens", request.Agent.Inference.Annotations)
	}
	if request.Agent.Inference.Annotations["llm.max_output_tokens"] != "8192" {
		t.Fatalf("annotations = %#v, want max output tokens", request.Agent.Inference.Annotations)
	}
	if request.Agent.Inference.Model != "gpt-test" {
		t.Fatalf("model = %q, want gpt-test", request.Agent.Inference.Model)
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

func TestFilterToolsAllowsDispatchToolWhenAllCasesTargetAllowedOperations(t *testing.T) {
	imageTool := tool.Spec{
		Name: "image",
		Dispatch: &tool.Dispatch{
			ActionField: "action",
			Cases: []tool.DispatchCase{
				{Action: "generate", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_generate"}}},
				{Action: "understand", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_understand"}}},
				{Action: "info", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_providers"}}},
			},
		},
	}
	spec := agent.Spec{
		Name: "main",
		Operations: []operation.Ref{
			{Name: "image_generate"},
			{Name: "image_understand"},
			{Name: "image_providers"},
		},
	}

	filtered := agentconfig.FilterTools(spec, []tool.Spec{imageTool})

	if len(filtered) != 1 || filtered[0].Name != "image" {
		t.Fatalf("filtered tools = %#v, want image action tool", filtered)
	}
}

func TestFilterToolsAllowsOperationWildcardSelectors(t *testing.T) {
	tools := []tool.Spec{{
		Name: "gitlab_mr",
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "gitlab_mr"},
		},
	}, {
		Name: "jira_issue_search",
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "jira_issue_search"},
		},
	}}
	spec := agent.Spec{
		Name:       "main",
		Operations: []operation.Ref{{Name: "gitlab_*"}},
	}

	filtered := agentconfig.FilterTools(spec, tools)

	if len(filtered) != 1 || filtered[0].Name != "gitlab_mr" {
		t.Fatalf("filtered tools = %#v, want only gitlab tool", filtered)
	}
}

func TestFilterToolsAllowsDispatchToolWithWildcardSelector(t *testing.T) {
	gitlabTool := tool.Spec{
		Name: "gitlab",
		Dispatch: &tool.Dispatch{
			ActionField: "action",
			Cases: []tool.DispatchCase{
				{Action: "mr", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "gitlab_mr"}}},
				{Action: "commit", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "gitlab_commit"}}},
			},
		},
	}
	spec := agent.Spec{
		Name:       "main",
		Operations: []operation.Ref{{Name: "gitlab_*"}},
	}

	filtered := agentconfig.FilterTools(spec, []tool.Spec{gitlabTool})

	if len(filtered) != 1 || filtered[0].Name != "gitlab" {
		t.Fatalf("filtered tools = %#v, want gitlab dispatch tool", filtered)
	}
}

func TestFilterToolsRejectsDispatchToolWhenAnyCaseIsNotAllowed(t *testing.T) {
	imageTool := tool.Spec{
		Name: "image",
		Dispatch: &tool.Dispatch{
			ActionField: "action",
			Cases: []tool.DispatchCase{
				{Action: "generate", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_generate"}}},
				{Action: "understand", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_understand"}}},
			},
		},
	}
	spec := agent.Spec{
		Name:       "main",
		Operations: []operation.Ref{{Name: "image_generate"}},
	}

	filtered := agentconfig.FilterTools(spec, []tool.Spec{imageTool})

	if len(filtered) != 0 {
		t.Fatalf("filtered tools = %#v, want action tool rejected", filtered)
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
			testContextProvider{name: "runtime", auto: true},
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
	if !hasContextProvider(request.Context, "docs") {
		t.Fatalf("context = %#v, want docs", request.Context)
	}
	if hasContextProvider(request.Context, "repo") {
		t.Fatalf("context = %#v, did not want repo", request.Context)
	}
	if !hasContextProvider(request.Context, "runtime") {
		t.Fatalf("context = %#v, want auto runtime provider", request.Context)
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

type testModelResolverWithSpec struct {
	model llmagent.Model
	spec  corellm.ModelSpec
}

func (r testModelResolverWithSpec) ResolveModel(context.Context, agent.Spec) (llmagent.Model, error) {
	return r.model, nil
}

func (r testModelResolverWithSpec) ResolveModelWithSpec(context.Context, agent.Spec) (ModelResolution, error) {
	return ModelResolution{Model: r.model, Spec: r.spec}, nil
}

type testContextProvider struct {
	name corecontext.ProviderName
	auto bool
}

func (p testContextProvider) Spec() corecontext.ProviderSpec {
	spec := corecontext.ProviderSpec{Name: p.name}
	if p.auto {
		spec.Annotations = map[string]string{corecontext.AnnotationAutoContext: "true"}
	}
	return spec
}

func (p testContextProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	return []corecontext.Block{{Provider: p.name, Content: string(p.name)}}, nil
}
