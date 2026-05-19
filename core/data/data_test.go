package data

import (
	"context"
	"testing"
)

func TestSourceSpecValidateRejectsDuplicateViews(t *testing.T) {
	spec := SourceSpec{
		Name: "gitlab",
		Entities: []EntitySpec{{
			Type: "gitlab.user",
		}},
		Views: []ViewSpec{
			{Name: "gitlab.user", Source: "gitlab.user"},
			{Name: "gitlab.user", Source: "gitlab.user"},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want duplicate view error")
	}
}

func TestScopeMatchesSelector(t *testing.T) {
	scope := Scope{TenantID: "tenant-a", AppID: "app-a", SessionID: "session-a"}
	if !scope.Matches(Scope{TenantID: "tenant-a", AppID: "app-a"}) {
		t.Fatal("Matches: want true for matching selector")
	}
	if scope.Matches(Scope{TenantID: "tenant-b"}) {
		t.Fatal("Matches: want false for mismatched tenant")
	}
}

func TestStoreInterfaceShape(t *testing.T) {
	var _ Store = noopStore{}
}

type noopStore struct{}

func (noopStore) UpsertRecords(context.Context, ...Record) error { return nil }
func (noopStore) DeleteRecords(context.Context, Scope, ...Ref) error {
	return nil
}
func (noopStore) GetRecord(context.Context, Scope, Ref) (Record, bool, error) {
	return Record{}, false, nil
}
func (noopStore) BatchGetRecords(context.Context, Scope, ...Ref) ([]Record, error) {
	return nil, nil
}
func (noopStore) QueryRecords(context.Context, Query) (QueryResult, error) {
	return QueryResult{}, nil
}
func (noopStore) UpsertRelations(context.Context, ...Relation) error { return nil }
func (noopStore) QueryRelations(context.Context, RelationQuery) (RelationResult, error) {
	return RelationResult{}, nil
}
func (noopStore) PutBlob(context.Context, Blob) (BlobRef, error) { return BlobRef{}, nil }
func (noopStore) GetBlob(context.Context, BlobRef) (Blob, bool, error) {
	return Blob{}, false, nil
}
