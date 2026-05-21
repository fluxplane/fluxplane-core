package daemon

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	coresession "github.com/fluxplane/engine/core/session"
	"github.com/fluxplane/engine/orchestration/channelruntime"
	clientapi "github.com/fluxplane/engine/orchestration/client"
	"github.com/fluxplane/engine/orchestration/session"
)

// Host is the process/control-plane view of a running runtime.
type Host struct {
	client         clientapi.ChannelClient
	sessionCatalog session.SessionCatalog
	startedAt      time.Time
	channels       []channelruntime.Channel
}

// Config configures a host.
type Config struct {
	Client         clientapi.ChannelClient
	SessionCatalog session.SessionCatalog
	Channels       []channelruntime.Channel
	StartedAt      time.Time
}

// New returns a host over an existing channel client.
func New(cfg Config) (*Host, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("daemon: channel client is nil")
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = time.Now().UTC()
	}
	return &Host{client: cfg.Client, sessionCatalog: cfg.SessionCatalog, startedAt: cfg.StartedAt, channels: append([]channelruntime.Channel(nil), cfg.Channels...)}, nil
}

// Status describes process/control-plane state.
type Status struct {
	StartedAt time.Time `json:"started_at"`
	Channels  []string  `json:"channels,omitempty"`
}

// ConfiguredSession describes one configured session profile known to the
// daemon host.
type ConfiguredSession struct {
	ID   string           `json:"id"`
	Spec coresession.Spec `json:"spec"`
}

// Status returns host status.
func (h *Host) Status(context.Context) (Status, error) {
	if h == nil || h.client == nil {
		return Status{}, fmt.Errorf("daemon: host is nil")
	}
	return Status{StartedAt: h.startedAt, Channels: h.channelNames()}, nil
}

// ListSessions lists sessions through the hosted channel client.
func (h *Host) ListSessions(ctx context.Context, req clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	if h == nil || h.client == nil {
		return nil, fmt.Errorf("daemon: host is nil")
	}
	return h.client.ListSessions(ctx, req)
}

// ListConfiguredSessions returns resource-configured session profiles.
func (h *Host) ListConfiguredSessions(context.Context) ([]ConfiguredSession, error) {
	if h == nil || h.client == nil {
		return nil, fmt.Errorf("daemon: host is nil")
	}
	out := make([]ConfiguredSession, 0, len(h.sessionCatalog))
	for _, binding := range h.sessionCatalog {
		out = append(out, ConfiguredSession{ID: binding.ID.Address(), Spec: binding.Spec})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// OpenConfiguredSession opens a configured session profile through the hosted
// channel client.
func (h *Host) OpenConfiguredSession(ctx context.Context, name coresession.Name, req clientapi.OpenRequest) (clientapi.SessionHandle, error) {
	if h == nil || h.client == nil {
		return nil, fmt.Errorf("daemon: host is nil")
	}
	if name == "" {
		return nil, fmt.Errorf("daemon: configured session name is empty")
	}
	req.Session = coresession.Ref{Name: name}
	return h.client.Open(ctx, req)
}

// RunChannels starts all configured long-running channels and blocks until the
// context is canceled or one channel exits with an error.
func (h *Host) RunChannels(ctx context.Context) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("daemon: host is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, len(h.channels))
	var wg sync.WaitGroup
	for _, ch := range h.channels {
		ch := ch
		if ch == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ch.Start(ctx, h.client); err != nil && ctx.Err() == nil {
				errs <- fmt.Errorf("daemon: channel %q: %w", ch.Name(), err)
				cancel()
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case err := <-errs:
		<-done
		return err
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		<-done
		return ctx.Err()
	}
}

func (h *Host) channelNames() []string {
	out := make([]string, 0, len(h.channels))
	for _, ch := range h.channels {
		if ch != nil && ch.Name() != "" {
			out = append(out, ch.Name())
		}
	}
	sort.Strings(out)
	return out
}
