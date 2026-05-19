package mirror

import (
	"context"
	"errors"
	"testing"
	"time"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
)

func TestServiceUpdatesAndSearchesStructuredRecords(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	service, err := New(store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	entity := coredatasource.EntitySpec{
		Type: "gitlab.user",
		Fields: []coredatasource.FieldSpec{
			{Name: "username", Searchable: true, Filterable: true, Identifier: true},
			{Name: "group_path", Searchable: true, Filterable: true},
		},
	}
	doc := coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user", ID: "42"},
		Title: "Ada Lovelace",
		Body:  "Platform engineer",
		URL:   "https://gitlab.example/ada",
		Metadata: map[string]string{
			"username":   "ada",
			"group_path": "abc",
		},
	}
	if _, err := service.UpdateRecord(ctx, doc, entity); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	batchResults, err := service.UpdateRecords(ctx, []coredatasource.CorpusDocument{{
		Ref:   coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user", ID: "43"},
		Title: "Grace Hopper",
		Metadata: map[string]string{
			"username":   "grace",
			"group_path": "abc",
		},
	}}, entity)
	if err != nil {
		t.Fatalf("UpdateRecords: %v", err)
	}
	if len(batchResults) != 1 || batchResults[0].Key == "" || batchResults[0].Status != "indexed" {
		t.Fatalf("batchResults = %#v, want indexed result", batchResults)
	}
	if err := service.PutRun(ctx, RunState{Datasource: "gitlab", Entity: "gitlab.user", Phase: "fields", Status: RunStatusComplete, CompletedAt: time.Now()}); err != nil {
		t.Fatalf("PutRun: %v", err)
	}
	result, err := Search(ctx, LookupRequest{Service: service, Datasource: "gitlab", Entity: "gitlab.user", Query: "ada", Filters: map[string]string{"group_path": "abc"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "42" || result.Records[0].Metadata["username"] != "ada" {
		t.Fatalf("records = %#v, want mirrored Ada record", result.Records)
	}
	record, err := Get(ctx, service, "gitlab", "gitlab.user", "42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.Title != "Ada Lovelace" || record.URL == "" {
		t.Fatalf("record = %#v, want hydrated title and URL", record)
	}
}

func TestRequireBuiltChecksRecordsOrCompletedRun(t *testing.T) {
	ctx := context.Background()
	service, err := New(newMemoryStore())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = RequireBuilt(ctx, service, "gitlab", "gitlab.user")
	if !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("RequireBuilt empty = %v, want ErrNotBuilt", err)
	}
	if err := service.PutRun(ctx, RunState{Datasource: "gitlab", Entity: "gitlab.user", Phase: "fields", Status: RunStatusComplete}); err != nil {
		t.Fatalf("PutRun: %v", err)
	}
	if err := RequireBuilt(ctx, service, "gitlab", "gitlab.user"); err != nil {
		t.Fatalf("RequireBuilt completed run: %v", err)
	}
}

type memoryStore struct {
	records map[string]Record
	runs    map[string]RunState
}

func newMemoryStore() *memoryStore {
	return &memoryStore{records: map[string]Record{}, runs: map[string]RunState{}}
}

func (s *memoryStore) UpsertRecord(_ context.Context, record Record) error {
	s.records[record.Key] = record
	return nil
}

func (s *memoryStore) UpsertRecords(_ context.Context, records ...Record) error {
	for _, record := range records {
		s.records[record.Key] = record
	}
	return nil
}

func (s *memoryStore) DeleteRecord(_ context.Context, ref coredatasource.RecordRef) error {
	delete(s.records, DocumentKey(ref))
	return nil
}

func (s *memoryStore) Record(_ context.Context, ref coredatasource.RecordRef) (Record, bool, error) {
	record, ok := s.records[DocumentKey(ref)]
	return record, ok, nil
}

func (s *memoryStore) SearchRecords(ctx context.Context, req SearchRequest) ([]Hit, error) {
	return SearchRecords(ctx, s.records, req)
}

func (s *memoryStore) RecordStatus(ctx context.Context, req StatusRequest) ([]RecordState, error) {
	var out []RecordState
	for _, record := range s.records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Datasource != "" && record.Ref.Datasource != req.Datasource {
			continue
		}
		if req.Entity != "" && record.Ref.Entity != req.Entity {
			continue
		}
		out = append(out, RecordState{Key: record.Key, Ref: record.Ref})
	}
	return out, nil
}

func (s *memoryStore) PutRun(_ context.Context, run RunState) error {
	run.Key = RunStorageKey(RunKey{Datasource: run.Datasource, Entity: run.Entity, Phase: run.Phase})
	s.runs[run.Key] = run
	return nil
}

func (s *memoryStore) Run(_ context.Context, key RunKey) (RunState, bool, error) {
	run, ok := s.runs[RunStorageKey(key)]
	return run, ok, nil
}

func (s *memoryStore) Runs(ctx context.Context, req StatusRequest) ([]RunState, error) {
	var out []RunState
	for _, run := range s.runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Datasource != "" && run.Datasource != req.Datasource {
			continue
		}
		if req.Entity != "" && run.Entity != req.Entity {
			continue
		}
		out = append(out, run)
	}
	return out, nil
}

func (s *memoryStore) DeleteRuns(ctx context.Context, req StatusRequest) error {
	for key, run := range s.runs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if req.Datasource != "" && run.Datasource != req.Datasource {
			continue
		}
		if req.Entity != "" && run.Entity != req.Entity {
			continue
		}
		delete(s.runs, key)
	}
	return nil
}
