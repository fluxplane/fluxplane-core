package appconfig

import (
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/agent"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/user"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
)

type builderPluginConfig struct {
	Auth   builderPluginAuth   `json:"auth,omitempty"`
	Search builderPluginSearch `json:"search,omitempty"`
}

type builderPluginAuth struct {
	Method string `json:"method,omitempty"`
}

type builderPluginSearch struct {
	Channels       []string `json:"channels,omitempty"`
	IncludeThreads *bool    `json:"include_threads,omitempty"`
}

func TestManifestBuilderBuildsBundleThroughManifestSemantics(t *testing.T) {
	includeThreads := true
	bundle, err := NewManifest("demo").
		WithSource(resource.SourceRef{ID: "demo", Scope: resource.ScopeEmbedded, Location: "examples/demo"}).
		WithDescription("Demo app.").
		WithDefaultModel("smart_model").
		WithModelAlias("openrouter", "openai/gpt-5.5", "smart_model", "gpt5").
		WithPlugin("identity").
		WithPluginConfig("slack-bot", "slack", builderPluginConfig{
			Auth: builderPluginAuth{Method: "token"},
			Search: builderPluginSearch{
				Channels:       []string{"dev-team"},
				IncludeThreads: &includeThreads,
			},
		}).
		WithIdentityGroup(user.Group{ID: "admins", Trust: user.TrustOperator}).
		WithIdentityRule(user.GroupRule{
			Match:  user.IdentityMatch{Provider: "slack", Resolution: user.ResolutionResolved},
			Groups: []user.ID{"admins"},
		}).
		WithDatasourceIndex(4, "15m").
		WithDatasourceSpec(coredatasource.Spec{
			Name:        "slack",
			Kind:        "slack",
			Description: "Slack content.",
			Entities:    []coredatasource.EntityType{"slack.message"},
			Config:      map[string]string{"instance": "slack-bot"},
			Index:       coredatasource.IndexSpec{Enabled: true, Freshness: "15m"},
		}).
		WithDefaultAgent(agent.Spec{
			Name:   "assistant",
			System: "Be useful.",
			Inference: agent.InferenceSpec{
				Model:           "smart_model",
				MaxOutputTokens: 2048,
				ReasoningEffort: "medium",
			},
			Turns:          agent.TurnPolicy{MaxSteps: 12},
			ActivationSets: []string{"identity"},
			Context:        []corecontext.ProviderRef{{Name: "identity.current"}},
			Datasources:    []coredatasource.Ref{{Name: "slack"}},
		}).
		WithSession(coresession.Spec{
			Name:        "default",
			Description: "Default entrypoint.",
			Agent:       agent.Ref{Name: "assistant"},
			Metadata:    map[string]string{"app": "demo"},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if bundle.Source.Location != "examples/demo" || bundle.Source.Scope != resource.ScopeEmbedded {
		t.Fatalf("source = %#v, want embedded examples/demo", bundle.Source)
	}
	if len(bundle.Apps) != 1 {
		t.Fatalf("apps len = %d, want 1", len(bundle.Apps))
	}
	app := bundle.Apps[0]
	if app.Name != "demo" || app.Description != "Demo app." {
		t.Fatalf("app = %#v, want demo description", app)
	}
	if app.DefaultAgent.Name != "assistant" {
		t.Fatalf("default agent = %#v, want assistant", app.DefaultAgent)
	}
	if app.Model.Model != "smart_model" || app.Model.Provider != "" {
		t.Fatalf("app model = %#v, want provider-agnostic smart_model", app.Model)
	}
	if app.Datasource.Index.Concurrency != 4 || app.Datasource.Index.Freshness != "15m" {
		t.Fatalf("datasource index = %#v, want 4/15m", app.Datasource.Index)
	}
	if len(app.Identity.Groups) != 1 || app.Identity.Groups[0].ID != "admins" || app.Identity.Groups[0].Trust != user.TrustOperator {
		t.Fatalf("identity groups = %#v, want admins operator", app.Identity.Groups)
	}
	if len(app.Identity.Rules) != 1 || app.Identity.Rules[0].Match.Provider != "slack" || app.Identity.Rules[0].Groups[0] != "admins" {
		t.Fatalf("identity rules = %#v, want slack resolved admins", app.Identity.Rules)
	}

	plugins := pluginsByInstance(bundle.Plugins)
	if plugins["identity"].Name != "identity" {
		t.Fatalf("identity plugin = %#v, want identity", plugins["identity"])
	}
	slackPlugin := plugins["slack-bot"]
	if slackPlugin.Name != "slack" || slackPlugin.Instance != "slack-bot" {
		t.Fatalf("slack plugin = %#v, want slack/slack-bot", slackPlugin)
	}
	auth, ok := slackPlugin.Config["auth"].(map[string]any)
	if !ok || auth["method"] != "token" {
		t.Fatalf("slack auth config = %#v, want token auth", slackPlugin.Config["auth"])
	}

	if len(bundle.LLMProviders) != 1 || bundle.LLMProviders[0].Name != "openrouter" {
		t.Fatalf("llm providers = %#v, want openrouter", bundle.LLMProviders)
	}
	aliases := map[string]string{}
	for _, alias := range bundle.LLMModelAliases {
		aliases[alias.Name] = alias.Target.String()
	}
	if aliases["smart_model"] != "openrouter/openai/gpt-5.5" || aliases["gpt5"] != "openrouter/openai/gpt-5.5" {
		t.Fatalf("model aliases = %#v, want openrouter targets", aliases)
	}

	if len(bundle.Datasources) != 1 || bundle.Datasources[0].Config["instance"] != "slack-bot" || !bundle.Datasources[0].Index.Enabled {
		t.Fatalf("datasources = %#v, want indexed slack datasource", bundle.Datasources)
	}
	if len(bundle.Agents) != 1 || bundle.Agents[0].Name != "assistant" || bundle.Agents[0].Turns.MaxSteps != 12 {
		t.Fatalf("agents = %#v, want assistant max_steps 12", bundle.Agents)
	}
	if len(bundle.Agents[0].Context) != 1 || bundle.Agents[0].Context[0].Name != "identity.current" {
		t.Fatalf("agent context = %#v, want identity.current", bundle.Agents[0].Context)
	}
	if len(bundle.Sessions) != 1 || bundle.Sessions[0].Agent.Name != "assistant" || bundle.Sessions[0].Metadata["app"] != "demo" {
		t.Fatalf("sessions = %#v, want default assistant session", bundle.Sessions)
	}
}

func TestManifestBuilderReturnsTypedPluginConfigErrorOnBuild(t *testing.T) {
	_, err := NewManifest("demo").
		WithPluginConfig("bad", "bad", []string{"not", "an", "object"}).
		Build()
	if err == nil || !strings.Contains(err.Error(), `plugin "bad" config`) {
		t.Fatalf("Build error = %v, want plugin config error", err)
	}
}

func TestManifestBuilderUsesManifestModelValidation(t *testing.T) {
	_, err := NewManifest("demo").
		WithModelAlias("openrouter", "openai/gpt-5.5", "smart_model").
		WithModelAlias("codex", "gpt-5.5", "smart_model").
		Build()
	if err == nil || !strings.Contains(err.Error(), `duplicate alias "smart_model"`) {
		t.Fatalf("Build error = %v, want duplicate alias validation", err)
	}
}

func pluginsByInstance(refs []resource.PluginRef) map[string]resource.PluginRef {
	out := map[string]resource.PluginRef{}
	for _, ref := range refs {
		out[ref.InstanceName()] = ref
	}
	return out
}
