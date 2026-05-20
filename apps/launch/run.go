package launch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	osuser "os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/codewandler/connectors/integrate"
	connectorsruntime "github.com/codewandler/connectors/runtime"
	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/distribution/localruntime"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/fluxplane/agentruntime/adapters/system/browsercdp"
	"github.com/fluxplane/agentruntime/adapters/ui/terminal"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/channel"
	coredata "github.com/fluxplane/agentruntime/core/data"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/agentfactory"
	"github.com/fluxplane/agentruntime/orchestration/app"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/datasourceindex"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	orchestrationidentity "github.com/fluxplane/agentruntime/orchestration/identity"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/taskexecutor"
	"github.com/fluxplane/agentruntime/plugins/bundles/coding"
	"github.com/fluxplane/agentruntime/plugins/integrations/confluence"
	"github.com/fluxplane/agentruntime/plugins/integrations/gitlab"
	"github.com/fluxplane/agentruntime/plugins/integrations/jira"
	"github.com/fluxplane/agentruntime/plugins/integrations/kubernetes"
	"github.com/fluxplane/agentruntime/plugins/integrations/loki"
	"github.com/fluxplane/agentruntime/plugins/integrations/mysql"
	"github.com/fluxplane/agentruntime/plugins/integrations/openai"
	"github.com/fluxplane/agentruntime/plugins/integrations/openapi"
	"github.com/fluxplane/agentruntime/plugins/integrations/slack"
	"github.com/fluxplane/agentruntime/plugins/integrations/web"
	"github.com/fluxplane/agentruntime/plugins/native/datasource"
	"github.com/fluxplane/agentruntime/plugins/native/discovery"
	"github.com/fluxplane/agentruntime/plugins/native/identity"
	"github.com/fluxplane/agentruntime/plugins/native/image"
	"github.com/fluxplane/agentruntime/plugins/native/memory"
	"github.com/fluxplane/agentruntime/plugins/native/sessionhistory"
	"github.com/fluxplane/agentruntime/plugins/native/skills"
	"github.com/fluxplane/agentruntime/plugins/native/task"
	"github.com/fluxplane/agentruntime/plugins/native/text"
	"github.com/fluxplane/agentruntime/plugins/native/workspace"
	"github.com/fluxplane/agentruntime/plugins/support/connector"
	"github.com/fluxplane/agentruntime/plugins/support/eventcatalog"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
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
	Dispatcher  *slack.Dispatcher
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
	if len(override.Workspace.EnvFiles) > 0 {
		base.Workspace.EnvFiles = append(base.Workspace.EnvFiles, override.Workspace.EnvFiles...)
	}
	if strings.TrimSpace(override.Workspace.ScratchRoot) != "" {
		base.Workspace.ScratchRoot = override.Workspace.ScratchRoot
	}
	if strings.TrimSpace(override.Data.Store.Kind) != "" {
		base.Data.Store.Kind = override.Data.Store.Kind
	}
	if strings.TrimSpace(override.Data.Store.DSN) != "" {
		base.Data.Store.DSN = override.Data.Store.DSN
	}
	if strings.TrimSpace(override.Data.Store.DSNEnv) != "" {
		base.Data.Store.DSNEnv = override.Data.Store.DSNEnv
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
	runtimeSystem := system.WithAuthorization(hostSystem, system.AuthorizationConfig{TraceAllows: opts.Debug})
	hostSystem.SetClarifier(terminal.Prompter{In: os.Stdin, Out: os.Stderr})
	browser, err := browsercdp.New(browsercdp.Config{Workspace: runtimeSystem.Workspace(), Headless: browserHeadless()})
	if err == nil {
		hostSystem.SetBrowser(browser)
	} else if opts.Debug {
		_, _ = fmt.Fprintf(os.Stderr, "browser disabled: %v\n", err)
	}

	connectorEngine, _, err := launchConnectorEngine(ctx, opts.AuthPath, opts.Launch.Connectors)
	if err != nil {
		return Runtime{}, err
	}
	var semanticIndex interface{ Close() error }
	var dataStore coredata.Store
	var closeDataStore func() error
	var closeThreadStore func()
	var stopTaskScheduler context.CancelFunc
	closeRuntime := func() {
		if stopTaskScheduler != nil {
			stopTaskScheduler()
		}
		if semanticIndex != nil {
			_ = semanticIndex.Close()
		}
		if closeDataStore != nil {
			_ = closeDataStore()
		}
		if closeThreadStore != nil {
			closeThreadStore()
		}
		if connectorEngine != nil {
			_ = connectorEngine.Close()
		}
	}

	dispatcher := slack.NewDispatcher()
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
	threadStore, eventStore, closeStore, err := openLocalThreadStore(eventRegistry, opts.Launch.Events)
	if err != nil {
		closeRuntime()
		return Runtime{}, err
	}
	closeThreadStore = closeStore
	var taskScheduler *taskexecutor.Scheduler
	var taskWorker *taskexecutor.DeferredWorker
	if bundleHasPlugin(bundles, task.Name) {
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
	available := availablePlugins(runtimeSystem, dispatcher, taskScheduler, opts.AuthPath)
	if opts.Plugins != nil {
		available = opts.Plugins(runtimeSystem)
	}
	if taskScheduler != nil {
		available = replacePlugin(available, task.NewWithRunnerAndSystem(taskScheduler, runtimeSystem))
	}
	if opts.Dev {
		available = appendPluginIfMissing(available, sessionhistory.New(threadStore))
	}
	plugins, err := selectDeclaredPlugins(bundles, available)
	if err != nil {
		closeRuntime()
		return Runtime{}, err
	}
	needsDataStore := opts.Dev || hasAnyDatasource(bundles) || bundleHasPlugin(bundles, memory.Name)
	needsDatasourceRuntime := opts.Dev || hasAnyDatasource(bundles) || bundleHasPlugin(bundles, memory.Name)
	if needsDataStore {
		var closeData func() error
		dataStore, closeData, err = openDataStore(ctx, opts.Launch.Data)
		if err != nil {
			closeRuntime()
			return Runtime{}, err
		}
		closeDataStore = closeData
	}
	if needsDatasourceRuntime {
		index, err := newSemanticIndex(root, bundles, "", "", "")
		if err != nil {
			closeRuntime()
			return Runtime{}, err
		}
		semanticIndex = index
		dataSources := datasourceDataSources(bundles)
		pluginDataSources, err := resolvedPluginDataSources(ctx, bundles, plugins, eventStore, dataStore)
		if err != nil {
			closeRuntime()
			return Runtime{}, err
		}
		dataSources = append(dataSources, pluginDataSources...)
		registry, err := datasourceRegistryWithOptions(ctx, bundles, plugins, root, eventStore, dataStore, datasource.RegistryOptions{SemanticIndex: index, DataSources: dataSources})
		if err != nil {
			closeRuntime()
			return Runtime{}, err
		}
		plugins = append(plugins, datasource.NewWithSemanticAndDataStore(registry, index, dataStore, dataSources...))
		ensurePluginRef(bundles, datasource.Name)
		ensureDatasourceCatalogAccess(bundles)
		warmupDone := startDatasourceIndexWarmup(ctx, registry, index, dataStore, dataSources, datasourceIndexFromBundles(bundles), opts.Debug)
		startDatasourceIndexEmbedWorker(ctx, warmupDone, index, opts.Debug)
	}
	approval := operationruntime.ApprovalGate(terminal.Approver{In: os.Stdin, Out: os.Stderr})
	if opts.Yolo {
		approval = operationruntime.AutoApprover{}
	}
	var bundleTransforms []app.BundleTransform
	if opts.Dev {
		bundleTransforms = append(bundleTransforms, enableDevSessionHistory)
	}
	identityResolver := launchIdentityResolver(ctx, runtimeSystem, opts.AuthPath, opts.Launch.Channels, bundles)
	composition, err := app.Compose(app.Config{
		Context:          ctx,
		Bundles:          bundles,
		Plugins:          plugins,
		BundleTransforms: bundleTransforms,
		EventStore:       eventStore,
		DataStore:        dataStore,
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:                   localSandbox{Root: root},
			ACL:                       localACL{},
			CommandRisk:               distrun.CommandRisk(root),
			Approval:                  approval,
			MaxCommandRisk:            operation.RiskMedium,
			ApproveOverMaxCommandRisk: opts.Yolo,
			AllowPure:                 true,
		})),
		IdentityResolver: identityResolver,
	})
	if err != nil {
		closeRuntime()
		return Runtime{}, err
	}
	if composition.Discoverer != nil {
		composition.Discoverer.Start(ctx)
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
		Channel:       channel.Ref{Name: "local"},
		Caller:        localCaller,
		Trust:         localTrust,
		Security:      composition.Security,
		SecurityTrace: opts.Debug,
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
		System:      runtimeSystem,
		Dispatcher:  dispatcher,
		Caller:      localCaller,
		Trust:       localTrust,
		Close:       closeRuntime,
	}, nil
}

func systemWorkspaceConfig(cfg distribution.WorkspaceConfig) system.WorkspaceConfig {
	out := system.WorkspaceConfig{
		ScratchRoot: strings.TrimSpace(cfg.ScratchRoot),
		EnvFiles:    trimLaunchStrings(cfg.EnvFiles),
	}
	for _, root := range cfg.Roots {
		out.Roots = append(out.Roots, system.WorkspaceRootConfig{
			Name:     strings.TrimSpace(root.Name),
			Path:     strings.TrimSpace(root.Path),
			Access:   system.WorkspaceAccess(strings.TrimSpace(root.Access)),
			Create:   root.Create,
			EnvFiles: trimLaunchStrings(root.EnvFiles),
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

func nativeAuthPath(path string) string {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(path) == "~/.connectors" {
		return runtimesecret.DefaultFileStorePath
	}
	return path
}

func slackConfigForInstance(bundles []resource.ContributionBundle, instance string) slack.Config {
	instance = strings.TrimSpace(instance)
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			if strings.TrimSpace(ref.Name) != slack.Name || ref.InstanceName() != instance {
				continue
			}
			cfg, err := pluginhost.DecodeConfig[slack.Config](ref.Config)
			if err != nil {
				return slack.Config{Auth: slack.AuthConfig{Method: slack.BotTokenMethod}}
			}
			return slack.NormalizeConfig(cfg)
		}
	}
	return slack.Config{Auth: slack.AuthConfig{Method: slack.BotTokenMethod}}
}

func availablePlugins(hostSystem system.System, dispatcher *slack.Dispatcher, taskRunner task.TaskRunner, authPath string) []pluginhost.Plugin {
	slackStore := runtimesecret.NewFileStore(nativeAuthPath(authPath))
	return []pluginhost.Plugin{
		workspace.New(hostSystem),
		discovery.New(),
		identity.New(),
		coding.New(hostSystem),
		openai.New(),
		slack.NewWithDispatcher(hostSystem, dispatcher, slackStore),
		gitlab.New(hostSystem),
		image.New(hostSystem),
		jira.New(hostSystem),
		confluence.New(hostSystem),
		kubernetes.New(hostSystem),
		loki.New(hostSystem),
		mysql.New(),
		openapi.New(hostSystem),
		memory.New(),
		task.NewWithRunnerAndSystem(taskRunner, hostSystem),
		skills.New(),
		text.New(),
		web.New(hostSystem),
	}
}

// AuthPluginRegistry returns first-party plugins that expose auth declarations
// for distribution-level connect commands.
func AuthPluginRegistry(context.Context) ([]pluginhost.Plugin, error) {
	return []pluginhost.Plugin{
		openai.New(),
		slack.New(nil),
		gitlab.New(nil),
		jira.New(nil),
		confluence.New(nil),
	}, nil
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

func launchIdentityResolver(ctx context.Context, sys system.System, authPath string, channels []distribution.Channel, bundles []resource.ContributionBundle) orchestrationidentity.Resolver {
	store := runtimesecret.NewFileStore(nativeAuthPath(authPath))
	var resolvers []orchestrationidentity.Resolver
	for _, doc := range channels {
		if doc.Type != "slack" {
			continue
		}
		ref := resource.PluginRef{Name: slack.Name, Instance: firstNonEmptyString(doc.Instance, doc.Connector, slack.Name)}
		session, err := slack.Resolve(ctx, sys, store, ref, slackConfigForInstance(bundles, ref.InstanceName()))
		if err != nil {
			continue
		}
		resolver := slack.NewIdentityResolver(slack.IdentityResolverConfig{
			ChannelName: doc.Name,
			BotToken:    session.BotToken,
			UserToken:   session.UserToken,
			AppToken:    session.AppToken,
		})
		if resolver != nil {
			resolvers = append(resolvers, resolver)
		}
	}
	switch len(resolvers) {
	case 0:
		return nil
	case 1:
		return resolvers[0]
	default:
		return orchestrationidentity.ChainResolver{Resolvers: resolvers}
	}
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
		if ref.Name != workspace.Name {
			refs = append(refs, ref)
		}
	}
	refs = append([]resource.PluginRef{{Name: workspace.Name}}, refs...)
	plugins := make([]pluginhost.Plugin, 0, len(refs))
	selected := map[string]bool{}
	for _, ref := range refs {
		plugin, ok := byName[ref.Name]
		if !ok {
			return nil, fmt.Errorf("launch: plugin %q is not available", ref.Key())
		}
		if selected[ref.Name] {
			continue
		}
		selected[ref.Name] = true
		plugins = append(plugins, plugin)
	}
	return plugins, nil
}

func launchConnectorEngine(ctx context.Context, authPath string, connectors map[string]distribution.Connector) (*connectorsruntime.Engine, []connector.Instance, error) {
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
	instances := make([]connector.Instance, 0, len(names))
	for _, instanceID := range names {
		connectorConfig := connectors[instanceID]
		kind := strings.TrimSpace(connectorConfig.Kind)
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
		instances = append(instances, connector.Instance{ID: instanceID, Kind: kind})
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
		openai.New(),
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
	return datasourceRegistryWithOptions(ctx, bundles, plugins, root, nil, nil, datasource.RegistryOptions{})
}

func datasourceRegistryWithOptions(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin, root string, eventStore event.Store, dataStore coredata.Store, opts datasource.RegistryOptions) (*coredatasource.Registry, error) {
	host, err := pluginhost.New(plugins...)
	if err != nil {
		return nil, err
	}
	host.SetEventStore(eventStore)
	host.SetDataStore(dataStore)
	resolved, err := host.Resolve(ctx, pluginRefs(bundles)...)
	if err != nil {
		return nil, err
	}
	var providers []coredatasource.Provider
	for _, contribution := range resolved.DatasourceProviders {
		providers = append(providers, contribution.Provider)
	}
	providers = append(providers, datasource.NewFilesystemProvider(os.DirFS(root)))
	specs := datasourceSpecs(bundles)
	specs = appendDatasourceSpecs(specs, datasourceSpecs(resolved.Bundles)...)
	return datasource.BuildRegistryWithOptions(ctx, specs, providers, opts)
}

func resolvedPluginDataSources(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin, eventStore event.Store, dataStore coredata.Store) ([]coredata.SourceSpec, error) {
	host, err := pluginhost.New(plugins...)
	if err != nil {
		return nil, err
	}
	host.SetEventStore(eventStore)
	host.SetDataStore(dataStore)
	resolved, err := host.Resolve(ctx, pluginRefs(bundles)...)
	if err != nil {
		return nil, err
	}
	return datasourceDataSources(resolved.Bundles), nil
}

func ensureSkillDatasource(bundles []resource.ContributionBundle) {
	if !bundleHasPlugin(bundles, skills.Name) || hasDatasource(bundles, skills.DatasourceName) || len(bundles) == 0 {
		return
	}
	bundles[0].Datasources = append(bundles[0].Datasources, skills.DatasourceSpec())
}

func ensurePluginRef(bundles []resource.ContributionBundle, name string) {
	if len(bundles) == 0 || bundleHasPlugin(bundles, name) {
		return
	}
	bundles[0].Plugins = append(bundles[0].Plugins, resource.PluginRef{Name: name})
}

func ensureDatasourceCatalogAccess(bundles []resource.ContributionBundle) {
	for bundleIndex := range bundles {
		for agentIndex := range bundles[bundleIndex].Agents {
			appendDatasourceRef(&bundles[bundleIndex].Agents[agentIndex].Datasources, coredatasource.Name(datasource.Name))
		}
	}
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

func datasourceDataSources(bundles []resource.ContributionBundle) []coredata.SourceSpec {
	var out []coredata.SourceSpec
	for _, bundle := range bundles {
		out = append(out, bundle.DataSources...)
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

type datasourceIndexWarmupResult struct {
	Result datasourceindex.Result
	Err    error
}

func startDatasourceIndexWarmup(ctx context.Context, registry *coredatasource.Registry, index *semantic.Index, dataStore coredata.Store, dataSources []coredata.SourceSpec, cfg coreapp.DatasourceIndexSpec, verbose bool) <-chan datasourceIndexWarmupResult {
	done := make(chan datasourceIndexWarmupResult, 1)
	if registry == nil {
		if verbose {
			slog.Info("datasource index warmup skipped", "reason", "registry_missing")
		}
		close(done)
		return done
	}
	if index == nil && dataStore == nil {
		if verbose {
			slog.Info("datasource index warmup skipped", "reason", "store_missing")
		}
		close(done)
		return done
	}
	if !registryHasIndexedDatasource(registry) {
		if verbose {
			slog.Info("datasource index warmup skipped", "reason", "no_indexed_datasources")
		}
		close(done)
		return done
	}
	go func() {
		defer close(done)
		concurrency, freshness, err := DatasourceIndexBuildConfig(cfg, 0, "")
		if err != nil {
			if verbose {
				slog.Warn("datasource index warmup config failed", "error", err)
			}
			done <- datasourceIndexWarmupResult{Err: err}
			return
		}
		jobs := indexedDatasourceJobLabels(registry)
		if verbose {
			slog.Info("datasource index warmup scheduled", "concurrency", concurrency, "freshness", freshness, "jobs", jobs, "job_count", len(jobs))
		}
		result, err := datasourceindex.Build(ctx, datasourceindex.Request{
			Registry:    registry,
			Index:       index,
			DataStore:   dataStore,
			DataSources: dataSources,
			IndexedOnly: true,
			Concurrency: concurrency,
			Freshness:   freshness,
			Progress:    datasourceIndexProgressLogger(verbose),
		})
		if err != nil && verbose {
			slog.Warn("datasource index warmup failed", "error", err)
		}
		done <- datasourceIndexWarmupResult{Result: result, Err: err}
	}()
	return done
}

func startDatasourceIndexEmbedWorker(ctx context.Context, warmupDone <-chan datasourceIndexWarmupResult, index *semantic.Index, verbose bool) {
	if index == nil {
		return
	}
	go func() {
		if warmupDone != nil {
			warmup, ok := <-warmupDone
			if !ok {
				return
			}
			if warmup.Err != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		embedResult, err := index.ProcessQueue(ctx, semantic.ProcessQueueRequest{Progress: datasourceIndexEmbedProgressLogger(verbose)})
		if err != nil {
			if verbose {
				slog.Warn("datasource semantic embed warmup failed", "error", err)
			}
			return
		}
		if verbose && (embedResult.Queued > 0 || embedResult.Failed > 0) {
			slog.Info("datasource semantic embed warmup summary", "queued", embedResult.Queued, "embedded", embedResult.Embedded, "skipped", embedResult.Skipped, "failed", embedResult.Failed)
		}
	}()
}

func datasourceIndexProgressLogger(verbose bool) datasourceindex.ProgressReporter {
	if !verbose {
		return nil
	}
	return datasourceIndexSlogProgress
}

func datasourceIndexEmbedProgressLogger(verbose bool) semantic.QueueProgressReporter {
	if !verbose {
		return nil
	}
	return datasourceIndexEmbedSlogProgress
}

func datasourceIndexSlogProgress(event datasourceindex.ProgressEvent) {
	switch event.Kind {
	case datasourceindex.ProgressEntityStart:
		slog.Info("datasource index warmup start", "datasource", event.Datasource, "entity", event.Entity, "phase", event.Phase)
	case datasourceindex.ProgressEntityFresh:
		slog.Info("datasource index warmup fresh", "datasource", event.Datasource, "entity", event.Entity, "phase", event.Phase, "fresh_until", event.FreshUntil)
	case datasourceindex.ProgressEntityRunningStale:
		slog.Warn("datasource index warmup previous run still marked running; rescanning", "datasource", event.Datasource, "entity", event.Entity, "phase", event.Phase, "reason", event.Message)
	case datasourceindex.ProgressPageFetched:
		slog.Info("datasource index warmup page", datasourceIndexPageLogArgs(event)...)
	case datasourceindex.ProgressDocumentFailed, datasourceindex.ProgressTombstoneFailed:
		slog.Warn("datasource index warmup item failed", "datasource", event.Datasource, "entity", event.Entity, "phase", event.Phase, "id", event.RecordID, "error", event.Message)
	case datasourceindex.ProgressDocumentQueued:
		slog.Info("datasource index warmup queued", "datasource", event.Datasource, "entity", event.Entity, "phase", event.Phase, "id", event.RecordID)
	case datasourceindex.ProgressEntityComplete:
		slog.Info("datasource index warmup complete", "datasource", event.Datasource, "entity", event.Entity, "phase", event.Phase, "documents", event.Documents, "indexed", event.Indexed, "queued", event.Queued, "skipped", event.Skipped, "deleted", event.Deleted, "failed", event.Failed)
	case datasourceindex.ProgressComplete:
		slog.Info("datasource index warmup summary", "phase", event.Phase, "documents", event.Documents, "indexed", event.Indexed, "queued", event.Queued, "skipped", event.Skipped, "deleted", event.Deleted, "failed", event.Failed)
	}
}

func datasourceIndexEmbedSlogProgress(event semantic.QueueProgressEvent) {
	switch event.Kind {
	case semantic.QueueProgressStart:
		slog.Info("datasource semantic embed warmup start", "phase", datasourceindex.PhaseEmbed, "queued", event.Queued)
	case semantic.QueueProgressEmbedded:
		slog.Info("datasource semantic embed warmup embedded", "datasource", event.Datasource, "entity", event.Entity, "phase", datasourceindex.PhaseEmbed, "id", event.RecordID)
	case semantic.QueueProgressSkipped:
		slog.Info("datasource semantic embed warmup skipped", "datasource", event.Datasource, "entity", event.Entity, "phase", datasourceindex.PhaseEmbed, "id", event.RecordID, "status", event.Status)
	case semantic.QueueProgressFailed:
		slog.Warn("datasource semantic embed warmup failed", "datasource", event.Datasource, "entity", event.Entity, "phase", datasourceindex.PhaseEmbed, "id", event.RecordID, "error", event.Message)
	case semantic.QueueProgressComplete:
		slog.Info("datasource semantic embed warmup complete", "phase", datasourceindex.PhaseEmbed, "queued", event.Queued, "embedded", event.Embedded, "skipped", event.Skipped, "failed", event.Failed)
	}
}

func registryHasIndexedDatasource(registry *coredatasource.Registry) bool {
	if registry == nil {
		return false
	}
	for _, accessor := range registry.All() {
		if accessor.Spec().Index.Enabled {
			return true
		}
	}
	return false
}

func pluginRefs(bundles []resource.ContributionBundle) []resource.PluginRef {
	seen := map[string]bool{}
	var out []resource.PluginRef
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			key := ref.Key()
			if ref.Name == datasource.Name || seen[key] {
				continue
			}
			seen[key] = true
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
		out[i].DataSources = append(out[i].DataSources[:0:0], bundle.DataSources...)
		out[i].Sessions = append(out[i].Sessions[:0:0], bundle.Sessions...)
		out[i].PostEditChecks = append(out[i].PostEditChecks[:0:0], bundle.PostEditChecks...)
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
