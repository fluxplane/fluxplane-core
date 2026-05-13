package connectorplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	connectoroperation "github.com/codewandler/connectors/operation"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
)

// DatasourceAction maps one datasource entity to explicit connector operations.
type DatasourceAction struct {
	Entity         coredatasource.EntitySpec
	Kind           string
	SearchOp       string
	GetOp          string
	QueryParam     string
	LimitParam     string
	IDParam        string
	ResultPath     string
	LocalFilter    bool
	QueryValue     func(string) string
	ParamDefaults  map[string]any
	TitleFields    []string
	TextFields     []string
	URLFields      []string
	IDFields       []string
	MetadataFields map[string][]string
}

// NewDatasourceProvider returns a connector-backed datasource provider.
func NewDatasourceProvider(executor Executor, actions []DatasourceAction) coredatasource.Provider {
	index := map[coredatasource.EntityType]DatasourceAction{}
	entityIndex := map[coredatasource.EntityType]coredatasource.EntitySpec{}
	for _, action := range actions {
		if action.Entity.Type == "" {
			continue
		}
		if action.QueryParam == "" {
			action.QueryParam = "query"
		}
		if action.LimitParam == "" {
			action.LimitParam = "limit"
		}
		if action.IDParam == "" {
			action.IDParam = "id"
		}
		action.Entity.Capabilities = action.capabilities()
		index[action.Entity.Type] = action
		entityIndex[action.Entity.Type] = action.Entity
	}
	return connectorDatasourceProvider{executor: executor, actions: index, entities: entityIndex}
}

func (a DatasourceAction) capabilities() []coredatasource.EntityCapability {
	var out []coredatasource.EntityCapability
	if a.SearchOp != "" {
		out = append(out, coredatasource.EntityCapabilitySearch)
	}
	if a.GetOp != "" {
		out = append(out, coredatasource.EntityCapabilityGet)
	}
	return out
}

type connectorDatasourceProvider struct {
	executor Executor
	actions  map[coredatasource.EntityType]DatasourceAction
	entities map[coredatasource.EntityType]coredatasource.EntitySpec
}

func (p connectorDatasourceProvider) Entities() []coredatasource.EntitySpec {
	out := make([]coredatasource.EntitySpec, 0, len(p.entities))
	for _, entity := range p.entities {
		out = append(out, entity)
	}
	return out
}

func (p connectorDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if spec.Connector == "" {
		return nil, fmt.Errorf("connector instance is required")
	}
	if p.executor == nil {
		return nil, fmt.Errorf("connector executor is nil")
	}
	actions := map[coredatasource.EntityType]DatasourceAction{}
	entities := map[coredatasource.EntityType]coredatasource.EntitySpec{}
	for _, entity := range spec.Entities {
		action, ok := p.actions[entity]
		if !ok || action.Kind == "" {
			return nil, fmt.Errorf("unsupported entity %q", entity)
		}
		if spec.Kind != "" && spec.Kind != action.Kind {
			return nil, fmt.Errorf("unsupported datasource kind %q for entity %q", spec.Kind, entity)
		}
		actions[entity] = action
		entities[entity] = action.Entity
	}
	return connectorAccessor{spec: spec, actions: actions, entities: entities, executor: p.executor}, nil
}

type connectorAccessor struct {
	spec     coredatasource.Spec
	actions  map[coredatasource.EntityType]DatasourceAction
	entities map[coredatasource.EntityType]coredatasource.EntitySpec
	executor Executor
}

func (a connectorAccessor) Spec() coredatasource.Spec { return a.spec }
func (a connectorAccessor) Entities() []coredatasource.EntitySpec {
	out := make([]coredatasource.EntitySpec, 0, len(a.entities))
	for _, entity := range a.entities {
		out = append(out, entity)
	}
	return out
}

func (a connectorAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	action, ok := a.actions[req.Entity]
	if !ok {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	if action.SearchOp == "" {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q entity %q does not support search", a.spec.Name, req.Entity)
	}
	params := map[string]any{}
	for key, value := range action.ParamDefaults {
		params[key] = value
	}
	for key, value := range req.Filters {
		params[key] = value
	}
	if action.QueryParam != "" && action.QueryParam != "-" {
		query := req.Query
		if action.QueryValue != nil {
			query = action.QueryValue(req.Query)
		}
		params[action.QueryParam] = query
	}
	if req.Limit > 0 {
		params[action.LimitParam] = req.Limit
		if action.LocalFilter && req.Limit < 200 {
			params[action.LimitParam] = 200
		}
	}
	result, err := a.executor.ExecWithInstance(ctx, a.spec.Connector, action.SearchOp, "", params)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	if result.Status != "" && result.Status != connectoroperation.StatusOK {
		return coredatasource.SearchResult{}, connectorResultError(result)
	}
	if result.Error != nil {
		return coredatasource.SearchResult{}, result.Error
	}
	records := a.records(action, req.Entity, result.Data)
	if action.LocalFilter && strings.TrimSpace(req.Query) != "" {
		records = filterRecords(records, req.Query)
		if req.Limit > 0 && len(records) > req.Limit {
			records = records[:req.Limit]
		}
	}
	return coredatasource.SearchResult{
		Datasource: a.spec.Name,
		Entity:     req.Entity,
		Records:    records,
		Total:      len(records),
	}, nil
}

func filterRecords(records []coredatasource.Record, query string) []coredatasource.Record {
	query = strings.ToLower(strings.TrimSpace(query))
	var out []coredatasource.Record
	for _, record := range records {
		values := []string{record.ID, record.Title, record.Content, record.URL}
		for key, value := range record.Metadata {
			values = append(values, key, value)
		}
		for _, value := range values {
			if strings.Contains(strings.ToLower(value), query) {
				out = append(out, record)
				break
			}
		}
	}
	return out
}

func (a connectorAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	action, ok := a.actions[req.Entity]
	if !ok {
		return coredatasource.Record{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	if action.GetOp == "" {
		return coredatasource.Record{}, fmt.Errorf("datasource %q entity %q does not support get", a.spec.Name, req.Entity)
	}
	params := map[string]any{action.IDParam: req.ID}
	result, err := a.executor.ExecWithInstance(ctx, a.spec.Connector, action.GetOp, "", params)
	if err != nil {
		return coredatasource.Record{}, err
	}
	if result.Status != "" && result.Status != connectoroperation.StatusOK {
		return coredatasource.Record{}, connectorResultError(result)
	}
	if result.Error != nil {
		return coredatasource.Record{}, result.Error
	}
	records := a.records(action, req.Entity, result.Data)
	if len(records) == 0 {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	return records[0], nil
}

func connectorResultError(result connectoroperation.Result) error {
	if result.Error != nil {
		return result.Error
	}
	return fmt.Errorf("connector operation returned status %s", result.Status)
}

func (a connectorAccessor) records(action DatasourceAction, entity coredatasource.EntityType, data any) []coredatasource.Record {
	items := flattenRecords(data)
	if strings.TrimSpace(action.ResultPath) != "" {
		items = flattenRecords(data, action.ResultPath)
	}
	records := make([]coredatasource.Record, 0, len(items))
	for _, item := range items {
		records = append(records, a.record(action, entity, item))
	}
	return records
}

func (a connectorAccessor) record(action DatasourceAction, entity coredatasource.EntityType, item map[string]any) coredatasource.Record {
	return coredatasource.Record{
		ID:         firstString(item, append(action.IDFields, "id", "iid", "key")...),
		Datasource: a.spec.Name,
		Entity:     entity,
		Title:      firstString(item, append(action.TitleFields, "title", "name", "summary", "path_with_namespace")...),
		Content:    firstString(item, append(action.TextFields, "description", "text", "content", "snippet")...),
		URL:        firstString(item, append(action.URLFields, "web_url", "url", "permalink")...),
		Metadata:   stringMetadata(item, action.MetadataFields),
		Raw:        item,
	}
}

func flattenRecords(data any, paths ...string) []map[string]any {
	if len(paths) > 0 {
		var out []map[string]any
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			value, ok := lookupAny(data, path)
			if !ok {
				continue
			}
			out = append(out, flattenRecords(value)...)
		}
		return out
	}
	var out []map[string]any
	for _, value := range candidateValues(data) {
		switch v := value.(type) {
		case []any:
			for _, item := range v {
				if m, ok := asMap(item); ok {
					out = append(out, m)
				}
			}
		case []map[string]any:
			out = append(out, v...)
		default:
			if m, ok := asMap(v); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func lookupAny(value any, path string) (any, bool) {
	if path == "" {
		return value, true
	}
	current := value
	for _, part := range strings.Split(path, ".") {
		m, ok := asMap(current)
		if !ok {
			return nil, false
		}
		next, ok := m[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func candidateValues(data any) []any {
	if m, ok := asMap(data); ok {
		for _, key := range []string{"records", "items", "data", "values", "matches", "projects", "issues", "users", "members", "messages"} {
			if value, exists := m[key]; exists {
				if nested, ok := asMap(value); ok {
					return candidateValues(nested)
				}
				return []any{value}
			}
		}
		if nested, ok := asMap(m["result"]); ok {
			return candidateValues(nested)
		}
	}
	return []any{data}
}

func asMap(value any) (map[string]any, bool) {
	if value == nil {
		return nil, false
	}
	if m, ok := value.(map[string]any); ok {
		return m, true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := lookupValue(m, key); ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func lookupValue(m map[string]any, key string) (any, bool) {
	if value, ok := m[key]; ok {
		return value, true
	}
	current := m
	parts := strings.Split(key, ".")
	for i, part := range parts {
		value, ok := current[part]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return value, true
		}
		next, ok := asMap(value)
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func stringMetadata(m map[string]any, fields map[string][]string) map[string]string {
	out := map[string]string{}
	for name, paths := range fields {
		if value := firstString(m, paths...); value != "" {
			out[name] = value
		}
	}
	for key, value := range m {
		if _, exists := out[key]; exists {
			continue
		}
		switch value.(type) {
		case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			out[key] = fmt.Sprint(value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
