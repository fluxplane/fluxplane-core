package launch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/natseventstore"
	"github.com/fluxplane/agentruntime/adapters/sqleventstore"
	"github.com/fluxplane/agentruntime/core/event"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

const defaultEventStoreDSNEnv = "AGENTRUNTIME_EVENTSTORE_NATS_DSN"

func openLocalThreadStore(registry *event.Registry, cfgs ...distribution.EventsConfig) (corethread.Store, event.Store, func(), error) {
	cfg := distribution.EventsConfig{}
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
	kind := strings.ToLower(strings.TrimSpace(cfg.Store.Kind))
	switch kind {
	case "", "sqlite", "local":
		return openSQLiteThreadStore(registry)
	case "nats", "jetstream", "nats-jetstream":
		return openNATSThreadStore(registry, cfg.Store)
	default:
		return nil, nil, nil, fmt.Errorf("launch: event store kind %q is not supported", cfg.Store.Kind)
	}
}

func openSQLiteThreadStore(registry *event.Registry) (corethread.Store, event.Store, func(), error) {
	path, err := defaultEventStorePath()
	if err != nil {
		return nil, nil, nil, err
	}
	events, err := sqleventstore.Open(path, registry)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("launch: open event store %s: %w", path, err)
	}
	threads, err := runtimethread.NewStore(events)
	if err != nil {
		_ = events.Close()
		return nil, nil, nil, err
	}
	return threads, events, func() { _ = events.Close() }, nil
}

func openNATSThreadStore(registry *event.Registry, cfg distribution.EventStoreConfig) (corethread.Store, event.Store, func(), error) {
	dsn := strings.TrimSpace(cfg.DSN)
	dsnEnv := strings.TrimSpace(cfg.DSNEnv)
	if dsnEnv == "" {
		dsnEnv = defaultEventStoreDSNEnv
	}
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv(dsnEnv))
	}
	if dsn == "" {
		return nil, nil, nil, fmt.Errorf("launch: event store nats dsn is empty; set runtime.events.store.dsn or %s", dsnEnv)
	}
	events, err := natseventstore.Open(context.Background(), natseventstore.Config{
		URL:          dsn,
		Stream:       strings.TrimSpace(cfg.Stream),
		Subject:      strings.TrimSpace(cfg.Subject),
		CreateStream: cfg.CreateStream,
	}, registry)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("launch: open nats event store: %w", err)
	}
	threads, err := runtimethread.NewStore(events)
	if err != nil {
		_ = events.Close()
		return nil, nil, nil, err
	}
	return threads, events, func() { _ = events.Close() }, nil
}

func defaultEventStorePath() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if base := strings.TrimSpace(os.Getenv("LocalAppData")); base != "" {
			return filepath.Join(base, "agentruntime", "events.sqlite"), nil
		}
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("launch: resolve config dir: %w", err)
		}
		return filepath.Join(base, "agentruntime", "events.sqlite"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("launch: resolve home dir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "agentruntime", "events.sqlite"), nil
	default:
		if base := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); base != "" {
			return filepath.Join(base, "agentruntime", "events.sqlite"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("launch: resolve home dir: %w", err)
		}
		return filepath.Join(home, ".local", "state", "agentruntime", "events.sqlite"), nil
	}
}
