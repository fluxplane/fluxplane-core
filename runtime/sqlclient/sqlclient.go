package sqlclient

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const defaultMaxRows = 100

// QueryRequest describes one bounded SQL query execution.
type QueryRequest struct {
	DriverName string
	DSN        string
	Query      string
	Timeout    time.Duration
	MaxRows    int
}

// QueryResult is the model-safe tabular result.
type QueryResult struct {
	Columns    []string         `json:"columns,omitempty"`
	Rows       []map[string]any `json:"rows,omitempty"`
	RowCount   int              `json:"row_count"`
	Truncated  bool             `json:"truncated,omitempty"`
	DurationMS int64            `json:"duration_ms,omitempty"`
}

// QueryReadOnly runs one query in a read-only transaction.
func QueryReadOnly(ctx context.Context, req QueryRequest) (QueryResult, error) {
	req.DriverName = strings.TrimSpace(req.DriverName)
	req.DSN = strings.TrimSpace(req.DSN)
	req.Query = strings.TrimSpace(req.Query)
	if req.DriverName == "" {
		return QueryResult{}, fmt.Errorf("sql driver name is required")
	}
	if req.DSN == "" {
		return QueryResult{}, fmt.Errorf("sql dsn is required")
	}
	if req.Query == "" {
		return QueryResult{}, fmt.Errorf("sql query is required")
	}
	if hasMultipleStatements(req.Query) {
		return QueryResult{}, fmt.Errorf("sql query must contain exactly one statement")
	}
	if req.MaxRows <= 0 {
		req.MaxRows = defaultMaxRows
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	started := time.Now()
	db, err := sql.Open(req.DriverName, req.DSN)
	if err != nil {
		return QueryResult{}, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return QueryResult{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, req.Query)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return QueryResult{}, err
	}
	out := QueryResult{Columns: columns}
	for rows.Next() {
		if len(out.Rows) >= req.MaxRows {
			out.Truncated = true
			break
		}
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return QueryResult{}, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = normalizeValue(values[i])
		}
		out.Rows = append(out.Rows, row)
		out.RowCount++
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, err
	}
	out.DurationMS = time.Since(started).Milliseconds()
	return out, nil
}

func normalizeValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	default:
		return v
	}
}

func hasMultipleStatements(query string) bool {
	query = strings.TrimSpace(query)
	if strings.HasSuffix(query, ";") {
		query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	}
	return strings.Contains(query, ";")
}
