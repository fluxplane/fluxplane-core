package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	"github.com/fluxplane/fluxplane-core/adapters/resources/appconfig"
	"github.com/fluxplane/fluxplane-core/apps/launch"
	"github.com/fluxplane/fluxplane-core/core/agent"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/user"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/confluence"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/gitlab"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/jira"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/openapi"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/slack"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/web"
	datasourceplugin "github.com/fluxplane/fluxplane-core/plugins/native/datasource"
	"github.com/fluxplane/fluxplane-core/plugins/native/identity"
	"github.com/fluxplane/fluxplane-core/plugins/native/memory"
	"github.com/fluxplane/fluxplane-core/plugins/native/skills"
	system "github.com/fluxplane/fluxplane-core/runtime/workspace"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

const (
	appName     = "slack-bot-coded"
	agentName   = "slack_bot"
	sessionName = "slack-main"
	modelName   = "openai/gpt-5.5"
	modelAlias  = "smart_model"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root, err := exampleRoot()
	if err != nil {
		return err
	}
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		return err
	}
	bundle, err := appBundle()
	if err != nil {
		return err
	}
	static := launch.StaticPluginView(ctx, launch.StaticPluginOptions{
		Bundles:                          []resource.ContributionBundle{bundle},
		IncludeConfigSchemaContributions: true,
		Plugins: func(launch.PluginFactoryContext) []pluginhost.Plugin {
			return appPlugins(sys)
		},
	})
	if diagnostics := errorDiagnostics(static.Diagnostics); len(diagnostics) > 0 {
		return fmt.Errorf("static plugin diagnostics: %s", diagnosticMessages(diagnostics))
	}
	bundles, err := appconfig.NormalizeBundles(static.Bundles)
	if err != nil {
		return err
	}
	spec, ok := findAgent(bundles, agentName)
	if !ok {
		return fmt.Errorf("agent %q not found", agentName)
	}
	operationCount, datasourceCount := countSurface(bundles)
	fmt.Printf("agent=%s activation_sets=%d context=%d datasources=%d operations=%d datasource_specs=%d\n",
		spec.Name, len(spec.ActivationSets), len(spec.Context), len(spec.Datasources), operationCount, datasourceCount)
	return nil
}

func appBundle() (resource.ContributionBundle, error) {
	includeThreads := true
	agentSpec := agent.Spec{
		Name:   agentName,
		System: slackBotSystemPrompt(),
		Inference: agent.InferenceSpec{
			Model:           modelAlias,
			ReasoningEffort: "medium",
		},
		Turns: agent.TurnPolicy{MaxSteps: 50},
		ActivationSets: []string{
			"channel",
			datasourceplugin.Name,
			identity.Name,
			skills.Name,
			memory.Name,
		},
		Context: []corecontext.ProviderRef{
			{Name: "identity.current"},
			{Name: datasourceplugin.ContextProvider},
			{Name: "datasource.detected"},
			{Name: skills.Name},
		},
		Datasources: []coredatasource.Ref{
			{Name: coredatasource.Name(slack.Name)},
			{Name: coredatasource.Name(gitlab.Name)},
			{Name: coredatasource.Name(jira.Name)},
			{Name: coredatasource.Name(confluence.Name)},
			{Name: "helpdesk_api_docs"},
			{Name: "local-docs"},
			{Name: "web_search"},
			{Name: coredatasource.Name(skills.Name)},
		},
	}
	return appconfig.NewManifest(appName).
		WithSource(resource.SourceRef{ID: appName, Scope: resource.ScopeEmbedded, Location: "examples/go/slack-bot-coded"}).
		WithDescription("Type-safe Go configured Slack bot example.").
		WithDefaultModel(modelAlias).
		WithModelAlias("openrouter", modelName, modelAlias, "gpt5").
		WithPlugin(identity.Name).
		WithPluginConfig("slack-bot", slack.Name, slack.Config{
			Auth: slack.AuthConfig{Method: slack.TokenMethod},
			Search: slack.SearchConfig{
				Channels:       []string{"dev-team"},
				HistoryWindow:  "90d",
				IncludeThreads: &includeThreads,
			},
		}).
		WithPluginConfig(gitlab.Name, gitlab.Name, gitlab.Config{
			BaseURL: "https://gitlab.example.com",
			Auth:    gitlab.AuthConfig{Method: gitlab.PersonalAccessTokenMethod, TokenEnv: "GITLAB_PERSONAL_TOKEN"},
		}).
		WithPluginConfig(jira.Name, jira.Name, jira.Config{
			CloudID: "00000000-0000-0000-0000-000000000000",
			Auth:    jira.AuthConfig{Method: jira.APITokenMethod, TokenEnv: "JIRA_API_TOKEN", Email: "bot@example.com"},
		}).
		WithPluginConfig(confluence.Name, confluence.Name, confluence.Config{
			CloudID: "00000000-0000-0000-0000-000000000000",
			Auth:    confluence.AuthConfig{Method: confluence.APITokenMethod, TokenEnv: "CONFLUENCE_API_TOKEN", Email: "bot@example.com"},
		}).
		WithPluginConfig("helpdesk", openapi.Name, openapi.Config{
			Specs: []openapi.SpecConfig{{
				File: "openapi/helpdesk.openapi.json",
				Operations: openapi.OperationsConfig{
					Prefix: "helpdesk",
				},
				Datasource: openapi.DatasourceConfig{
					Name:  "helpdesk_api_docs",
					Index: openapi.DatasourceIndexConfig{Enabled: true, Freshness: "24h"},
				},
				Auth: openapi.AuthConfig{Schemes: map[string]openapi.AuthSchemeConfig{
					"bearerAuth": {
						Method:      auth.MethodEnv,
						Kind:        sharedsecret.KindBearerToken,
						DisplayName: "Helpdesk API bearer token",
						Env:         auth.EnvSpec{Name: "HELPDESK_API_TOKEN"},
					},
				}},
			}},
		}).
		WithPlugin(web.Name).
		WithPlugin(skills.Name).
		WithPlugin(memory.Name).
		WithPlugin(datasourceplugin.Name).
		WithIdentityGroup(user.Group{ID: "slack-bot-admin", Trust: user.TrustOperator}).
		WithIdentityGroup(user.Group{ID: "slack-bot-users", Trust: user.TrustInternal}).
		WithIdentityGroup(user.Group{ID: "anonymous", Trust: user.TrustPublic}).
		WithIdentityRule(user.GroupRule{
			Match:  user.IdentityMatch{Provider: slack.Name, Resolution: user.ResolutionResolved},
			Groups: []user.ID{"slack-bot-users"},
		}).
		WithIdentityRule(user.GroupRule{
			Match:  user.IdentityMatch{Provider: slack.Name, ProviderID: "U0000000000", Resolution: user.ResolutionResolved},
			Groups: []user.ID{"slack-bot-admin"},
		}).
		WithIdentityRule(user.GroupRule{
			Match:  user.IdentityMatch{Provider: slack.Name, Resolution: user.ResolutionUnresolved},
			Groups: []user.ID{"anonymous"},
		}).
		WithDatasourceIndex(4, "15m").
		WithDatasourceSpec(coredatasource.Spec{
			Name:        coredatasource.Name(slack.Name),
			Kind:        slack.Name,
			Description: "Slack workspace content available to the bot.",
			Entities: []coredatasource.EntityType{
				"slack.user",
				"slack.channel",
				"slack.message",
				"slack.thread_message",
			},
			Config: map[string]string{"instance": "slack-bot"},
			Index:  coredatasource.IndexSpec{Enabled: true, Freshness: "15m"},
		}).
		WithDatasourceSpec(coredatasource.Spec{
			Name:        coredatasource.Name(gitlab.Name),
			Kind:        gitlab.Name,
			Description: "GitLab project and merge request data.",
			Entities: []coredatasource.EntityType{
				"gitlab.project",
				"gitlab.merge_request",
				"gitlab.merge_request_note",
				"gitlab.pipeline",
				"gitlab.user",
				"gitlab.group",
			},
			Config: map[string]string{"instance": gitlab.Name},
			Index:  coredatasource.IndexSpec{Enabled: true, Freshness: "15m"},
		}).
		WithDatasourceSpec(coredatasource.Spec{
			Name:        coredatasource.Name(jira.Name),
			Kind:        jira.Name,
			Description: "Jira issues visible to the configured plugin instance.",
			Entities:    []coredatasource.EntityType{"jira.issue", "jira.project"},
			Config:      map[string]string{"instance": jira.Name},
		}).
		WithDatasourceSpec(coredatasource.Spec{
			Name:        coredatasource.Name(confluence.Name),
			Kind:        confluence.Name,
			Description: "Confluence pages visible to the configured plugin instance.",
			Entities:    []coredatasource.EntityType{"confluence.page", "confluence.space"},
			Config:      map[string]string{"instance": confluence.Name},
		}).
		WithDatasourceSpec(coredatasource.Spec{
			Name:        "local-docs",
			Kind:        "filesystem",
			Description: "Local markdown and text files in this example.",
			Entities:    []coredatasource.EntityType{"file.document"},
			Config:      map[string]string{"path": ".", "include": "*.md,*.txt"},
		}).
		WithDatasourceSpec(coredatasource.Spec{
			Name:        "web_search",
			Kind:        "web_search",
			Description: "Public web search results.",
			Entities:    []coredatasource.EntityType{"web.search_result"},
		}).
		WithDefaultAgent(agentSpec).
		WithSession(coresession.Spec{
			Name:        sessionName,
			Description: "Slack channel entrypoint.",
			Agent:       agent.Ref{Name: agentName},
			Metadata:    map[string]string{"app": appName},
		}).
		Build()
}

func appPlugins(sys fpsystem.System) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		identity.New(),
		slack.NewWithBoundaries(slack.Boundaries{Network: sys.Network(), Environment: sys.Environment()}, nil),
		gitlab.NewWithBoundaries(gitlab.Boundaries{Network: sys.Network(), Environment: sys.Environment()}),
		jira.NewWithBoundaries(jira.Boundaries{Network: sys.Network()}),
		confluence.NewWithBoundaries(confluence.Boundaries{Network: sys.Network()}),
		openapi.NewWithBoundaries(openapi.Boundaries{Network: sys.Network(), Environment: sys.Environment()}),
		web.NewWithBoundaries(web.Boundaries{Network: sys.Network(), Environment: sys.Environment()}),
		skills.New(),
		memory.New(),
		datasourceplugin.New(nil),
	}
}

func findAgent(bundles []resource.ContributionBundle, name string) (agent.Spec, bool) {
	for _, bundle := range bundles {
		for _, spec := range bundle.Agents {
			if string(spec.Name) == name {
				return spec, true
			}
		}
	}
	return agent.Spec{}, false
}

func countSurface(bundles []resource.ContributionBundle) (int, int) {
	var operations int
	var datasources int
	for _, bundle := range bundles {
		operations += len(bundle.Operations)
		datasources += len(bundle.Datasources) + len(bundle.DataSources)
	}
	return operations, datasources
}

func errorDiagnostics(diagnostics []resource.Diagnostic) []resource.Diagnostic {
	var out []resource.Diagnostic
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == resource.SeverityError {
			out = append(out, diagnostic)
		}
	}
	return out
}

func diagnosticMessages(diagnostics []resource.Diagnostic) string {
	values := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		values = append(values, diagnostic.Message)
	}
	return strings.Join(values, "; ")
}

func exampleRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve example root")
	}
	return filepath.Dir(file), nil
}

func slackBotSystemPrompt() string {
	return strings.TrimSpace(`
You are a helpful Slack assistant. Keep replies concise and format them with
standard Markdown. For factual questions, ground answers in configured
datasources and include source references. Use channel_send only for useful
intermediate updates in long-running turns. Use datasource_search with entity
filters for Slack, GitLab, Jira, Confluence, local docs, Helpdesk API docs, and
public web results. Use datasource_get for exact records and datasource_relation
for exact provider relationships such as Slack channel members or thread
messages.
`)
}
