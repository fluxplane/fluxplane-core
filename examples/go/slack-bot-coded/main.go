package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fluxplane/fluxplane-core/adapters/resources/appconfig"
	"github.com/fluxplane/fluxplane-core/apps/launch"
	"github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
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
	"github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-core/sdk"
)

const (
	appName     = "slack-bot-coded"
	agentName   = "slack_bot"
	sessionName = "slack-main"
	modelName   = "openai/gpt-5.5"
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
		Plugins: func(system.System) []pluginhost.Plugin {
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
	pluginRefs, err := configuredPlugins()
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	agentSpec := agent.Spec{
		Name:   agentName,
		System: slackBotSystemPrompt(),
		Driver: agent.DriverSpec{
			Kind: llmagent.DriverKind,
		},
		Inference: agent.InferenceSpec{
			Model:           modelName,
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
		Datasources: datasourceRefs(
			slack.Name,
			gitlab.Name,
			jira.Name,
			confluence.Name,
			"helpdesk_api_docs",
			"local-docs",
			"web_search",
			skills.Name,
		),
	}
	builder := sdk.NewApp(appName).
		WithSource(resource.SourceRef{ID: appName, Scope: resource.ScopeEmbedded, Location: "examples/go/slack-bot-coded"}).
		WithDescription("Type-safe Go configured Slack bot example.").
		WithModel("openrouter", modelName, "chat").
		WithDefaultAgent(agentSpec).
		WithDefaultSession(coresession.Spec{
			Name:        sessionName,
			Description: "Slack channel entrypoint.",
			Agent:       agent.Ref{Name: agentName},
			Metadata:    map[string]string{"app": appName},
		})
	for _, ref := range pluginRefs {
		builder = builder.WithPlugin(ref)
	}
	bundle := builder.Build()
	if len(bundle.Apps) > 0 {
		bundle.Apps[0].Plugins = appPluginRefs(pluginRefs)
		bundle.Apps[0].Identity = identitySpec()
		bundle.Apps[0].Datasource = coreapp.DatasourceSpec{
			Index: coreapp.DatasourceIndexSpec{Concurrency: 4, Freshness: "15m"},
			Datasources: []coredatasource.Spec{
				datasourceSpec(slack.Name, slack.Name, "Slack workspace content available to the bot.", []string{"slack.user", "slack.channel", "slack.message", "slack.thread_message"}, map[string]string{"instance": "slack-bot"}, true),
				datasourceSpec(gitlab.Name, gitlab.Name, "GitLab project and merge request data.", []string{"gitlab.project", "gitlab.merge_request", "gitlab.merge_request_note", "gitlab.pipeline", "gitlab.user", "gitlab.group"}, map[string]string{"instance": gitlab.Name}, true),
				datasourceSpec(jira.Name, jira.Name, "Jira issues visible to the configured plugin instance.", []string{"jira.issue", "jira.project"}, map[string]string{"instance": jira.Name}, false),
				datasourceSpec(confluence.Name, confluence.Name, "Confluence pages visible to the configured plugin instance.", []string{"confluence.page", "confluence.space"}, map[string]string{"instance": confluence.Name}, false),
				datasourceSpec("local-docs", "filesystem", "Local markdown and text files in this example.", []string{"file.document"}, map[string]string{"path": ".", "include": "*.md,*.txt"}, false),
				datasourceSpec("web_search", "web_search", "Public web search results.", []string{"web.search_result"}, nil, false),
			},
		}
	}
	return bundle, nil
}

func configuredPlugins() ([]resource.PluginRef, error) {
	includeThreads := true
	slackRef, err := typedPluginRef(slack.Name, "slack-bot", slack.Config{
		Auth: slack.AuthConfig{Method: slack.TokenMethod},
		Search: slack.SearchConfig{
			Channels:       []string{"dev-team"},
			HistoryWindow:  "90d",
			IncludeThreads: &includeThreads,
		},
	})
	if err != nil {
		return nil, err
	}
	gitlabRef, err := typedPluginRef(gitlab.Name, gitlab.Name, gitlab.Config{
		BaseURL: "https://gitlab.example.com",
		Auth:    gitlab.AuthConfig{Method: gitlab.PersonalAccessTokenMethod, TokenEnv: "GITLAB_PERSONAL_TOKEN"},
	})
	if err != nil {
		return nil, err
	}
	jiraRef, err := typedPluginRef(jira.Name, jira.Name, jira.Config{
		CloudID: "00000000-0000-0000-0000-000000000000",
		Auth:    jira.AuthConfig{Method: jira.APITokenMethod, TokenEnv: "JIRA_API_TOKEN", Email: "bot@example.com"},
	})
	if err != nil {
		return nil, err
	}
	confluenceRef, err := typedPluginRef(confluence.Name, confluence.Name, confluence.Config{
		CloudID: "00000000-0000-0000-0000-000000000000",
		Auth:    confluence.AuthConfig{Method: confluence.APITokenMethod, TokenEnv: "CONFLUENCE_API_TOKEN", Email: "bot@example.com"},
	})
	if err != nil {
		return nil, err
	}
	openAPIRef, err := typedPluginRef(openapi.Name, "helpdesk", openapi.Config{
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
					Method:      coresecret.AuthMethodEnv,
					Kind:        coresecret.KindBearerToken,
					DisplayName: "Helpdesk API bearer token",
					Env:         coresecret.EnvSpec{Name: "HELPDESK_API_TOKEN"},
				},
			}},
		}},
	})
	if err != nil {
		return nil, err
	}
	return []resource.PluginRef{
		{Name: identity.Name},
		slackRef,
		gitlabRef,
		jiraRef,
		confluenceRef,
		openAPIRef,
		{Name: web.Name},
		{Name: skills.Name},
		{Name: memory.Name},
		{Name: datasourceplugin.Name},
	}, nil
}

func typedPluginRef[T any](name, instance string, cfg T) (resource.PluginRef, error) {
	raw, err := pluginhost.ConfigMap(cfg)
	if err != nil {
		return resource.PluginRef{}, err
	}
	return resource.PluginRef{Name: name, Instance: instance, Config: raw}, nil
}

func appPluginRefs(refs []resource.PluginRef) []coreapp.PluginRef {
	out := make([]coreapp.PluginRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, coreapp.PluginRef{Kind: ref.Name, Instance: ref.Instance, Config: ref.Config})
	}
	return out
}

func identitySpec() coreapp.IdentitySpec {
	return coreapp.IdentitySpec{
		Groups: []user.Group{
			{ID: "slack-bot-admin", Trust: user.TrustOperator},
			{ID: "slack-bot-users", Trust: user.TrustInternal},
			{ID: "anonymous", Trust: user.TrustPublic},
		},
		Rules: []user.GroupRule{
			{Match: user.IdentityMatch{Provider: slack.Name, Resolution: user.ResolutionResolved}, Groups: []user.ID{"slack-bot-users"}},
			{Match: user.IdentityMatch{Provider: slack.Name, ProviderID: "U0000000000", Resolution: user.ResolutionResolved}, Groups: []user.ID{"slack-bot-admin"}},
			{Match: user.IdentityMatch{Provider: slack.Name, Resolution: user.ResolutionUnresolved}, Groups: []user.ID{"anonymous"}},
		},
	}
}

func datasourceSpec(name, kind, description string, entities []string, config map[string]string, indexed bool) coredatasource.Spec {
	spec := coredatasource.Spec{
		Name:        coredatasource.Name(name),
		Kind:        kind,
		Description: description,
		Entities:    entityTypes(entities...),
		Config:      config,
	}
	if indexed {
		spec.Index = coredatasource.IndexSpec{Enabled: true, Freshness: "15m"}
	}
	return spec
}

func entityTypes(values ...string) []coredatasource.EntityType {
	out := make([]coredatasource.EntityType, 0, len(values))
	for _, value := range values {
		out = append(out, coredatasource.EntityType(value))
	}
	return out
}

func datasourceRefs(values ...string) []coredatasource.Ref {
	out := make([]coredatasource.Ref, 0, len(values))
	for _, value := range values {
		out = append(out, coredatasource.Ref{Name: coredatasource.Name(value)})
	}
	return out
}

func appPlugins(sys system.System) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		identity.New(),
		slack.New(sys),
		gitlab.New(sys),
		jira.New(sys),
		confluence.New(sys),
		openapi.New(sys),
		web.New(sys),
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
