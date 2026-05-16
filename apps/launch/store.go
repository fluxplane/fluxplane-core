package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/sqleventstore"
	"github.com/fluxplane/agentruntime/core/event"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

func openLocalThreadStore(registry *event.Registry) (corethread.Store, event.Store, func(), error) {
	path, err := defaultEventStorePath()
	if err != nil {
		return nil, nil, nil, err
	}
	events, err := sqleventstore.Open(path, registry)
	if err != nil {
		return nil, nil, nil, err
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
