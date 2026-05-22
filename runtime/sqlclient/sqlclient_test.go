package sqlclient

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestQueryReadOnlyReturnsRowsAndTruncates(t *testing.T) {
	dsn := "file:sqlclient-query-readonly?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`create table items (id integer primary key, name text); insert into items(name) values ('one'), ('two'), ('three')`); err != nil {
		t.Fatalf("seed sqlite db: %v", err)
	}

	result, err := QueryReadOnly(context.Background(), QueryRequest{
		DriverName: "sqlite",
		DSN:        dsn,
		Query:      "select id, name from items order by id",
		MaxRows:    2,
		Timeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("QueryReadOnly: %v", err)
	}
	if got, want := result.Columns, []string{"id", "name"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("columns = %#v, want %#v", got, want)
	}
	if result.RowCount != 2 || !result.Truncated {
		t.Fatalf("result = %#v, want two rows and truncated=true", result)
	}
	if result.Rows[0]["name"] != "one" || result.Rows[1]["name"] != "two" {
		t.Fatalf("rows = %#v, want ordered names", result.Rows)
	}
}

func TestQueryReadOnlyValidatesInput(t *testing.T) {
	tests := []struct {
		name string
		req  QueryRequest
		want string
	}{
		{name: "driver", req: QueryRequest{DSN: "dsn", Query: "select 1"}, want: "driver name is required"},
		{name: "dsn", req: QueryRequest{DriverName: "sqlite", Query: "select 1"}, want: "dsn is required"},
		{name: "query", req: QueryRequest{DriverName: "sqlite", DSN: "dsn"}, want: "query is required"},
		{name: "multiple statements", req: QueryRequest{DriverName: "sqlite", DSN: "dsn", Query: "select 1; select 2"}, want: "exactly one statement"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := QueryReadOnly(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("QueryReadOnly error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestNormalizeValueAndStatementDetection(t *testing.T) {
	now := time.Date(2026, 5, 22, 10, 30, 0, 123, time.FixedZone("test", 2*60*60))
	if got := normalizeValue([]byte("hello")); got != "hello" {
		t.Fatalf("normalizeValue bytes = %#v, want hello", got)
	}
	if got := normalizeValue(now); got != now.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("normalizeValue time = %#v, want UTC RFC3339", got)
	}
	if hasMultipleStatements("select 1;") {
		t.Fatal("single trailing semicolon classified as multiple statements")
	}
	if !hasMultipleStatements("select 1; select 2") {
		t.Fatal("multiple statements were not detected")
	}
}
