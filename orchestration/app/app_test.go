package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/core/workflow"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
)

func TestComposeRegistersResourceCommandsAgainstProvidedOperations(t *testing.T) {
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := Compose(Config{
		Operations: []operation.Operation{echo},
		Bundles: []resource.ContributionBundle{{
			Commands: []command.Spec{{
				Path: command.Path{"echo"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "echo"},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, ok := composition.Commands.Resolve(command.Path{"echo"}); !ok {
		t.Fatal("command was not registered")
	}
	if op, ok := composition.Operations.Resolve(operation.Ref{Name: "echo"}); !ok || op == nil {
		t.Fatal("operation was not registered")
	}
}

func TestComposeBuildsIdentityResolverFromAppIdentity(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Apps: []coreapp.Spec{{
				Name: "demo",
				Identity: coreapp.IdentitySpec{
					Users: []user.User{{
						ID:         "timo@company.org",
						Identities: []user.Identity{{Provider: "slack", ProviderID: "U123"}},
					}},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if composition.IdentityResolver == nil {
		t.Fatal("IdentityResolver is nil, want directory resolver")
	}
}

func TestComposeBuildsIdentityResolverFromIdentityRules(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Apps: []coreapp.Spec{{
				Name: "demo",
				Identity: coreapp.IdentitySpec{
					Rules: []user.GroupRule{{
						Match:  user.IdentityMatch{Provider: "slack", Resolution: user.ResolutionUnresolved},
						Groups: []user.ID{"anonymous"},
					}},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if composition.IdentityResolver == nil {
		t.Fatal("IdentityResolver is nil, want directory resolver for rules-only identity config")
	}
}

func TestComposeIndexesLLMProviderContributions(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			LLMProviders: []corellm.ProviderSpec{{
				Name: "openai",
				Models: []corellm.ModelSpec{{
					Ref: corellm.ModelRef{Name: "gpt-test"},
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.LLMProviderSpecs) != 1 {
		t.Fatalf("LLMProviderSpecs len = %d, want 1", len(composition.LLMProviderSpecs))
	}
	if len(composition.LLMProviderCatalog) != 1 {
		t.Fatalf("LLMProviderCatalog len = %d, want 1", len(composition.LLMProviderCatalog))
	}
}

func TestComposeIndexesLLMModelAliasContributions(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			LLMModelAliases: []corellm.ModelAliasSpec{{
				Name:   "codex",
				Target: corellm.ModelRef{Provider: "codex", Name: "gpt-5.5"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.LLMModelAliases) != 1 {
		t.Fatalf("LLMModelAliases len = %d, want 1", len(composition.LLMModelAliases))
	}
	if len(composition.LLMModelAliasCatalog) != 1 {
		t.Fatalf("LLMModelAliasCatalog len = %d, want 1", len(composition.LLMModelAliasCatalog))
	}
}

func TestComposeRejectsCommandTargetingUnknownOperation(t *testing.T) {
	_, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Commands: []command.Spec{{
				Path: command.Path{"missing"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "missing"},
				},
			}},
		}},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want unknown operation error")
	}
}

func TestComposeBuildsEventRegistryFromPluginContributions(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "event-plugin"}},
		}},
		Plugins: []pluginhost.Plugin{eventPlugin{}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if composition.EventRegistry == nil {
		t.Fatal("event registry is nil")
	}
	decoded, ok, err := composition.EventRegistry.TryDecode(testPluginEvent{}.EventName(), json.RawMessage(`{"value":"ok"}`))
	if err != nil {
		t.Fatalf("TryDecode: %v", err)
	}
	if !ok {
		t.Fatal("plugin event was not registered")
	}
	got, ok := decoded.(testPluginEvent)
	if !ok || got.Value != "ok" {
		t.Fatalf("decoded = %#v, want testPluginEvent ok", decoded)
	}
}

func TestNewEventRegistryRejectsDuplicateEventNames(t *testing.T) {
	_, err := eventregistry.New(eventregistry.Config{
		EventTypes: []coreevent.Event{testPluginEvent{}, testPluginEvent{}},
	})
	if err == nil {
		t.Fatal("NewEventRegistry error is nil, want duplicate event name")
	}
}

func TestComposeRejectsCommandTargetingDeclarationOnlyOperation(t *testing.T) {
	_, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Source:     resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/spec-only"},
			Operations: []operation.Spec{{Ref: operation.Ref{Name: "declared"}}},
			Commands: []command.Spec{{
				Path: command.Path{"declared"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "declared"},
				},
			}},
		}},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want declaration-only operation error")
	}
}

func TestComposeIndexesAppResourceSpecsAndDefaultSession(t *testing.T) {
	source := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "apps/engineer/resources/.agents"}
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Source: source,
			Apps: []coreapp.Spec{{
				Name:         "engineer",
				DefaultAgent: agent.Ref{Name: "main"},
			}},
			Agents: []agent.Spec{{
				Name:   "main",
				System: "You maintain this repository.",
				Skills: []skill.Ref{{
					Name: "golang-pro",
				}},
				Context: []corecontext.ProviderRef{{
					Name: "repo",
				}},
			}},
			Skills: []skill.Spec{{
				Name:        "golang-pro",
				Description: "Go engineering guidance",
			}},
			ContextProviders: []corecontext.ProviderSpec{{
				Name:  "repo",
				Kinds: []corecontext.BlockKind{corecontext.BlockText},
			}},
			Workflows: []workflow.Spec{{
				Name: "feature",
				Steps: []workflow.Step{{
					ID:    "design",
					Kind:  workflow.StepAgent,
					Agent: agent.Ref{Name: "main"},
				}},
			}},
			Commands: []command.Spec{
				{
					Path: command.Path{"review"},
					Target: invocation.Target{
						Kind:   invocation.TargetPrompt,
						Prompt: "Review the current change.",
					},
				},
				{
					Path: command.Path{"feat"},
					Target: invocation.Target{
						Kind:     invocation.TargetWorkflow,
						Workflow: "feature",
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.AppCatalog) != 1 {
		t.Fatalf("app catalog len = %d, want 1", len(composition.AppCatalog))
	}
	if len(composition.AgentCatalog) != 1 {
		t.Fatalf("agent catalog len = %d, want 1", len(composition.AgentCatalog))
	}
	if len(composition.SkillCatalog) != 1 {
		t.Fatalf("skill catalog len = %d, want 1", len(composition.SkillCatalog))
	}
	if len(composition.ContextProviders) != 1 {
		t.Fatalf("context provider catalog len = %d, want 1", len(composition.ContextProviders))
	}
	if len(composition.WorkflowCatalog) != 1 {
		t.Fatalf("workflow catalog len = %d, want 1", len(composition.WorkflowCatalog))
	}
	if _, ok := composition.Commands.Resolve(command.Path{"review"}); !ok {
		t.Fatal("prompt command was not registered")
	}
	if _, ok := composition.Commands.Resolve(command.Path{"feat"}); !ok {
		t.Fatal("workflow command was not registered")
	}
	workflowID, err := composition.Resolver.Resolve("workflow", "feature")
	if err != nil {
		t.Fatalf("Resolve feature: %v", err)
	}
	commandID, err := composition.Resolver.Resolve("command", "feat")
	if err != nil {
		t.Fatalf("Resolve feat: %v", err)
	}
	binding := composition.CommandCatalog[commandID.Address()]
	if !binding.TargetID.Equal(workflowID) {
		t.Fatalf("workflow command target = %s, want %s", binding.TargetID.Address(), workflowID.Address())
	}
	sessionID, err := composition.Resolver.Resolve("session", "default")
	if err != nil {
		t.Fatalf("Resolve default: %v", err)
	}
	sessionBinding := composition.SessionCatalog[sessionID.Address()]
	if sessionBinding.Spec.Name != "default" {
		t.Fatalf("default session name = %q, want default", sessionBinding.Spec.Name)
	}
	if sessionBinding.Spec.Agent.Name != "main" {
		t.Fatalf("default session agent = %q, want main", sessionBinding.Spec.Agent.Name)
	}
}

func TestComposeRejectsCommandTargetingUnknownWorkflow(t *testing.T) {
	_, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Commands: []command.Spec{{
				Path: command.Path{"feat"},
				Target: invocation.Target{
					Kind:     invocation.TargetWorkflow,
					Workflow: "missing",
				},
			}},
		}},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want unknown workflow error")
	}
}

func TestComposeRejectsAppDefaultAgentWhenUnbound(t *testing.T) {
	_, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Apps: []coreapp.Spec{{
				Name:         "demo",
				DefaultAgent: agent.Ref{Name: "missing"},
			}},
		}},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want default agent resolution error")
	}
}

func TestComposeRejectsAppDefaultSessionWhenUnbound(t *testing.T) {
	_, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Apps: []coreapp.Spec{{
				Name:           "demo",
				DefaultSession: coresession.Ref{Name: "missing"},
			}},
		}},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want default session resolution error")
	}
}

func TestComposeResolvesPluginContributions(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{echoPlugin{}},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "echo-plugin"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, ok := composition.Commands.Resolve(command.Path{"echo"}); !ok {
		t.Fatal("plugin command was not registered")
	}
	if op, ok := composition.Operations.Resolve(operation.Ref{Name: "echo"}); !ok || op == nil {
		t.Fatal("plugin operation was not registered")
	}
}

func TestComposeResolvesConfiguredPluginContributions(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{textplugin.New()},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{
				Name: textplugin.Name,
				Config: map[string]any{
					"operations": []any{"upper"},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, ok := composition.Commands.Resolve(command.Path{"text", "upper"}); ok {
		t.Fatal("configured plugin operation was exposed as a command")
	}
	if _, ok := composition.Commands.Resolve(command.Path{"text", "lower"}); ok {
		t.Fatal("unconfigured plugin command was registered")
	}
	if op, ok := composition.Operations.Resolve(operation.Ref{Name: "upper"}); !ok || op == nil {
		t.Fatal("configured plugin operation was not registered")
	}
	if len(composition.OperationSpecs) != 1 {
		t.Fatalf("operation specs len = %d, want 1", len(composition.OperationSpecs))
	}
	if composition.OperationSpecs[0].Ref.Name != "upper" {
		t.Fatalf("operation spec = %#v, want upper", composition.OperationSpecs[0].Ref)
	}
	id, err := composition.Resolver.Resolve("operation", "text:upper")
	if err != nil {
		t.Fatalf("Resolve text:upper: %v", err)
	}
	if got, want := id.Address(), "embedded:plugins/text:upper"; got != want {
		t.Fatalf("resolved operation = %q, want %q", got, want)
	}
}

func TestComposeAllowsDuplicateOperationNamesAcrossResourceIDs(t *testing.T) {
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := Compose(Config{
		Operations: []operation.Operation{echo},
		Plugins:    []pluginhost.Plugin{echoPlugin{}},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "echo-plugin"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.OperationCatalog) != 2 {
		t.Fatalf("operation catalog len = %d, want 2", len(composition.OperationCatalog))
	}
	id, err := composition.Resolver.Resolve("operation", "echo-plugin:echo")
	if err != nil {
		t.Fatalf("Resolve echo-plugin:echo: %v", err)
	}
	if got, want := id.Address(), "embedded:plugins/echo-plugin:echo"; got != want {
		t.Fatalf("resolved operation = %q, want %q", got, want)
	}
}

func TestComposeAllowsDuplicateCommandPathAcrossResourceIDs(t *testing.T) {
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	sourceA := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/a"}
	sourceB := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/b"}
	composition, err := Compose(Config{
		Operations: []operation.Operation{echo},
		Bundles: []resource.ContributionBundle{
			{
				Source: sourceA,
				Commands: []command.Spec{{
					Path: command.Path{"echo"},
					Target: invocation.Target{
						Kind:      invocation.TargetOperation,
						Operation: operation.Ref{Name: "echo"},
					},
				}},
			},
			{
				Source: sourceB,
				Commands: []command.Spec{{
					Path: command.Path{"echo"},
					Target: invocation.Target{
						Kind:      invocation.TargetOperation,
						Operation: operation.Ref{Name: "echo"},
					},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.CommandCatalog) != 2 {
		t.Fatalf("command catalog len = %d, want 2", len(composition.CommandCatalog))
	}
	id, err := composition.Resolver.Resolve("command", "a:echo")
	if err != nil {
		t.Fatalf("Resolve a:echo: %v", err)
	}
	if got, want := id.Address(), "embedded:plugins/a:echo"; got != want {
		t.Fatalf("resolved command = %q, want %q", got, want)
	}
}

func TestComposeBindsPluginCommandToSiblingOperationWithSameShortName(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{
			sameNamePlugin{name: "foo"},
			sameNamePlugin{name: "bar"},
		},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "foo"}, {Name: "bar"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.OperationCatalog) != 2 {
		t.Fatalf("operation catalog len = %d, want 2", len(composition.OperationCatalog))
	}
	fooOp, err := composition.Resolver.Resolve("operation", "foo:run")
	if err != nil {
		t.Fatalf("Resolve foo:run: %v", err)
	}
	barOp, err := composition.Resolver.Resolve("operation", "bar:run")
	if err != nil {
		t.Fatalf("Resolve bar:run: %v", err)
	}
	if fooOp.Equal(barOp) {
		t.Fatalf("foo and bar resolved to same operation: %s", fooOp.Address())
	}
	fooCommand, err := composition.Resolver.Resolve("command", "foo:run")
	if err != nil {
		t.Fatalf("Resolve command foo:run: %v", err)
	}
	binding := composition.CommandCatalog[fooCommand.Address()]
	if !binding.OperationID.Equal(fooOp) {
		t.Fatalf("foo command operation = %s, want %s", binding.OperationID.Address(), fooOp.Address())
	}
	if _, err := composition.Resolver.Resolve("operation", "run"); err == nil {
		t.Fatal("Resolve run error is nil, want ambiguity")
	}
}

func TestComposeRejectsDuplicateOperationSpecsWithSourceDiagnostic(t *testing.T) {
	sourceA := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/a"}
	sourceB := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/a"}
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{
			{
				Source:     sourceA,
				Operations: []operation.Spec{{Ref: operation.Ref{Name: "echo"}}},
			},
			{
				Source:     sourceB,
				Operations: []operation.Spec{{Ref: operation.Ref{Name: "echo"}}},
			},
		},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want duplicate operation spec error")
	}
	if len(composition.Diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1", len(composition.Diagnostics))
	}
	if composition.Diagnostics[0].Source.Location != sourceB.Location {
		t.Fatalf("diagnostic source location = %q, want %s", composition.Diagnostics[0].Source.Location, sourceB.Location)
	}
}

func TestComposeIndexesSessionProfiles(t *testing.T) {
	source := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "apps/demo"}
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Source: source,
			Sessions: []coresession.Spec{{
				Name: "coder",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.SessionCatalog) != 1 {
		t.Fatalf("session catalog len = %d, want 1", len(composition.SessionCatalog))
	}
	if len(composition.SessionSpecs) != 1 || composition.SessionSpecs[0].Name != "coder" {
		t.Fatalf("session specs = %#v, want coder", composition.SessionSpecs)
	}
	id, err := composition.Resolver.Resolve("session", "demo:coder")
	if err != nil {
		t.Fatalf("Resolve demo:coder: %v", err)
	}
	if got, want := id.Address(), "embedded:apps/demo:coder"; got != want {
		t.Fatalf("resolved session = %q, want %q", got, want)
	}
	binding, err := composition.SessionCatalog.Resolve("demo:coder")
	if err != nil {
		t.Fatalf("SessionCatalog.Resolve: %v", err)
	}
	if !binding.ID.Equal(id) {
		t.Fatalf("binding id = %s, want %s", binding.ID.Address(), id.Address())
	}
}

func TestComposeRejectsDuplicateSessionProfilesWithSourceDiagnostic(t *testing.T) {
	sourceA := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "apps/demo"}
	sourceB := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "apps/demo"}
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{
			{
				Source:   sourceA,
				Sessions: []coresession.Spec{{Name: "coder"}},
			},
			{
				Source:   sourceB,
				Sessions: []coresession.Spec{{Name: "coder"}},
			},
		},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want duplicate session error")
	}
	if len(composition.Diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1", len(composition.Diagnostics))
	}
	if composition.Diagnostics[0].Source.Location != sourceB.Location {
		t.Fatalf("diagnostic source location = %q, want %s", composition.Diagnostics[0].Source.Location, sourceB.Location)
	}
}

type echoPlugin struct{}

func (echoPlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: "echo-plugin"}
}

type testPluginEvent struct {
	Value string `json:"value"`
}

func (testPluginEvent) EventName() coreevent.Name { return "test.plugin.event" }

type eventPlugin struct{}

func (eventPlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: "event-plugin"}
}

func (eventPlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		EventTypes: []coreevent.Event{testPluginEvent{}},
	}, nil
}

func (echoPlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Commands: []command.Spec{{
			Path: command.Path{"echo"},
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "echo"},
			},
		}},
	}, nil
}

func (echoPlugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(input)
		}),
	}, nil
}

type sameNamePlugin struct {
	name string
}

func (p sameNamePlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: p.name}
}

func (p sameNamePlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Operations: []operation.Spec{{Ref: operation.Ref{Name: "run"}}},
		Commands: []command.Spec{{
			Path: command.Path{p.name, "run"},
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "run"},
			},
		}},
	}, nil
}

func (p sameNamePlugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operation.New(operation.Spec{Ref: operation.Ref{Name: "run"}}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(map[string]any{"plugin": p.name, "input": input})
		}),
	}, nil
}
