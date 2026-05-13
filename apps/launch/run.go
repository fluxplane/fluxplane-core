package launch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	connectorsruntime "github.com/codewandler/connectors/runtime"
	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/browsercdp"
	"github.com/fluxplane/agentruntime/adapters/distribution/localruntime"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/core/channel"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/app"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/plugins/jiraplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

type LocalRuntimeConfig struct {
	Root                string
	Spec                coredistribution.Spec
	Bundles             []resource.ContributionBundle
	Plugins             func(system.System) []pluginhost.Plugin
	ToolProjection      agentruntime.ToolProjectionConfig
	AllowPrivateNetwork bool
	Connectors          map[string]distribution.Connector
	AuthPath            string
}

type AttachOptions struct {
	AuthPath string
}

// AttachLocalRuntime gives a loaded distribution the concrete local session
// opener used by distribution run surfaces.
func AttachLocalRuntime(loaded distribution.Loaded) distribution.Loaded {
	return AttachLocalRuntimeWithOptions(loaded, AttachOptions{})
}

func AttachLocalRuntimeWithOptions(loaded distribution.Loaded, opts AttachOptions) distribution.Loaded {
	if !needsLocalRuntimeOpener(loaded.Distribution.Runtime) {
		return loaded
	}
	loaded.Distribution.Runtime = NewLocalRuntime(LocalRuntimeConfig{
		Root:                loaded.Root,
		Spec:                loaded.Distribution.Spec,
		Bundles:             loaded.Distribution.Bundles,
		AllowPrivateNetwork: true,
		Connectors:          loaded.Launch.Connectors,
		AuthPath:            opts.AuthPath,
	})
	return loaded
}

func NewLocalRuntime(cfg LocalRuntimeConfig) distribution.Runtime {
	return localruntime.Runtime{
		DefaultSession:      cfg.Spec.DefaultSession,
		DefaultConversation: cfg.Spec.DefaultConversation,
		Open: func(ctx context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
			return openLocalSession(ctx, cfg, req)
		},
	}
}

func needsLocalRuntimeOpener(runtime distribution.Runtime) bool {
	if runtime == nil {
		return true
	}
	if local, ok := runtime.(localruntime.Runtime); ok {
		return local.Open == nil
	}
	return false
}

func openLocalSession(ctx context.Context, cfg LocalRuntimeConfig, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	root := cfg.Root
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	hostSystem, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: cfg.AllowPrivateNetwork})
	if err != nil {
		return nil, err
	}
	hostSystem.SetClarifier(terminalui.Prompter{In: os.Stdin, Out: os.Stderr})
	browser, err := browsercdp.New(browsercdp.Config{Workspace: hostSystem.Workspace(), Headless: true})
	if err == nil {
		hostSystem.SetBrowser(browser)
	} else if req.Debug {
		_, _ = fmt.Fprintf(os.Stderr, "browser disabled: %v\n", err)
	}

	connectorEngine, connectorInstances, err := runConnectorEngine(ctx, cfg.AuthPath, cfg.Connectors)
	if err != nil {
		return nil, err
	}
	if connectorEngine != nil {
		defer func() { _ = connectorEngine.Close() }()
	}

	bundles := cloneBundles(cfg.Bundles)
	ensureSkillDatasource(bundles)
	plugins := basePlugins(hostSystem, connectorEngine, connectorInstances)
	if cfg.Plugins != nil {
		plugins = cfg.Plugins(hostSystem)
	}
	if hasAnyDatasource(bundles) {
		registry, err := datasourceRegistry(ctx, bundles, plugins, root)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, datasourceplugin.New(registry))
		ensurePluginRef(bundles, datasourceplugin.Name)
	}
	composition, err := app.Compose(app.Config{
		Context: ctx,
		Bundles: bundles,
		Plugins: plugins,
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:        localSandbox{Root: root},
			ACL:            localACL{},
			CommandRisk:    distrun.CommandRisk(root),
			MaxCommandRisk: operation.RiskMedium,
			AllowPure:      true,
		})),
	})
	if err != nil {
		return nil, err
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModelResolver: distrun.ModelResolver{
			Provider:     req.Provider,
			Model:        req.Model,
			DefaultModel: cfg.Spec.DefaultModel.Model,
			Debug:        req.Debug,
		},
		LLMStreamPolicy: distrun.DebugStreamPolicy(req.Debug),
		ToolProjection: firstToolProjection(cfg.ToolProjection, agentruntime.ToolProjectionConfig{
			AllowSideEffects:      true,
			MaxRisk:               operation.RiskMedium,
			IncludeBareOperations: true,
		}),
		Channel: channel.Ref{Name: "local"},
		Caller: policy.Caller{
			Kind: policy.CallerUser,
			Principal: policy.Principal{
				Kind: "user",
				ID:   "agentsdk",
				Name: "agentsdk",
			},
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		return nil, err
	}
	return service.Open(ctx, agentruntime.OpenRequest{
		Session:      req.Session,
		Conversation: req.Conversation,
	})
}

func firstToolProjection(value, fallback agentruntime.ToolProjectionConfig) agentruntime.ToolProjectionConfig {
	if value.AllowSideEffects ||
		value.MaxRisk != "" ||
		value.IncludeBareOperations ||
		value.PreferCommandProjection ||
		len(value.Commands) > 0 ||
		len(value.Operations) > 0 {
		return value
	}
	return fallback
}

func basePlugins(hostSystem system.System, connectorEngine connectorplugin.Executor, connectorInstances []connectorplugin.Instance) []pluginhost.Plugin {
	dispatcher := slackplugin.NewDispatcher()
	return []pluginhost.Plugin{
		codingplugin.New(hostSystem),
		slackplugin.NewWithConnectors(dispatcher, connectorEngine, connectorInstancesForKind(connectorInstances, slackplugin.Name)),
		gitlabplugin.New(connectorEngine, connectorInstancesForKind(connectorInstances, gitlabplugin.Name)),
		jiraplugin.New(connectorEngine, connectorInstancesForKind(connectorInstances, jiraplugin.Name)),
		planexecplugin.New(),
		skillplugin.New(),
		textplugin.New(),
		webplugin.New(hostSystem),
	}
}

func runConnectorEngine(ctx context.Context, authPath string, connectors map[string]distribution.Connector) (*connectorsruntime.Engine, []connectorplugin.Instance, error) {
	if len(connectors) == 0 {
		return nil, nil, nil
	}
	engine, providers, err := newServeConnectEngine(ctx, authPath)
	if err != nil {
		return nil, nil, err
	}
	knownProviders := map[string]bool{}
	for _, provider := range providers {
		knownProviders[provider] = true
	}
	names := make([]string, 0, len(connectors))
	for name := range connectors {
		names = append(names, name)
	}
	sort.Strings(names)
	instances := make([]connectorplugin.Instance, 0, len(names))
	for _, instanceID := range names {
		connector := connectors[instanceID]
		kind := strings.TrimSpace(connector.Kind)
		if kind == "" {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("run: connector instance %q kind is empty", instanceID)
		}
		if !knownProviders[kind] {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("run: connector instance %q uses unknown provider %q (available: %s)", instanceID, kind, strings.Join(providers, ", "))
		}
		stored, err := engine.Instances.Load(ctx, instanceID)
		if err != nil {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("run: load connector instance %q: %w", instanceID, err)
		}
		if stored.Connector != kind {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("run: connector instance %q has kind %q, want %q", instanceID, stored.Connector, kind)
		}
		if err := engine.ConnectInstance(ctx, instanceID); err != nil {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("run: connect %s connector instance %q: %w", kind, instanceID, err)
		}
		instances = append(instances, connectorplugin.Instance{ID: instanceID, Kind: kind})
	}
	return engine, instances, nil
}

func datasourceRegistry(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin, root string) (*coredatasource.Registry, error) {
	host, err := pluginhost.New(plugins...)
	if err != nil {
		return nil, err
	}
	resolved, err := host.Resolve(ctx, pluginRefs(bundles)...)
	if err != nil {
		return nil, err
	}
	var providers []coredatasource.Provider
	for _, contribution := range resolved.DatasourceProviders {
		providers = append(providers, contribution.Provider)
	}
	providers = append(providers, datasourceplugin.NewFilesystemProvider(os.DirFS(root)))
	return datasourceplugin.BuildRegistry(ctx, datasourceSpecs(bundles), providers)
}

func ensureSkillDatasource(bundles []resource.ContributionBundle) {
	if !bundleHasPlugin(bundles, skillplugin.Name) || hasDatasource(bundles, skillplugin.DatasourceName) || len(bundles) == 0 {
		return
	}
	bundles[0].Datasources = append(bundles[0].Datasources, skillplugin.DatasourceSpec())
}

func ensurePluginRef(bundles []resource.ContributionBundle, name string) {
	if len(bundles) == 0 || bundleHasPlugin(bundles, name) {
		return
	}
	bundles[0].Plugins = append(bundles[0].Plugins, resource.PluginRef{Name: name})
}

func bundleHasPlugin(bundles []resource.ContributionBundle, name string) bool {
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			if ref.Name == name {
				return true
			}
		}
	}
	return false
}

func hasAnyDatasource(bundles []resource.ContributionBundle) bool {
	return len(datasourceSpecs(bundles)) > 0
}

func hasDatasource(bundles []resource.ContributionBundle, name coredatasource.Name) bool {
	for _, spec := range datasourceSpecs(bundles) {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func datasourceSpecs(bundles []resource.ContributionBundle) []coredatasource.Spec {
	var out []coredatasource.Spec
	for _, bundle := range bundles {
		out = append(out, bundle.Datasources...)
	}
	return out
}

func pluginRefs(bundles []resource.ContributionBundle) []resource.PluginRef {
	seen := map[string]bool{}
	var out []resource.PluginRef
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			if ref.Name == datasourceplugin.Name || seen[ref.Name] {
				continue
			}
			seen[ref.Name] = true
			out = append(out, ref)
		}
	}
	return out
}

func cloneBundles(bundles []resource.ContributionBundle) []resource.ContributionBundle {
	out := make([]resource.ContributionBundle, len(bundles))
	copy(out, bundles)
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type localSandbox struct {
	Root string
}

func (s localSandbox) Check(_ operation.Context, spec operation.Spec, input operation.Value) error {
	if spec.Semantics.Effects.Has(operation.EffectProcess) {
		_ = input
		_ = s.Root
	}
	return nil
}

type localACL struct{}

func (localACL) Authorize(operation.Context, operation.Spec, operation.Value) error {
	return nil
}
