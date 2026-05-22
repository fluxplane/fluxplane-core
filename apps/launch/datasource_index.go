package launch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	embedaxon "github.com/fluxplane/engine/adapters/embeddings/axon"
	coreapp "github.com/fluxplane/engine/core/app"
	coredata "github.com/fluxplane/engine/core/data"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	coredistribution "github.com/fluxplane/engine/core/distribution"
	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/orchestration/distribution"
	"github.com/fluxplane/engine/orchestration/eventregistry"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/plugins/integrations/confluence"
	"github.com/fluxplane/engine/plugins/integrations/gitlab"
	"github.com/fluxplane/engine/plugins/integrations/jira"
	"github.com/fluxplane/engine/plugins/integrations/slack"
	"github.com/fluxplane/engine/plugins/integrations/web"
	"github.com/fluxplane/engine/plugins/native/datasource"
	"github.com/fluxplane/engine/plugins/native/sessionhistory"
	"github.com/fluxplane/engine/plugins/native/skills"
	"github.com/fluxplane/engine/plugins/native/task"
	"github.com/fluxplane/engine/plugins/native/text"
	"github.com/fluxplane/engine/plugins/support/eventcatalog"
	"github.com/fluxplane/engine/runtime/datasource/semantic"
	runtimesecret "github.com/fluxplane/engine/runtime/secret"
	"github.com/fluxplane/engine/runtime/system"
)

// DatasourceIndexOptions configures local datasource indexing assembly.
type DatasourceIndexOptions struct {
	Root               string
	Spec               coredistribution.Spec
	Bundles            []resource.ContributionBundle
	Launch             distribution.LaunchConfig
	AuthPath           string
	AllowPluginAuthEnv bool
	StorePath          string
	Provider           string
	Model              string
	Dev                bool
	PluginFactory      func(PluginFactoryContext) []pluginhost.Plugin
}

// DatasourceIndexRuntime contains the assembled registry and datasource index.
type DatasourceIndexRuntime struct {
	Registry *coredatasource.Registry
	Index    *semantic.Index
	Data     coredata.Store
	Sources  []coredata.SourceSpec
	Config   coreapp.DatasourceIndexSpec
	Close    func() error
}

// NewDatasourceIndexRuntime assembles datasource providers and index dependencies.
func NewDatasourceIndexRuntime(ctx context.Context, opts DatasourceIndexOptions) (DatasourceIndexRuntime, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return DatasourceIndexRuntime{}, err
	}
	hostSystem, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		return DatasourceIndexRuntime{}, err
	}
	var index *semantic.Index
	var dataStore coredata.Store
	var closeDataStore func() error
	var eventStore event.Store
	var closeThreadStore func()
	closeFn := func() error {
		if closeDataStore != nil {
			if err := closeDataStore(); err != nil {
				return err
			}
		}
		if closeThreadStore != nil {
			closeThreadStore()
		}
		if index != nil {
			return index.Close()
		}
		return nil
	}
	dispatcher := slack.NewDispatcher()
	nativeStore := runtimesecret.NewFileStore(nativeAuthPath(opts.AuthPath))
	nativeResolver := nativeAuthResolver(hostSystem, nativeStore, opts.AllowPluginAuthEnv)
	plugins := datasourceIndexPlugins(hostSystem, dispatcher, nativeStore, nativeResolver)
	if opts.PluginFactory != nil {
		for _, plugin := range opts.PluginFactory(PluginFactoryContext{
			System:             hostSystem,
			Dispatcher:         dispatcher,
			NativeAuthStore:    nativeStore,
			NativeAuthResolver: nativeResolver,
		}) {
			plugins = replacePlugin(plugins, plugin)
		}
	}
	bundles := cloneBundles(opts.Bundles)
	ensureSkillDatasource(bundles)
	if opts.Dev {
		bundles = ensureDevSessionHistoryPlugin(bundles)
		eventRegistry, err := eventregistry.New(eventregistry.Config{EventTypes: appendBundleEventTypes(eventcatalog.All(), bundles)})
		if err != nil {
			_ = closeFn()
			return DatasourceIndexRuntime{}, err
		}
		threadStore, openedEventStore, closeStore, err := openLocalThreadStore(eventRegistry)
		if err != nil {
			_ = closeFn()
			return DatasourceIndexRuntime{}, err
		}
		eventStore = openedEventStore
		closeThreadStore = closeStore
		plugins = appendPluginIfMissing(plugins, sessionhistory.New(threadStore))
	}
	index, err = newSemanticIndex(root, bundles, opts.StorePath, opts.Provider, opts.Model)
	if err != nil {
		_ = closeFn()
		return DatasourceIndexRuntime{}, err
	}
	dataStore, closeDataStore, err = openDataStore(ctx, opts.Launch.Data)
	if err != nil {
		_ = closeFn()
		return DatasourceIndexRuntime{}, err
	}
	dataSources := datasourceDataSources(bundles)
	pluginDataSources, err := resolvedPluginDataSources(ctx, bundles, plugins, eventStore, dataStore)
	if err != nil {
		_ = closeFn()
		return DatasourceIndexRuntime{}, err
	}
	dataSources = append(dataSources, pluginDataSources...)
	registry, err := datasourceRegistryWithOptions(ctx, bundles, plugins, root, eventStore, dataStore, datasource.RegistryOptions{SemanticIndex: index, DataSources: dataSources})
	if err != nil {
		_ = closeFn()
		return DatasourceIndexRuntime{}, err
	}
	return DatasourceIndexRuntime{Registry: registry, Index: index, Data: dataStore, Sources: dataSources, Config: datasourceIndexFromBundles(bundles), Close: closeFn}, nil
}

func datasourceIndexPlugins(hostSystem system.System, dispatcher *slack.Dispatcher, nativeStore runtimesecret.FileStore, nativeResolver runtimesecret.Resolver) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		slack.NewWithResolver(hostSystem, dispatcher, nativeResolver, nativeStore),
		gitlab.NewWithResolver(hostSystem, nativeResolver),
		jira.NewWithResolver(hostSystem, nativeStore, nativeResolver),
		confluence.NewWithResolver(hostSystem, nativeStore, nativeResolver),
		task.New(),
		skills.New(),
		text.New(),
		web.New(hostSystem),
	}
}

func semanticSearchFromBundles(bundles []resource.ContributionBundle) coreapp.SemanticSearchSpec {
	for _, bundle := range bundles {
		for _, app := range bundle.Apps {
			return app.SemanticSearch
		}
	}
	return coreapp.SemanticSearchSpec{}
}

func datasourceIndexFromBundles(bundles []resource.ContributionBundle) coreapp.DatasourceIndexSpec {
	for _, bundle := range bundles {
		for _, app := range bundle.Apps {
			return app.Datasource.Index
		}
	}
	return coreapp.DatasourceIndexSpec{}
}

func DatasourceIndexBuildConfig(cfg coreapp.DatasourceIndexSpec, concurrencyOverride int, freshnessOverride string) (int, time.Duration, error) {
	concurrency := concurrencyOverride
	if concurrency <= 0 {
		concurrency = cfg.Concurrency
	}
	if concurrency <= 0 {
		concurrency = 4
	}
	freshnessValue := strings.TrimSpace(freshnessOverride)
	if freshnessValue == "" {
		freshnessValue = strings.TrimSpace(cfg.Freshness)
	}
	if freshnessValue == "" {
		return concurrency, 0, nil
	}
	freshness, err := time.ParseDuration(freshnessValue)
	if err != nil {
		return 0, 0, fmt.Errorf("datasource index freshness: %w", err)
	}
	return concurrency, freshness, nil
}

func newSemanticIndex(root string, bundles []resource.ContributionBundle, storeOverride, providerOverride, modelOverride string) (*semantic.Index, error) {
	appSemantic := semanticSearchFromBundles(bundles)
	storePath := strings.TrimSpace(storeOverride)
	if storePath == "" {
		storePath = appSemantic.Store.Path
	}
	if storePath == "" {
		storePath = filepath.Join(root, ".agents", "index", "datasources.json")
	} else if !filepath.IsAbs(storePath) {
		storePath = filepath.Join(root, storePath)
	}
	providerName := strings.ToLower(firstNonEmptyString(providerOverride, appSemantic.Embeddings.Provider, embedaxon.ProviderName))
	embedderModel := firstNonEmptyString(modelOverride, appSemantic.Embeddings.Model)
	embedder, model, err := semanticEmbedder(providerName, embedderModel)
	if err != nil {
		return nil, err
	}
	return semantic.New(
		embedder,
		semantic.NewJSONStore(storePath),
		semantic.Config{
			Model: model,
			Chunking: coredatasource.ChunkingSpec{
				Strategy:      appSemantic.Defaults.Chunking.Strategy,
				TargetTokens:  appSemantic.Defaults.Chunking.TargetTokens,
				OverlapTokens: appSemantic.Defaults.Chunking.OverlapTokens,
			},
			Retrieval: coredatasource.RetrievalSpec{
				Mode:     appSemantic.Defaults.Retrieval.Mode,
				Limit:    appSemantic.Defaults.Retrieval.Limit,
				MinScore: appSemantic.Defaults.Retrieval.MinScore,
			},
		},
	)
}

func semanticEmbedder(provider, model string) (semantic.Embedder, string, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", embedaxon.ProviderName, "hugot", "local":
		embedder := embedaxon.New(embedaxon.Config{Model: model})
		return embedder, embedder.Model(), nil
	case "hash", "local/hash", "local/hash-embedding":
		model = firstNonEmptyString(model, "local/hash-embedding")
		return semantic.HashEmbedder{ModelName: model}, model, nil
	case "openai":
		return nil, "", fmt.Errorf("semantic embeddings provider %q is not implemented yet", provider)
	default:
		return nil, "", fmt.Errorf("semantic embeddings provider %q is not supported", provider)
	}
}
