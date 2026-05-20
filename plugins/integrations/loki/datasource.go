package loki

import (
	"context"
	"fmt"
	"strings"

	coredata "github.com/fluxplane/agentruntime/core/data"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimedata "github.com/fluxplane/agentruntime/runtime/data"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
)

type DetectedEndpoint struct {
	ID           string            `json:"id" datasource:"id" jsonschema:"description=Stable endpoint candidate id."`
	URL          string            `json:"url" datasource:"url,searchable" jsonschema:"description=Candidate Loki URL."`
	Namespace    string            `json:"namespace,omitempty" datasource:"filterable" jsonschema:"description=Kubernetes namespace."`
	ResourceKind string            `json:"resource_kind,omitempty" datasource:"filterable" jsonschema:"description=Source resource kind."`
	ResourceName string            `json:"resource_name,omitempty" datasource:"searchable,filterable" jsonschema:"description=Source resource name."`
	PortName     string            `json:"port_name,omitempty" datasource:"filterable" jsonschema:"description=Source port name."`
	Port         int               `json:"port,omitempty" datasource:"filterable" jsonschema:"description=Source port number."`
	Labels       map[string]string `json:"labels,omitempty" datasource:"searchable" jsonschema:"description=Source labels."`
	Score        float64           `json:"score,omitempty" datasource:"filterable" jsonschema:"description=Discovery score."`
	ProbeStatus  string            `json:"probe_status,omitempty" datasource:"filterable" jsonschema:"description=Probe status."`
	ProbeError   string            `json:"probe_error,omitempty" jsonschema:"description=Probe error."`
	Version      string            `json:"version,omitempty" datasource:"filterable" jsonschema:"description=Loki version."`
	Provenance   string            `json:"provenance,omitempty" datasource:"searchable" jsonschema:"description=Candidate match reasons."`
}

type Label struct {
	ID        string `json:"id" datasource:"id" jsonschema:"description=Label record id."`
	Name      string `json:"name" datasource:"searchable,filterable" jsonschema:"description=Label name."`
	Value     string `json:"value,omitempty" datasource:"searchable,filterable" jsonschema:"description=Label value."`
	Namespace string `json:"namespace,omitempty" datasource:"filterable" jsonschema:"description=Namespace scope."`
	Since     string `json:"since,omitempty" datasource:"filterable" jsonschema:"description=Window start."`
	Until     string `json:"until,omitempty" datasource:"filterable" jsonschema:"description=Window end."`
}

// DataSourceSpec describes Loki live data.
func DataSourceSpec() coredata.SourceSpec {
	return runtimedata.SourceFromDatasource(Name, Name, entitySpecs())
}

func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
	return []coredatasource.Provider{lokiDatasourceProvider{plugin: p}}, nil
}

type lokiDatasourceProvider struct {
	plugin Plugin
}

func (p lokiDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return entitySpecs()
}

func (p lokiDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if spec.Kind != Name {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	selected := entitySpecs()
	if len(spec.Entities) > 0 {
		var err error
		selected, err = runtimedatasource.SelectEntities(Name, entitySpecs(), spec.Entities)
		if err != nil {
			return nil, err
		}
	}
	plugin := p.plugin
	plugin.cfg = mergeDatasourceConfig(plugin.cfg, spec.Config)
	return &lokiAccessor{plugin: plugin, spec: spec, entities: selected}, nil
}

type lokiAccessor struct {
	plugin   Plugin
	spec     coredatasource.Spec
	entities []coredatasource.EntitySpec
}

func (a *lokiAccessor) Spec() coredatasource.Spec { return a.spec }

func (a *lokiAccessor) Entities() []coredatasource.EntitySpec {
	return append([]coredatasource.EntitySpec(nil), a.entities...)
}

func (a *lokiAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = LogEntryEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	switch entity {
	case DetectedEndpointEntity:
		return a.searchEndpoints(ctx, req)
	case LabelEntity:
		return a.searchLabels(ctx, req)
	case LogEntryEntity, StreamEntity:
		return a.searchLogs(ctx, req, entity)
	default:
		return coredatasource.SearchResult{}, fmt.Errorf("entity %q does not support search", entity)
	}
}

func (a *lokiAccessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	search, err := a.Search(ctx, coredatasource.SearchRequest{Entity: req.Entity, Limit: req.Limit, Filters: req.Filters})
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	return coredatasource.ListResult{Datasource: search.Datasource, Entity: search.Entity, Records: search.Records, Total: search.Total, Complete: true}, nil
}

func (a *lokiAccessor) searchEndpoints(context.Context, coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	var records []coredatasource.Record
	for _, record := range a.plugin.endpointRegistry().List(Name) {
		resolved := record.Resolved
		if resolved.URL == "" {
			resolved.URL = record.Spec.URL
		}
		raw := DetectedEndpoint{
			ID:           string(resolved.Ref),
			URL:          resolved.URL,
			Namespace:    resolved.Source.Namespace,
			ResourceKind: resolved.Source.Kind,
			ResourceName: resolved.Source.Name,
			Labels:       record.Spec.Labels,
			Provenance:   record.Metadata["provenance"],
		}
		records = append(records, coredatasource.Record{Datasource: a.spec.Name, Entity: DetectedEndpointEntity, ID: raw.ID, URL: raw.URL, Title: raw.ResourceName, Content: raw.Provenance, Raw: raw, Metadata: map[string]string{"namespace": raw.Namespace, "resource_kind": raw.ResourceKind}})
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: DetectedEndpointEntity, Records: records, Total: len(records)}, nil
}

func (a *lokiAccessor) searchLabels(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	labels := a.plugin.labels()(operationContext(ctx), LabelsInput{Label: req.Filters["label"], Namespace: req.Filters["namespace"], Since: req.Filters["since"], Until: req.Filters["until"], Limit: req.Limit})
	if labels.Status != operation.StatusOK {
		if labels.Error != nil {
			return coredatasource.SearchResult{}, fmt.Errorf("%s", labels.Error.Message)
		}
		return coredatasource.SearchResult{}, fmt.Errorf("loki labels failed")
	}
	out, ok := labels.Output.(LabelsOutput)
	if !ok {
		return coredatasource.SearchResult{}, fmt.Errorf("unexpected labels output")
	}
	var records []coredatasource.Record
	for _, value := range out.Values {
		raw := Label{ID: out.Label + ":" + value, Name: out.Label, Value: value, Namespace: req.Filters["namespace"], Since: req.Filters["since"], Until: req.Filters["until"]}
		if out.Label == "" {
			raw.ID = value
			raw.Name = value
		}
		records = append(records, coredatasource.Record{Datasource: a.spec.Name, Entity: LabelEntity, ID: raw.ID, Title: firstNonEmpty(raw.Value, raw.Name), Raw: raw})
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: LabelEntity, Records: records, Total: len(records)}, nil
}

func (a *lokiAccessor) searchLogs(ctx context.Context, req coredatasource.SearchRequest, entity coredatasource.EntityType) (coredatasource.SearchResult, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		query = req.Filters["query"]
	}
	if query == "" {
		return coredatasource.SearchResult{}, fmt.Errorf("loki search requires LogQL query")
	}
	out, err := a.plugin.runQuery(operationContext(ctx), QueryInput{Query: query, Namespace: req.Filters["namespace"], Since: req.Filters["since"], Until: req.Filters["until"], Limit: req.Limit})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	var records []coredatasource.Record
	if entity == StreamEntity {
		for _, stream := range out.Streams {
			raw := stream
			records = append(records, coredatasource.Record{Datasource: a.spec.Name, Entity: entity, ID: raw.ID, Title: raw.App, Content: fmt.Sprintf("%d entries", raw.EntryCount), Raw: raw, Metadata: map[string]string{"namespace": raw.Namespace, "pod": raw.Pod}})
		}
	} else {
		for _, entry := range out.Entries {
			raw := entry
			records = append(records, coredatasource.Record{Datasource: a.spec.Name, Entity: entity, ID: raw.ID, Title: raw.Timestamp, Content: raw.Line, Raw: raw, Metadata: map[string]string{"namespace": raw.Namespace, "pod": raw.Pod, "container": raw.Container}})
		}
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: entity, Records: records, Total: len(records)}, nil
}

func entitySpecs() []coredatasource.EntitySpec {
	logEntry := runtimedatasource.EntityOf[LogEntry](LogEntryEntity, "Loki log entry.")
	logEntry.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityList}
	stream := runtimedatasource.EntityOf[Stream](StreamEntity, "Loki log stream.")
	stream.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityList}
	label := runtimedatasource.EntityOf[Label](LabelEntity, "Loki label name or value.")
	label.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityList}
	endpoint := runtimedatasource.EntityOf[DetectedEndpoint](DetectedEndpointEntity, "Discovered Loki endpoint.")
	endpoint.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityList}
	return []coredatasource.EntitySpec{logEntry, stream, label, endpoint}
}

func mergeDatasourceConfig(cfg Config, config map[string]string) Config {
	if value := strings.TrimSpace(config["url"]); value != "" {
		cfg.URL = value
	}
	if value := strings.TrimSpace(config["url_env"]); value != "" {
		cfg.URLEnv = value
	}
	if value := strings.TrimSpace(config["endpoint_ref"]); value != "" {
		cfg.EndpointRef = value
	}
	if value := strings.TrimSpace(config["default_namespace"]); value != "" {
		cfg.DefaultNamespace = value
	}
	if value := strings.TrimSpace(config["default_since"]); value != "" {
		cfg.DefaultSince = value
	}
	return normalizeConfig(cfg)
}

func operationContext(ctx context.Context) operation.Context {
	return operation.NewContext(ctx, event.Discard())
}
