package launch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	runtimesecret "github.com/fluxplane/fluxplane-auth/authsecret"
	embedaxon "github.com/fluxplane/fluxplane-core/adapters/embeddings/axon"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	"github.com/fluxplane/fluxplane-core/orchestration/eventregistry"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/slack"
	"github.com/fluxplane/fluxplane-core/plugins/native/datasource"
	"github.com/fluxplane/fluxplane-core/plugins/native/sessionhistory"
	"github.com/fluxplane/fluxplane-core/plugins/native/skills"
	"github.com/fluxplane/fluxplane-core/plugins/native/task"
	"github.com/fluxplane/fluxplane-core/plugins/native/text"
	usageplugin "github.com/fluxplane/fluxplane-core/plugins/native/usage"
	"github.com/fluxplane/fluxplane-core/plugins/support/eventcatalog"
	"github.com/fluxplane/fluxplane-core/runtime/datasource/semantic"
	"github.com/fluxplane/fluxplane-event"
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
	hostSystem, err := newHost(hostConfig{Root: root, AllowPrivateNetwork: true})
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
	auth := NewPluginAuthContext(PluginAuthOptions{
		Environment:        pluginAuthEnvironment(hostSystem),
		AuthPath:           opts.AuthPath,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
	})
	plugins := datasourceIndexPlugins(hostSystem, dispatcher, auth.Store, auth.Resolver)
	if opts.PluginFactory != nil {
		for _, plugin := range opts.PluginFactory(PluginFactoryContext{
			System:             hostSystem,
			Dispatcher:         dispatcher,
			NativeAuthStore:    auth.Store,
			NativeAuthResolver: auth.Resolver,
		}) {
			plugins = replacePlugin(plugins, plugin)
		}
	}
	bundles := cloneBundles(opts.Bundles)
	ensureSkillDatasource(bundles)
	if opts.Dev {
		bundles = ensureDevSessionHistoryPlugin(bundles)
		bundles = ensureDevUsagePlugin(bundles)
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
		plugins = appendPluginIfMissing(plugins, usageplugin.New(nil))
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
	registry, err := datasourceRegistryWithOptions(ctx, bundles, plugins, root, eventStore, dataStore, nil, nil, nil, datasource.RegistryOptions{SemanticIndex: index, DataSources: dataSources})
	if err != nil {
		_ = closeFn()
		return DatasourceIndexRuntime{}, err
	}
	return DatasourceIndexRuntime{Registry: registry, Index: index, Data: dataStore, Sources: dataSources, Config: datasourceIndexFromBundles(bundles), Close: closeFn}, nil
}

func datasourceIndexPlugins(hostSystem fpsystem.System, dispatcher *slack.Dispatcher, nativeStore sharedsecret.FileStore, nativeResolver runtimesecret.Resolver) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		slack.NewWithResolver(hostSystem, dispatcher, nativeResolver, nativeStore),
		task.New(),
		skills.New(),
		text.New(),
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
	storePath, err := semanticIndexStorePath(root, appSemantic, storeOverride)
	if err != nil {
		return nil, err
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

func semanticIndexStorePath(root string, appSemantic coreapp.SemanticSearchSpec, storeOverride string) (string, error) {
	storePath := strings.TrimSpace(storeOverride)
	if storePath == "" {
		storePath = appSemantic.Store.Path
	}
	if storePath != "" {
		if filepath.IsAbs(storePath) {
			return storePath, nil
		}
		return filepath.Join(root, storePath), nil
	}
	return defaultSemanticIndexStorePath(root)
}

var semanticIndexPathSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func defaultSemanticIndexStorePath(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err == nil {
		root = absRoot
	}
	root = filepath.Clean(root)
	stateDir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	name := semanticIndexPathSanitizer.ReplaceAllString(filepath.Base(root), "-")
	name = strings.Trim(name, ".-")
	if name == "" {
		name = "app"
	}
	sum := sha256.Sum256([]byte(root))
	key := name + "-" + hex.EncodeToString(sum[:8])
	return filepath.Join(stateDir, "datasource-indexes", key, "datasources.json"), nil
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
