package launch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/codewandler/connectors/integrate"
	connectorsruntime "github.com/codewandler/connectors/runtime"
	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/fluxplane/agentruntime/adapters/connectauth"
	distrun "github.com/fluxplane/agentruntime/adapters/distribution/run"
	distserve "github.com/fluxplane/agentruntime/adapters/distribution/serve"
	"github.com/fluxplane/agentruntime/adapters/httpcontrol"
	"github.com/fluxplane/agentruntime/adapters/httpssechannel"
	"github.com/fluxplane/agentruntime/core/channel"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/channelruntime"
	"github.com/fluxplane/agentruntime/orchestration/daemon"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/plugins/jiraplugin"
	"github.com/fluxplane/agentruntime/plugins/openaiplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

type Options struct {
	AppDir   string
	Debug    bool
	AuthPath string
}

func Serve(ctx context.Context, opts Options) error {
	configureServeLogging(opts.Debug)
	cfgFile, err := appconfig.LoadDirFile(ctx, opts.AppDir)
	if err != nil {
		return err
	}
	if err := cfgFile.Validate(); err != nil {
		return err
	}
	root, err := filepath.Abs(opts.AppDir)
	if err != nil {
		return err
	}
	hostSystem, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		return err
	}
	dispatcher := slackplugin.NewDispatcher()
	connectorEngine, connectorInstances, err := serveConnectorEngine(ctx, opts, cfgFile.Connectors)
	if err != nil {
		return err
	}
	if connectorEngine != nil {
		defer func() { _ = connectorEngine.Close() }()
	}
	slackPlugin := slackplugin.NewWithConnectors(dispatcher, connectorEngine, connectorInstancesForKind(connectorInstances, slackplugin.Name))
	gitlabPlugin := gitlabplugin.New(connectorEngine, connectorInstancesForKind(connectorInstances, gitlabplugin.Name))
	jiraPlugin := jiraplugin.New(connectorEngine, connectorInstancesForKind(connectorInstances, jiraplugin.Name))
	basePlugins := []pluginhost.Plugin{
		slackPlugin,
		gitlabPlugin,
		jiraPlugin,
		planexecplugin.New(),
		skillplugin.New(),
		textplugin.New(),
		webplugin.New(hostSystem),
	}
	bundle := cfgFile.Bundle
	plugins := basePlugins
	if serveBundleHasPlugin(bundle, skillplugin.Name) && !serveHasDatasource(bundle, skillplugin.DatasourceName) {
		bundle.Datasources = append(bundle.Datasources, skillplugin.DatasourceSpec())
	}
	if len(bundle.Datasources) > 0 {
		registry, err := serveDatasourceRegistry(ctx, bundle, basePlugins, root)
		if err != nil {
			return err
		}
		plugins = append(plugins, datasourceplugin.New(registry))
		if !serveBundleHasPlugin(bundle, datasourceplugin.Name) {
			bundle.Plugins = append(bundle.Plugins, resource.PluginRef{Name: datasourceplugin.Name})
		}
	}
	composition, err := app.Compose(app.Config{
		Bundles: []agentruntime.ResourceBundle{bundle},
		Plugins: plugins,
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:   localSandbox{Root: root},
			ACL:       localACL{},
			AllowPure: true,
		})),
	})
	if err != nil {
		return err
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModelResolver: distrun.ModelResolver{Provider: "openai", Debug: opts.Debug},
		LLMStreamPolicy:  distrun.DebugStreamPolicy(opts.Debug),
		ToolProjection: agentruntime.ToolProjectionConfig{
			AllowSideEffects:      true,
			MaxRisk:               operation.RiskMedium,
			IncludeBareOperations: true,
		},
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
		return err
	}
	channels, err := serveChannels(ctx, cfgFile.Daemon.Channels, opts, dispatcher)
	if err != nil {
		return err
	}
	host, err := daemon.New(daemon.Config{
		Client:         service,
		SessionCatalog: composition.SessionCatalog,
		Channels:       channels,
	})
	if err != nil {
		return err
	}
	if err := startServeListeners(ctx, cfgFile.Daemon.Listeners, cfgFile.Daemon.Channels, service, host); err != nil {
		return err
	}
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if opts.Debug {
		_, _ = fmt.Fprintf(os.Stderr, "agentsdk serve loaded %s\n", cfgFile.Path)
	}
	if len(channels) == 0 {
		<-runCtx.Done()
		return nil
	}
	for _, ch := range channels {
		if ch != nil && ch.Name() != "" {
			_, _ = fmt.Fprintf(os.Stderr, "channel %s starting\n", ch.Name())
		}
	}
	if err := host.RunChannels(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func serveConnectorEngine(ctx context.Context, opts Options, docs map[string]appconfig.ConnectorDoc) (*connectorsruntime.Engine, []connectorplugin.Instance, error) {
	if len(docs) == 0 {
		return nil, nil, nil
	}
	engine, providers, err := newServeConnectEngine(ctx, opts.AuthPath)
	if err != nil {
		return nil, nil, err
	}
	knownProviders := map[string]bool{}
	for _, provider := range providers {
		knownProviders[provider] = true
	}
	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)
	instances := make([]connectorplugin.Instance, 0, len(names))
	for _, instanceID := range names {
		doc := docs[instanceID]
		kind := strings.TrimSpace(doc.Kind)
		if kind == "" {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: connector instance %q kind is empty", instanceID)
		}
		if !knownProviders[kind] {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: connector instance %q uses unknown provider %q (available: %s)", instanceID, kind, strings.Join(providers, ", "))
		}
		stored, err := engine.Instances.Load(ctx, instanceID)
		if err != nil {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: load connector instance %q: %w", instanceID, err)
		}
		if stored.Connector != kind {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: connector instance %q has kind %q, want %q", instanceID, stored.Connector, kind)
		}
		if err := engine.ConnectInstance(ctx, instanceID); err != nil {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: connect %s connector instance %q: %w", kind, instanceID, err)
		}
		instances = append(instances, connectorplugin.Instance{ID: instanceID, Kind: kind})
	}
	return engine, instances, nil
}

func newServeConnectEngine(ctx context.Context, basePath string) (*connectorsruntime.Engine, []string, error) {
	providers, err := serveConnectorProviderNames(ctx)
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

func serveConnectorProviderNames(ctx context.Context) ([]string, error) {
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

func serveDatasourceRegistry(ctx context.Context, bundle resource.ContributionBundle, plugins []pluginhost.Plugin, root string) (*coredatasource.Registry, error) {
	host, err := pluginhost.New(plugins...)
	if err != nil {
		return nil, err
	}
	refs := make([]resource.PluginRef, 0, len(bundle.Plugins))
	for _, ref := range bundle.Plugins {
		if ref.Name != datasourceplugin.Name {
			refs = append(refs, ref)
		}
	}
	resolved, err := host.Resolve(ctx, refs...)
	if err != nil {
		return nil, err
	}
	var providers []coredatasource.Provider
	for _, contribution := range resolved.DatasourceProviders {
		providers = append(providers, contribution.Provider)
	}
	providers = append(providers, datasourceplugin.NewFilesystemProvider(os.DirFS(root)))
	return datasourceplugin.BuildRegistry(ctx, bundle.Datasources, providers)
}

func serveBundleHasPlugin(bundle resource.ContributionBundle, name string) bool {
	for _, ref := range bundle.Plugins {
		if ref.Name == name {
			return true
		}
	}
	return false
}

func serveHasDatasource(bundle resource.ContributionBundle, name string) bool {
	for _, spec := range bundle.Datasources {
		if string(spec.Name) == name {
			return true
		}
	}
	return false
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

func serveChannels(ctx context.Context, docs []appconfig.ChannelDoc, opts Options, dispatcher *slackplugin.Dispatcher) ([]channelruntime.Channel, error) {
	var out []channelruntime.Channel
	store := connectauth.NewStore(opts.AuthPath)
	for _, doc := range docs {
		switch doc.Type {
		case "direct":
			continue
		case "slack":
			creds, err := store.LoadSlack(ctx, doc.Connector)
			if err != nil {
				return nil, err
			}
			sessionName := doc.Session
			if sessionName == "" {
				sessionName = doc.Name
			}
			ch, err := slackplugin.NewChannel(slackplugin.ChannelConfig{
				Name:       doc.Name,
				Session:    agentruntime.SessionRef{Name: agentruntime.SessionName(sessionName)},
				BotToken:   creds.BotToken,
				UserToken:  creds.UserToken,
				AppToken:   creds.AppToken,
				BotUserID:  creds.BotUserID,
				TeamID:     creds.TeamID,
				Debug:      opts.Debug,
				Access:     slackAccess(doc.Access),
				Dispatcher: dispatcher,
			})
			if err != nil {
				return nil, err
			}
			out = append(out, ch)
		default:
			return nil, fmt.Errorf("serve: unsupported channel type %q", doc.Type)
		}
	}
	return out, nil
}

func configureServeLogging(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func slackAccess(doc appconfig.AccessDoc) slackplugin.AccessPolicy {
	return slackplugin.AccessPolicy{
		Mode:             doc.Mode,
		AllowUsers:       append([]string(nil), doc.AllowUsers...),
		DenyUsers:        append([]string(nil), doc.DenyUsers...),
		AllowChannels:    append([]string(nil), doc.AllowChannels...),
		DenyChannels:     append([]string(nil), doc.DenyChannels...),
		AllowKinds:       append([]string(nil), doc.AllowKinds...),
		DefaultTrust:     userTrust(doc.DefaultTrust),
		Operators:        append([]string(nil), doc.Operators...),
		InternalUsers:    append([]string(nil), doc.InternalUsers...),
		InternalChannels: append([]string(nil), doc.InternalChannels...),
		Sharing:          firstNonEmptyString(doc.Sharing, "strict"),
	}
}

func userTrust(raw string) user.TrustLevel {
	switch strings.TrimSpace(raw) {
	case "operator":
		return user.TrustOperator
	case "internal":
		return user.TrustInternal
	default:
		return user.TrustPublic
	}
}

func startServeListeners(ctx context.Context, listeners []appconfig.ListenerDoc, channels []appconfig.ChannelDoc, client agentruntime.ChannelClient, host *daemon.Host) error {
	needsDirect := map[string]bool{}
	for _, ch := range channels {
		if ch.Type == "direct" && ch.Listener != "" {
			needsDirect[ch.Listener] = true
		}
	}
	for _, listenerDoc := range listeners {
		if listenerDoc.Type != "http" {
			return fmt.Errorf("serve: unsupported listener type %q", listenerDoc.Type)
		}
		mux := http.NewServeMux()
		controlServer, err := httpcontrol.NewServer(httpcontrol.ServerConfig{Host: host})
		if err != nil {
			return err
		}
		mux.Handle("/control/", http.StripPrefix("/control", controlServer))
		if needsDirect[listenerDoc.Name] {
			channelServer, err := httpssechannel.NewServer(httpssechannel.ServerConfig{Client: client})
			if err != nil {
				return err
			}
			mux.Handle("/", channelServer)
		}
		ln, display, cleanup, err := distserve.Listen(listenerDoc.Addr)
		if err != nil {
			return err
		}
		handler, err := distserve.ListenerHandler(listenerDoc, mux)
		if err != nil {
			cleanup()
			return err
		}
		server := &http.Server{Handler: handler}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
			cleanup()
		}()
		go func() {
			if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				_, _ = fmt.Fprintf(os.Stderr, "listener %s failed: %v\n", listenerDoc.Name, err)
				cleanup()
			}
		}()
		_, _ = fmt.Fprintf(os.Stderr, "listener %s on %s\n", listenerDoc.Name, display)
	}
	return nil
}
