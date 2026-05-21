package semantic

import (
	"context"
	"errors"
	"testing"

	coredatasource "github.com/fluxplane/engine/core/datasource"
)

func TestSearchFieldIndexRequiresConfiguredIndex(t *testing.T) {
	_, err := SearchFieldIndex(context.Background(), FieldLookupRequest{Datasource: "docs", Entity: "doc"})
	if !errors.Is(err, ErrFieldIndexNotConfigured) {
		t.Fatalf("SearchFieldIndex error = %v, want ErrFieldIndexNotConfigured", err)
	}
}

func TestSearchFieldIndexRequiresBuiltIndex(t *testing.T) {
	index, err := New(HashEmbedder{}, NewJSONStore(""), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = SearchFieldIndex(context.Background(), FieldLookupRequest{Index: index, Datasource: "docs", Entity: "doc"})
	if !errors.Is(err, ErrFieldIndexNotBuilt) {
		t.Fatalf("SearchFieldIndex error = %v, want ErrFieldIndexNotBuilt", err)
	}
}

func TestSearchFieldIndexFiltersAndPaginatesFieldRecords(t *testing.T) {
	ctx := context.Background()
	index, err := New(HashEmbedder{}, NewJSONStore(""), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	entity := testLookupEntitySpec()
	for _, doc := range []coredatasource.CorpusDocument{
		testLookupDoc("1", "One", "42", "Namespace"),
		testLookupDoc("2", "Two", "42", "Project"),
		testLookupDoc("3", "Three", "42", "Project"),
		testLookupDoc("4", "Four", "99", "Project"),
	} {
		if _, err := index.UpdateRecord(ctx, doc, entity); err != nil {
			t.Fatalf("UpdateRecord: %v", err)
		}
	}
	first, err := SearchFieldIndex(ctx, FieldLookupRequest{
		Index:      index,
		Datasource: "gitlab",
		Entity:     "gitlab.user_membership",
		Filters:    map[string]string{"user_id": "42", "source_type": "Project"},
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("SearchFieldIndex first: %v", err)
	}
	if len(first.Records) != 1 || first.NextCursor == "" || first.Complete {
		t.Fatalf("first = %#v, want one record and next cursor", first)
	}
	second, err := SearchFieldIndex(ctx, FieldLookupRequest{
		Index:      index,
		Datasource: "gitlab",
		Entity:     "gitlab.user_membership",
		Filters:    map[string]string{"user_id": "42", "source_type": "Project"},
		Limit:      1,
		Cursor:     first.NextCursor,
	})
	if err != nil {
		t.Fatalf("SearchFieldIndex second: %v", err)
	}
	if len(second.Records) != 1 || second.Records[0].ID == first.Records[0].ID || !second.Complete {
		t.Fatalf("second = %#v, want final different record", second)
	}
}

func TestSearchFieldIndexReturnsCompleteWhenPageExactlyFull(t *testing.T) {
	ctx := context.Background()
	index, err := New(HashEmbedder{}, NewJSONStore(""), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := index.UpdateRecord(ctx, testLookupDoc("1", "One", "42", "Namespace"), testLookupEntitySpec()); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	result, err := SearchFieldIndex(ctx, FieldLookupRequest{
		Index:      index,
		Datasource: "gitlab",
		Entity:     "gitlab.user_membership",
		Filters:    map[string]string{"user_id": "42"},
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("SearchFieldIndex: %v", err)
	}
	if len(result.Records) != 1 || result.NextCursor != "" || !result.Complete {
		t.Fatalf("result = %#v, want exactly one complete page", result)
	}
}

func TestGetFieldRecordReturnsExactRecord(t *testing.T) {
	ctx := context.Background()
	index, err := New(HashEmbedder{}, NewJSONStore(""), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, doc := range []coredatasource.CorpusDocument{
		testLookupDoc("42:project:1", "One", "42", "Project"),
		testLookupDoc("42:project:12", "Runtime", "42", "Project"),
		testLookupDoc("42:project:123", "Three", "42", "Project"),
	} {
		if _, err := index.UpdateRecord(ctx, doc, testLookupEntitySpec()); err != nil {
			t.Fatalf("UpdateRecord: %v", err)
		}
	}
	record, err := GetFieldRecord(ctx, index, "gitlab", "gitlab.user_membership", "42:project:12")
	if err != nil {
		t.Fatalf("GetFieldRecord: %v", err)
	}
	if record.ID != "42:project:12" || record.Metadata["source_type"] != "Project" {
		t.Fatalf("record = %#v, want exact indexed membership", record)
	}
}

func testLookupEntitySpec() coredatasource.EntitySpec {
	return coredatasource.EntitySpec{
		Type: "gitlab.user_membership",
		Fields: []coredatasource.FieldSpec{
			{Name: "id", Identifier: true},
			{Name: "title", Searchable: true},
			{Name: "user_id", Filterable: true},
			{Name: "source_type", Filterable: true},
		},
	}
}

func testLookupDoc(id, title, userID, sourceType string) coredatasource.CorpusDocument {
	return coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user_membership", ID: id},
		Title: title,
		Metadata: map[string]string{
			"id":          id,
			"user_id":     userID,
			"source_type": sourceType,
		},
	}
}
