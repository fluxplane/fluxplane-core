package launch

import (
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/plugins/support/eventcatalog"
)

func TestOpenLocalThreadStoreRejectsMissingNATSDSN(t *testing.T) {
	registry, err := eventregistry.New(eventregistry.Config{EventTypes: eventcatalog.All()})
	if err != nil {
		t.Fatalf("NewEventRegistry: %v", err)
	}
	t.Setenv("AGENTRUNTIME_TEST_EMPTY_NATS_DSN", "")
	_, _, _, err = openLocalThreadStore(registry, distribution.EventsConfig{
		Store: distribution.EventStoreConfig{Kind: "nats", DSNEnv: "AGENTRUNTIME_TEST_EMPTY_NATS_DSN"},
	})
	if err == nil || !strings.Contains(err.Error(), "event store nats dsn is empty") {
		t.Fatalf("openLocalThreadStore error = %v, want missing dsn", err)
	}
}

func TestOpenLocalThreadStoreRejectsUnsupportedEventStore(t *testing.T) {
	registry, err := eventregistry.New(eventregistry.Config{EventTypes: eventcatalog.All()})
	if err != nil {
		t.Fatalf("NewEventRegistry: %v", err)
	}
	_, _, _, err = openLocalThreadStore(registry, distribution.EventsConfig{
		Store: distribution.EventStoreConfig{Kind: "redis"},
	})
	if err == nil || !strings.Contains(err.Error(), `event store kind "redis" is not supported`) {
		t.Fatalf("openLocalThreadStore error = %v, want unsupported kind", err)
	}
}

func TestOpenLocalThreadStoreDefaultsToSQLite(t *testing.T) {
	registry, err := eventregistry.New(eventregistry.Config{EventTypes: eventcatalog.All()})
	if err != nil {
		t.Fatalf("NewEventRegistry: %v", err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	threads, events, closeStore, err := openLocalThreadStore(registry)
	if err != nil {
		t.Fatalf("openLocalThreadStore: %v", err)
	}
	defer closeStore()
	if threads == nil || events == nil {
		t.Fatalf("openLocalThreadStore returned nil stores")
	}
}
