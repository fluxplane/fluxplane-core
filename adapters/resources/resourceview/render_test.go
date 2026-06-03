package resourceview

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	"github.com/fluxplane/fluxplane-core/core/channel"
	corecommand "github.com/fluxplane/fluxplane-core/core/command"
	corelanguage "github.com/fluxplane/fluxplane-core/core/language"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/workflow"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	coreevent "github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-skill"
)

func TestRenderTreeShowsEveryContributionKind(t *testing.T) {
	var out bytes.Buffer
	if err := RenderTree(&out, []resource.ContributionBundle{testBundle()}, []resource.Diagnostic{{
		Severity: resource.SeverityError,
		Message:  "external diagnostic",
	}}); err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"Sources:",
		"test/source",
		"apps",
		"sessions",
		"agents",
		"commands",
		"workflows",
		"operation_sets",
		"toolchains",
		"operations",
		"datasources",
		"llm providers",
		"skills",
		"context_providers",
		"event_types",
		"plugins",
		"test.event",
		"lookup-set",
		"go",
		"lookup",
		"gpt-test",
		"standalone",
		"references: references/guide.md",
		"bundle diagnostic",
		"external diagnostic",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tree output missing %q:\n%s", want, text)
		}
	}
}

func TestRenderTreeRendersPluginContributionsAfterRegularBundles(t *testing.T) {
	base := resource.ContributionBundle{
		Source: resource.SourceRef{ID: "app", Scope: resource.ScopeProject, Location: "fluxplane.yaml"},
		Plugins: []resource.PluginRef{{
			Name: "lookup",
		}},
	}
	plugin := resource.ContributionBundle{
		Source: resource.SourceRef{ID: "plugin:lookup", Scope: resource.ScopeEmbedded, Location: "plugins/lookup", Ref: "lookup"},
		OperationSets: []operation.Set{{
			Name:       "lookup-tools",
			Operations: []operation.Ref{{Name: "lookup_doc"}},
		}},
		Operations: []operation.Spec{{
			Ref: operation.Ref{Name: "lookup_doc"},
		}},
		ContextProviders: []corecontext.ProviderSpec{{
			Name: "lookup.context",
		}},
	}
	var out bytes.Buffer
	if err := RenderTree(&out, []resource.ContributionBundle{base, plugin}, nil); err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"plugins",
		"lookup",
		"Plugin contributions:",
		"embedded:plugins/lookup",
		"operation_sets",
		"lookup-tools",
		"lookup_doc",
		"context_providers",
		"lookup.context",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tree output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "contributes:") {
		t.Fatalf("tree output contains nested plugin contribution summary:\n%s", text)
	}
	if strings.Contains(text, "├── operations") {
		t.Fatalf("operation set member rendered again under operations:\n%s", text)
	}
	if strings.Index(text, "app") > strings.Index(text, "Plugin contributions:") {
		t.Fatalf("regular bundle rendered after plugin contributions:\n%s", text)
	}
}

func TestRenderTreeLabelsImplicitPlugins(t *testing.T) {
	base := resource.ContributionBundle{
		Source: resource.SourceRef{ID: "app", Scope: resource.ScopeProject, Location: "fluxplane.yaml"},
		Plugins: []resource.PluginRef{{
			Name: "datasource",
		}},
	}
	plugin := resource.ContributionBundle{
		Source: resource.SourceRef{ID: "plugin:datasource", Scope: resource.ScopeEmbedded, Location: "plugins/datasource", Ref: "datasource"},
		ContextProviders: []corecontext.ProviderSpec{{
			Name: "datasource.catalog",
		}},
	}
	var out bytes.Buffer
	if err := RenderTreeWithOptions(&out, []resource.ContributionBundle{base, plugin}, nil, TreeOptions{
		ImplicitPlugins: map[string]bool{"datasource": true},
	}); err != nil {
		t.Fatalf("RenderTreeWithOptions: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"plugin:datasource (implicit)",
		"datasource (implicit)",
		"embedded:plugins/datasource (implicit)",
		"Plugin contributions:",
		"datasource.catalog",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tree output missing %q:\n%s", want, text)
		}
	}
}

func TestNewOutputIncludesEveryContributionKind(t *testing.T) {
	out := NewOutput([]resource.ContributionBundle{testBundle()}, nil)
	if len(out.Resources) != 16 {
		t.Fatalf("resources len = %d, want 16", len(out.Resources))
	}
	assertLen(t, "sources", len(out.Sources), 1)
	assertLen(t, "apps", len(out.Apps), 1)
	assertLen(t, "sessions", len(out.Sessions), 1)
	assertLen(t, "agents", len(out.Agents), 1)
	assertLen(t, "commands", len(out.Commands), 1)
	assertLen(t, "workflows", len(out.Workflows), 1)
	assertLen(t, "operation_sets", len(out.OperationSets), 1)
	assertLen(t, "toolchains", len(out.Toolchains), 1)
	assertLen(t, "operations", len(out.Operations), 2)
	assertLen(t, "datasources", len(out.Datasources), 1)
	assertLen(t, "llm_providers", len(out.LLMProviders), 1)
	assertLen(t, "llm_model_aliases", len(out.LLMModelAliases), 1)
	assertLen(t, "skills", len(out.Skills), 1)
	assertLen(t, "context_providers", len(out.ContextProviders), 1)
	assertLen(t, "event_types", len(out.EventTypes), 1)
	assertLen(t, "plugins", len(out.Plugins), 1)
	assertLen(t, "diagnostics", len(out.Diagnostics), 1)

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"event_types":["test.event"]`) {
		t.Fatalf("json output missing event_types: %s", raw)
	}
}

func assertLen(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s len = %d, want %d", name, got, want)
	}
}

func testBundle() resource.ContributionBundle {
	source := resource.SourceRef{
		ID:       "test/source",
		Scope:    resource.ScopeEmbedded,
		Location: "apps/test",
	}
	return resource.ContributionBundle{
		Source: source,
		Apps: []coreapp.Spec{{
			Name:         "test",
			DefaultAgent: agent.Ref{Name: "assistant"},
		}},
		Sessions: []coresession.Spec{{
			Name:    "main",
			Agent:   agent.Ref{Name: "assistant"},
			Channel: channel.Ref{Name: "local"},
		}},
		Agents: []agent.Spec{{
			Name:       "assistant",
			Operations: []operation.Ref{{Name: "lookup"}},
			Tools:      []agent.ToolRef{{Name: "lookup"}},
			Commands:   []agent.CommandRef{{Name: "/inspect"}},
			Skills:     []skill.Ref{{Name: "guidance"}},
		}},
		Commands: []corecommand.Spec{{
			Path: corecommand.Path{"inspect"},
		}},
		Workflows: []workflow.Spec{{
			Name: "review",
		}},
		OperationSets: []operation.Set{{
			Name:       "lookup-set",
			Operations: []operation.Ref{{Name: "lookup"}},
		}},
		Toolchains: []corelanguage.ToolchainSpec{{
			ID: "go",
		}},
		Operations: []operation.Spec{{
			Ref: operation.Ref{Name: "lookup"},
		}, {
			Ref: operation.Ref{Name: "standalone"},
		}},
		Datasources: []coredatasource.Spec{{
			Name: "docs",
			Kind: "memory",
		}},
		LLMProviders: []corellm.ProviderSpec{{
			Name: "openai",
			Models: []corellm.ModelSpec{{
				Ref: corellm.ModelRef{Name: "gpt-test"},
			}},
		}},
		LLMModelAliases: []corellm.ModelAliasSpec{{
			Name:   "codex",
			Target: corellm.ModelRef{Provider: "codex", Name: "gpt-5.5"},
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
			Message:  "bundle diagnostic",
		}},
	}
}

type testEvent struct{}

func (testEvent) EventName() coreevent.Name { return "test.event" }
