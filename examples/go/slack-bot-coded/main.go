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
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/user"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/slack"
	datasourceplugin "github.com/fluxplane/fluxplane-core/plugins/native/datasource"
	"github.com/fluxplane/fluxplane-core/plugins/native/identity"
	"github.com/fluxplane/fluxplane-core/plugins/native/memory"
	"github.com/fluxplane/fluxplane-core/plugins/native/skills"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	system "github.com/fluxplane/fluxplane-core/runtime/workspace"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
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
			{Name: "local-docs"},
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
			Name:        "local-docs",
			Kind:        "filesystem",
			Description: "Local markdown and text files in this example.",
			Entities:    []coredatasource.EntityType{"file.document"},
			Config:      map[string]string{"path": ".", "include": "*.md,*.txt"},
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
filters for Slack, local docs, and Helpdesk API docs. Use datasource_get for
exact records and datasource_relation for exact provider relationships such as
Slack channel members or thread messages.
`)
}
