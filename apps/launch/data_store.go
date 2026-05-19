package launch

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"

	datasqlstore "github.com/fluxplane/agentruntime/adapters/datastore/sqlstore"
	coredata "github.com/fluxplane/agentruntime/core/data"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	runtimedata "github.com/fluxplane/agentruntime/runtime/data"
)

const defaultDataStoreDSNEnv = "AGENTRUNTIME_DATASTORE_MYSQL_DSN"

func openDataStore(ctx context.Context, cfg distribution.DataConfig) (coredata.Store, func() error, error) {
	kind := strings.ToLower(strings.TrimSpace(cfg.Store.Kind))
	switch kind {
	case "", "memory", "mem":
		return runtimedata.NewMemoryStore(), nil, nil
	case "mysql":
		dsn := strings.TrimSpace(cfg.Store.DSN)
		dsnEnv := strings.TrimSpace(cfg.Store.DSNEnv)
		if dsnEnv == "" {
			dsnEnv = defaultDataStoreDSNEnv
		}
		if dsn == "" {
			dsn = strings.TrimSpace(os.Getenv(dsnEnv))
		}
		if dsn == "" {
			return nil, nil, fmt.Errorf("data store mysql dsn is empty; set data.store.dsn or %s", dsnEnv)
		}
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, nil, fmt.Errorf("data store mysql open: %w", err)
		}
		store, err := datasqlstore.OpenDB(ctx, db, datasqlstore.DialectMySQL)
		if err != nil {
			_ = db.Close()
			return nil, nil, err
		}
		return store, db.Close, nil
	default:
		return nil, nil, fmt.Errorf("data store kind %q is not supported", cfg.Store.Kind)
	}
}
