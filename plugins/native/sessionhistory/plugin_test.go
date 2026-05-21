package sessionhistory

import (
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/engine/core/channel"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	coresession "github.com/fluxplane/engine/core/session"
	corethread "github.com/fluxplane/engine/core/thread"
	"github.com/fluxplane/engine/runtime/eventstore"
	runtimethread "github.com/fluxplane/engine/runtime/thread"
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
	if !strings.HasPrefix(record.URL, "session://thread_test/session.operation/") {
		t.Fatalf("record URL = %q", record.URL)
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

func TestCorpusSessionHistoryMessages(t *testing.T) {
	ctx := context.Background()
	store := newTestThreadStore(t)
	snapshot, err := store.Create(ctx, corethread.CreateParams{ID: "thread_corpus"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = store.Append(ctx, corethread.Ref{ID: snapshot.ID, BranchID: snapshot.BranchID},
		corethread.AppendRecord{Event: event.Record{
			Name: coresession.EventInputReceived,
			Payload: coresession.InputReceived{
				Message: channel.Message{Content: "first corpus marker"},
			},
		}},
		corethread.AppendRecord{Event: event.Record{
			Name: coresession.EventInputReceived,
			Payload: coresession.InputReceived{
				Message: channel.Message{Content: "second corpus marker"},
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
	corpus := accessor.(coredatasource.CorpusProvider)
	first, err := corpus.Corpus(ctx, coredatasource.CorpusRequest{Entity: EntityMessage, Limit: 1})
	if err != nil {
		t.Fatalf("Corpus first: %v", err)
	}
	if len(first.Documents) != 1 || first.NextCursor == "" || first.Complete {
		t.Fatalf("first page = %#v", first)
	}
	doc := first.Documents[0]
	if doc.Ref.Datasource != DatasourceName || doc.Ref.Entity != EntityMessage || doc.Ref.ID == "" {
		t.Fatalf("doc ref = %#v", doc.Ref)
	}
	if !strings.HasPrefix(doc.URL, "session://thread_corpus/session.message/") || doc.URL != doc.Ref.URL {
		t.Fatalf("doc URL = %q ref URL = %q", doc.URL, doc.Ref.URL)
	}
	if !strings.Contains(doc.Body, "corpus marker") || doc.Fingerprint == "" {
		t.Fatalf("doc = %#v", doc)
	}
	if len(doc.Chunks) == 0 || !strings.Contains(doc.Chunks[0].Text, "corpus marker") {
		t.Fatalf("doc chunks = %#v", doc.Chunks)
	}

	second, err := corpus.Corpus(ctx, coredatasource.CorpusRequest{Entity: EntityMessage, Cursor: first.NextCursor, Limit: 1})
	if err != nil {
		t.Fatalf("Corpus second: %v", err)
	}
	if len(second.Documents) != 1 || second.NextCursor != "" || !second.Complete {
		t.Fatalf("second page = %#v", second)
	}
}

func TestCorpusChunksLongSessionHistoryRecords(t *testing.T) {
	record := coredatasource.Record{
		ID:         "thread_test:session.message:1",
		Datasource: DatasourceName,
		Entity:     EntityMessage,
		Title:      "long message",
		Content:    strings.Repeat("alpha beta gamma delta ", 200),
		URL:        "session://thread_test/session.message/1",
		Metadata:   map[string]string{"thread_id": "thread_test"},
	}

	doc := corpusDocument(record)

	if len(doc.Chunks) < 2 {
		t.Fatalf("chunks len = %d, want multiple bounded chunks", len(doc.Chunks))
	}
	for _, chunk := range doc.Chunks {
		if len([]rune(chunk.Text)) > corpusChunkChars {
			t.Fatalf("chunk length = %d, want <= %d", len([]rune(chunk.Text)), corpusChunkChars)
		}
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
