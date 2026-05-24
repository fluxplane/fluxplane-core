package usage

import (
	"context"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/event"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	coreusage "github.com/fluxplane/fluxplane-core/core/usage"
	"github.com/fluxplane/fluxplane-core/runtime/eventstore"
	runtimethread "github.com/fluxplane/fluxplane-core/runtime/thread"
)

func TestUsageDatasourceSearchesTokenUsageEvents(t *testing.T) {
	ctx := context.Background()
	store := newTestThreadStore(t)
	snapshot, err := store.Create(ctx, corethread.CreateParams{ID: "thread_usage", Metadata: map[string]string{"session": "coder"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = store.Append(ctx, corethread.Ref{ID: snapshot.ID, BranchID: snapshot.BranchID}, corethread.AppendRecord{Event: event.Record{
		Name: coresession.EventRuntimeEmitted,
		Payload: coresession.RuntimeEmitted{
			Name: coreusage.EventRecordedName,
			Payload: coreusage.Recorded{
				Source:  "llmagent",
				Subject: coreusage.Subject{Kind: coreusage.SubjectLLM, Provider: "codex", Name: "gpt-5.5"},
				Measurements: []coreusage.Measurement{
					{Metric: coreusage.MetricLLMInputTokens, Quantity: 1200, Unit: coreusage.UnitToken, Direction: coreusage.DirectionInput},
					{Metric: coreusage.MetricLLMOutputTokens, Quantity: 34, Unit: coreusage.UnitToken, Direction: coreusage.DirectionOutput},
					{Metric: coreusage.MetricCost, Quantity: 0.0012, Unit: coreusage.UnitCurrency, Dimensions: map[string]string{"currency": "USD"}},
				},
			},
		},
	}})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	accessor, err := provider{threads: store}.Open(ctx, DatasourceSpec())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	searcher := accessor.(coredatasource.Searcher)
	result, err := searcher.Search(ctx, coredatasource.SearchRequest{
		Entity:  EntityRecord,
		Query:   "gpt-5.5",
		Filters: map[string]string{"session": "coder", "provider": "codex"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 1 || len(result.Records) != 1 {
		t.Fatalf("records = %d total = %d, want 1", len(result.Records), result.Total)
	}
	record := result.Records[0]
	if record.Metadata["input_tokens"] != "1200" || record.Metadata["output_tokens"] != "34" || record.Metadata["total_tokens"] != "1234" {
		t.Fatalf("metadata = %#v, want token totals", record.Metadata)
	}
	if record.Metadata["cost"] != "0.0012" || record.Metadata["currency"] != "USD" {
		t.Fatalf("metadata = %#v, want cost", record.Metadata)
	}
	if !strings.HasPrefix(record.URL, "usage://thread_usage:usage.record:") {
		t.Fatalf("record URL = %q", record.URL)
	}

	getter := accessor.(coredatasource.Getter)
	got, err := getter.Get(ctx, coredatasource.GetRequest{Entity: EntityRecord, ID: record.ID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != record.ID {
		t.Fatalf("Get ID = %q, want %q", got.ID, record.ID)
	}
}

func TestUsageDatasourceListsPersistedRuntimePayloadMaps(t *testing.T) {
	ctx := context.Background()
	store := newTestThreadStore(t)
	snapshot, err := store.Create(ctx, corethread.CreateParams{ID: "thread_usage_map"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = store.Append(ctx, corethread.Ref{ID: snapshot.ID, BranchID: snapshot.BranchID}, corethread.AppendRecord{Event: event.Record{
		Name: coresession.EventRuntimeEmitted,
		Payload: coresession.RuntimeEmitted{
			Name: coreusage.EventRecordedName,
			Payload: map[string]any{
				"source":  "shell_exec",
				"subject": map[string]any{"kind": "process", "name": "go test"},
				"measurements": []map[string]any{
					{"metric": "wall_time", "quantity": 250.0, "unit": "millisecond"},
				},
			},
		},
	}})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	accessor, err := provider{threads: store}.Open(ctx, DatasourceSpec())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	lister := accessor.(coredatasource.Lister)
	result, err := lister.List(ctx, coredatasource.ListRequest{Entity: EntityRecord, Filters: map[string]string{"subject_kind": "process"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Metadata["wall_time_ms"] != "250" {
		t.Fatalf("result = %#v, want process wall time", result)
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
