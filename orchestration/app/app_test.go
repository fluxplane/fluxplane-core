package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	corereaction "github.com/fluxplane/agentruntime/core/reaction"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/core/workflow"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/orchestration/identity"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/native/text"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
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

func TestComposeCollectsPluginExternalIdentityResolvers(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "external-identity"}},
		}},
		Plugins: []pluginhost.Plugin{externalIdentityPlugin{}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if composition.ExternalIdentity == nil {
		t.Fatal("ExternalIdentity is nil, want plugin resolver")
	}
	actor := identity.EnrichActor(context.Background(), user.Actor{
		User:       user.User{ID: "timo@company.org"},
		Identity:   user.Identity{Provider: "slack", ProviderID: "U123"},
		Resolution: user.ResolutionResolved,
	}, composition.ExternalIdentity)
	if len(actor.Identities) != 2 || actor.Identities[1].Provider != "gitlab/main" || actor.Identities[1].ProviderID != "tfriedl" {
		t.Fatalf("identities = %#v, want Slack plus GitLab identity", actor.Identities)
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

func TestComposeCarriesEnvironmentPluginContributions(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{environmentPlugin{}},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "environment-plugin"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.EnvironmentObservers) != 2 {
		t.Fatalf("environment observers len = %d, want baseline plus plugin observer", len(composition.EnvironmentObservers))
	}
	if composition.EnvironmentObservers[0].Spec().Name != runtimeenvironment.BaselineObserverName {
		t.Fatalf("first observer = %#v, want baseline observer", composition.EnvironmentObservers[0].Spec())
	}
	if len(composition.SignalDerivers) != 1 {
		t.Fatalf("signal derivers len = %d, want 1", len(composition.SignalDerivers))
	}
	if len(composition.ReactionRules) != 1 || composition.ReactionRules[0].Name != "go-skill" {
		t.Fatalf("reaction rules = %#v, want go-skill", composition.ReactionRules)
	}
}

func TestComposeAppliesConfiguredObserverOverridesToSelectedImplementations(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{environmentPlugin{}},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "environment-plugin"}},
			Skills:  []skill.Spec{{Name: "go"}},
			Observers: []coreenvironment.ObserverSpec{{
				Name:            "project.inventory",
				Phase:           coreenvironment.PhaseLazy,
				ObservableKinds: []string{"project.inventory.summary"},
				Annotations: map[string]string{
					"configured": "true",
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", composition.Diagnostics)
	}
	var got coreenvironment.ObserverSpec
	for _, observer := range composition.EnvironmentObservers {
		if observer.Spec().Name == "project.inventory" {
			got = observer.Spec()
			break
		}
	}
	if got.Name == "" {
		t.Fatal("project.inventory observer not found")
	}
	if got.Phase != coreenvironment.PhaseLazy {
		t.Fatalf("phase = %q, want lazy override", got.Phase)
	}
	if len(got.ObservableKinds) != 1 || got.ObservableKinds[0] != "project.inventory.summary" {
		t.Fatalf("observable kinds = %#v, want configured narrow kind", got.ObservableKinds)
	}
	if got.Annotations["configured"] != "true" {
		t.Fatalf("annotations = %#v, want configured annotation", got.Annotations)
	}
}

func TestComposeAppliesConfiguredObserverDisableToSelectedImplementations(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{environmentPlugin{}},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "environment-plugin"}},
			Skills:  []skill.Spec{{Name: "go"}},
			Observers: []coreenvironment.ObserverSpec{{
				Name:     "project.inventory",
				Disabled: true,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", composition.Diagnostics)
	}
	for _, observer := range composition.EnvironmentObservers {
		if observer.Spec().Name == "project.inventory" {
			t.Fatalf("project.inventory observer remained after disable: %#v", observer.Spec())
		}
	}
	if len(composition.EnvironmentObservers) != 1 || composition.EnvironmentObservers[0].Spec().Name != runtimeenvironment.BaselineObserverName {
		t.Fatalf("environment observers = %#v, want only baseline observer", composition.EnvironmentObservers)
	}
}

func TestComposeCarriesBundleReactions(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Reactions: []corereaction.Rule{{
				Name: "go-skill",
				When: corereaction.Matcher{Signal: "language.detected", Target: "go"},
				Actions: []corereaction.Action{{
					Kind:  corereaction.ActionActivateSkill,
					Skill: skill.Ref{Name: "go"},
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.ReactionRules) != 1 || composition.ReactionRules[0].Name != "go-skill" {
		t.Fatalf("reaction rules = %#v, want go-skill", composition.ReactionRules)
	}
}

func TestComposeRunsBundleSignalDeriversAsTemplates(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			SignalDerivers: []coreenvironment.SignalDeriverSpec{{
				Name:             "taskfile.signals",
				ObservationKinds: []string{"project.task_runner"},
				Signals: []coreenvironment.SignalTemplate{{
					Kind:   "project.task_runner.detected",
					Target: "taskfile",
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.SignalDerivers) != 1 {
		t.Fatalf("signal derivers len = %d, want 1", len(composition.SignalDerivers))
	}
	signals, err := composition.SignalDerivers[0].Derive(context.Background(), runtimeenvironment.SignalDeriveRequest{
		Observations: []coreenvironment.Observation{{
			Kind:  "project.task_runner",
			Scope: "workspace:/repo",
		}},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(signals) != 1 || signals[0].Kind != "project.task_runner.detected" || signals[0].Target != "taskfile" {
		t.Fatalf("signals = %#v, want taskfile signal", signals)
	}
}

func TestComposeRunsPluginSignalDeriverSpecsAsTemplates(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{templateSignalPlugin{}},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "template-signal-plugin"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.SignalDerivers) != 1 {
		t.Fatalf("signal derivers len = %d, want 1", len(composition.SignalDerivers))
	}
	signals, err := composition.SignalDerivers[0].Derive(context.Background(), runtimeenvironment.SignalDeriveRequest{
		Observations: []coreenvironment.Observation{{
			Kind:  "template.observation",
			Scope: "workspace:/repo",
		}},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(signals) != 1 || signals[0].Kind != "template.signal" || signals[0].Scope != "workspace:/repo" {
		t.Fatalf("signals = %#v, want template signal", signals)
	}
}

func TestComposeDiagnosesConfiguredObserverWithoutEnabledImplementation(t *testing.T) {
	source := resource.SourceRef{Scope: resource.ScopeProject, Location: ".coder.yaml"}
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Source: source,
			Observers: []coreenvironment.ObserverSpec{{
				Name: "kubernetes.context",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.Diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1: %#v", len(composition.Diagnostics), composition.Diagnostics)
	}
	diagnostic := composition.Diagnostics[0]
	if diagnostic.Severity != resource.SeverityWarning || diagnostic.Source.Location != source.Location {
		t.Fatalf("diagnostic = %#v, want warning from %s", diagnostic, source.Location)
	}
	if !strings.Contains(diagnostic.Message, `observer "kubernetes.context"`) || !strings.Contains(diagnostic.Message, "no enabled runtime or plugin") {
		t.Fatalf("diagnostic message = %q, want unavailable observer", diagnostic.Message)
	}
}

func TestComposeDoesNotDiagnoseDisabledObserverWithoutEnabledImplementation(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Observers: []coreenvironment.ObserverSpec{{
				Name:     "kubernetes.context",
				Disabled: true,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", composition.Diagnostics)
	}
}

func TestComposeDoesNotDiagnoseConfiguredBaselineObserver(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Observers: []coreenvironment.ObserverSpec{{
				Name: runtimeenvironment.BaselineObserverName,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", composition.Diagnostics)
	}
}

func TestComposeDiagnosesSignalDeriverSpecWithoutTemplateOrImplementation(t *testing.T) {
	source := resource.SourceRef{Scope: resource.ScopeProject, Location: "agentsdk.app.yaml"}
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Source: source,
			SignalDerivers: []coreenvironment.SignalDeriverSpec{{
				Name:             "custom.signals",
				ObservationKinds: []string{"custom.observation"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.Diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1: %#v", len(composition.Diagnostics), composition.Diagnostics)
	}
	diagnostic := composition.Diagnostics[0]
	if diagnostic.Severity != resource.SeverityWarning || diagnostic.Source.Location != source.Location {
		t.Fatalf("diagnostic = %#v, want warning from %s", diagnostic, source.Location)
	}
	if !strings.Contains(diagnostic.Message, `signal deriver "custom.signals"`) || !strings.Contains(diagnostic.Message, "no enabled runtime or plugin") {
		t.Fatalf("diagnostic message = %q, want unavailable signal deriver", diagnostic.Message)
	}
}

func TestComposeDiagnosesReactionTargetsOutsideSelectedGraph(t *testing.T) {
	source := resource.SourceRef{Scope: resource.ScopeProject, Location: "agentsdk.app.yaml"}
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Source: source,
			Reactions: []corereaction.Rule{{
				Name: "missing-targets",
				When: corereaction.Matcher{Signal: "integration.available"},
				Actions: []corereaction.Action{
					{Kind: corereaction.ActionActivateSkill, Skill: skill.Ref{Name: "missing-skill"}},
					{Kind: corereaction.ActionEnableOperationSet, OperationSet: "missing-ops"},
					{Kind: corereaction.ActionEnableDatasource, Datasource: coredatasource.Ref{Name: "missing-datasource"}},
					{Kind: corereaction.ActionEnableContext, ContextProvider: corecontext.ProviderRef{Name: "missing.context"}},
					{Kind: corereaction.ActionRunWorkflow, Workflow: corereaction.WorkflowAction{Name: "missing-workflow"}},
					{Kind: corereaction.ActionRunOperation, Operation: corereaction.OperationAction{Operation: operation.Ref{Name: "missing-op"}}},
					{Kind: corereaction.ActionRunCommand, Command: command.Invocation{Path: command.Path{"missing"}}},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.Diagnostics) != 7 {
		t.Fatalf("diagnostics len = %d, want 7: %#v", len(composition.Diagnostics), composition.Diagnostics)
	}
	joined := diagnosticsText(composition.Diagnostics)
	for _, want := range []string{"unknown skill", "unknown operation set", "unknown datasource", "unknown context provider", "unknown workflow", "unknown operation", "unknown command"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diagnostics = %s\nmissing %q", joined, want)
		}
	}
	for _, diagnostic := range composition.Diagnostics {
		if diagnostic.Severity != resource.SeverityWarning || diagnostic.Source.Location != source.Location {
			t.Fatalf("diagnostic = %#v, want warning from %s", diagnostic, source.Location)
		}
	}
}

func TestComposeDoesNotDiagnoseKnownReactionTargets(t *testing.T) {
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := Compose(Config{
		Operations: []operation.Operation{echo},
		Bundles: []resource.ContributionBundle{{
			Skills: []skill.Spec{{
				Name: "go",
				References: []skill.ReferenceSpec{{
					Path: "references/testing.md",
				}},
			}},
			OperationSets: []operation.Set{{
				Name: "go-tools",
			}},
			Datasources: []coredatasource.Spec{{
				Name:     "docs",
				Kind:     "test",
				Entities: []coredatasource.EntityType{"doc"},
			}},
			ContextProviders: []corecontext.ProviderSpec{{
				Name: "docs.context",
			}},
			Workflows: []workflow.Spec{{
				Name: "inspect",
				Steps: []workflow.Step{{
					ID:        "echo",
					Operation: operation.Ref{Name: "echo"},
				}},
			}},
			Commands: []command.Spec{{
				Path: command.Path{"echo"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "echo"},
				},
			}},
			Reactions: []corereaction.Rule{{
				Name: "known-targets",
				When: corereaction.Matcher{Signal: "language.detected"},
				Actions: []corereaction.Action{
					{Kind: corereaction.ActionActivateSkill, Skill: skill.Ref{Name: "go"}},
					{Kind: corereaction.ActionActivateReference, Reference: corereaction.ReferenceAction{Skill: skill.Ref{Name: "go"}, Path: "references/testing.md"}},
					{Kind: corereaction.ActionEnableOperationSet, OperationSet: "go-tools"},
					{Kind: corereaction.ActionEnableDatasource, Datasource: coredatasource.Ref{Name: "docs"}},
					{Kind: corereaction.ActionEnableContext, ContextProvider: corecontext.ProviderRef{Name: "docs.context"}},
					{Kind: corereaction.ActionRunWorkflow, Workflow: corereaction.WorkflowAction{Name: "inspect"}},
					{Kind: corereaction.ActionRunOperation, Operation: corereaction.OperationAction{Operation: operation.Ref{Name: "echo"}}},
					{Kind: corereaction.ActionRunCommand, Command: command.Invocation{Path: command.Path{"echo"}}},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", composition.Diagnostics)
	}
}

func TestComposeConvertsSkillTriggersToSignalDeriverAndReactions(t *testing.T) {
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Skills: []skill.Spec{{
				Name:     "go",
				Triggers: []string{"go trigger"},
				References: []skill.ReferenceSpec{{
					Path:     "references/testing.md",
					Triggers: []string{"testing trigger"},
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", composition.Diagnostics)
	}
	if !hasReactionRule(composition.ReactionRules, "skill.trigger.go") {
		t.Fatalf("reaction rules = %#v, missing skill trigger rule", composition.ReactionRules)
	}
	if !hasReactionRule(composition.ReactionRules, "skill.reference.trigger.go.references/testing.md") {
		t.Fatalf("reaction rules = %#v, missing reference trigger rule", composition.ReactionRules)
	}
	var deriver runtimeenvironment.SignalDeriver
	for _, candidate := range composition.SignalDerivers {
		if candidate.Spec().Name == skillTriggerDeriverName {
			deriver = candidate
			break
		}
	}
	if deriver == nil {
		t.Fatalf("signal derivers = %#v, missing skill trigger deriver", composition.SignalDerivers)
	}
	signals, err := deriver.Derive(context.Background(), runtimeenvironment.SignalDeriveRequest{Observations: []coreenvironment.Observation{{
		Kind:    "channel.message",
		Content: "please use go trigger and testing trigger",
	}}})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if !hasEnvironmentSignal(signals, signalSkillRequested, "go") || !hasEnvironmentSignal(signals, signalSkillReferenceNeeded, "references/testing.md") {
		t.Fatalf("signals = %#v, want skill and reference request signals", signals)
	}
}

func diagnosticsText(diagnostics []resource.Diagnostic) string {
	var out []string
	for _, diagnostic := range diagnostics {
		out = append(out, diagnostic.Message)
	}
	return strings.Join(out, "\n")
}

func hasReactionRule(rules []corereaction.Rule, name string) bool {
	for _, rule := range rules {
		if rule.Name == name {
			return true
		}
	}
	return false
}

func hasEnvironmentSignal(signals []coreenvironment.Signal, kind, target string) bool {
	for _, signal := range signals {
		if signal.Kind == kind && signal.Target == target {
			return true
		}
	}
	return false
}

func TestComposeResolvesConfiguredPluginContributions(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{text.New()},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{
				Name: text.Name,
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

type externalIdentityPlugin struct{}

func (externalIdentityPlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: "external-identity"}
}

func (externalIdentityPlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (externalIdentityPlugin) ExternalIdentityResolvers(context.Context, pluginhost.Context) ([]identity.ExternalResolver, error) {
	return []identity.ExternalResolver{identity.ExternalResolverFunc(func(_ context.Context, req identity.ExternalRequest) (identity.ExternalResult, error) {
		if req.Actor.User.ID != "timo@company.org" {
			return identity.ExternalResult{}, nil
		}
		return identity.ExternalResult{Identities: []user.Identity{{Provider: "gitlab/main", ProviderID: "tfriedl"}}}, nil
	})}, nil
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

type environmentPlugin struct{}

func (environmentPlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: "environment-plugin"}
}

func (environmentPlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (environmentPlugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeenvironment.Observer, error) {
	return []runtimeenvironment.Observer{testEnvironmentObserver{}}, nil
}

func (environmentPlugin) SignalDerivers(context.Context, pluginhost.Context) ([]runtimeenvironment.SignalDeriver, error) {
	return []runtimeenvironment.SignalDeriver{testSignalDeriver{}}, nil
}

func (environmentPlugin) Reactions(context.Context, pluginhost.Context) ([]corereaction.Rule, error) {
	return []corereaction.Rule{{
		Name: "go-skill",
		When: corereaction.Matcher{Signal: "language.detected", Target: "go"},
		Actions: []corereaction.Action{{
			Kind:  corereaction.ActionActivateSkill,
			Skill: skill.Ref{Name: "go"},
		}},
	}}, nil
}

type testEnvironmentObserver struct{}

func (testEnvironmentObserver) Spec() coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{Name: "project.inventory", Phase: coreenvironment.PhaseSessionOpen}
}

func (testEnvironmentObserver) Observe(context.Context, runtimeenvironment.ObservationRequest) ([]coreenvironment.Observation, error) {
	return []coreenvironment.Observation{{Kind: "project.inventory"}}, nil
}

type testSignalDeriver struct{}

func (testSignalDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return coreenvironment.SignalDeriverSpec{Name: "project.signals"}
}

func (testSignalDeriver) Derive(context.Context, runtimeenvironment.SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	return []coreenvironment.Signal{{Kind: "language.detected", Target: "go"}}, nil
}

type templateSignalPlugin struct{}

func (templateSignalPlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: "template-signal-plugin"}
}

func (templateSignalPlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		SignalDerivers: []coreenvironment.SignalDeriverSpec{{
			Name:             "template.signals",
			ObservationKinds: []string{"template.observation"},
			Signals: []coreenvironment.SignalTemplate{{
				Kind: "template.signal",
			}},
		}},
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
