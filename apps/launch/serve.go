package launch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	fluxplane "github.com/fluxplane/fluxplane-core"
	"github.com/fluxplane/fluxplane-core/adapters/channels/httpsse"
	controlhttp "github.com/fluxplane/fluxplane-core/adapters/control/http"
	distlocal "github.com/fluxplane/fluxplane-core/adapters/distribution/local"
	distserve "github.com/fluxplane/fluxplane-core/adapters/distribution/serve"
	"github.com/fluxplane/fluxplane-core/adapters/resources/appconfig"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/user"
	"github.com/fluxplane/fluxplane-core/orchestration/agentfactory"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	orchestrationsession "github.com/fluxplane/fluxplane-core/orchestration/session"

	"github.com/fluxplane/fluxplane-core/orchestration/channelruntime"
	"github.com/fluxplane/fluxplane-core/orchestration/daemon"
	orchestrationdistribution "github.com/fluxplane/fluxplane-core/orchestration/distribution"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	triggerhost "github.com/fluxplane/fluxplane-core/orchestration/trigger"

	"github.com/fluxplane/fluxplane-core/plugins/integrations/slack"
	"github.com/fluxplane/fluxplane-core/runtime/system"
)

type Options struct {
	AppDir             string
	Profile            string
	Profiles           []string
	Debug              bool
	Verbose            bool
	Yolo               bool
	Dev                bool
	AuthPath           string
	AllowPluginAuthEnv bool
	Provider           string
	Model              string
	Thinking           string
	ThinkingSet        bool
	Effort             string
	EffortSet          bool
	EnvFiles           []string
	HealthAddr         string
	ToolProjection     fluxplane.ToolProjectionConfig
	ModelResolver      agentfactory.ModelResolver
}

type ServeDistributionOptions struct {
	Root                string
	Spec                coredistribution.Spec
	Bundles             []resource.ContributionBundle
	Launch              orchestrationdistribution.LaunchConfig
	AuthPath            string
	AllowPluginAuthEnv  bool
	Provider            string
	Model               string
	Thinking            string
	ThinkingSet         bool
	Effort              string
	EffortSet           bool
	HealthAddr          string
	Debug               bool
	Verbose             bool
	Yolo                bool
	Dev                 bool
	Plugins             func(system.System) []pluginhost.Plugin
	PluginFactory       func(PluginFactoryContext) []pluginhost.Plugin
	ToolProjection      fluxplane.ToolProjectionConfig
	ModelResolver       agentfactory.ModelResolver
	AllowPrivateNetwork bool
}

var serveShutdownGrace = 5 * time.Second

func Serve(ctx context.Context, opts Options) error {
	configureServeLogging(opts.Debug)
	loaded, err := distlocal.LoadWithOptions(ctx, opts.AppDir, distlocal.LoadOptions{Profile: opts.Profile, Profiles: opts.Profiles})
	if err != nil {
		return err
	}
	if err := validateServeLaunch(loaded, opts.AppDir); err != nil {
		return err
	}
	loaded.Launch.Workspace.EnvFiles = append(loaded.Launch.Workspace.EnvFiles, trimLaunchStrings(opts.EnvFiles)...)
	return ServeDistribution(ctx, ServeDistributionOptions{
		Root:                loaded.Root,
		Spec:                loaded.Distribution.Spec,
		Bundles:             loaded.Distribution.Bundles,
		Launch:              loaded.Launch,
		AuthPath:            opts.AuthPath,
		AllowPluginAuthEnv:  opts.AllowPluginAuthEnv,
		Provider:            opts.Provider,
		Model:               opts.Model,
		Thinking:            opts.Thinking,
		ThinkingSet:         opts.ThinkingSet,
		Effort:              opts.Effort,
		EffortSet:           opts.EffortSet,
		HealthAddr:          opts.HealthAddr,
		Debug:               opts.Debug,
		Verbose:             opts.Verbose,
		Yolo:                opts.Yolo,
		Dev:                 opts.Dev,
		ToolProjection:      opts.ToolProjection,
		ModelResolver:       opts.ModelResolver,
		AllowPrivateNetwork: true,
	})
}

func ServeDistribution(ctx context.Context, opts ServeDistributionOptions) error {
	configureServeLogging(opts.Debug)
	runtime, err := Launch(ctx, RuntimeOptions{
		Root:                opts.Root,
		Spec:                opts.Spec,
		Bundles:             opts.Bundles,
		Launch:              opts.Launch,
		AuthPath:            opts.AuthPath,
		AllowPluginAuthEnv:  opts.AllowPluginAuthEnv,
		Provider:            opts.Provider,
		Model:               opts.Model,
		Thinking:            opts.Thinking,
		ThinkingSet:         opts.ThinkingSet,
		Effort:              opts.Effort,
		EffortSet:           opts.EffortSet,
		Debug:               opts.Debug,
		Yolo:                opts.Yolo,
		Dev:                 opts.Dev,
		Plugins:             opts.Plugins,
		PluginFactory:       opts.PluginFactory,
		ToolProjection:      opts.ToolProjection,
		ModelResolver:       opts.ModelResolver,
		AllowPrivateNetwork: opts.AllowPrivateNetwork,
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	client := defaultServeChannelClient(runtime.Service, opts.Spec, runtime.Composition.SessionCatalog)
	channels, err := serveChannels(ctx, opts.Launch.Channels, opts.Bundles, Options{AuthPath: opts.AuthPath, AllowPluginAuthEnv: opts.AllowPluginAuthEnv, Debug: opts.Debug}, runtime.Dispatcher, runtime.System)
	if err != nil {
		return err
	}
	host, err := daemon.New(daemon.Config{
		Client:         client,
		SessionCatalog: runtime.Composition.SessionCatalog,
		Channels:       channels,
	})
	if err != nil {
		return err
	}
	if err := startServeListeners(ctx, opts.Launch.Listeners, opts.Launch.Channels, client, host, runtime.Caller, runtime.Trust); err != nil {
		return err
	}
	if err := startHealthListener(ctx, opts.HealthAddr, host); err != nil {
		return err
	}
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if opts.Debug {
		_, _ = fmt.Fprintf(os.Stderr, "fluxplane serve loaded %s\n", opts.Root)
	}
	serveCtx, cancelServe := context.WithCancel(runCtx)
	defer cancelServe()
	if opts.Verbose {
		cancelEvents, err := startServeEventLogger(serveCtx, client, os.Stderr)
		if err != nil {
			return err
		}
		defer cancelEvents()
		logServeVerboseReady(os.Stderr, opts.Root)
	}
	errs := make(chan error, 2)
	running := 0
	if len(opts.Launch.Triggers) > 0 {
		triggers, err := triggerhost.New(triggerhost.Config{
			Client: client,
			Specs:  opts.Launch.Triggers,
			Caller: runtime.Caller,
			Trust:  runtime.Trust,
		})
		if err != nil {
			return err
		}
		if opts.Verbose {
			logServeTriggerStart(os.Stderr, opts.Launch.Triggers)
		}
		running++
		go func() {
			err := triggers.Run(serveCtx)
			if errors.Is(err, context.Canceled) {
				err = nil
			}
			errs <- err
		}()
	}
	for _, ch := range channels {
		if ch != nil && ch.Name() != "" {
			_, _ = fmt.Fprintf(os.Stderr, "channel %s starting\n", ch.Name())
		}
	}
	if len(channels) > 0 {
		running++
		go func() {
			err := host.RunChannels(serveCtx)
			if errors.Is(err, context.Canceled) {
				err = nil
			}
			errs <- err
		}()
	}
	if running == 0 {
		<-runCtx.Done()
		return nil
	}
	for i := 0; i < running; i++ {
		select {
		case err := <-errs:
			if err != nil {
				cancelServe()
				return err
			}
		case <-runCtx.Done():
			cancelServe()
			pending := running - i
			if !waitServeShutdown(errs, pending, serveShutdownGrace) && opts.Verbose {
				_, _ = fmt.Fprintf(os.Stderr, "serve shutdown timed out after %s; exiting\n", serveShutdownGrace)
			}
			return nil
		}
	}
	return nil
}

type serveDefaultChannelClient struct {
	client   fluxplane.ChannelClient
	spec     coredistribution.Spec
	sessions orchestrationsession.SessionCatalog
}

func defaultServeChannelClient(client fluxplane.ChannelClient, spec coredistribution.Spec, sessions orchestrationsession.SessionCatalog) fluxplane.ChannelClient {
	if client == nil {
		return nil
	}
	return serveDefaultChannelClient{client: client, spec: spec, sessions: sessions}
}

func (c serveDefaultChannelClient) Open(ctx context.Context, req fluxplane.OpenRequest) (fluxplane.Session, error) {
	if req.Session.Name == "" {
		req.Session = c.spec.DefaultSession
	}
	if req.Conversation.ID == "" {
		req.Conversation = c.spec.DefaultConversation
	}
	session, err := c.client.Open(ctx, req)
	if err != nil {
		return nil, err
	}
	return serveDefaultSession{session: session, spec: c.spec, sessions: c.sessions}, nil
}

func (c serveDefaultChannelClient) Resume(ctx context.Context, req fluxplane.ResumeRequest) (fluxplane.Session, error) {
	if _, err := c.client.Resume(ctx, req); err != nil {
		return nil, err
	}
	return c.Open(ctx, fluxplane.OpenRequest{ThreadID: req.ThreadID})
}

func (c serveDefaultChannelClient) ListSessions(ctx context.Context, req fluxplane.ListSessionsRequest) ([]fluxplane.SessionSummary, error) {
	summaries, err := c.client.ListSessions(ctx, req)
	if err != nil {
		return nil, err
	}
	for i := range summaries {
		summaries[i].Info = c.defaultSessionInfo(summaries[i].Info)
	}
	return summaries, nil
}

func (c serveDefaultChannelClient) OnEvent(ctx context.Context, fn func(clientapi.Event)) (func(), error) {
	watcher, ok := c.client.(serveEventWatcher)
	if !ok {
		return nil, fmt.Errorf("serve: --verbose is unavailable for this channel client")
	}
	return watcher.OnEvent(ctx, fn)
}

func (c serveDefaultChannelClient) defaultSessionInfo(info fluxplane.SessionInfo) fluxplane.SessionInfo {
	if info.Session.Name == "" {
		info.Session = c.defaultSessionRef()
	}
	if info.Conversation.ID == "" {
		info.Conversation = c.spec.DefaultConversation
	}
	return info
}

func (c serveDefaultChannelClient) defaultSessionRef() fluxplane.SessionRef {
	if c.spec.DefaultSession.Name == "" {
		return fluxplane.SessionRef{}
	}
	binding, err := c.sessions.Resolve(string(c.spec.DefaultSession.Name))
	if err != nil {
		return c.spec.DefaultSession
	}
	return fluxplane.SessionRef{Name: fluxplane.SessionName(binding.ID.Address())}
}

type serveDefaultSession struct {
	session  fluxplane.Session
	spec     coredistribution.Spec
	sessions orchestrationsession.SessionCatalog
}

func (s serveDefaultSession) Info() fluxplane.SessionInfo {
	return serveDefaultChannelClient{spec: s.spec, sessions: s.sessions}.defaultSessionInfo(s.session.Info())
}

func (s serveDefaultSession) Submit(ctx context.Context, submission fluxplane.Submission) (fluxplane.Run, error) {
	return s.session.Submit(ctx, submission)
}

func (s serveDefaultSession) Events(ctx context.Context, opts fluxplane.EventOptions) (<-chan fluxplane.Event, func(), error) {
	return s.session.Events(ctx, opts)
}

func (s serveDefaultSession) OnEvent(ctx context.Context, fn func(fluxplane.Event)) (func(), error) {
	return s.session.OnEvent(ctx, fn)
}

func (s serveDefaultSession) Close(ctx context.Context) error {
	return s.session.Close(ctx)
}

func waitServeShutdown(errs <-chan error, pending int, grace time.Duration) bool {
	if pending <= 0 {
		return true
	}
	if grace <= 0 {
		for i := 0; i < pending; i++ {
			<-errs
		}
		return true
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	for i := 0; i < pending; i++ {
		select {
		case <-errs:
		case <-timer.C:
			return false
		}
	}
	return true
}

func listenerAuthority(listener orchestrationdistribution.Listener, caller policy.Caller, trust policy.Trust) httpsse.Authority {
	mode := strings.ToLower(strings.TrimSpace(distserve.AuthString(listener.Auth, "mode")))
	authority := httpsse.Authority{Caller: caller, Trust: trust}
	switch mode {
	case "", "local_socket":
		authority.AllowTrustDowngrade = !distserve.AddrIsTCP(listener.Addr)
	case "bearer", "token":
		authority.AllowTrustDowngrade = true
		if authority.Trust.Level == "" || policy.TrustSatisfies(authority.Trust.Level, policy.TrustPrivileged) {
			authority.Trust.Level = policy.TrustVerified
		}
	}
	return authority
}

func validateServeLaunch(loaded orchestrationdistribution.Loaded, initPath string) error {
	if len(loaded.Launch.Listeners) == 0 && len(loaded.Launch.Channels) == 0 && len(loaded.Launch.Triggers) == 0 {
		if loaded.Manifest == "" {
			if strings.TrimSpace(initPath) == "" {
				initPath = loaded.Root
			}
			return fmt.Errorf("serve: %s is not initialized; run \"fluxplane init %s\" to create a minimal local app manifest", loaded.Root, initPath)
		}
		return fmt.Errorf("serve: distribution %q has no daemon listeners or channels or triggers", loaded.Distribution.Spec.Name)
	}
	return nil
}

func serveChannels(ctx context.Context, docs []orchestrationdistribution.Channel, bundles []resource.ContributionBundle, opts Options, dispatcher *slack.Dispatcher, sys system.System) ([]channelruntime.Channel, error) {
	var out []channelruntime.Channel
	auth := NewPluginAuthContext(PluginAuthOptions{
		System:             sys,
		AuthPath:           opts.AuthPath,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
	})
	for _, doc := range docs {
		switch doc.Type {
		case "direct":
			continue
		case "slack":
			ref := resource.PluginRef{Name: slack.Name, Instance: firstNonEmptyString(doc.Instance, slack.Name)}
			cfg := slackConfigForInstance(bundles, ref.InstanceName())
			session, err := slack.ResolveWithResolver(ctx, sys, auth.Resolver, ref, cfg)
			if err != nil {
				slog.Warn("slack channel skipped because auth is not connected", "channel", doc.Name, "instance", ref.InstanceName(), "error", err)
				continue
			}
			if session.AppToken == "" {
				slog.Warn("slack channel skipped because app_token is not connected", "channel", doc.Name, "instance", ref.InstanceName(), "hint", fmt.Sprintf("run fluxplane auth connect --plugin slack --instance %s --method %s --field app_token=<value>", ref.InstanceName(), slack.TokenMethod))
				continue
			}
			sessionName := doc.Session
			if sessionName == "" {
				sessionName = doc.Name
			}
			ch, err := slack.NewChannel(slack.ChannelConfig{
				Name:            doc.Name,
				Session:         fluxplane.SessionRef{Name: fluxplane.SessionName(sessionName)},
				BotToken:        session.BotToken,
				UserToken:       session.UserToken,
				AppToken:        session.AppToken,
				TokenPreference: cfg.Auth.ChannelToken,
				Debug:           opts.Debug,
				Access:          slackAccess(doc.Access),
				Dispatcher:      dispatcher,
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

func trimLaunchStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func slackAccess(doc orchestrationdistribution.Access) slack.AccessPolicy {
	return slack.AccessPolicy{
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

func startServeListeners(ctx context.Context, listeners []orchestrationdistribution.Listener, channels []orchestrationdistribution.Channel, client fluxplane.ChannelClient, host *daemon.Host, caller policy.Caller, trust policy.Trust) error {
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
		controlServer, err := controlhttp.NewServer(controlhttp.ServerConfig{Host: host})
		if err != nil {
			return err
		}
		mux.Handle("/control/", http.StripPrefix("/control", controlServer))
		if needsDirect[listenerDoc.Name] {
			channelServer, err := httpsse.NewServer(httpsse.ServerConfig{
				Client:    client,
				Authority: listenerAuthority(listenerDoc, caller, trust),
			})
			if err != nil {
				return err
			}
			mux.Handle("/", channelServer)
		}
		ln, display, cleanup, err := distserve.Listen(listenerDoc.Addr)
		if err != nil {
			return err
		}
		handler, err := distserve.ListenerHandler(appconfig.ListenerDoc{
			Name: listenerDoc.Name,
			Type: listenerDoc.Type,
			Addr: listenerDoc.Addr,
			Auth: listenerDoc.Auth,
		}, mux)
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

func startHealthListener(ctx context.Context, addr string, host *daemon.Host) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	controlServer, err := controlhttp.NewServer(controlhttp.ServerConfig{Host: host})
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/control/", http.StripPrefix("/control", controlServer))
	ln, display, cleanup, err := distserve.Listen(addr)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		cleanup()
	}()
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintf(os.Stderr, "health listener failed: %v\n", err)
			cleanup()
		}
	}()
	_, _ = fmt.Fprintf(os.Stderr, "health listener on %s\n", display)
	return nil
}
