package openapi

import (
	"context"
	"fmt"
	"sort"
	"strings"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimedatasource "github.com/fluxplane/fluxplane-core/runtime/datasource"
	"github.com/getkin/kin-openapi/openapi3"
)

type docRecord struct {
	ID      string                    `json:"id" datasource:"id" jsonschema:"description=Stable documentation record id."`
	Entity  coredatasource.EntityType `json:"entity" datasource:"filterable" jsonschema:"description=OpenAPI documentation entity."`
	Title   string                    `json:"title" datasource:"searchable" jsonschema:"description=Record title."`
	Content string                    `json:"content" datasource:"searchable,corpus" jsonschema:"description=Record content."`
	URL     string                    `json:"url,omitempty" datasource:"url" jsonschema:"description=Documentation URL."`
	Method  string                    `json:"method,omitempty" datasource:"filterable" jsonschema:"description=HTTP method."`
	Path    string                    `json:"path,omitempty" datasource:"searchable,filterable" jsonschema:"description=OpenAPI path."`
	Name    string                    `json:"name,omitempty" datasource:"searchable,filterable" jsonschema:"description=OpenAPI component or field name."`
	Status  string                    `json:"status,omitempty" datasource:"filterable" jsonschema:"description=Response status."`
	Scheme  string                    `json:"scheme,omitempty" datasource:"filterable" jsonschema:"description=Security scheme."`
	Raw     any                       `json:"raw,omitempty" jsonschema:"description=Original record metadata."`
}

func (p Plugin) DatasourceProviders(ctx context.Context, _ pluginhost.Context) ([]coredatasource.Provider, error) {
	generated, err := p.generated(ctx)
	if err != nil {
		return nil, err
	}
	return []coredatasource.Provider{openAPIProvider{docs: generated.Docs}}, nil
}

type openAPIProvider struct {
	docs []docRecord
}

func (p openAPIProvider) Entities() []coredatasource.EntitySpec {
	return entitySpecs()
}

func (p openAPIProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if spec.Kind != Name {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	entities := entitySpecs()
	if len(spec.Entities) > 0 {
		selected, err := runtimedatasource.SelectEntities(Name, entities, spec.Entities)
		if err != nil {
			return nil, err
		}
		entities = selected
	}
	return &openAPIAccessor{spec: spec, entities: entities, docs: p.docs}, nil
}

type openAPIAccessor struct {
	spec     coredatasource.Spec
	entities []coredatasource.EntitySpec
	docs     []docRecord
}

func (a *openAPIAccessor) Spec() coredatasource.Spec { return a.spec }

func (a *openAPIAccessor) Entities() []coredatasource.EntitySpec {
	return append([]coredatasource.EntitySpec(nil), a.entities...)
}

func (a *openAPIAccessor) Search(_ context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = OperationEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	var records []coredatasource.Record
	for _, doc := range a.docs {
		if doc.Entity != entity {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(doc.Title+" "+doc.Content+" "+doc.Name+" "+doc.Path), query) {
			continue
		}
		records = append(records, a.record(doc))
		if len(records) >= limit {
			break
		}
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: entity, Records: records, Total: len(records)}, nil
}

func (a *openAPIAccessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	search, err := a.Search(ctx, coredatasource.SearchRequest{Entity: req.Entity, Limit: req.Limit, Filters: req.Filters})
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	return coredatasource.ListResult{Datasource: search.Datasource, Entity: search.Entity, Records: search.Records, Total: search.Total, Complete: true}, nil
}

func (a *openAPIAccessor) Get(_ context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	for _, doc := range a.docs {
		if doc.Entity == req.Entity && doc.ID == req.ID {
			return a.record(doc), nil
		}
	}
	return coredatasource.Record{}, coredatasource.ErrNotFound
}

func (a *openAPIAccessor) Corpus(_ context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	entity := req.Entity
	if entity == "" {
		entity = OperationEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.CorpusPage{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	var records []coredatasource.Record
	for _, doc := range a.docs {
		if doc.Entity == entity {
			records = append(records, a.record(doc))
		}
	}
	return runtimedatasource.CorpusPageFromRecords(records, len(records), req.Limit, 1), nil
}

func (a *openAPIAccessor) record(doc docRecord) coredatasource.Record {
	metadata := map[string]string{}
	if doc.Method != "" {
		metadata["method"] = doc.Method
	}
	if doc.Path != "" {
		metadata["path"] = doc.Path
	}
	if doc.Name != "" {
		metadata["name"] = doc.Name
	}
	if doc.Status != "" {
		metadata["status"] = doc.Status
	}
	if doc.Scheme != "" {
		metadata["scheme"] = doc.Scheme
	}
	return coredatasource.Record{
		Datasource: a.spec.Name,
		Entity:     doc.Entity,
		ID:         doc.ID,
		Title:      doc.Title,
		Content:    doc.Content,
		URL:        doc.URL,
		Metadata:   metadata,
		Raw:        doc,
	}
}

func entitySpecs() []coredatasource.EntitySpec {
	operation := runtimedatasource.EntityOf[docRecord](OperationEntity, "OpenAPI operation documentation.")
	operation.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityList, coredatasource.EntityCapabilityGet, coredatasource.EntityCapabilityIndex}
	schema := runtimedatasource.EntityOf[docRecord](SchemaEntity, "OpenAPI schema documentation.")
	schema.Capabilities = operation.Capabilities
	parameter := runtimedatasource.EntityOf[docRecord](ParameterEntity, "OpenAPI parameter documentation.")
	parameter.Capabilities = operation.Capabilities
	response := runtimedatasource.EntityOf[docRecord](ResponseEntity, "OpenAPI response documentation.")
	response.Capabilities = operation.Capabilities
	security := runtimedatasource.EntityOf[docRecord](SecuritySchemeEntity, "OpenAPI security scheme documentation.")
	security.Capabilities = operation.Capabilities
	return []coredatasource.EntitySpec{operation, schema, parameter, response, security}
}

func docsForSpec(datasource coredatasource.Name, doc *openapi3.T) []docRecord {
	var docs []docRecord
	for _, path := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Value(path)
		if item == nil {
			continue
		}
		for _, method := range sortedOperationMethods(item.Operations()) {
			op := item.GetOperation(method)
			if op == nil {
				continue
			}
			name := operationBaseName(method, path, op)
			title := strings.ToUpper(method) + " " + path
			content := strings.Join(nonEmpty([]string{op.Summary, op.Description, "Tags: " + strings.Join(op.Tags, ", ")}), "\n")
			docs = append(docs, docRecord{ID: "operation:" + name, Entity: OperationEntity, Title: title, Content: content, Method: strings.ToUpper(method), Path: path, Name: name})
			for _, param := range append(parametersFromRefs(item.Parameters), parametersFromRefs(op.Parameters)...) {
				if param == nil {
					continue
				}
				id := "parameter:" + name + ":" + param.In + ":" + param.Name
				docs = append(docs, docRecord{ID: id, Entity: ParameterEntity, Title: param.In + " parameter " + param.Name, Content: param.Description, Method: strings.ToUpper(method), Path: path, Name: param.Name})
			}
			if op.Responses != nil {
				for _, status := range op.Responses.Keys() {
					respRef := op.Responses.Value(status)
					if respRef == nil || respRef.Value == nil {
						continue
					}
					desc := ""
					if respRef.Value.Description != nil {
						desc = *respRef.Value.Description
					}
					docs = append(docs, docRecord{ID: "response:" + name + ":" + status, Entity: ResponseEntity, Title: "Response " + status + " for " + title, Content: desc, Method: strings.ToUpper(method), Path: path, Status: status})
				}
			}
		}
	}
	if doc.Components != nil {
		schemaNames := make([]string, 0, len(doc.Components.Schemas))
		for name := range doc.Components.Schemas {
			schemaNames = append(schemaNames, name)
		}
		sort.Strings(schemaNames)
		for _, name := range schemaNames {
			ref := doc.Components.Schemas[name]
			docs = append(docs, docRecord{ID: "schema:" + name, Entity: SchemaEntity, Title: "Schema " + name, Content: schemaSummary(ref), Name: name})
		}
		securityNames := make([]string, 0, len(doc.Components.SecuritySchemes))
		for name := range doc.Components.SecuritySchemes {
			securityNames = append(securityNames, name)
		}
		sort.Strings(securityNames)
		for _, name := range securityNames {
			ref := doc.Components.SecuritySchemes[name]
			if ref == nil || ref.Value == nil {
				continue
			}
			scheme := ref.Value
			content := strings.Join(nonEmpty([]string{scheme.Description, "type: " + scheme.Type, "scheme: " + scheme.Scheme, "in: " + scheme.In, "name: " + scheme.Name}), "\n")
			docs = append(docs, docRecord{ID: "security:" + name, Entity: SecuritySchemeEntity, Title: "Security scheme " + name, Content: content, Name: name, Scheme: firstNonEmpty(scheme.Scheme, scheme.Type)})
		}
	}
	_ = datasource
	return docs
}

func docDescription(doc *openapi3.T) string {
	if doc == nil || doc.Info == nil {
		return ""
	}
	return firstNonEmpty(doc.Info.Description, doc.Info.Title)
}

func schemaSummary(ref *openapi3.SchemaRef) string {
	if ref == nil || ref.Value == nil {
		return ""
	}
	schema := ref.Value
	var parts []string
	parts = append(parts, nonEmpty([]string{schema.Title, schema.Description})...)
	if schema.Type != nil {
		parts = append(parts, "type: "+strings.Join(*schema.Type, ", "))
	}
	if len(schema.Required) > 0 {
		parts = append(parts, "required: "+strings.Join(schema.Required, ", "))
	}
	if len(schema.Properties) > 0 {
		var props []string
		for name := range schema.Properties {
			props = append(props, name)
		}
		sort.Strings(props)
		parts = append(parts, "properties: "+strings.Join(props, ", "))
	}
	return strings.Join(parts, "\n")
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
