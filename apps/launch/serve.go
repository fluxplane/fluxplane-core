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
	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/fluxplane/agentruntime/adapters/connectauth"
	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	distserve "github.com/fluxplane/agentruntime/adapters/distribution/serve"
	"github.com/fluxplane/agentruntime/adapters/httpcontrol"
	"github.com/fluxplane/agentruntime/adapters/httpssechannel"
	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/channelruntime"
	"github.com/fluxplane/agentruntime/orchestration/daemon"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
)

type Options struct {
	AppDir   string
	Debug    bool
	AuthPath string
}

func Serve(ctx context.Context, opts Options) error {
	configureServeLogging(opts.Debug)
	loaded, err := distlocal.Load(ctx, opts.AppDir)
	if err != nil {
		return err
	}
	runtime, err := Launch(ctx, RuntimeOptions{
		Root:                loaded.Root,
		Spec:                loaded.Distribution.Spec,
		Bundles:             loaded.Distribution.Bundles,
		Launch:              loaded.Launch,
		AuthPath:            opts.AuthPath,
		Debug:               opts.Debug,
		AllowPrivateNetwork: true,
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	channels, err := serveChannels(ctx, loaded.Launch.Channels, opts, runtime.Dispatcher)
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
	if err := startServeListeners(ctx, loaded.Launch.Listeners, loaded.Launch.Channels, runtime.Service, host); err != nil {
		return err
	}
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if opts.Debug {
		_, _ = fmt.Fprintf(os.Stderr, "agentsdk serve loaded %s\n", loaded.Manifest)
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

func serveChannels(ctx context.Context, docs []distribution.Channel, opts Options, dispatcher *slackplugin.Dispatcher) ([]channelruntime.Channel, error) {
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

func slackAccess(doc distribution.Access) slackplugin.AccessPolicy {
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

func startServeListeners(ctx context.Context, listeners []distribution.Listener, channels []distribution.Channel, client agentruntime.ChannelClient, host *daemon.Host) error {
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
