package datasource

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	coredata "github.com/fluxplane/agentruntime/core/data"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
)

const (
	CatalogSourceEntity   coredatasource.EntityType = "datasource.source"
	CatalogEntityEntity   coredatasource.EntityType = "datasource.entity"
	CatalogRelationEntity coredatasource.EntityType = "datasource.relation"
	CatalogViewEntity     coredatasource.EntityType = "datasource.view"
)

type catalogAccessor struct {
	registry    *coredatasource.Registry
	dataSources []coredata.SourceSpec
}

var _ coredatasource.Accessor = catalogAccessor{}
var _ coredatasource.Searcher = catalogAccessor{}
var _ coredatasource.Lister = catalogAccessor{}
var _ coredatasource.Getter = catalogAccessor{}
var _ coredatasource.BatchGetter = catalogAccessor{}

func (a catalogAccessor) Spec() coredatasource.Spec {
	return coredatasource.Spec{
		Name:        coredatasource.Name(Name),
		Description: "Synthetic datasource catalog for the current agent.",
		Kind:        "synthetic",
		Entities:    []coredatasource.EntityType{CatalogSourceEntity, CatalogEntityEntity, CatalogRelationEntity, CatalogViewEntity},
	}
}

func (a catalogAccessor) Entities() []coredatasource.EntitySpec {
	capabilities := []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
	}
	return []coredatasource.EntitySpec{
		{
			Type:         CatalogSourceEntity,
			Description:  "Configured datasource instances visible to the current agent.",
			Capabilities: capabilities,
			Fields: []coredatasource.FieldSpec{
				{Name: "name", Type: coredatasource.FieldString, Identifier: true, Searchable: true, Filterable: true},
				{Name: "kind", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
				{Name: "connector", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
				{Name: "entity_count", Type: coredatasource.FieldNumber, Filterable: true},
				{Name: "view_count", Type: coredatasource.FieldNumber, Filterable: true},
			},
		},
		{
			Type:         CatalogEntityEntity,
			Description:  "Datasource entity schemas visible to the current agent.",
			Capabilities: capabilities,
			Fields: []coredatasource.FieldSpec{
				{Name: "datasource", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
				{Name: "entity", Type: coredatasource.FieldString, Identifier: true, Searchable: true, Filterable: true},
				{Name: "capabilities", Type: coredatasource.FieldArray, Searchable: true, Filterable: true},
				{Name: "fields", Type: coredatasource.FieldArray, Searchable: true},
				{Name: "relations", Type: coredatasource.FieldArray, Searchable: true},
			},
		},
		{
			Type:         CatalogRelationEntity,
			Description:  "Datasource relation schemas visible to the current agent.",
			Capabilities: capabilities,
			Fields: []coredatasource.FieldSpec{
				{Name: "datasource", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
				{Name: "entity", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
				{Name: "relation", Type: coredatasource.FieldString, Identifier: true, Searchable: true, Filterable: true},
				{Name: "target_entity", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
				{Name: "exact", Type: coredatasource.FieldBoolean, Filterable: true},
			},
		},
		{
			Type:         CatalogViewEntity,
			Description:  "Datasource materialized view schemas visible to the current agent.",
			Capabilities: capabilities,
			Fields: []coredatasource.FieldSpec{
				{Name: "datasource", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
				{Name: "view", Type: coredatasource.FieldString, Identifier: true, Searchable: true, Filterable: true},
				{Name: "entity", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
				{Name: "source", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
				{Name: "includes", Type: coredatasource.FieldArray, Searchable: true},
				{Name: "fields", Type: coredatasource.FieldArray, Searchable: true},
				{Name: "query_hints", Type: coredatasource.FieldArray, Searchable: true, Filterable: true},
			},
		},
	}
}

func (a catalogAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	records, err := a.records(ctx, req.Entity)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	records = filterCatalogRecords(records, req.Query, req.Filters)
	total := len(records)
	records, _, _ = paginateCatalogRecords(records, req.Limit, "")
	return coredatasource.SearchResult{Datasource: coredatasource.Name(Name), Entity: req.Entity, Records: records, Total: total}, nil
}

func (a catalogAccessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	records, err := a.records(ctx, req.Entity)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	records = filterCatalogRecords(records, "", req.Filters)
	page, next, err := paginateCatalogRecords(records, req.Limit, req.Cursor)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	return coredatasource.ListResult{
		Datasource: coredatasource.Name(Name),
		Entity:     req.Entity,
		Records:    page,
		Total:      len(records),
		NextCursor: next,
		Complete:   next == "",
	}, nil
}

func (a catalogAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	records, err := a.records(ctx, req.Entity)
	if err != nil {
		return coredatasource.Record{}, err
	}
	for _, record := range records {
		if record.ID == req.ID {
			return record, nil
		}
	}
	return coredatasource.Record{}, coredatasource.ErrNotFound
}

func (a catalogAccessor) BatchGet(ctx context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	out := coredatasource.BatchGetResult{Datasource: coredatasource.Name(Name), Entity: req.Entity}
	for _, id := range req.IDs {
		record, err := a.Get(ctx, coredatasource.GetRequest{Entity: req.Entity, ID: id})
		if err != nil {
			out.Errors = append(out.Errors, coredatasource.BatchGetError{ID: id, Message: err.Error()})
			continue
		}
		out.Records = append(out.Records, record)
	}
	return out, nil
}

func (a catalogAccessor) records(ctx context.Context, entity coredatasource.EntityType) ([]coredatasource.Record, error) {
	if a.registry == nil {
		return nil, nil
	}
	switch entity {
	case CatalogSourceEntity, CatalogEntityEntity, CatalogRelationEntity, CatalogViewEntity:
	default:
		return nil, fmt.Errorf("datasource catalog entity %q is not supported", entity)
	}
	allowedNames, err := allowedSet(ctx)
	if err != nil {
		return nil, err
	}
	var records []coredatasource.Record
	for _, accessor := range a.registry.All() {
		spec := accessor.Spec()
		if !allowedNames[spec.Name] {
			continue
		}
		switch entity {
		case CatalogSourceEntity:
			records = append(records, a.sourceCatalogRecord(accessor))
		case CatalogEntityEntity:
			for _, entitySpec := range accessor.Entities() {
				records = append(records, entityCatalogRecord(accessor, entitySpec))
			}
		case CatalogRelationEntity:
			for _, entitySpec := range accessor.Entities() {
				for _, relation := range entitySpec.Relations {
					records = append(records, relationCatalogRecord(accessor, entitySpec, relation))
				}
			}
		case CatalogViewEntity:
			for _, view := range a.viewsForAccessor(accessor) {
				records = append(records, viewCatalogRecord(accessor, view))
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Entity != records[j].Entity {
			return records[i].Entity < records[j].Entity
		}
		return records[i].ID < records[j].ID
	})
	return records, nil
}

func (a catalogAccessor) viewsForAccessor(accessor coredatasource.Accessor) []coredata.ViewSpec {
	if accessor == nil {
		return nil
	}
	spec := accessor.Spec()
	var views []coredata.ViewSpec
	for _, source := range a.dataSources {
		if source.Name != coredata.SourceName(spec.Name) && source.Kind != spec.Kind {
			continue
		}
		views = append(views, source.Views...)
	}
	return views
}

func (a catalogAccessor) sourceCatalogRecord(accessor coredatasource.Accessor) coredatasource.Record {
	spec := accessor.Spec()
	entityTypes := make([]string, 0, len(accessor.Entities()))
	for _, entity := range accessor.Entities() {
		entityTypes = append(entityTypes, string(entity.Type))
	}
	sort.Strings(entityTypes)
	metadata := map[string]string{
		"name":         string(spec.Name),
		"kind":         spec.Kind,
		"connector":    spec.Connector,
		"entity_count": strconv.Itoa(len(entityTypes)),
		"view_count":   strconv.Itoa(len(a.viewsForAccessor(accessor))),
		"entities":     strings.Join(entityTypes, ","),
	}
	return coredatasource.Record{
		ID:         string(spec.Name),
		Datasource: coredatasource.Name(Name),
		Entity:     CatalogSourceEntity,
		Title:      string(spec.Name),
		Content:    strings.TrimSpace(strings.Join([]string{spec.Description, spec.Kind, spec.Connector, strings.Join(entityTypes, " ")}, " ")),
		Metadata:   cleanCatalogMetadata(metadata),
		Raw:        spec,
	}
}

func entityCatalogRecord(accessor coredatasource.Accessor, entity coredatasource.EntitySpec) coredatasource.Record {
	spec := accessor.Spec()
	capabilities := stringifyCapabilities(entityCapabilities(accessor, entity))
	fields := make([]string, 0, len(entity.Fields))
	for _, field := range entity.Fields {
		fields = append(fields, field.Name)
	}
	relations := make([]string, 0, len(entity.Relations))
	for _, relation := range entity.Relations {
		relations = append(relations, relation.Name+"->"+string(relation.TargetEntity))
	}
	sort.Strings(fields)
	sort.Strings(relations)
	metadata := map[string]string{
		"datasource":   string(spec.Name),
		"entity":       string(entity.Type),
		"capabilities": strings.Join(capabilities, ","),
		"fields":       strings.Join(fields, ","),
		"relations":    strings.Join(relations, ","),
	}
	return coredatasource.Record{
		ID:         string(spec.Name) + "/" + string(entity.Type),
		Datasource: coredatasource.Name(Name),
		Entity:     CatalogEntityEntity,
		Title:      string(entity.Type),
		Content:    strings.TrimSpace(strings.Join([]string{entity.Description, strings.Join(capabilities, " "), strings.Join(fields, " "), strings.Join(relations, " ")}, " ")),
		Metadata:   cleanCatalogMetadata(metadata),
		Raw:        entity,
	}
}

func relationCatalogRecord(accessor coredatasource.Accessor, entity coredatasource.EntitySpec, relation coredatasource.RelationSpec) coredatasource.Record {
	spec := accessor.Spec()
	metadata := map[string]string{
		"datasource":    string(spec.Name),
		"entity":        string(entity.Type),
		"relation":      relation.Name,
		"target_entity": string(relation.TargetEntity),
		"exact":         strconv.FormatBool(relation.Exact),
	}
	title := string(entity.Type) + "." + relation.Name
	return coredatasource.Record{
		ID:         string(spec.Name) + "/" + string(entity.Type) + "/" + relation.Name,
		Datasource: coredatasource.Name(Name),
		Entity:     CatalogRelationEntity,
		Title:      title,
		Content:    strings.TrimSpace(strings.Join([]string{relation.Description, title, string(relation.TargetEntity)}, " ")),
		Metadata:   cleanCatalogMetadata(metadata),
		Raw:        relation,
	}
}

func viewCatalogRecord(accessor coredatasource.Accessor, view coredata.ViewSpec) coredatasource.Record {
	spec := accessor.Spec()
	fields := make([]string, 0, len(view.Fields))
	for _, field := range view.Fields {
		fields = append(fields, field.Name)
	}
	includes := make([]string, 0, len(view.Includes))
	for _, include := range view.Includes {
		includes = append(includes, string(include.Relation)+"->"+string(include.Target))
	}
	hints := make([]string, 0, len(view.QueryHints))
	for _, hint := range view.QueryHints {
		hints = append(hints, string(hint))
	}
	sort.Strings(fields)
	sort.Strings(includes)
	sort.Strings(hints)
	metadata := map[string]string{
		"datasource":  string(spec.Name),
		"view":        string(view.Name),
		"entity":      string(view.Entity),
		"source":      string(view.Source),
		"includes":    strings.Join(includes, ","),
		"fields":      strings.Join(fields, ","),
		"query_hints": strings.Join(hints, ","),
	}
	return coredatasource.Record{
		ID:         string(spec.Name) + "/" + string(view.Name),
		Datasource: coredatasource.Name(Name),
		Entity:     CatalogViewEntity,
		Title:      string(view.Name),
		Content:    strings.TrimSpace(strings.Join([]string{view.Description, string(view.Entity), string(view.Source), strings.Join(includes, " "), strings.Join(fields, " "), strings.Join(hints, " ")}, " ")),
		Metadata:   cleanCatalogMetadata(metadata),
		Raw:        view,
	}
}

func filterCatalogRecords(records []coredatasource.Record, query string, filters map[string]string) []coredatasource.Record {
	query = strings.ToLower(strings.TrimSpace(query))
	var out []coredatasource.Record
	for _, record := range records {
		if query != "" && !catalogRecordMatches(record, query) {
			continue
		}
		if !catalogRecordMatchesFilters(record, filters) {
			continue
		}
		out = append(out, record)
	}
	return out
}

func catalogRecordMatches(record coredatasource.Record, query string) bool {
	values := []string{record.ID, string(record.Datasource), string(record.Entity), record.Title, record.Content}
	for key, value := range record.Metadata {
		values = append(values, key, value)
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func catalogRecordMatchesFilters(record coredatasource.Record, filters map[string]string) bool {
	for key, want := range filters {
		want = strings.ToLower(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		if strings.ToLower(strings.TrimSpace(record.Metadata[key])) != want {
			return false
		}
	}
	return true
}

func paginateCatalogRecords(records []coredatasource.Record, limit int, cursor string) ([]coredatasource.Record, string, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	offset := 0
	if strings.TrimSpace(cursor) != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 {
			return nil, "", fmt.Errorf("invalid datasource catalog cursor %q", cursor)
		}
		offset = parsed
	}
	if offset >= len(records) {
		return nil, "", nil
	}
	records = records[offset:]
	next := ""
	if len(records) > limit {
		records = records[:limit]
		next = strconv.Itoa(offset + limit)
	}
	return records, next, nil
}

func stringifyCapabilities(capabilities []coredatasource.EntityCapability) []string {
	out := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		out = append(out, string(capability))
	}
	sort.Strings(out)
	return out
}

func cleanCatalogMetadata(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	return out
}
