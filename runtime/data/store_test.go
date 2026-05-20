package data

import (
	"context"
	"strings"
	"testing"

	coredata "github.com/fluxplane/agentruntime/core/data"
	"github.com/fluxplane/agentruntime/core/thread"
)

func TestMemoryStoreQueriesEmbeddedRelationSummaries(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	record := coredata.Record{
		Ref: coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user_with_groups", ID: "42"},
		Scope: coredata.Scope{
			TenantID: "tenant-a",
			AppID:    "app-a",
		},
		Title: "Ada",
		Fields: map[string][]string{
			"username": []string{"ada"},
		},
		Relations: map[string][]coredata.Summary{
			"groups": {{
				Ref:   coredata.Ref{Source: "gitlab", Entity: "gitlab.group", ID: "10"},
				Title: "ABC",
				Fields: map[string]string{
					"path": "abc",
					"name": "ABC",
				},
			}},
		},
	}
	if err := store.UpsertRecords(ctx, record); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}
	result, err := store.QueryRecords(ctx, coredata.Query{
		Scope:   coredata.Scope{TenantID: "tenant-a"},
		Sources: []coredata.SourceName{"gitlab"},
		Views:   []coredata.ViewName{"gitlab.user_with_groups"},
		RelationFilters: []coredata.RelationFilter{{
			Relation: "groups",
			Target:   "gitlab.group",
			Filters:  map[string]string{"path": "abc"},
		}},
	})
	if err != nil {
		t.Fatalf("QueryRecords: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Ref.ID != "42" {
		t.Fatalf("records = %#v, want user 42", result.Records)
	}
}

func TestMemoryStoreScopesRecords(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.UpsertRecords(ctx,
		coredata.Record{Ref: coredata.Ref{Source: "memory", Entity: "memory.item", ID: "user"}, Scope: coredata.Scope{UserID: "user-a"}, Title: "User memory"},
		coredata.Record{Ref: coredata.Ref{Source: "memory", Entity: "memory.item", ID: "session"}, Scope: coredata.Scope{SessionID: "session-a"}, Title: "Session memory"},
	); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}
	result, err := store.QueryRecords(ctx, coredata.Query{Scope: coredata.Scope{UserID: "user-a"}})
	if err != nil {
		t.Fatalf("QueryRecords: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Ref.ID != "user" {
		t.Fatalf("records = %#v, want user-scoped memory only", result.Records)
	}
	batch, err := store.BatchGetRecords(ctx, coredata.Scope{},
		coredata.Ref{Source: "memory", Entity: "memory.item", ID: "session"},
		coredata.Ref{Source: "memory", Entity: "memory.item", ID: "missing"},
		coredata.Ref{Source: "memory", Entity: "memory.item", ID: "user"},
	)
	if err != nil {
		t.Fatalf("BatchGetRecords: %v", err)
	}
	if len(batch) != 2 || batch[0].Ref.ID != "session" || batch[1].Ref.ID != "user" {
		t.Fatalf("batch = %#v, want existing records in request order", batch)
	}
	if err := store.UpsertRecords(ctx,
		coredata.Record{Ref: coredata.Ref{Source: "memory", Entity: "memory.item", ID: "shared"}, Scope: coredata.Scope{SessionID: "session-a"}, Title: "A"},
		coredata.Record{Ref: coredata.Ref{Source: "memory", Entity: "memory.item", ID: "shared"}, Scope: coredata.Scope{SessionID: "session-b"}, Title: "B"},
	); err != nil {
		t.Fatalf("UpsertRecords scoped duplicate refs: %v", err)
	}
	got, ok, err := store.GetRecord(ctx, coredata.Scope{SessionID: "session-b"}, coredata.Ref{Source: "memory", Entity: "memory.item", ID: "shared"})
	if err != nil {
		t.Fatalf("GetRecord scoped duplicate: %v", err)
	}
	if !ok || got.Title != "B" {
		t.Fatalf("GetRecord scoped duplicate = %#v ok=%v, want B", got, ok)
	}
	if err := store.UpsertRecords(ctx,
		coredata.Record{Ref: coredata.Ref{Source: "memory", Entity: "memory.item", ID: "thread-shared"}, Scope: coredata.Scope{ThreadID: thread.ID("thread-a")}, Title: "Thread A"},
		coredata.Record{Ref: coredata.Ref{Source: "memory", Entity: "memory.item", ID: "thread-shared"}, Scope: coredata.Scope{ThreadID: thread.ID("thread-b")}, Title: "Thread B"},
	); err != nil {
		t.Fatalf("UpsertRecords thread scoped duplicate refs: %v", err)
	}
	got, ok, err = store.GetRecord(ctx, coredata.Scope{ThreadID: thread.ID("thread-b")}, coredata.Ref{Source: "memory", Entity: "memory.item", ID: "thread-shared"})
	if err != nil {
		t.Fatalf("GetRecord thread scoped duplicate: %v", err)
	}
	if !ok || got.Title != "Thread B" {
		t.Fatalf("GetRecord thread scoped duplicate = %#v ok=%v, want Thread B", got, ok)
	}
}

func TestMemoryStoreStoresRelationsAndBlobs(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	relation := coredata.Relation{
		Source: coredata.Ref{Source: "slack", Entity: "slack.channel", View: "slack.channel_with_members", ID: "C123"},
		Name:   "members",
		Target: coredata.Ref{Source: "slack", Entity: "slack.user", ID: "U123"},
		Scope:  coredata.Scope{AppID: "app-a"},
	}
	if err := store.UpsertRelations(ctx, relation); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	relations, err := store.QueryRelations(ctx, coredata.RelationQuery{Scope: coredata.Scope{AppID: "app-a"}, Relation: "members"})
	if err != nil {
		t.Fatalf("QueryRelations: %v", err)
	}
	if len(relations.Relations) != 1 || relations.Relations[0].Target.ID != "U123" {
		t.Fatalf("relations = %#v, want U123", relations.Relations)
	}
	ref, err := store.PutBlob(ctx, coredata.Blob{Content: []byte("artifact")})
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	if ref.ID == "" || ref.Size != int64(len("artifact")) || !strings.HasPrefix(ref.Digest, "sha256:") {
		t.Fatalf("blob ref = %#v, want id, size, digest", ref)
	}
	blob, ok, err := store.GetBlob(ctx, ref)
	if err != nil {
		t.Fatalf("GetBlob: %v", err)
	}
	if !ok || string(blob.Content) != "artifact" {
		t.Fatalf("blob = %#v ok=%v, want content", blob, ok)
	}
}
