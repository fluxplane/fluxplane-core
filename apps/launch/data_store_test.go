package launch

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	runtimedata "github.com/fluxplane/fluxplane-core/runtime/data"
)

func TestOpenDataStoreDefaultsToMemory(t *testing.T) {
	store, closeStore, err := openDataStore(context.Background(), distribution.DataConfig{})
	if err != nil {
		t.Fatalf("openDataStore: %v", err)
	}
	if closeStore != nil {
		t.Fatalf("closeStore = %T, want nil for memory store", closeStore)
	}
	if _, ok := store.(*runtimedata.MemoryStore); !ok {
		t.Fatalf("store = %T, want runtime data memory store", store)
	}
}

func TestOpenDataStoreRejectsMissingMySQLDSN(t *testing.T) {
	_, _, err := openDataStore(context.Background(), distribution.DataConfig{
		Store: distribution.DataStoreConfig{Kind: "mysql", DSNEnv: "FLUXPLANE_TEST_EMPTY_DSN"},
	})
	if err == nil {
		t.Fatal("openDataStore succeeded, want missing DSN error")
	}
}
