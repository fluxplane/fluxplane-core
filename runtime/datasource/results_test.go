package datasource

import (
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
)

func TestSelectEntitiesPreservesOrder(t *testing.T) {
	available := []coredatasource.EntitySpec{
		{Type: "doc"},
		{Type: "user"},
	}
	selected, err := SelectEntities("test", available, []coredatasource.EntityType{"user", "doc"})
	if err != nil {
		t.Fatalf("SelectEntities: %v", err)
	}
	if len(selected) != 2 || selected[0].Type != "user" || selected[1].Type != "doc" {
		t.Fatalf("selected = %#v, want requested order", selected)
	}
}

func TestSelectEntitiesReportsProviderEntity(t *testing.T) {
	_, err := SelectEntities("test", []coredatasource.EntitySpec{{Type: "doc"}}, []coredatasource.EntityType{"missing"})
	if err == nil || !strings.Contains(err.Error(), `unsupported test datasource entity "missing"`) {
		t.Fatalf("SelectEntities error = %v, want provider scoped unsupported entity", err)
	}
}

func TestRecordHelpers(t *testing.T) {
	values := []string{"one", "", "two"}
	records := NonEmptyRecordsFrom(values, func(value string) coredatasource.Record {
		return coredatasource.Record{ID: value}
	})
	if len(records) != 2 || records[0].ID != "one" || records[1].ID != "two" {
		t.Fatalf("records = %#v, want non-empty IDs only", records)
	}
	all := RecordsFrom(values, func(value string) coredatasource.Record {
		return coredatasource.Record{ID: value}
	})
	if len(all) != 3 {
		t.Fatalf("all records len = %d, want 3", len(all))
	}
}

func TestResultCursors(t *testing.T) {
	if next := PageNextCursor(20, 20, 1); next != "2" {
		t.Fatalf("PageNextCursor = %q, want 2", next)
	}
	if next := PageNextCursor(19, 20, 1); next != "" {
		t.Fatalf("PageNextCursor = %q, want empty final page", next)
	}
	if next := OffsetNextCursor(20, 20, 45, 20); next != "40" {
		t.Fatalf("OffsetNextCursor = %q, want 40", next)
	}
	if next := OffsetNextCursor(40, 5, 45, 20); next != "" {
		t.Fatalf("OffsetNextCursor = %q, want empty final page", next)
	}
}

func TestResultConstructorsSetCompleteness(t *testing.T) {
	records := []coredatasource.Record{{ID: "one"}}
	list := ListResult("docs", "doc", records, 10, "2")
	if list.Complete || list.NextCursor != "2" || list.Total != 10 {
		t.Fatalf("list = %#v, want incomplete with total", list)
	}
	req := coredatasource.RelationRequest{Entity: "user", ID: "42", Relation: "docs"}
	relation := RelationResult("docs", req, "doc", records, -1, "", true)
	if !relation.Complete || relation.Total != 1 || !relation.Exact {
		t.Fatalf("relation = %#v, want complete exact default total", relation)
	}
}

func TestRecordsToCorpusDocuments(t *testing.T) {
	records := []coredatasource.Record{{
		ID:         "one",
		Datasource: "docs",
		Entity:     "doc",
		Title:      "One",
		Content:    "Body",
		URL:        "https://example.test/one",
		Metadata:   map[string]string{"kind": "test"},
	}}
	docs := RecordsToCorpusDocuments(records)
	if len(docs) != 1 {
		t.Fatalf("documents len = %d, want 1", len(docs))
	}
	doc := docs[0]
	if doc.Ref.ID != "one" || doc.Title != "One" || doc.Body != "Body" || doc.URL != records[0].URL || doc.Metadata["kind"] != "test" {
		t.Fatalf("document = %#v, want record fields preserved", doc)
	}
}
