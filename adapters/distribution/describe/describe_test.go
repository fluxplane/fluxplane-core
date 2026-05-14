package describe

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/channel"
	corecommand "github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/workflow"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
)

func TestRenderTreeShowsMetadataAndAllStaticResourceKinds(t *testing.T) {
	var out bytes.Buffer
	if err := RenderTree(&out, testDistribution()); err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"Distribution:",
		"name:                testdist",
		"default model:       openai/gpt-test (coding)",
		"Sources:",
		"embedded/testdist",
		"apps",
		"sessions",
		"agents",
		"commands",
		"workflows",
		"operation_sets",
		"operations",
		"datasources",
		"skills",
		"context_providers",
		"event_types",
		"plugins",
		"test.event",
		"lookup-set",
		"lookup",
		"standalone",
		"references: references/guide.md",
		"Diagnostics:",
		"test diagnostic",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tree output missing %q:\n%s", want, text)
		}
	}
}

func TestRenderJSONIncludesAllStaticResourceGroups(t *testing.T) {
	var out bytes.Buffer
	if err := RenderJSON(&out, testDistribution()); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var decoded Output
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if decoded.Distribution.Name != "testdist" {
		t.Fatalf("distribution name = %q, want testdist", decoded.Distribution.Name)
	}
	assertLen(t, "sources", len(decoded.Sources), 1)
	assertLen(t, "resources", len(decoded.Resources), 13)
	assertLen(t, "apps", len(decoded.Apps), 1)
	assertLen(t, "sessions", len(decoded.Sessions), 1)
	assertLen(t, "agents", len(decoded.Agents), 1)
	assertLen(t, "commands", len(decoded.Commands), 1)
	assertLen(t, "workflows", len(decoded.Workflows), 1)
	assertLen(t, "operation_sets", len(decoded.OperationSets), 1)
	assertLen(t, "operations", len(decoded.Operations), 2)
	assertLen(t, "datasources", len(decoded.Datasources), 1)
	assertLen(t, "skills", len(decoded.Skills), 1)
	assertLen(t, "context_providers", len(decoded.ContextProviders), 1)
	assertLen(t, "event_types", len(decoded.EventTypes), 1)
	assertLen(t, "plugins", len(decoded.Plugins), 1)
	assertLen(t, "diagnostics", len(decoded.Diagnostics), 1)
}

func TestRenderYAMLIncludesDistributionAndResources(t *testing.T) {
	var out bytes.Buffer
	if err := RenderYAML(&out, testDistribution()); err != nil {
		t.Fatalf("RenderYAML: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"distribution:",
		"name: testdist",
		"resources:",
		"operation_sets:",
		"plugins:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("yaml output missing %q:\n%s", want, text)
		}
	}
}

func TestRenderAgentTreeShowsDetailedAgent(t *testing.T) {
	var out bytes.Buffer
	if err := RenderAgentTree(&out, testDistribution(), "assistant"); err != nil {
		t.Fatalf("RenderAgentTree: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"Agent:",
		"name:                assistant",
		"resource:            embedded:apps/testdist:assistant",
		"source:              embedded/testdist",
		"Operations:",
		"Tools:",
		"Commands:",
		"Skills:",
		"Related:",
		"apps: testdist",
		"sessions: main",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("agent tree output missing %q:\n%s", want, text)
		}
	}
}

func TestRenderAgentJSONMatchesQualifiedResourceRef(t *testing.T) {
	var out bytes.Buffer
	if err := RenderAgentJSON(&out, testDistribution(), "embedded:assistant"); err != nil {
		t.Fatalf("RenderAgentJSON: %v", err)
	}
	var decoded AgentOutput
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if decoded.Agent.Name != "assistant" {
		t.Fatalf("agent name = %q, want assistant", decoded.Agent.Name)
	}
	if len(decoded.Apps) != 1 || len(decoded.Sessions) != 1 {
		t.Fatalf("related apps/sessions = %d/%d, want 1/1", len(decoded.Apps), len(decoded.Sessions))
	}
}

func TestAgentReturnsNotFoundAndAmbiguousErrors(t *testing.T) {
	_, err := Agent(testDistribution(), "missing")
	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("missing error = %v, want ErrAgentNotFound", err)
	}

	dist := testDistribution()
	dist.Bundles = append(dist.Bundles, resource.ContributionBundle{
		Source: resource.SourceRef{ID: "other", Scope: resource.ScopeEmbedded, Location: "apps/other"},
		Agents: []agent.Spec{{
			Name: "assistant",
		}},
	})
	_, err = Agent(dist, "assistant")
	if !errors.Is(err, ErrAgentAmbiguous) {
		t.Fatalf("ambiguous error = %v, want ErrAgentAmbiguous", err)
	}
}

func assertLen(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s len = %d, want %d", name, got, want)
	}
}

func testDistribution() distribution.Distribution {
	source := resource.SourceRef{
		ID:       "embedded/testdist",
		Scope:    resource.ScopeEmbedded,
		Location: "apps/testdist",
	}
	return distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "testdist",
			Title:               "Test Distribution",
			Description:         "Distribution used by renderer tests.",
			DefaultSession:      coresession.Ref{Name: "main"},
			DefaultConversation: channel.ConversationRef{ID: "conversation"},
			DefaultModel: coredistribution.ModelDefault{
				Provider: "openai",
				Model:    "gpt-test",
				UseCase:  "coding",
			},
			Surfaces: coredistribution.Surfaces{CLI: true, REPL: true, OneShot: true},
			Commands: []coredistribution.Command{{
				Name:        "custom",
				Description: "custom command",
			}},
		},
		Bundles: []resource.ContributionBundle{{
			Source: source,
			Apps: []coreapp.Spec{{
				Name:         "testdist",
				DefaultAgent: agent.Ref{Name: "assistant"},
				Plugins: []coreapp.PluginRef{{
					Name: "testplugin",
				}},
			}},
			Sessions: []coresession.Spec{{
				Name:    "main",
				Agent:   agent.Ref{Name: "assistant"},
				Channel: channel.Ref{Name: "local"},
			}},
			Agents: []agent.Spec{{
				Name:        "assistant",
				Description: "Detailed assistant.",
				System:      "Follow instructions.",
				Operations:  []operation.Ref{{Name: "lookup"}},
				Tools:       []agent.ToolRef{{Name: "lookup"}},
				Commands:    []agent.CommandRef{{Name: "/inspect"}},
				Skills:      []skill.Ref{{Name: "guidance"}},
			}},
			Commands: []corecommand.Spec{{
				Path: corecommand.Path{"inspect"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "lookup"},
				},
			}},
			Workflows: []workflow.Spec{{
				Name: "review",
			}},
			OperationSets: []operation.Set{{
				Name:       "lookup-set",
				Operations: []operation.Ref{{Name: "lookup"}},
			}},
			Operations: []operation.Spec{{
				Ref: operation.Ref{Name: "lookup"},
			}, {
				Ref: operation.Ref{Name: "standalone"},
			}},
			Datasources: []coredatasource.Spec{{
				Name:     "docs",
				Entities: []coredatasource.EntityType{"doc"},
				Kind:     "memory",
			}},
			Skills: []skill.Spec{{
				Name: "guidance",
				References: []skill.ReferenceSpec{{
					Path: "references/guide.md",
				}},
			}},
			ContextProviders: []corecontext.ProviderSpec{{
				Name: "workspace",
			}},
			EventTypes: []coreevent.Event{testEvent{}},
			Plugins: []resource.PluginRef{{
				Name: "testplugin",
			}},
			Diagnostics: []resource.Diagnostic{{
				Severity: resource.SeverityWarning,
				Message:  "test diagnostic",
			}},
		}},
	}
}

type testEvent struct{}

func (testEvent) EventName() coreevent.Name { return "test.event" }
