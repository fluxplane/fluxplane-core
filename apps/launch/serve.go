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

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/channels/httpsse"
	controlhttp "github.com/fluxplane/agentruntime/adapters/control/http"
	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	distserve "github.com/fluxplane/agentruntime/adapters/distribution/serve"
	"github.com/fluxplane/agentruntime/adapters/resources/appconfig"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/agentfactory"

	"github.com/fluxplane/agentruntime/orchestration/channelruntime"
	"github.com/fluxplane/agentruntime/orchestration/daemon"
	orchestrationdistribution "github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"

	"github.com/fluxplane/agentruntime/plugins/integrations/slack"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
)

type Options struct {
	AppDir        string
	Debug         bool
	Yolo          bool
	Dev           bool
	AuthPath      string
	Provider      string
	Model         string
	Thinking      string
	ThinkingSet   bool
	Effort        string
	EffortSet     bool
	EnvFiles      []string
	HealthAddr    string
	ModelResolver agentfactory.ModelResolver
}

type ServeDistributionOptions struct {
	Root                string
	Spec                coredistribution.Spec
	Bundles             []resource.ContributionBundle
	Launch              orchestrationdistribution.LaunchConfig
	AuthPath            string
	Provider            string
	Model               string
	Thinking            string
	ThinkingSet         bool
	Effort              string
	EffortSet           bool
	HealthAddr          string
	Debug               bool
	Yolo                bool
	Dev                 bool
	Plugins             func(system.System) []pluginhost.Plugin
	ToolProjection      agentruntime.ToolProjectionConfig
	ModelResolver       agentfactory.ModelResolver
	AllowPrivateNetwork bool
}

func Serve(ctx context.Context, opts Options) error {
	configureServeLogging(opts.Debug)
	loaded, err := distlocal.Load(ctx, opts.AppDir)
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
		Provider:            opts.Provider,
		Model:               opts.Model,
		Thinking:            opts.Thinking,
		ThinkingSet:         opts.ThinkingSet,
		Effort:              opts.Effort,
		EffortSet:           opts.EffortSet,
		HealthAddr:          opts.HealthAddr,
		Debug:               opts.Debug,
		Yolo:                opts.Yolo,
		Dev:                 opts.Dev,
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
		ToolProjection:      opts.ToolProjection,
		ModelResolver:       opts.ModelResolver,
		AllowPrivateNetwork: opts.AllowPrivateNetwork,
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	channels, err := serveChannels(ctx, opts.Launch.Channels, opts.Bundles, Options{AuthPath: opts.AuthPath, Debug: opts.Debug}, runtime.Dispatcher, runtime.System)
	if err != nil {
		return err
	}
	host, err := daemon.New(daemon.Config{
		Client:         runtime.Service,
		SessionCatalog: runtime.Composition.SessionCatalog,
		Channels:       channels,
	})
	if err != nil {
		return err
	}
	if err := startServeListeners(ctx, opts.Launch.Listeners, opts.Launch.Channels, runtime.Service, host, runtime.Caller, runtime.Trust); err != nil {
		return err
	}
	if err := startHealthListener(ctx, opts.HealthAddr, host); err != nil {
		return err
	}
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if opts.Debug {
		_, _ = fmt.Fprintf(os.Stderr, "coder app serve loaded %s\n", opts.Root)
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
	if len(loaded.Launch.Listeners) == 0 && len(loaded.Launch.Channels) == 0 {
		if loaded.Manifest == "" {
			if strings.TrimSpace(initPath) == "" {
				initPath = loaded.Root
			}
			return fmt.Errorf("serve: %s is not initialized; run \"coder app init %s\" to create a minimal local app manifest", loaded.Root, initPath)
		}
		return fmt.Errorf("serve: distribution %q has no daemon listeners or channels", loaded.Distribution.Spec.Name)
	}
	return nil
}

func serveChannels(ctx context.Context, docs []orchestrationdistribution.Channel, bundles []resource.ContributionBundle, opts Options, dispatcher *slack.Dispatcher, sys system.System) ([]channelruntime.Channel, error) {
	var out []channelruntime.Channel
	store := runtimesecret.NewFileStore(nativeAuthPath(opts.AuthPath))
	for _, doc := range docs {
		switch doc.Type {
		case "direct":
			continue
		case "slack":
			ref := resource.PluginRef{Name: slack.Name, Instance: firstNonEmptyString(doc.Instance, doc.Connector, slack.Name)}
			cfg := slackConfigForInstance(bundles, ref.InstanceName())
			session, err := slack.Resolve(ctx, sys, store, ref, cfg)
			if err != nil {
				return nil, err
			}
			if session.AppToken == "" {
				return nil, fmt.Errorf("serve: slack channel %q requires app_token for Socket Mode; run coder auth connect --plugin slack --instance %s --method %s --field app_token=<value>", doc.Name, ref.InstanceName(), slack.TokenMethod)
			}
			sessionName := doc.Session
			if sessionName == "" {
				sessionName = doc.Name
			}
			ch, err := slack.NewChannel(slack.ChannelConfig{
				Name:            doc.Name,
				Session:         agentruntime.SessionRef{Name: agentruntime.SessionName(sessionName)},
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

func startServeListeners(ctx context.Context, listeners []orchestrationdistribution.Listener, channels []orchestrationdistribution.Channel, client agentruntime.ChannelClient, host *daemon.Host, caller policy.Caller, trust policy.Trust) error {
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
