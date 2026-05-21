package mysql

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"

	coreendpoint "github.com/fluxplane/engine/core/endpoint"
	"github.com/fluxplane/engine/core/operation"
	coresecret "github.com/fluxplane/engine/core/secret"
	runtimesecret "github.com/fluxplane/engine/runtime/secret"
	"github.com/fluxplane/engine/runtime/sqlclient"
)

type QueryInput struct {
	Query       string `json:"query"`
	EndpointRef string `json:"endpoint_ref,omitempty"`
	Database    string `json:"database,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
	MaxRows     int    `json:"max_rows,omitempty"`
}

type QueryOutput struct {
	EndpointRef string           `json:"endpoint_ref,omitempty"`
	EndpointURL string           `json:"endpoint_url,omitempty"`
	Database    string           `json:"database,omitempty"`
	Columns     []string         `json:"columns,omitempty"`
	Rows        []map[string]any `json:"rows,omitempty"`
	RowCount    int              `json:"row_count"`
	Truncated   bool             `json:"truncated,omitempty"`
	DurationMS  int64            `json:"duration_ms,omitempty"`
	Source      any              `json:"source,omitempty"`
}

type resolvedEndpoint struct {
	Ref      string
	URL      string
	AuthRef  string
	Source   any
	Metadata map[string]string
}

func (p Plugin) query() func(operation.Context, QueryInput) operation.Result {
	return func(ctx operation.Context, in QueryInput) operation.Result {
		out, err := p.runQuery(ctx, in)
		if err != nil {
			return operation.Failed("mysql_query_failed", redactError(err), nil)
		}
		return operation.OK(out)
	}
}

func (p Plugin) runQuery(ctx operation.Context, in QueryInput) (QueryOutput, error) {
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" {
		return QueryOutput{}, fmt.Errorf("query is required")
	}
	endpoint, ok := p.resolveEndpoint(in.EndpointRef)
	if !ok {
		return QueryOutput{}, fmt.Errorf("endpoint_ref is required and must resolve to a discovered endpoint")
	}
	timeout, err := duration(firstNonEmpty(in.Timeout, p.cfg.Timeout), 10*time.Second)
	if err != nil {
		return QueryOutput{}, err
	}
	maxRows := in.MaxRows
	if maxRows <= 0 {
		maxRows = p.cfg.MaxRows
	}
	if maxRows <= 0 {
		maxRows = 100
	}
	material, _, err := p.resolveCredential(ctx, endpoint.AuthRef)
	if err != nil {
		return QueryOutput{}, err
	}
	target, err := mysqlTargetFrom(endpoint, material.Value, firstNonEmpty(in.Database, p.cfg.Database))
	if err != nil {
		return QueryOutput{}, err
	}
	result, err := sqlclient.QueryReadOnly(ctx, sqlclient.QueryRequest{
		DriverName: "mysql",
		DSN:        target.DSN,
		Query:      in.Query,
		Timeout:    timeout,
		MaxRows:    maxRows,
	})
	if err != nil {
		return QueryOutput{}, err
	}
	return QueryOutput{
		EndpointRef: endpoint.Ref,
		EndpointURL: target.SafeURL,
		Database:    target.Database,
		Columns:     result.Columns,
		Rows:        result.Rows,
		RowCount:    result.RowCount,
		Truncated:   result.Truncated,
		DurationMS:  result.DurationMS,
		Source:      endpoint.Source,
	}, nil
}

func (p Plugin) resolveCredential(ctx operation.Context, authRef string) (coresecret.Material, bool, error) {
	authRef = strings.TrimSpace(authRef)
	if authRef == "" {
		return coresecret.Material{}, false, nil
	}
	broker := runtimesecret.NewBroker(p.secrets)
	if broker == nil {
		return coresecret.Material{}, false, fmt.Errorf("secret resolver is nil")
	}
	material, ok, err := broker.Use(ctx, coresecret.ParseRef(authRef))
	if err != nil {
		return coresecret.Material{}, false, err
	}
	if !ok {
		return coresecret.Material{}, false, fmt.Errorf("secret %s was not found", authRef)
	}
	return material, true, nil
}

type mysqlTarget struct {
	DSN      string
	SafeURL  string
	Database string
}

func mysqlTargetFrom(endpoint resolvedEndpoint, secretValue, databaseOverride string) (mysqlTarget, error) {
	if target, ok, err := targetFromSecret(secretValue, databaseOverride); err != nil || ok {
		return target, err
	}
	endpointURL := strings.TrimSpace(endpoint.URL)
	if endpointURL == "" {
		return mysqlTarget{}, fmt.Errorf("endpoint has no URL and credential is not a MySQL DSN")
	}
	parsed, err := url.Parse(endpointURL)
	if err != nil {
		return mysqlTarget{}, fmt.Errorf("parse endpoint URL: %w", err)
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" {
		return mysqlTarget{}, fmt.Errorf("endpoint URL has no host")
	}
	user := parsed.User.Username()
	password, hasPassword := parsed.User.Password()
	if secretValue != "" && !looksLikeDSN(secretValue) {
		password = secretValue
		hasPassword = true
	}
	if user == "" {
		user = "root"
	}
	database := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if strings.TrimSpace(databaseOverride) != "" {
		database = strings.TrimSpace(databaseOverride)
	}
	return targetFromParts(user, password, hasPassword, host, port, database), nil
}

func targetFromSecret(secretValue, databaseOverride string) (mysqlTarget, bool, error) {
	secretValue = strings.TrimSpace(secretValue)
	if secretValue == "" || !looksLikeDSN(secretValue) {
		return mysqlTarget{}, false, nil
	}
	if strings.Contains(secretValue, "://") {
		parsed, err := url.Parse(secretValue)
		if err != nil {
			return mysqlTarget{}, true, err
		}
		user := parsed.User.Username()
		password, hasPassword := parsed.User.Password()
		database := strings.Trim(strings.TrimSpace(parsed.Path), "/")
		if strings.TrimSpace(databaseOverride) != "" {
			database = strings.TrimSpace(databaseOverride)
		}
		return targetFromParts(user, password, hasPassword, parsed.Hostname(), parsed.Port(), database), true, nil
	}
	if strings.HasPrefix(secretValue, "{") {
		var payload struct {
			Username string `json:"username"`
			User     string `json:"user"`
			Password string `json:"password"`
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Database string `json:"database"`
			DB       string `json:"db"`
			DSN      string `json:"dsn"`
		}
		if err := json.Unmarshal([]byte(secretValue), &payload); err != nil {
			return mysqlTarget{}, true, err
		}
		if payload.DSN != "" {
			return targetFromSecret(payload.DSN, databaseOverride)
		}
		database := firstNonEmpty(databaseOverride, payload.Database, payload.DB)
		port := ""
		if payload.Port > 0 {
			port = strconv.Itoa(payload.Port)
		}
		return targetFromParts(firstNonEmpty(payload.Username, payload.User), payload.Password, payload.Password != "", payload.Host, port, database), true, nil
	}
	target, err := targetFromDriverDSN(secretValue, databaseOverride)
	return target, true, err
}

func targetFromDriverDSN(dsn, databaseOverride string) (mysqlTarget, error) {
	safe := redactDSN(dsn)
	database := databaseFromDriverDSN(dsn)
	if strings.TrimSpace(databaseOverride) != "" {
		database = strings.TrimSpace(databaseOverride)
	}
	return mysqlTarget{DSN: dsn, SafeURL: safe, Database: database}, nil
}

func targetFromParts(user, password string, hasPassword bool, host, port, database string) mysqlTarget {
	if port == "" {
		port = "3306"
	}
	address := net.JoinHostPort(host, port)
	cfg := mysql.NewConfig()
	cfg.User = strings.TrimSpace(user)
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = address
	cfg.DBName = database
	cfg.ParseTime = true
	dsn := cfg.FormatDSN()
	if !hasPassword {
		cfg.Passwd = ""
		dsn = cfg.FormatDSN()
	}
	safeURL := "mysql://" + host
	if port != "" {
		safeURL = "mysql://" + net.JoinHostPort(host, port)
	}
	if database != "" {
		safeURL += "/" + database
	}
	return mysqlTarget{DSN: dsn, SafeURL: safeURL, Database: database}
}

func looksLikeDSN(value string) bool {
	value = strings.TrimSpace(value)
	return strings.Contains(value, "://") || strings.Contains(value, "@tcp(") || strings.HasPrefix(value, "{")
}

func databaseFromDriverDSN(dsn string) string {
	if idx := strings.Index(dsn, ")/"); idx >= 0 {
		rest := dsn[idx+2:]
		if q := strings.Index(rest, "?"); q >= 0 {
			rest = rest[:q]
		}
		return strings.Trim(rest, "/")
	}
	return ""
}

func redactDSN(dsn string) string {
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err == nil {
			parsed.User = nil
			return parsed.String()
		}
	}
	if at := strings.Index(dsn, "@tcp("); at >= 0 {
		return "mysql://" + strings.TrimPrefix(dsn[at+5:], "(")
	}
	return "mysql://redacted"
}

func endpointRef(value string) coreendpoint.Ref {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "@endpoint/") {
		return coreendpoint.Ref(value)
	}
	return coreendpoint.NewRef(value)
}

func duration(value string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", value, err)
	}
	return parsed, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func redactError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(msg, "@tcp(") || strings.Contains(msg, "://") {
		return "mysql operation failed"
	}
	return msg
}
