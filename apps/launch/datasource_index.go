package launch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	embedaxon "github.com/fluxplane/agentruntime/adapters/embed/axon"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/plugins/jiraplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/sessionhistoryplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
	"github.com/fluxplane/agentruntime/runtime/system"
)

// DatasourceIndexOptions configures local semantic datasource indexing assembly.
type DatasourceIndexOptions struct {
	Root      string
	Spec      coredistribution.Spec
	Bundles   []resource.ContributionBundle
	Launch    distribution.LaunchConfig
	AuthPath  string
	StorePath string
	Provider  string
	Model     string
	Dev       bool
}

// DatasourceIndexRuntime contains the assembled registry and semantic index.
type DatasourceIndexRuntime struct {
	Registry *coredatasource.Registry
	Index    *semantic.Index
	Close    func() error
}

// NewDatasourceIndexRuntime assembles datasource providers and semantic index dependencies.
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
	connectorEngine, connectorInstances, err := launchConnectorEngine(ctx, opts.AuthPath, opts.Launch.Connectors)
	if err != nil {
		return DatasourceIndexRuntime{}, err
	}
	var index *semantic.Index
	var closeThreadStore func()
	closeFn := func() error {
		if closeThreadStore != nil {
			closeThreadStore()
		}
		if connectorEngine != nil {
			if err := connectorEngine.Close(); err != nil {
				return err
			}
		}
		if index != nil {
			return index.Close()
		}
		return nil
	}
	plugins := datasourceIndexPlugins(hostSystem, connectorEngine, connectorInstances)
	bundles := cloneBundles(opts.Bundles)
	ensureSkillDatasource(bundles)
	if opts.Dev {
		bundles = ensureDevSessionHistoryPlugin(bundles)
		eventRegistry, err := eventregistry.New(eventregistry.Config{Bundles: bundles, EventTypes: eventcatalog.All()})
		if err != nil {
			_ = closeFn()
			return DatasourceIndexRuntime{}, err
		}
		threadStore, closeStore, err := openLocalThreadStore(eventRegistry)
		if err != nil {
			_ = closeFn()
			return DatasourceIndexRuntime{}, err
		}
		closeThreadStore = closeStore
		plugins = appendPluginIfMissing(plugins, sessionhistoryplugin.New(threadStore))
	}
	registry, err := datasourceRegistry(ctx, bundles, plugins, root)
	if err != nil {
		_ = closeFn()
		return DatasourceIndexRuntime{}, err
	}
	index, err = newSemanticIndex(root, bundles, opts.StorePath, opts.Provider, opts.Model)
	if err != nil {
		_ = closeFn()
		return DatasourceIndexRuntime{}, err
	}
	return DatasourceIndexRuntime{Registry: registry, Index: index, Close: closeFn}, nil
}

func datasourceIndexPlugins(hostSystem system.System, executor connectorplugin.Executor, instances []connectorplugin.Instance) []pluginhost.Plugin {
	dispatcher := slackplugin.NewDispatcher()
	return []pluginhost.Plugin{
		slackplugin.NewWithConnectors(dispatcher, executor, connectorInstancesForKind(instances, slackplugin.Name)),
		gitlabplugin.New(executor, connectorInstancesForKind(instances, gitlabplugin.Name)),
		jiraplugin.New(executor, connectorInstancesForKind(instances, jiraplugin.Name)),
		planexecplugin.New(),
		skillplugin.New(),
		textplugin.New(),
		webplugin.New(hostSystem),
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
