package launch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	osuser "os/user"
	"path/filepath"
	"strings"
	"sync"

	fluxplane "github.com/fluxplane/engine"
	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
	"github.com/fluxplane/engine/adapters/distribution/localruntime"
	distrun "github.com/fluxplane/engine/adapters/distribution/run"
	"github.com/fluxplane/engine/adapters/resources/appconfig"
	"github.com/fluxplane/engine/adapters/system/browsercdp"
	"github.com/fluxplane/engine/adapters/ui/terminal"
	coreapp "github.com/fluxplane/engine/core/app"
	"github.com/fluxplane/engine/core/channel"
	coredata "github.com/fluxplane/engine/core/data"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	coredistribution "github.com/fluxplane/engine/core/distribution"
	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/orchestration/agentfactory"
	"github.com/fluxplane/engine/orchestration/app"
	clientapi "github.com/fluxplane/engine/orchestration/client"
	"github.com/fluxplane/engine/orchestration/datasourceindex"
	"github.com/fluxplane/engine/orchestration/distribution"
	"github.com/fluxplane/engine/orchestration/eventregistry"
	orchestrationidentity "github.com/fluxplane/engine/orchestration/identity"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/orchestration/taskexecutor"
	"github.com/fluxplane/engine/plugins/bundles/coding"
	"github.com/fluxplane/engine/plugins/integrations/confluence"
	"github.com/fluxplane/engine/plugins/integrations/gitlab"
	"github.com/fluxplane/engine/plugins/integrations/jira"
	"github.com/fluxplane/engine/plugins/integrations/kubernetes"
	"github.com/fluxplane/engine/plugins/integrations/loki"
	"github.com/fluxplane/engine/plugins/integrations/mysql"
	"github.com/fluxplane/engine/plugins/integrations/openai"
	"github.com/fluxplane/engine/plugins/integrations/openapi"
	"github.com/fluxplane/engine/plugins/integrations/slack"
	"github.com/fluxplane/engine/plugins/integrations/web"
	"github.com/fluxplane/engine/plugins/native/datasource"
	"github.com/fluxplane/engine/plugins/native/discovery"
	goalplugin "github.com/fluxplane/engine/plugins/native/goal"
	"github.com/fluxplane/engine/plugins/native/identity"
	"github.com/fluxplane/engine/plugins/native/image"
	"github.com/fluxplane/engine/plugins/native/memory"
	"github.com/fluxplane/engine/plugins/native/sessionhistory"
	"github.com/fluxplane/engine/plugins/native/skills"
	"github.com/fluxplane/engine/plugins/native/task"
	"github.com/fluxplane/engine/plugins/native/text"
	usageplugin "github.com/fluxplane/engine/plugins/native/usage"
	"github.com/fluxplane/engine/plugins/native/workspace"
	"github.com/fluxplane/engine/plugins/support/eventcatalog"
	"github.com/fluxplane/engine/runtime/authstatus"
	"github.com/fluxplane/engine/runtime/datasource/semantic"
	runtimeevidence "github.com/fluxplane/engine/runtime/evidence"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
	runtimesecret "github.com/fluxplane/engine/runtime/secret"
	"github.com/fluxplane/engine/runtime/system"
	runtimetask "github.com/fluxplane/engine/runtime/task"
)

type LocalRuntimeConfig struct {
	Root                string
	Spec                coredistribution.Spec
	Bundles             []resource.ContributionBundle
	Plugins             func(system.System) []pluginhost.Plugin
	PluginFactory       func(PluginFactoryContext) []pluginhost.Plugin
	ToolProjection      fluxplane.ToolProjectionConfig
	ModelResolver       agentfactory.ModelResolver
	AllowPrivateNetwork bool
	Launch              distribution.LaunchConfig
	AuthPath            string
	AllowPluginAuthEnv  bool
	Dev                 bool
}

type AttachOptions struct {
	AuthPath           string
	AllowPluginAuthEnv bool
	Dev                bool
}

// RuntimeOptions describes the surface-neutral local launch inputs shared by
// run, serve, and first-party distribution executables.
type RuntimeOptions struct {
	Root                string
	Spec                coredistribution.Spec
	Bundles             []resource.ContributionBundle
	Launch              distribution.LaunchConfig
	AuthPath            string
	AllowPluginAuthEnv  bool
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
	PluginFactory       func(PluginFactoryContext) []pluginhost.Plugin
	ToolProjection      fluxplane.ToolProjectionConfig
	ModelResolver       agentfactory.ModelResolver
	AllowPrivateNetwork bool
}

type PluginFactoryContext struct {
	System             system.System
	Dispatcher         *slack.Dispatcher
	TaskRunner         task.TaskRunner
	NativeAuthStore    runtimesecret.FileStore
	NativeAuthResolver runtimesecret.Resolver
}

// Runtime is the composed local runtime plus the resources needed by hosting
// surfaces such as serve.
type Runtime struct {
	Service     fluxplane.ChannelClient
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
		AllowPluginAuthEnv:  opts.AllowPluginAuthEnv,
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
		AllowPluginAuthEnv:  cfg.AllowPluginAuthEnv || req.AllowPluginAuthEnv,
		Provider:            req.Provider,
		Model:               req.Model,
		Thinking:            req.Thinking,
		ThinkingSet:         req.ThinkingSet,
		Effort:              req.Effort,
		EffortSet:           req.EffortSet,
		ToolProjection:      mergeToolProjectionMaxRisk(cfg.ToolProjection, req.MaxToolRisk),
		Debug:               req.Debug,
		Yolo:                req.Yolo,
		Dev:                 cfg.Dev || req.Dev,
		Plugins:             cfg.Plugins,
		PluginFactory:       cfg.PluginFactory,
		ModelResolver:       cfg.ModelResolver,
		AllowPrivateNetwork: cfg.AllowPrivateNetwork || req.AllowPrivateNetwork,
	})
	if err != nil {
		return nil, err
	}
	session, err := runtime.Service.Open(ctx, fluxplane.OpenRequest{
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
	if len(override.Listeners) > 0 {
		base.Listeners = override.Listeners
	}
	if len(override.Channels) > 0 {
		base.Channels = override.Channels
	}
	if len(override.Triggers) > 0 {
		base.Triggers = override.Triggers
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
	auth := NewPluginAuthContext(PluginAuthOptions{
		System:             runtimeSystem,
		AuthPath:           opts.AuthPath,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
	})
	available := availablePluginsWithAuth(runtimeSystem, dispatcher, taskScheduler, auth.Store, auth.Resolver)
	if opts.PluginFactory != nil {
		available = opts.PluginFactory(PluginFactoryContext{
			System:             runtimeSystem,
			Dispatcher:         dispatcher,
			TaskRunner:         taskScheduler,
			NativeAuthStore:    auth.Store,
			NativeAuthResolver: auth.Resolver,
		})
	} else if opts.Plugins != nil {
		available = opts.Plugins(runtimeSystem)
	}
	if taskScheduler != nil {
		available = replacePlugin(available, task.NewWithRunnerAndSystem(taskScheduler, runtimeSystem))
	}
	if opts.Dev {
		available = appendPluginIfMissing(available, sessionhistory.New(threadStore))
	}
	available = appendPluginIfMissing(available, goalplugin.New())
	ensurePluginRef(bundles, goalplugin.Name)
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
	bundleTransforms := []app.BundleTransform{appconfig.NormalizeBundles}
	if opts.Dev {
		bundleTransforms = append(bundleTransforms, enableDevSessionHistory)
	}
	identityResolver := launchIdentityResolver(ctx, runtimeSystem, auth, opts.Launch.Channels, bundles)
	authObservers, authDerivers, err := authEnvironmentContributions(ctx, bundles, plugins, auth)
	if err != nil {
		closeRuntime()
		return Runtime{}, err
	}
	composition, err := app.Compose(app.Config{
		Context:              ctx,
		Bundles:              bundles,
		Plugins:              plugins,
		BundleTransforms:     bundleTransforms,
		EnvironmentObservers: authObservers,
		AssertionDerivers:    authDerivers,
		EventStore:           eventStore,
		DataStore:            dataStore,
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
	toolProjection := firstToolProjection(opts.ToolProjection, defaultToolProjection())
	toolProjection.NamedPluginInstances = mergeNamedPluginInstances(toolProjection.NamedPluginInstances, gitlabNamedPluginInstances(ctx, bundles, runtimeSystem, auth))
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
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
		ToolProjection:  toolProjection,
		Channel:         channel.Ref{Name: "local"},
		Caller:          localCaller,
		Trust:           localTrust,
		Security:        composition.Security,
		SecurityTrace:   opts.Debug,
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

func defaultToolProjection() fluxplane.ToolProjectionConfig {
	return fluxplane.ToolProjectionConfig{
		AllowSideEffects:        true,
		AllowApprovalRequired:   true,
		IncludeBareOperations:   true,
		PreferCommandProjection: true,
	}
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

func firstToolProjection(value, fallback fluxplane.ToolProjectionConfig) fluxplane.ToolProjectionConfig {
	if value.AllowSideEffects ||
		value.AllowApprovalRequired ||
		value.IncludeBareOperations ||
		value.PreferCommandProjection ||
		len(value.NamedPluginInstances) > 0 ||
		len(value.Commands) > 0 ||
		len(value.Operations) > 0 {
		return value
	}
	if value.MaxRisk != "" {
		fallback.MaxRisk = value.MaxRisk
		return fallback
	}
	return fallback
}

func mergeNamedPluginInstances(base, extra map[string]map[string]bool) map[string]map[string]bool {
	if len(extra) == 0 {
		return base
	}
	out := map[string]map[string]bool{}
	for kind, instances := range base {
		out[kind] = map[string]bool{}
		for instance, enabled := range instances {
			out[kind][instance] = enabled
		}
	}
	for kind, instances := range extra {
		if out[kind] == nil {
			out[kind] = map[string]bool{}
			for instance, enabled := range instances {
				out[kind][instance] = enabled
			}
			continue
		}
		for instance, enabled := range out[kind] {
			out[kind][instance] = enabled && instances[instance]
		}
	}
	return out
}

func mergeToolProjectionMaxRisk(cfg fluxplane.ToolProjectionConfig, risk operation.RiskLevel) fluxplane.ToolProjectionConfig {
	if risk != "" {
		cfg = firstToolProjection(cfg, defaultToolProjection())
		cfg.MaxRisk = risk
	}
	return cfg
}

func gitlabNamedPluginInstances(ctx context.Context, bundles []resource.ContributionBundle, sys system.System, auth PluginAuthContext) map[string]map[string]bool {
	refs := gitLabPluginRefs(bundles)
	if len(refs) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, ref := range refs {
		cfg, err := gitlabConfigForRef(ref)
		if err != nil {
			continue
		}
		scopes, ok, err := gitlab.TokenScopes(ctx, sys, auth.Resolver, ref, cfg)
		if err != nil || !ok || !stringIn(scopes, "api") {
			allowed[ref.InstanceName()] = false
			continue
		}
		allowed[ref.InstanceName()] = true
	}
	return map[string]map[string]bool{gitlab.Name: allowed}
}

func gitLabPluginRefs(bundles []resource.ContributionBundle) []resource.PluginRef {
	var refs []resource.PluginRef
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			if strings.EqualFold(strings.TrimSpace(ref.Name), gitlab.Name) {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func gitlabConfigForRef(ref resource.PluginRef) (gitlab.Config, error) {
	return pluginhost.DecodeConfig[gitlab.Config](ref.Config)
}

func stringIn(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
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
				return slack.Config{Auth: slack.AuthConfig{Method: slack.TokenMethod}}
			}
			return slack.NormalizeConfig(cfg)
		}
	}
	return slack.Config{Auth: slack.AuthConfig{Method: slack.TokenMethod}}
}

func availablePlugins(hostSystem system.System, dispatcher *slack.Dispatcher, taskRunner task.TaskRunner, authPath string, allowPluginAuthEnv bool) []pluginhost.Plugin {
	auth := NewPluginAuthContext(PluginAuthOptions{
		System:             hostSystem,
		AuthPath:           authPath,
		AllowPluginAuthEnv: allowPluginAuthEnv,
	})
	return availablePluginsWithAuth(hostSystem, dispatcher, taskRunner, auth.Store, auth.Resolver)
}

func availablePluginsWithAuth(hostSystem system.System, dispatcher *slack.Dispatcher, taskRunner task.TaskRunner, nativeStore runtimesecret.FileStore, nativeResolver runtimesecret.Resolver) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		workspace.New(hostSystem),
		discovery.New(),
		identity.New(),
		coding.New(hostSystem),
		goalplugin.New(),
		openai.New(),
		slack.NewWithResolver(hostSystem, dispatcher, nativeResolver, nativeStore),
		gitlab.NewWithResolver(hostSystem, nativeResolver),
		image.New(hostSystem),
		jira.NewWithResolver(hostSystem, nativeStore, nativeResolver),
		confluence.NewWithResolver(hostSystem, nativeStore, nativeResolver),
		kubernetes.New(hostSystem),
		loki.New(hostSystem),
		mysql.New(),
		openapi.New(hostSystem),
		memory.New(),
		task.NewWithRunnerAndSystem(taskRunner, hostSystem),
		skills.New(),
		text.New(),
		usageplugin.New(nil),
		web.New(hostSystem),
	}
}

// AuthPluginRegistry returns first-party plugins that expose auth declarations
// for distribution-level connect commands.
func AuthPluginRegistry(context.Context) ([]pluginhost.Plugin, error) {
	hostSystem, err := system.NewHost(system.Config{AllowPrivateNetwork: true})
	if err != nil {
		return nil, err
	}
	return []pluginhost.Plugin{
		openai.New(),
		slack.New(nil),
		gitlab.New(hostSystem),
		jira.New(hostSystem),
		confluence.New(hostSystem),
	}, nil
}

// ManifestAuthTargetRegistry returns auth targets declared by a local app
// manifest scope.
func ManifestAuthTargetRegistry(loader Loader) func(context.Context) ([]pluginhost.AuthTarget, error) {
	if loader == nil {
		loader = distlocal.Load
	}
	return func(ctx context.Context) ([]pluginhost.AuthTarget, error) {
		loaded, err := loader(ctx, ".")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(loaded.Manifest) == "" {
			return nil, fmt.Errorf("auth: no app manifest found in %s", loaded.Root)
		}
		hostSystem, err := system.NewHost(system.Config{Root: loaded.Root, AllowPrivateNetwork: true})
		if err != nil {
			return nil, err
		}
		return pluginhost.ResolveAuthTargets(ctx, pluginRefs(loaded.Distribution.Bundles), availablePlugins(hostSystem, nil, nil, "", false))
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

func launchIdentityResolver(ctx context.Context, sys system.System, auth PluginAuthContext, channels []distribution.Channel, bundles []resource.ContributionBundle) orchestrationidentity.Resolver {
	var resolvers []orchestrationidentity.Resolver
	for _, doc := range channels {
		if doc.Type != "slack" {
			continue
		}
		ref := resource.PluginRef{Name: slack.Name, Instance: firstNonEmptyString(doc.Instance, slack.Name)}
		session, err := slack.ResolveWithResolver(ctx, sys, auth.Resolver, ref, slackConfigForInstance(bundles, ref.InstanceName()))
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

func authEnvironmentContributions(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin, auth PluginAuthContext) ([]runtimeevidence.Observer, []runtimeevidence.AssertionDeriver, error) {
	targets, err := authTargets(ctx, bundles, plugins)
	if err != nil {
		return nil, nil, err
	}
	if len(targets) == 0 {
		return nil, nil, nil
	}
	return []runtimeevidence.Observer{authstatus.NewObserver(targets, auth.Resolver)}, []runtimeevidence.AssertionDeriver{authstatus.NewAssertionDeriver()}, nil
}

func authTargets(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin) ([]authstatus.Target, error) {
	targets, err := pluginhost.ResolveAuthTargets(ctx, pluginRefs(bundles), plugins)
	if err != nil {
		return nil, err
	}
	out := make([]authstatus.Target, 0, len(targets))
	for _, target := range targets {
		out = append(out, authstatus.Target{Ref: target.Ref, Methods: target.Methods})
	}
	return out, nil
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
		out[i].ActivationSets = append(out[i].ActivationSets[:0:0], bundle.ActivationSets...)
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
