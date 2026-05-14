package sessionhistoryplugin

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/channel"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

func TestSearchSessionHistoryOperations(t *testing.T) {
	ctx := context.Background()
	store := newTestThreadStore(t)
	snapshot, err := store.Create(ctx, corethread.CreateParams{ID: "thread_test", Metadata: map[string]string{"session": "coder"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = store.Append(ctx, corethread.Ref{ID: snapshot.ID, BranchID: snapshot.BranchID},
		corethread.AppendRecord{Event: event.Record{
			Name: coresession.EventInputReceived,
			Payload: coresession.InputReceived{
				Message: channel.Message{Content: "please run tests"},
			},
		}},
		corethread.AppendRecord{Event: event.Record{
			Name: coresession.EventOperationCompleted,
			Payload: coresession.OperationCompleted{
				Operation: operation.Ref{Name: "go_test"},
				Result:    operation.OK("ok"),
			},
		}},
	)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	accessor, err := provider{threads: store}.Open(ctx, DatasourceSpec())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	searcher := accessor.(coredatasource.Searcher)
	result, err := searcher.Search(ctx, coredatasource.SearchRequest{
		Entity:  EntityOperation,
		Query:   "go_test",
		Filters: map[string]string{"session": "coder", "status": "ok"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 1 || len(result.Records) != 1 {
		t.Fatalf("records = %d total = %d, want 1", len(result.Records), result.Total)
	}
	record := result.Records[0]
	if record.Metadata["operation"] != "go_test" {
		t.Fatalf("operation metadata = %q", record.Metadata["operation"])
	}

	getter := accessor.(coredatasource.Getter)
	got, err := getter.Get(ctx, coredatasource.GetRequest{Entity: EntityOperation, ID: record.ID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != record.ID {
		t.Fatalf("Get ID = %q, want %q", got.ID, record.ID)
	}
}

func newTestThreadStore(t *testing.T) corethread.Store {
	t.Helper()
	store, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}
