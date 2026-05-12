package daemon

import (
	"context"
	"fmt"
	"time"

	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
)

// Host is the process/control-plane view of a running runtime.
type Host struct {
	client    clientapi.ChannelClient
	startedAt time.Time
}

// Config configures a host.
type Config struct {
	Client    clientapi.ChannelClient
	StartedAt time.Time
}

// New returns a host over an existing channel client.
func New(cfg Config) (*Host, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("daemon: channel client is nil")
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = time.Now().UTC()
	}
	return &Host{client: cfg.Client, startedAt: cfg.StartedAt}, nil
}

// Status describes process/control-plane state.
type Status struct {
	StartedAt time.Time `json:"started_at"`
}

// Status returns host status.
func (h *Host) Status(context.Context) (Status, error) {
	if h == nil || h.client == nil {
		return Status{}, fmt.Errorf("daemon: host is nil")
	}
	return Status{StartedAt: h.startedAt}, nil
}

// ListSessions lists sessions through the hosted channel client.
func (h *Host) ListSessions(ctx context.Context, req clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	if h == nil || h.client == nil {
		return nil, fmt.Errorf("daemon: host is nil")
	}
	return h.client.ListSessions(ctx, req)
}
