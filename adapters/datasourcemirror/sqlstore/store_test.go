package sqlstore

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/runtime/datasource/mirror"
	_ "github.com/go-sql-driver/mysql"
	tc_mysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	_ "modernc.org/sqlite"
)

func TestSQLiteStoreImplementsMirrorStore(t *testing.T) {
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
	assertSQLiteIndex(t, db, "datasource_mirror_record", "datasource_mirror_record_scan")
	assertSQLiteIndex(t, db, "datasource_mirror_filter", "datasource_mirror_filter_lookup")
	exerciseStore(t, ctx, store)
}

func TestMySQLStoreImplementsMirrorStore(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") != "1" {
		t.Skip("set TEST_INTEGRATION=1 to run MySQL testcontainers mirror store test")
	}
	ctx := context.Background()
	container, err := tc_mysql.Run(ctx, "mysql:8.0.36",
		tc_mysql.WithDatabase("agentruntime"),
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

func exerciseStore(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	service, err := mirror.New(store)
	if err != nil {
		t.Fatalf("mirror.New: %v", err)
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
		Title: "Ada",
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
	run := mirror.RunState{
		Datasource:  "gitlab",
		Entity:      "gitlab.user",
		Phase:       "fields",
		Status:      mirror.RunStatusComplete,
		StartedAt:   time.Now().Add(-time.Minute).UTC(),
		CompletedAt: time.Now().UTC(),
		Documents:   1,
		Indexed:     1,
	}
	if err := service.PutRun(ctx, run); err != nil {
		t.Fatalf("PutRun: %v", err)
	}
	if err := mirror.RequireBuilt(ctx, service, "gitlab", "gitlab.user"); err != nil {
		t.Fatalf("RequireBuilt: %v", err)
	}
	result, err := mirror.Search(ctx, mirror.LookupRequest{
		Service:    service,
		Datasource: "gitlab",
		Entity:     "gitlab.user",
		Query:      "ada",
		Filters:    map[string]string{"group_path": "abc"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "42" || result.Records[0].Metadata["username"] != "ada" {
		t.Fatalf("records = %#v, want Ada record", result.Records)
	}
	longPrefix := strings.Repeat("x", 191)
	for _, doc := range []coredatasource.CorpusDocument{
		{
			Ref:      coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user", ID: "long-a"},
			Title:    "Long A",
			Metadata: map[string]string{"username": "long-a", "group_path": longPrefix + "a"},
		},
		{
			Ref:      coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user", ID: "long-b"},
			Title:    "Long B",
			Metadata: map[string]string{"username": "long-b", "group_path": longPrefix + "b"},
		},
	} {
		if _, err := service.UpdateRecord(ctx, doc, entity); err != nil {
			t.Fatalf("UpdateRecord long filter: %v", err)
		}
	}
	filtered, err := service.SearchRecords(ctx, mirror.SearchRequest{
		Datasources: []coredatasource.Name{"gitlab"},
		Entities:    []coredatasource.EntityType{"gitlab.user"},
		Filters:     map[string]string{"group_path": longPrefix + "b"},
	})
	if err != nil {
		t.Fatalf("SearchRecords long filter: %v", err)
	}
	if len(filtered.Hits) != 1 || filtered.Hits[0].Record.ID != "long-b" {
		t.Fatalf("long filter hits = %#v, want only long-b", filtered.Hits)
	}
	record, err := service.Record(ctx, coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user", ID: "42"})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if record.Title != "Ada" || record.URL == "" {
		t.Fatalf("record = %#v, want title and URL", record)
	}
	gotRun, ok, err := service.Run(ctx, mirror.RunKey{Datasource: "gitlab", Entity: "gitlab.user", Phase: "fields"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !ok || gotRun.Status != mirror.RunStatusComplete || gotRun.Indexed != 1 {
		t.Fatalf("run = %#v ok=%v, want complete indexed run", gotRun, ok)
	}
	if err := service.DeleteRecord(ctx, coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user", ID: "42"}); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	_, err = service.Record(ctx, coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user", ID: "42"})
	if err != coredatasource.ErrNotFound {
		t.Fatalf("Record after delete error = %v, want ErrNotFound", err)
	}
}

func assertSQLiteIndex(t *testing.T, db *sql.DB, table, name string) {
	t.Helper()
	rows, err := db.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		t.Fatalf("index_list %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq int
		var gotName string
		var unique, partial int
		var origin string
		if err := rows.Scan(&seq, &gotName, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index_list %s: %v", table, err)
		}
		if gotName == name {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("index_list %s rows: %v", table, err)
	}
	t.Fatalf("missing sqlite index %s on %s", name, table)
}
