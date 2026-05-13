package connectorplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	connectoroperation "github.com/codewandler/connectors/operation"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
)

// DatasourceAction maps one datasource entity to explicit connector operations.
type DatasourceAction struct {
	Entity          coredatasource.EntitySpec
	Kind            string
	SearchOp        string
	GetOp           string
	QueryParam      string
	LimitParam      string
	IDParam         string
	CursorParam     string
	NextCursorPath  string
	ResultPath      string
	LocalFilter     bool
	QueryValue      func(string) string
	ParamDefaults   map[string]any
	TitleFields     []string
	TextFields      []string
	URLFields       []string
	IDFields        []string
	MetadataFields  map[string][]string
	Corpus          CorpusPolicy
	RecordTransform func(coredatasource.Record) coredatasource.Record
	Relations       []DatasourceRelationAction
	MaxPages        int
}

// CorpusPolicy maps connector records into semantic index corpus text.
type CorpusPolicy struct {
	TitleFields    []string
	BodyFields     []string
	MetadataFields map[string][]string
	ExcludeFields  []string
}

// DatasourceRelationAction maps one entity relationship to a connector operation.
type DatasourceRelationAction struct {
	Name           string
	Description    string
	TargetEntity   coredatasource.EntityType
	Operation      string
	IDParam        string
	LimitParam     string
	CursorParam    string
	ResultPath     string
	NextCursorPath string
	Exact          bool
	ParamDefaults  map[string]any
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
		if action.CursorParam == "" {
			action.CursorParam = "cursor"
		}
		action.Entity.Capabilities = action.capabilities()
		action.Entity.Relations = append(action.Entity.Relations, action.relationSpecs()...)
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
	if len(a.Relations) > 0 {
		out = append(out, coredatasource.EntityCapabilityRelation)
	}
	if a.SearchOp != "" {
		out = append(out, coredatasource.EntityCapabilitySemanticSearch)
	}
	return out
}

func (a DatasourceAction) relationSpecs() []coredatasource.RelationSpec {
	out := make([]coredatasource.RelationSpec, 0, len(a.Relations))
	seen := map[string]bool{}
	for _, relation := range a.Relations {
		name := strings.TrimSpace(relation.Name)
		if name == "" || relation.TargetEntity == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, coredatasource.RelationSpec{
			Name:         name,
			Description:  relation.Description,
			TargetEntity: relation.TargetEntity,
			Exact:        relation.Exact,
		})
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
	records, err := a.searchRecords(ctx, action, req.Entity, params, strings.TrimSpace(req.Query) != "")
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
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

const defaultMaxDatasourcePages = 10

func (a connectorAccessor) searchRecords(ctx context.Context, action DatasourceAction, entity coredatasource.EntityType, params map[string]any, filtering bool) ([]coredatasource.Record, error) {
	if !action.LocalFilter || !filtering || strings.TrimSpace(action.NextCursorPath) == "" {
		result, err := a.exec(ctx, action.SearchOp, params)
		if err != nil {
			return nil, err
		}
		return a.records(action, entity, result.Data), nil
	}
	records, err := a.listRecords(ctx, action, entity, params)
	if err != nil {
		return nil, err
	}
	return records, nil
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
	result, err := a.exec(ctx, action.GetOp, params)
	if err != nil {
		return coredatasource.Record{}, err
	}
	records := a.records(action, req.Entity, result.Data)
	if len(records) == 0 {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	return records[0], nil
}

func (a connectorAccessor) BatchGet(ctx context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	action, ok := a.actions[req.Entity]
	if !ok {
		return coredatasource.BatchGetResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	ids := cleanedIDs(req.IDs)
	out := coredatasource.BatchGetResult{Datasource: a.spec.Name, Entity: req.Entity}
	if len(ids) == 0 {
		return out, nil
	}
	found := map[string]coredatasource.Record{}
	if action.SearchOp != "" && action.QueryParam == "-" {
		records, err := a.listRecords(ctx, action, req.Entity, map[string]any{})
		if err != nil {
			return coredatasource.BatchGetResult{}, err
		}
		for _, record := range records {
			if record.ID != "" {
				found[record.ID] = record
			}
		}
	}
	for _, id := range ids {
		if record, ok := found[id]; ok {
			out.Records = append(out.Records, record)
			continue
		}
		if action.GetOp == "" {
			out.Errors = append(out.Errors, coredatasource.BatchGetError{ID: id, Message: "record not found"})
			continue
		}
		record, err := a.Get(ctx, coredatasource.GetRequest{Entity: req.Entity, ID: id})
		if errors.Is(err, coredatasource.ErrNotFound) {
			out.Errors = append(out.Errors, coredatasource.BatchGetError{ID: id, Message: err.Error()})
			continue
		}
		if err != nil {
			out.Errors = append(out.Errors, coredatasource.BatchGetError{ID: id, Message: err.Error()})
			continue
		}
		out.Records = append(out.Records, record)
	}
	return out, nil
}

func (a connectorAccessor) Relation(ctx context.Context, req coredatasource.RelationRequest) (coredatasource.RelationResult, error) {
	action, ok := a.actions[req.Entity]
	if !ok {
		return coredatasource.RelationResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	relation, ok := relationAction(action.Relations, req.Relation)
	if !ok {
		return coredatasource.RelationResult{}, fmt.Errorf("datasource %q entity %q does not expose relation %q", a.spec.Name, req.Entity, req.Relation)
	}
	ids, rawRecords, nextCursor, err := a.fetchRelation(ctx, action, relation, req)
	if err != nil {
		return coredatasource.RelationResult{}, err
	}
	records := rawRecords
	if len(ids) > 0 {
		batch, err := a.BatchGet(ctx, coredatasource.BatchGetRequest{Entity: relation.TargetEntity, IDs: ids})
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		records = append(records, recordsForIDs(ids, batch.Records, a.spec.Name, relation.TargetEntity)...)
	}
	return coredatasource.RelationResult{
		Datasource:   a.spec.Name,
		Entity:       req.Entity,
		ID:           req.ID,
		Relation:     relation.Name,
		TargetEntity: relation.TargetEntity,
		Records:      records,
		Total:        len(records),
		NextCursor:   nextCursor,
		Complete:     nextCursor == "",
		Exact:        relation.Exact,
	}, nil
}

func (a connectorAccessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	action, ok := a.actions[req.Entity]
	if !ok {
		return coredatasource.CorpusPage{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	if action.SearchOp == "" {
		return coredatasource.CorpusPage{}, fmt.Errorf("datasource %q entity %q does not support corpus enumeration", a.spec.Name, req.Entity)
	}
	params := map[string]any{}
	for key, value := range action.ParamDefaults {
		params[key] = value
	}
	if action.QueryParam != "" && action.QueryParam != "-" {
		params[action.QueryParam] = ""
	}
	if req.Limit > 0 {
		params[action.LimitParam] = req.Limit
	}
	records, err := a.searchRecords(ctx, action, req.Entity, params, false)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	docs := make([]coredatasource.CorpusDocument, 0, len(records))
	for _, record := range records {
		if record.ID == "" {
			continue
		}
		docs = append(docs, action.corpusDocument(record))
	}
	return coredatasource.CorpusPage{Documents: docs, Complete: true}, nil
}

func (a connectorAccessor) fetchRelation(ctx context.Context, source DatasourceAction, relation DatasourceRelationAction, req coredatasource.RelationRequest) ([]string, []coredatasource.Record, string, error) {
	maxPages := source.MaxPages
	if maxPages <= 0 {
		maxPages = defaultMaxDatasourcePages
	}
	if strings.TrimSpace(req.Cursor) != "" {
		maxPages = 1
	}
	params := copyMap(relation.ParamDefaults)
	params[firstNonEmpty(relation.IDParam, source.IDParam, "id")] = req.ID
	if req.Limit > 0 {
		params[firstNonEmpty(relation.LimitParam, "limit")] = req.Limit
	}
	if strings.TrimSpace(req.Cursor) != "" {
		params[firstNonEmpty(relation.CursorParam, "cursor")] = strings.TrimSpace(req.Cursor)
	}
	var ids []string
	var records []coredatasource.Record
	var nextCursor string
	for page := 0; page < maxPages; page++ {
		result, err := a.exec(ctx, relation.Operation, params)
		if err != nil {
			return nil, nil, "", err
		}
		pageIDs, pageRecords := relationRecords(result.Data, relation.ResultPath, a.spec.Name, relation.TargetEntity)
		ids = append(ids, pageIDs...)
		records = append(records, pageRecords...)
		nextCursor = strings.TrimSpace(firstStringFromAny(result.Data, relation.NextCursorPath))
		if req.Limit > 0 && len(ids)+len(records) >= req.Limit {
			ids = trimStrings(ids, req.Limit)
			records = trimRecords(records, req.Limit-len(ids))
			break
		}
		if nextCursor == "" {
			break
		}
		params[firstNonEmpty(relation.CursorParam, "cursor")] = nextCursor
	}
	return ids, records, nextCursor, nil
}

func (a connectorAccessor) listRecords(ctx context.Context, action DatasourceAction, entity coredatasource.EntityType, params map[string]any) ([]coredatasource.Record, error) {
	maxPages := action.MaxPages
	if maxPages <= 0 {
		maxPages = defaultMaxDatasourcePages
	}
	pageParams := copyMap(action.ParamDefaults)
	for key, value := range params {
		pageParams[key] = value
	}
	if action.LimitParam != "" {
		if _, ok := pageParams[action.LimitParam]; !ok {
			pageParams[action.LimitParam] = 200
		}
	}
	var records []coredatasource.Record
	for page := 0; page < maxPages; page++ {
		result, err := a.exec(ctx, action.SearchOp, pageParams)
		if err != nil {
			return nil, err
		}
		records = append(records, a.records(action, entity, result.Data)...)
		cursor := strings.TrimSpace(firstStringFromAny(result.Data, action.NextCursorPath))
		if cursor == "" {
			break
		}
		pageParams[action.CursorParam] = cursor
	}
	return records, nil
}

func (a connectorAccessor) exec(ctx context.Context, op string, params map[string]any) (connectoroperation.Result, error) {
	result, err := a.executor.ExecWithInstance(ctx, a.spec.Connector, op, "", params)
	if err != nil {
		return connectoroperation.Result{}, err
	}
	if result.Status != "" && result.Status != connectoroperation.StatusOK {
		return connectoroperation.Result{}, connectorResultError(result)
	}
	if result.Error != nil {
		return connectoroperation.Result{}, result.Error
	}
	return result, nil
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
	record := coredatasource.Record{
		ID:         firstString(item, append(action.IDFields, "id", "iid", "key")...),
		Datasource: a.spec.Name,
		Entity:     entity,
		Title:      firstString(item, append(action.TitleFields, "title", "name", "summary", "path_with_namespace")...),
		Content:    firstString(item, append(action.TextFields, "description", "text", "content", "snippet")...),
		URL:        firstString(item, append(action.URLFields, "web_url", "url", "permalink")...),
		Metadata:   stringMetadata(item, action.MetadataFields),
		Raw:        item,
	}
	if action.RecordTransform != nil {
		record = action.RecordTransform(record)
	}
	return record
}

func (a DatasourceAction) corpusDocument(record coredatasource.Record) coredatasource.CorpusDocument {
	policy := a.Corpus
	title := firstNonEmpty(record.Title, firstStringFromRaw(record.Raw, policy.TitleFields...), firstStringFromRaw(record.Raw, a.TitleFields...))
	body := strings.Join(cleanStrings(
		firstStringFromRaw(record.Raw, policy.BodyFields...),
		record.Content,
	), "\n\n")
	if strings.TrimSpace(body) == "" {
		body = record.Content
	}
	metadata := cloneStringMap(record.Metadata)
	for key, paths := range policy.MetadataFields {
		if value := firstStringFromRaw(record.Raw, paths...); value != "" {
			if metadata == nil {
				metadata = map[string]string{}
			}
			metadata[key] = value
		}
	}
	for _, key := range policy.ExcludeFields {
		delete(metadata, key)
	}
	data, _ := json.Marshal(struct {
		ID       string
		Title    string
		Body     string
		URL      string
		Metadata map[string]string
		Raw      any
	}{record.ID, title, body, record.URL, metadata, record.Raw})
	sum := sha256.Sum256(data)
	return coredatasource.CorpusDocument{
		Ref: coredatasource.RecordRef{
			Datasource: record.Datasource,
			Entity:     record.Entity,
			ID:         record.ID,
			URL:        record.URL,
		},
		Title:       title,
		Body:        body,
		URL:         record.URL,
		Metadata:    metadata,
		Fingerprint: hex.EncodeToString(sum[:]),
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
		for _, key := range []string{"records", "items", "data", "values", "matches", "projects", "issues", "users", "members", "messages", "channels", "user", "channel"} {
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

func relationAction(relations []DatasourceRelationAction, name string) (DatasourceRelationAction, bool) {
	name = strings.TrimSpace(name)
	for _, relation := range relations {
		if relation.Name == name && relation.Operation != "" && relation.TargetEntity != "" {
			return relation, true
		}
	}
	return DatasourceRelationAction{}, false
}

func relationRecords(data any, path string, datasource coredatasource.Name, entity coredatasource.EntityType) ([]string, []coredatasource.Record) {
	value := data
	if strings.TrimSpace(path) != "" {
		if nested, ok := lookupAny(data, path); ok {
			value = nested
		} else {
			return nil, nil
		}
	}
	var ids []string
	var records []coredatasource.Record
	for _, item := range listValues(value) {
		switch v := item.(type) {
		case string:
			if id := strings.TrimSpace(v); id != "" {
				ids = append(ids, id)
			}
		default:
			if m, ok := asMap(v); ok {
				record := coredatasource.Record{
					ID:         firstString(m, "id", "user", "iid", "key"),
					Datasource: datasource,
					Entity:     entity,
					Title:      firstString(m, "name", "real_name", "title"),
					Content:    firstString(m, "description", "text", "content", "snippet"),
					URL:        firstString(m, "web_url", "url", "permalink"),
					Metadata:   stringMetadata(m, nil),
					Raw:        m,
				}
				if record.ID != "" {
					records = append(records, record)
				}
			}
		}
	}
	return ids, records
}

func recordsForIDs(ids []string, records []coredatasource.Record, datasource coredatasource.Name, entity coredatasource.EntityType) []coredatasource.Record {
	byID := map[string]coredatasource.Record{}
	for _, record := range records {
		if record.ID != "" {
			byID[record.ID] = record
		}
	}
	out := make([]coredatasource.Record, 0, len(ids))
	for _, id := range ids {
		if record, ok := byID[id]; ok {
			out = append(out, record)
			continue
		}
		out = append(out, coredatasource.Record{
			ID:         id,
			Datasource: datasource,
			Entity:     entity,
			Title:      id,
		})
	}
	return out
}

func listValues(value any) []any {
	switch v := value.(type) {
	case nil:
		return nil
	case []any:
		return v
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	case []map[string]any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	default:
		return []any{v}
	}
}

func cleanedIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func trimStrings(values []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func trimRecords(values []coredatasource.Record, limit int) []coredatasource.Record {
	if limit <= 0 {
		return nil
	}
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func copyMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func firstStringFromAny(value any, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	nested, ok := lookupAny(value, path)
	if !ok {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(nested))
	if text == "" || text == "<nil>" {
		return ""
	}
	return text
}

func firstStringFromRaw(value any, paths ...string) string {
	m, ok := asMap(value)
	if !ok {
		return ""
	}
	return firstString(m, paths...)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cleanStrings(values ...string) []string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
