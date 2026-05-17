package launch

import (
	"context"
	"fmt"
	"os"
	osuser "os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/codewandler/connectors/integrate"
	connectorsruntime "github.com/codewandler/connectors/runtime"
	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/browsercdp"
	"github.com/fluxplane/agentruntime/adapters/distribution/localruntime"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/core/channel"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/agentfactory"
	"github.com/fluxplane/agentruntime/orchestration/app"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/taskexecutor"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/plugins/imageplugin"
	"github.com/fluxplane/agentruntime/plugins/jiraplugin"
	"github.com/fluxplane/agentruntime/plugins/openaiplugin"
	"github.com/fluxplane/agentruntime/plugins/sessionhistoryplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
	"github.com/fluxplane/agentruntime/plugins/taskplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	"github.com/fluxplane/agentruntime/plugins/workspaceplugin"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
	runtimetask "github.com/fluxplane/agentruntime/runtime/task"
)

type LocalRuntimeConfig struct {
	Root                string
	Spec                coredistribution.Spec
	Bundles             []resource.ContributionBundle
	Plugins             func(system.System) []pluginhost.Plugin
	ToolProjection      agentruntime.ToolProjectionConfig
	ModelResolver       agentfactory.ModelResolver
	AllowPrivateNetwork bool
	Launch              distribution.LaunchConfig
	AuthPath            string
	Dev                 bool
}

type AttachOptions struct {
	AuthPath string
	Dev      bool
}

// RuntimeOptions describes the surface-neutral local launch inputs shared by
// run, serve, and first-party distribution executables.
type RuntimeOptions struct {
	Root                string
	Spec                coredistribution.Spec
	Bundles             []resource.ContributionBundle
	Launch              distribution.LaunchConfig
	AuthPath            string
	Provider            string
	Model               string
	Thinking            string
	ThinkingSet         bool
	Effort              string
	EffortSet           bool
	Debug               bool
	Yolo                bool
	Dev                 bool
	Plugins             func(system.System) []pluginhost.Plugin
	ToolProjection      agentruntime.ToolProjectionConfig
	ModelResolver       agentfactory.ModelResolver
	AllowPrivateNetwork bool
}

// Runtime is the composed local runtime plus the resources needed by hosting
// surfaces such as serve.
type Runtime struct {
	Service     agentruntime.ChannelClient
	Composition app.Composition
	System      system.System
	Dispatcher  *slackplugin.Dispatcher
	Caller      policy.Caller
	Trust       policy.Trust
	Close       func()
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
		Launch:              loaded.Launch,
		AuthPath:            opts.AuthPath,
		Dev:                 opts.Dev,
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
	runtime, err := Launch(ctx, RuntimeOptions{
		Root:                cfg.Root,
		Spec:                cfg.Spec,
		Bundles:             cfg.Bundles,
		Launch:              mergeLaunchConfig(cfg.Launch, req.Launch),
		AuthPath:            cfg.AuthPath,
		Provider:            req.Provider,
		Model:               req.Model,
		Thinking:            req.Thinking,
		ThinkingSet:         req.ThinkingSet,
		Effort:              req.Effort,
		EffortSet:           req.EffortSet,
		Debug:               req.Debug,
		Yolo:                req.Yolo,
		Dev:                 cfg.Dev || req.Dev,
		Plugins:             cfg.Plugins,
		ToolProjection:      cfg.ToolProjection,
		ModelResolver:       cfg.ModelResolver,
		AllowPrivateNetwork: cfg.AllowPrivateNetwork,
	})
	if err != nil {
		return nil, err
	}
	session, err := runtime.Service.Open(ctx, agentruntime.OpenRequest{
		Session:      req.Session,
		Conversation: req.Conversation,
	})
	if err != nil {
		runtime.Close()
		return nil, err
	}
	return &sessionWithRuntime{SessionHandle: session, closeRuntime: runtime.Close}, nil
}

func mergeLaunchConfig(base, override distribution.LaunchConfig) distribution.LaunchConfig {
	if len(override.Connectors) > 0 {
		base.Connectors = override.Connectors
	}
	if len(override.Listeners) > 0 {
		base.Listeners = override.Listeners
	}
	if len(override.Channels) > 0 {
		base.Channels = override.Channels
	}
	if len(override.Workspace.Roots) > 0 {
		base.Workspace.Roots = append(base.Workspace.Roots, override.Workspace.Roots...)
	}
	if strings.TrimSpace(override.Workspace.ScratchRoot) != "" {
		base.Workspace.ScratchRoot = override.Workspace.ScratchRoot
	}
	return base
}

type sessionWithRuntime struct {
	clientapi.SessionHandle
	closeOnce    sync.Once
	closeRuntime func()
}

func (s *sessionWithRuntime) Close(ctx context.Context) error {
	err := s.SessionHandle.Close(ctx)
	if s.closeRuntime != nil {
		s.closeOnce.Do(s.closeRuntime)
	}
	return err
}

// Launch composes a loaded distribution into a runnable local service.
func Launch(ctx context.Context, opts RuntimeOptions) (Runtime, error) {
	root := opts.Root
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Runtime{}, err
	}
	hostSystem, err := system.NewHost(system.Config{
		Root:                root,
		Workspace:           systemWorkspaceConfig(opts.Launch.Workspace),
		AllowPrivateNetwork: opts.AllowPrivateNetwork,
	})
	if err != nil {
		return Runtime{}, err
	}
	hostSystem.SetClarifier(terminalui.Prompter{In: os.Stdin, Out: os.Stderr})
	browser, err := browsercdp.New(browsercdp.Config{Workspace: hostSystem.Workspace(), Headless: browserHeadless()})
	if err == nil {
		hostSystem.SetBrowser(browser)
	} else if opts.Debug {
		_, _ = fmt.Fprintf(os.Stderr, "browser disabled: %v\n", err)
	}

	connectorEngine, connectorInstances, err := launchConnectorEngine(ctx, opts.AuthPath, opts.Launch.Connectors)
	if err != nil {
		return Runtime{}, err
	}
	var semanticIndex interface{ Close() error }
	var closeThreadStore func()
	var stopTaskScheduler context.CancelFunc
	closeRuntime := func() {
		if stopTaskScheduler != nil {
			stopTaskScheduler()
		}
		if semanticIndex != nil {
			_ = semanticIndex.Close()
		}
		if closeThreadStore != nil {
			closeThreadStore()
		}
		if connectorEngine != nil {
			_ = connectorEngine.Close()
		}
	}

	dispatcher := slackplugin.NewDispatcher()
	bundles := cloneBundles(opts.Bundles)
	ensureSkillDatasource(bundles)
	if opts.Dev {
		bundles = ensureDevSessionHistoryPlugin(bundles)
	}
	eventRegistry, err := eventregistry.New(eventregistry.Config{EventTypes: appendBundleEventTypes(eventcatalog.All(), bundles)})
	if err != nil {
		closeRuntime()
		return Runtime{}, err
	}
	threadStore, eventStore, closeStore, err := openLocalThreadStore(eventRegistry)
	if err != nil {
		closeRuntime()
		return Runtime{}, err
	}
	closeThreadStore = closeStore
	var taskScheduler *taskexecutor.Scheduler
	var taskWorker *taskexecutor.DeferredWorker
	if bundleHasPlugin(bundles, taskplugin.Name) {
		taskStore, err := runtimetask.NewStore(eventStore)
		if err != nil {
			closeRuntime()
			return Runtime{}, err
		}
		taskWorker = &taskexecutor.DeferredWorker{}
		taskScheduler, err = taskexecutor.New(taskexecutor.Config{
			Store:  taskStore,
			Worker: taskWorker,
		})
		if err != nil {
			closeRuntime()
			return Runtime{}, err
		}
		eventStore = taskexecutor.NewNotifyingEventStore(eventStore, taskScheduler)
	}
	available := availablePlugins(hostSystem, connectorEngine, connectorInstances, dispatcher, taskScheduler)
	if opts.Plugins != nil {
		available = opts.Plugins(hostSystem)
	}
	if taskScheduler != nil {
		available = replacePlugin(available, taskplugin.NewWithRunnerAndSystem(taskScheduler, hostSystem))
	}
	if opts.Dev {
		available = appendPluginIfMissing(available, sessionhistoryplugin.New(threadStore))
	}
	plugins, err := selectDeclaredPlugins(bundles, available)
	if err != nil {
		closeRuntime()
		return Runtime{}, err
	}
	if opts.Dev || hasAnyDatasource(bundles) {
		registry, err := datasourceRegistry(ctx, bundles, plugins, root)
		if err != nil {
			closeRuntime()
			return Runtime{}, err
		}
		index, err := newSemanticIndex(root, bundles, "", "", "")
		if err != nil {
			closeRuntime()
			return Runtime{}, err
		}
		semanticIndex = index
		plugins = append(plugins, datasourceplugin.NewWithSemantic(registry, index))
		ensurePluginRef(bundles, datasourceplugin.Name)
	}
	approval := operationruntime.ApprovalGate(terminalui.Approver{In: os.Stdin, Out: os.Stderr})
	if opts.Yolo {
		approval = operationruntime.AutoApprover{}
	}
	var bundleTransforms []app.BundleTransform
	if opts.Dev {
		bundleTransforms = append(bundleTransforms, enableDevSessionHistory)
	}
	composition, err := app.Compose(app.Config{
		Context:          ctx,
		Bundles:          bundles,
		Plugins:          plugins,
		BundleTransforms: bundleTransforms,
		EventStore:       eventStore,
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:                   localSandbox{Root: root},
			ACL:                       localACL{},
			CommandRisk:               distrun.CommandRisk(root),
			Approval:                  approval,
			MaxCommandRisk:            operation.RiskMedium,
			ApproveOverMaxCommandRisk: opts.Yolo,
			AllowPure:                 true,
		})),
	})
	if err != nil {
		closeRuntime()
		return Runtime{}, err
	}
	localCaller := localUserCaller()
	localTrust := policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}, VerifiedBy: "local_process", Reason: "local runtime"}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		ThreadStore: threadStore,
		EventStore:  eventStore,
		LLMModelResolver: firstModelResolver(opts.ModelResolver, distrun.ModelResolver{
			Provider:        opts.Provider,
			Model:           opts.Model,
			Thinking:        opts.Thinking,
			ThinkingSet:     opts.ThinkingSet,
			Effort:          opts.Effort,
			EffortSet:       opts.EffortSet,
			DefaultProvider: opts.Spec.DefaultModel.Provider,
			DefaultModel:    opts.Spec.DefaultModel.Model,
			Debug:           opts.Debug,
			ProviderSpecs:   composition.LLMProviderSpecs,
			ModelAliases:    composition.LLMModelAliases,
		}),
		LLMStreamPolicy: distrun.DebugStreamPolicy(opts.Debug),
		ToolProjection: firstToolProjection(opts.ToolProjection, agentruntime.ToolProjectionConfig{
			AllowSideEffects:      true,
			MaxRisk:               operation.RiskMedium,
			IncludeBareOperations: true,
		}),
		Channel:  channel.Ref{Name: "local"},
		Caller:   localCaller,
		Trust:    localTrust,
		Security: composition.Security,
	})
	if err != nil {
		closeRuntime()
		return Runtime{}, err
	}
	if taskScheduler != nil && taskWorker != nil {
		taskWorker.Set(taskexecutor.ChannelWorker{Client: service})
		taskScheduler.SetRuntimeEventPublisher(taskexecutor.RuntimeEventPublisherFunc(service.PublishRuntimeEvent))
		schedulerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		stopTaskScheduler = cancel
		go taskScheduler.Start(schedulerCtx)
	}
	return Runtime{
		Service:     service,
		Composition: composition,
		System:      hostSystem,
		Dispatcher:  dispatcher,
		Caller:      localCaller,
		Trust:       localTrust,
		Close:       closeRuntime,
	}, nil
}

func systemWorkspaceConfig(cfg distribution.WorkspaceConfig) system.WorkspaceConfig {
	out := system.WorkspaceConfig{ScratchRoot: strings.TrimSpace(cfg.ScratchRoot)}
	for _, root := range cfg.Roots {
		out.Roots = append(out.Roots, system.WorkspaceRootConfig{
			Name:   strings.TrimSpace(root.Name),
			Path:   strings.TrimSpace(root.Path),
			Access: system.WorkspaceAccess(strings.TrimSpace(root.Access)),
			Create: root.Create,
		})
	}
	return out
}

func browserHeadless() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTRUNTIME_BROWSER_HEADLESS"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
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

func availablePlugins(hostSystem system.System, connectorEngine connectorplugin.Executor, connectorInstances []connectorplugin.Instance, dispatcher *slackplugin.Dispatcher, taskRunner taskplugin.TaskRunner) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		workspaceplugin.New(hostSystem),
		codingplugin.New(hostSystem),
		openaiplugin.New(),
		slackplugin.NewWithConnectors(dispatcher, connectorEngine, connectorInstancesForKind(connectorInstances, slackplugin.Name)),
		gitlabplugin.New(connectorEngine, connectorInstancesForKind(connectorInstances, gitlabplugin.Name)),
		imageplugin.New(hostSystem),
		jiraplugin.New(connectorEngine, connectorInstancesForKind(connectorInstances, jiraplugin.Name)),
		taskplugin.NewWithRunnerAndSystem(taskRunner, hostSystem),
		skillplugin.New(),
		textplugin.New(),
		webplugin.New(hostSystem),
	}
}

func appendPluginIfMissing(plugins []pluginhost.Plugin, plugin pluginhost.Plugin) []pluginhost.Plugin {
	if plugin == nil {
		return plugins
	}
	name := strings.TrimSpace(plugin.Manifest().Name)
	for _, existing := range plugins {
		if existing != nil && strings.TrimSpace(existing.Manifest().Name) == name {
			return plugins
		}
	}
	return append(plugins, plugin)
}

func replacePlugin(plugins []pluginhost.Plugin, plugin pluginhost.Plugin) []pluginhost.Plugin {
	if plugin == nil {
		return plugins
	}
	name := strings.TrimSpace(plugin.Manifest().Name)
	for i, existing := range plugins {
		if existing != nil && strings.TrimSpace(existing.Manifest().Name) == name {
			out := append([]pluginhost.Plugin(nil), plugins...)
			out[i] = plugin
			return out
		}
	}
	return append(plugins, plugin)
}

func connectorInstancesForKind(instances []connectorplugin.Instance, kind string) []connectorplugin.Instance {
	var out []connectorplugin.Instance
	for _, instance := range instances {
		if instance.Kind == kind {
			out = append(out, instance)
		}
	}
	return out
}

func selectDeclaredPlugins(bundles []resource.ContributionBundle, available []pluginhost.Plugin) ([]pluginhost.Plugin, error) {
	byName := map[string]pluginhost.Plugin{}
	for _, plugin := range available {
		if plugin == nil {
			continue
		}
		name := strings.TrimSpace(plugin.Manifest().Name)
		if name == "" {
			return nil, fmt.Errorf("launch: plugin has empty name")
		}
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("launch: plugin %q is registered more than once", name)
		}
		byName[name] = plugin
	}
	refs := make([]resource.PluginRef, 0, len(pluginRefs(bundles))+1)
	for _, ref := range pluginRefs(bundles) {
		if ref.Name != workspaceplugin.Name {
			refs = append(refs, ref)
		}
	}
	refs = append([]resource.PluginRef{{Name: workspaceplugin.Name}}, refs...)
	plugins := make([]pluginhost.Plugin, 0, len(refs))
	for _, ref := range refs {
		plugin, ok := byName[ref.Name]
		if !ok {
			return nil, fmt.Errorf("launch: plugin %q is not available", ref.Name)
		}
		plugins = append(plugins, plugin)
	}
	return plugins, nil
}

func launchConnectorEngine(ctx context.Context, authPath string, connectors map[string]distribution.Connector) (*connectorsruntime.Engine, []connectorplugin.Instance, error) {
	if len(connectors) == 0 {
		return nil, nil, nil
	}
	engine, providers, err := newConnectEngine(ctx, authPath)
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
			return nil, nil, fmt.Errorf("launch: connector instance %q kind is empty", instanceID)
		}
		if !knownProviders[kind] {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("launch: connector instance %q uses unknown provider %q (available: %s)", instanceID, kind, strings.Join(providers, ", "))
		}
		stored, err := engine.Instances.Load(ctx, instanceID)
		if err != nil {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("launch: load connector instance %q: %w", instanceID, err)
		}
		if stored.Connector != kind {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("launch: connector instance %q has kind %q, want %q", instanceID, stored.Connector, kind)
		}
		if err := engine.ConnectInstance(ctx, instanceID); err != nil {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("launch: connect %s connector instance %q: %w", kind, instanceID, err)
		}
		instances = append(instances, connectorplugin.Instance{ID: instanceID, Kind: kind})
	}
	return engine, instances, nil
}

func newConnectEngine(ctx context.Context, basePath string) (*connectorsruntime.Engine, []string, error) {
	providers, err := connectorProviderNames(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(providers) == 0 {
		return nil, nil, fmt.Errorf("connect: no connector providers registered")
	}
	resolvedPath, err := resolveConnectorsPath(basePath)
	if err != nil {
		return nil, nil, err
	}
	engine, err := integrate.Engine(
		integrate.WithBasePath(resolvedPath),
		integrate.WithAllowList(providers...),
	)
	if err != nil {
		return nil, nil, err
	}
	return engine, providers, nil
}

func connectorProviderNames(ctx context.Context) ([]string, error) {
	plugins := []pluginhost.Plugin{
		openaiplugin.New(),
		slackplugin.New(nil),
		gitlabplugin.New(nil, nil),
		jiraplugin.New(nil, nil),
	}
	seen := map[string]bool{}
	var names []string
	for _, plugin := range plugins {
		contributor, ok := plugin.(pluginhost.ConnectorProviderContributor)
		if !ok {
			continue
		}
		manifest := plugin.Manifest()
		providers, err := contributor.ConnectorProviders(ctx, pluginhost.Context{Ref: resource.PluginRef{Name: manifest.Name}})
		if err != nil {
			return nil, err
		}
		for _, provider := range providers {
			name := strings.TrimSpace(provider.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func resolveConnectorsPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "~/.connectors"
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path, nil
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
	specs := datasourceSpecs(bundles)
	specs = appendDatasourceSpecs(specs, datasourceSpecs(resolved.Bundles)...)
	return datasourceplugin.BuildRegistry(ctx, specs, providers)
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

func appendDatasourceSpecs(specs []coredatasource.Spec, candidates ...coredatasource.Spec) []coredatasource.Spec {
	seen := map[coredatasource.Name]bool{}
	for _, spec := range specs {
		seen[spec.Name] = true
	}
	for _, spec := range candidates {
		if seen[spec.Name] {
			continue
		}
		specs = append(specs, spec)
		seen[spec.Name] = true
	}
	return specs
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
	for i, bundle := range bundles {
		out[i] = bundle
		out[i].Apps = append(out[i].Apps[:0:0], bundle.Apps...)
		out[i].Agents = append(out[i].Agents[:0:0], bundle.Agents...)
		out[i].OperationSets = append(out[i].OperationSets[:0:0], bundle.OperationSets...)
		out[i].ToolSets = append(out[i].ToolSets[:0:0], bundle.ToolSets...)
		out[i].Operations = append(out[i].Operations[:0:0], bundle.Operations...)
		out[i].Commands = append(out[i].Commands[:0:0], bundle.Commands...)
		out[i].Datasources = append(out[i].Datasources[:0:0], bundle.Datasources...)
		out[i].Sessions = append(out[i].Sessions[:0:0], bundle.Sessions...)
		out[i].Skills = append(out[i].Skills[:0:0], bundle.Skills...)
		out[i].ContextProviders = append(out[i].ContextProviders[:0:0], bundle.ContextProviders...)
		out[i].Workflows = append(out[i].Workflows[:0:0], bundle.Workflows...)
		out[i].EventTypes = append(out[i].EventTypes[:0:0], bundle.EventTypes...)
		out[i].Plugins = append(out[i].Plugins[:0:0], bundle.Plugins...)
		out[i].Diagnostics = append(out[i].Diagnostics[:0:0], bundle.Diagnostics...)
	}
	return out
}

func appendBundleEventTypes(base []event.Event, bundles []resource.ContributionBundle) []event.Event {
	out := append([]event.Event(nil), base...)
	for _, bundle := range bundles {
		out = append(out, bundle.EventTypes...)
	}
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

func (localACL) Authorize(ctx operation.Context, op operation.Operation, input operation.Value) error {
	return (operationruntime.AuthorizationGate{}).Authorize(ctx, op, input)
}

func localUserCaller() policy.Caller {
	raw := ""
	name := ""
	if current, err := osuser.Current(); err == nil && current != nil {
		raw = strings.TrimSpace(current.Username)
		name = localUsername(raw)
	}
	if name == "" {
		name = localUsername(os.Getenv("USER"))
	}
	if name == "" {
		name = localUsername(os.Getenv("USERNAME"))
	}
	if name == "" {
		name = "local"
	}
	canonical := name
	if !strings.Contains(canonical, "@") {
		canonical += "@localhost"
	}
	return policy.Caller{
		Kind: policy.CallerUser,
		Principal: policy.Principal{
			Kind: "user",
			ID:   canonical,
			Name: name,
		},
		Source: "local",
	}
}

func localUsername(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	parts := strings.Split(raw, "/")
	raw = parts[len(parts)-1]
	if i := strings.LastIndex(raw, "@"); i > 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}
