package sqlstore

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	_ "github.com/go-sql-driver/mysql"
	tc_mysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	_ "modernc.org/sqlite"
)

func TestSQLiteStoreImplementsCoreDataStore(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := OpenDB(ctx, db, DialectSQLite)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if _, err := OpenDB(ctx, db, DialectSQLite); err != nil {
		t.Fatalf("OpenDB second time: %v", err)
	}
	exerciseStore(t, ctx, store)
}

func TestMySQLStoreImplementsCoreDataStore(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") != "1" {
		t.Skip("set TEST_INTEGRATION=1 to run MySQL testcontainers data store test")
	}
	ctx := context.Background()
	container, err := tc_mysql.Run(ctx, "mysql:8.0.36",
		tc_mysql.WithDatabase("fluxplane"),
		tc_mysql.WithUsername("test"),
		tc_mysql.WithPassword("test"),
	)
	if err != nil {
		t.Fatalf("mysql container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "parseTime=true")
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := OpenDB(ctx, db, DialectMySQL)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if _, err := OpenDB(ctx, db, DialectMySQL); err != nil {
		t.Fatalf("OpenDB second time: %v", err)
	}
	exerciseStore(t, ctx, store)
}

func exerciseStore(t *testing.T, ctx context.Context, store coredata.Store) {
	t.Helper()
	userA := coredata.Record{
		Ref:     coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user_with_groups", ID: "42"},
		Scope:   coredata.Scope{WorkspaceID: "workspace-a", SessionID: "session-a"},
		Title:   "Ada",
		Content: "Platform engineer",
		URL:     "https://gitlab.example/ada",
		Fields: map[string][]string{
			"username": {"ada"},
			"email":    {"ada@example.test"},
		},
		Relations: map[string][]coredata.Summary{
			"groups": {{
				Ref:   coredata.Ref{Source: "gitlab", Entity: "gitlab.group", ID: "abc"},
				Title: "ABC",
				Fields: map[string]string{
					"path": "abc",
					"name": "ABC",
				},
			}},
		},
		Raw:      map[string]any{"id": float64(42), "username": "ada"},
		Metadata: map[string]string{"source_url": "https://gitlab.example/api/v4/users/42"},
	}
	userB := userA
	userB.Scope = coredata.Scope{WorkspaceID: "workspace-a", SessionID: "session-b"}
	userB.Title = "Ada B"
	userB.Fields = map[string][]string{"username": {"ada-b"}}
	userB.Relations = map[string][]coredata.Summary{
		"groups": {{
			Ref:    coredata.Ref{Source: "gitlab", Entity: "gitlab.group", ID: "def"},
			Title:  "DEF",
			Fields: map[string]string{"path": "def"},
		}},
	}
	channel := coredata.Record{
		Ref:     coredata.Ref{Source: "slack", Entity: "slack.channel", View: "slack.channel_with_members", ID: "C123"},
		Scope:   coredata.Scope{WorkspaceID: "workspace-a"},
		Title:   "platform",
		Content: "Platform channel",
		Fields:  map[string][]string{"name": {"platform"}},
		Relations: map[string][]coredata.Summary{
			"members": {{
				Ref:   coredata.Ref{Source: "slack", Entity: "slack.user", ID: "U123"},
				Title: "Ada Slack",
				Fields: map[string]string{
					"id":   "U123",
					"name": "ada",
				},
			}},
		},
	}
	if err := store.UpsertRecords(ctx, userA, userB, channel); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}

	got, ok, err := store.GetRecord(ctx, coredata.Scope{SessionID: "session-b"}, userB.Ref)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if !ok || got.Title != "Ada B" {
		t.Fatalf("GetRecord = %#v ok=%v, want scoped session-b record", got, ok)
	}

	result, err := store.QueryRecords(ctx, coredata.Query{
		Scope:   coredata.Scope{WorkspaceID: "workspace-a"},
		Sources: []coredata.SourceName{"gitlab"},
		Views:   []coredata.ViewName{"gitlab.user_with_groups"},
		Text:    "platform",
		Filters: map[string]string{"username": "ada"},
		RelationFilters: []coredata.RelationFilter{{
			Relation: "groups",
			Target:   "gitlab.group",
			Filters:  map[string]string{"path": "abc"},
		}},
	})
	if err != nil {
		t.Fatalf("QueryRecords: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Ref.ID != "42" || result.Records[0].Title != "Ada" {
		t.Fatalf("records = %#v, want Ada in group abc", result.Records)
	}

	slackResult, err := store.QueryRecords(ctx, coredata.Query{
		Sources: []coredata.SourceName{"slack"},
		Views:   []coredata.ViewName{"slack.channel_with_members"},
		RelationFilters: []coredata.RelationFilter{{
			Relation: "members",
			Target:   "slack.user",
			Filters:  map[string]string{"id": "U123"},
		}},
	})
	if err != nil {
		t.Fatalf("QueryRecords slack: %v", err)
	}
	if len(slackResult.Records) != 1 || slackResult.Records[0].Ref.ID != "C123" {
		t.Fatalf("slack records = %#v, want channel C123", slackResult.Records)
	}

	longPrefix := strings.Repeat("x", 210)
	longA := userA
	longA.Ref = coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user", ID: "long-a"}
	longA.Scope = coredata.Scope{}
	longA.Fields = map[string][]string{"external_url": {longPrefix + "a"}}
	longB := longA
	longB.Ref.ID = "long-b"
	longB.Fields = map[string][]string{"external_url": {longPrefix + "b"}}
	if err := store.UpsertRecords(ctx, longA, longB); err != nil {
		t.Fatalf("UpsertRecords long filters: %v", err)
	}
	longResult, err := store.QueryRecords(ctx, coredata.Query{
		Sources: []coredata.SourceName{"gitlab"},
		Filters: map[string]string{"external_url": longPrefix + "b"},
	})
	if err != nil {
		t.Fatalf("QueryRecords long filter: %v", err)
	}
	if len(longResult.Records) != 1 || longResult.Records[0].Ref.ID != "long-b" {
		t.Fatalf("long filter records = %#v, want exact long-b match", longResult.Records)
	}

	relation := coredata.Relation{
		Source:  channel.Ref,
		Name:    "members",
		Target:  coredata.Ref{Source: "slack", Entity: "slack.user", ID: "U123"},
		Scope:   coredata.Scope{WorkspaceID: "workspace-a"},
		Summary: coredata.Summary{Title: "Ada Slack", Fields: map[string]string{"name": "ada"}},
	}
	if err := store.UpsertRelations(ctx, relation); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	relations, err := store.QueryRelations(ctx, coredata.RelationQuery{
		Scope:    coredata.Scope{WorkspaceID: "workspace-a"},
		Relation: "members",
		Source:   channel.Ref,
		Target:   coredata.Ref{Source: "slack", Entity: "slack.user"},
	})
	if err != nil {
		t.Fatalf("QueryRelations: %v", err)
	}
	if len(relations.Relations) != 1 || relations.Relations[0].Target.ID != "U123" {
		t.Fatalf("relations = %#v, want U123", relations.Relations)
	}

	refA, err := store.PutBlob(ctx, coredata.Blob{
		Ref:     coredata.BlobRef{Scope: coredata.Scope{UserID: "user-a"}, Metadata: map[string]string{"owner": "a"}},
		Content: []byte("artifact"),
	})
	if err != nil {
		t.Fatalf("PutBlob a: %v", err)
	}
	refB, err := store.PutBlob(ctx, coredata.Blob{
		Ref:     coredata.BlobRef{Scope: coredata.Scope{UserID: "user-b"}, Metadata: map[string]string{"owner": "b"}},
		Content: []byte("artifact"),
	})
	if err != nil {
		t.Fatalf("PutBlob b: %v", err)
	}
	if refA.ID == "" || refA.ID != refB.ID || refA.Size != int64(len("artifact")) || !strings.HasPrefix(refA.Digest, "sha256:") {
		t.Fatalf("blob refs = %#v %#v, want shared content id with size and digest", refA, refB)
	}
	blob, ok, err := store.GetBlob(ctx, coredata.BlobRef{ID: refA.ID, Scope: coredata.Scope{UserID: "user-a"}})
	if err != nil {
		t.Fatalf("GetBlob: %v", err)
	}
	if !ok || string(blob.Content) != "artifact" || blob.Ref.Metadata["owner"] != "a" {
		t.Fatalf("blob = %#v ok=%v, want user-a artifact", blob, ok)
	}

	if err := store.DeleteRecords(ctx, coredata.Scope{SessionID: "session-a"}, userA.Ref); err != nil {
		t.Fatalf("DeleteRecords: %v", err)
	}
	if _, ok, err := store.GetRecord(ctx, coredata.Scope{SessionID: "session-a"}, userA.Ref); err != nil || ok {
		t.Fatalf("GetRecord deleted ok=%v err=%v, want missing", ok, err)
	}
	if _, ok, err := store.GetRecord(ctx, coredata.Scope{SessionID: "session-b"}, userB.Ref); err != nil || !ok {
		t.Fatalf("GetRecord other scope ok=%v err=%v, want present", ok, err)
	}
}
