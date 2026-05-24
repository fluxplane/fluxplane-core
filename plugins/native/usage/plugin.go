// Package usageplugin exposes usage events as a datasource.
package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	coreusage "github.com/fluxplane/fluxplane-core/core/usage"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimethread "github.com/fluxplane/fluxplane-core/runtime/thread"
)

const (
	Name           = "usage"
	DatasourceName = coredatasource.Name("usage")

	EntityRecord = coredatasource.EntityType("usage.record")

	defaultLimit = 25
	maxLimit     = 250
)

// Plugin contributes a datasource over persisted usage events.
type Plugin struct {
	threads corethread.Store
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}

// New returns a usage plugin. If threads is nil, DatasourceProviders builds a
// thread projection from the plugin host event store.
func New(threads corethread.Store) Plugin {
	return Plugin{threads: threads}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Usage datasource over persisted runtime events."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{Datasources: []coredatasource.Spec{DatasourceSpec()}}, nil
}

func (p Plugin) DatasourceProviders(_ context.Context, ctx pluginhost.Context) ([]coredatasource.Provider, error) {
	threads := p.threads
	if threads == nil {
		if ctx.EventStore == nil {
			return nil, fmt.Errorf("usage: event store is nil")
		}
		store, err := runtimethread.NewStore(ctx.EventStore)
		if err != nil {
			return nil, err
		}
		threads = store
	}
	return []coredatasource.Provider{provider{threads: threads}}, nil
}

// DatasourceSpec returns the configured usage datasource.
func DatasourceSpec() coredatasource.Spec {
	return coredatasource.Spec{
		Name:        DatasourceName,
		Description: "Persisted runtime usage records, including LLM tokens, costs, request counts, bytes, and wall time.",
		Entities:    []coredatasource.EntityType{EntityRecord},
		Kind:        Name,
	}
}

type provider struct {
	threads corethread.Store
}

var _ coredatasource.Provider = provider{}

func (p provider) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{entitySpec()}
}

func (p provider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if p.threads == nil {
		return nil, fmt.Errorf("usage: thread store is nil")
	}
	if spec.Kind != "" && spec.Kind != Name {
		return nil, fmt.Errorf("usage: unsupported kind %q", spec.Kind)
	}
	return accessor{threads: p.threads, spec: spec}, nil
}

type accessor struct {
	threads corethread.Store
	spec    coredatasource.Spec
}

var _ coredatasource.Accessor = accessor{}
var _ coredatasource.Searcher = accessor{}
var _ coredatasource.Lister = accessor{}
var _ coredatasource.Getter = accessor{}
var _ coredatasource.BatchGetter = accessor{}
var _ coredatasource.CorpusProvider = accessor{}

func (a accessor) Spec() coredatasource.Spec {
	if a.spec.Name == "" {
		return DatasourceSpec()
	}
	return a.spec
}

func (a accessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{entitySpec()}
}

func (a accessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	if err := validateEntity(req.Entity); err != nil {
		return coredatasource.SearchResult{}, err
	}
	records, err := a.records(ctx, req.Filters)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	filtered := records[:0]
	for _, record := range records {
		if !matchQuery(record, query) || !matchFilters(record, req.Filters) {
			continue
		}
		filtered = append(filtered, record)
	}
	sortRecordsNewestFirst(filtered)
	total := len(filtered)
	limit := normalizeLimit(req.Limit)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return coredatasource.SearchResult{
		Datasource: a.Spec().Name,
		Entity:     EntityRecord,
		Records:    filtered,
		Total:      total,
	}, nil
}

func (a accessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	if err := validateEntity(req.Entity); err != nil {
		return coredatasource.ListResult{}, err
	}
	records, err := a.records(ctx, req.Filters)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	filtered := records[:0]
	for _, record := range records {
		if !matchFilters(record, req.Filters) {
			continue
		}
		filtered = append(filtered, record)
	}
	sortRecordsNewestFirst(filtered)
	offset, err := parseCursor(req.Cursor)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	total := len(filtered)
	limit := normalizeLimit(req.Limit)
	if offset >= len(filtered) {
		return coredatasource.ListResult{Datasource: a.Spec().Name, Entity: EntityRecord, Total: total, Complete: true}, nil
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	out := coredatasource.ListResult{
		Datasource: a.Spec().Name,
		Entity:     EntityRecord,
		Records:    filtered[offset:end],
		Total:      total,
		Complete:   end >= len(filtered),
	}
	if !out.Complete {
		out.NextCursor = strconv.Itoa(end)
	}
	return out, nil
}

func (a accessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if err := validateEntity(req.Entity); err != nil {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	threadID, sequence, ok := parseRecordID(req.ID)
	if !ok {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	snapshot, err := a.threads.Read(ctx, corethread.ReadParams{ID: corethread.ID(threadID)})
	if errors.Is(err, corethread.ErrNotFound) {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	if err != nil {
		return coredatasource.Record{}, err
	}
	for _, record := range projectSnapshot(a.Spec().Name, snapshot) {
		if record.Metadata["thread_id"] == threadID && record.Metadata["sequence"] == strconv.FormatInt(sequence, 10) {
			return record, nil
		}
	}
	return coredatasource.Record{}, coredatasource.ErrNotFound
}

func (a accessor) BatchGet(ctx context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	if err := validateEntity(req.Entity); err != nil {
		return coredatasource.BatchGetResult{}, err
	}
	out := coredatasource.BatchGetResult{Datasource: a.Spec().Name, Entity: EntityRecord}
	for _, id := range req.IDs {
		record, err := a.Get(ctx, coredatasource.GetRequest{Entity: EntityRecord, ID: id})
		if err != nil {
			out.Errors = append(out.Errors, coredatasource.BatchGetError{ID: id, Message: err.Error()})
			continue
		}
		out.Records = append(out.Records, record)
	}
	return out, nil
}

func (a accessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	if err := validateEntity(req.Entity); err != nil {
		return coredatasource.CorpusPage{}, err
	}
	records, err := a.records(ctx, nil)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	sortRecordsNewestFirst(records)
	offset, err := parseCursor(req.Cursor)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	limit := normalizeLimit(req.Limit)
	if offset >= len(records) {
		return coredatasource.CorpusPage{Complete: true}, nil
	}
	end := offset + limit
	if end > len(records) {
		end = len(records)
	}
	out := coredatasource.CorpusPage{Complete: end >= len(records)}
	for _, record := range records[offset:end] {
		out.Documents = append(out.Documents, corpusDocument(record))
	}
	if !out.Complete {
		out.NextCursor = strconv.Itoa(end)
	}
	return out, nil
}

func (a accessor) records(ctx context.Context, filters map[string]string) ([]coredatasource.Record, error) {
	if threadID := strings.TrimSpace(filters["thread_id"]); threadID != "" {
		snapshot, err := a.threads.Read(ctx, corethread.ReadParams{ID: corethread.ID(threadID)})
		if errors.Is(err, corethread.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return projectSnapshot(a.Spec().Name, snapshot), nil
	}
	page, err := a.threads.List(ctx, corethread.ListParams{IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	var out []coredatasource.Record
	for _, snapshot := range page.Threads {
		out = append(out, projectSnapshot(a.Spec().Name, snapshot)...)
	}
	return out, nil
}

func entitySpec() coredatasource.EntitySpec {
	return coredatasource.EntitySpec{
		Type:        EntityRecord,
		Description: "One persisted usage.recorded runtime event.",
		Capabilities: []coredatasource.EntityCapability{
			coredatasource.EntityCapabilitySearch,
			coredatasource.EntityCapabilityList,
			coredatasource.EntityCapabilityGet,
			coredatasource.EntityCapabilitySemanticSearch,
		},
		Fields: []coredatasource.FieldSpec{
			{Name: "thread_id", Type: coredatasource.FieldString, Filterable: true},
			{Name: "sequence", Type: coredatasource.FieldNumber, Filterable: true, Identifier: true},
			{Name: "time", Type: coredatasource.FieldString, Filterable: true, Sortable: true},
			{Name: "session", Type: coredatasource.FieldString, Filterable: true},
			{Name: "source", Type: coredatasource.FieldString, Filterable: true, Searchable: true},
			{Name: "subject_kind", Type: coredatasource.FieldString, Filterable: true, Searchable: true},
			{Name: "provider", Type: coredatasource.FieldString, Filterable: true, Searchable: true},
			{Name: "name", Type: coredatasource.FieldString, Filterable: true, Searchable: true},
			{Name: "input_tokens", Type: coredatasource.FieldNumber, Filterable: true},
			{Name: "cached_input_tokens", Type: coredatasource.FieldNumber, Filterable: true},
			{Name: "output_tokens", Type: coredatasource.FieldNumber, Filterable: true},
			{Name: "reasoning_tokens", Type: coredatasource.FieldNumber, Filterable: true},
			{Name: "total_tokens", Type: coredatasource.FieldNumber, Filterable: true},
			{Name: "cost", Type: coredatasource.FieldNumber, Filterable: true},
			{Name: "currency", Type: coredatasource.FieldString, Filterable: true},
			{Name: "requests", Type: coredatasource.FieldNumber, Filterable: true},
			{Name: "bytes", Type: coredatasource.FieldNumber, Filterable: true},
			{Name: "wall_time_ms", Type: coredatasource.FieldNumber, Filterable: true},
		},
	}
}

func projectSnapshot(datasource coredatasource.Name, snapshot corethread.Snapshot) []coredatasource.Record {
	var out []coredatasource.Record
	for _, stored := range snapshot.Events {
		payload, name := unwrapRuntimeEvent(stored.Event.Payload, stored.Event.Name)
		if name != coreusage.EventRecordedName {
			continue
		}
		recorded, ok := decodeUsage(payload)
		if !ok || recorded.Empty() {
			continue
		}
		out = append(out, usageRecord(datasource, snapshot, stored, recorded))
	}
	return out
}

func unwrapRuntimeEvent(payload any, name event.Name) (any, event.Name) {
	switch p := payload.(type) {
	case coresession.RuntimeEmitted:
		return p.Payload, p.Name
	case *coresession.RuntimeEmitted:
		if p != nil {
			return p.Payload, p.Name
		}
	}
	return payload, name
}

func decodeUsage(payload any) (coreusage.Recorded, bool) {
	switch p := payload.(type) {
	case coreusage.Recorded:
		return p, true
	case *coreusage.Recorded:
		if p != nil {
			return *p, true
		}
		return coreusage.Recorded{}, false
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return coreusage.Recorded{}, false
	}
	var recorded coreusage.Recorded
	if err := json.Unmarshal(raw, &recorded); err != nil {
		return coreusage.Recorded{}, false
	}
	return recorded, true
}

func usageRecord(datasource coredatasource.Name, snapshot corethread.Snapshot, stored corethread.Record, recorded coreusage.Recorded) coredatasource.Record {
	metadata := map[string]string{
		"thread_id": string(snapshot.ID),
		"sequence":  strconv.FormatInt(int64(stored.Sequence), 10),
		"branch_id": string(stored.BranchID),
		"time":      stored.Event.Time.Format(time.RFC3339Nano),
	}
	for key, value := range snapshot.Metadata {
		if value != "" {
			metadata[key] = value
		}
	}
	if stored.Event.Scope.SessionID != "" {
		metadata["session"] = stored.Event.Scope.SessionID
	}
	if stored.Event.Scope.ChannelID != "" {
		metadata["channel"] = stored.Event.Scope.ChannelID
	}
	if stored.Event.Scope.UserID != "" {
		metadata["user"] = stored.Event.Scope.UserID
	}
	metadata["source"] = recorded.Source
	metadata["subject_kind"] = string(recorded.Subject.Kind)
	metadata["provider"] = recorded.Subject.Provider
	metadata["name"] = recorded.Subject.Name
	if recorded.Subject.ID != "" {
		metadata["subject_id"] = recorded.Subject.ID
	}
	for key, value := range recorded.Subject.Attributes {
		if value != "" {
			metadata["subject."+key] = value
		}
	}
	totals := usageTotals(recorded)
	addQuantity(metadata, "input_tokens", totals.inputTokens)
	addQuantity(metadata, "cached_input_tokens", totals.cachedInputTokens)
	addQuantity(metadata, "output_tokens", totals.outputTokens)
	addQuantity(metadata, "reasoning_tokens", totals.reasoningTokens)
	addQuantity(metadata, "total_tokens", totals.totalTokens)
	addQuantity(metadata, "cost", totals.cost)
	addQuantity(metadata, "requests", totals.requests)
	addQuantity(metadata, "bytes", totals.bytes)
	addQuantity(metadata, "wall_time_ms", totals.wallTimeMillis)
	if totals.currency != "" {
		metadata["currency"] = totals.currency
	}
	title := usageTitle(recorded, totals)
	content := usageContent(recorded, totals)
	id := recordID(snapshot.ID, stored.Sequence)
	return coredatasource.Record{
		ID:         id,
		Datasource: datasource,
		Entity:     EntityRecord,
		Title:      title,
		Content:    content,
		URL:        "usage://" + id,
		Metadata:   metadata,
		Raw:        recorded,
	}
}

type totals struct {
	inputTokens       float64
	cachedInputTokens float64
	outputTokens      float64
	reasoningTokens   float64
	totalTokens       float64
	cost              float64
	requests          float64
	bytes             float64
	wallTimeMillis    float64
	currency          string
}

func usageTotals(recorded coreusage.Recorded) totals {
	var out totals
	for _, measurement := range recorded.Measurements {
		switch measurement.Metric {
		case coreusage.MetricLLMInputTokens:
			out.inputTokens += measurement.Quantity
		case coreusage.MetricLLMCachedTokens:
			out.cachedInputTokens += measurement.Quantity
		case coreusage.MetricLLMOutputTokens:
			out.outputTokens += measurement.Quantity
		case coreusage.MetricLLMReasoningTokens:
			out.reasoningTokens += measurement.Quantity
		case coreusage.MetricLLMTotalTokens:
			out.totalTokens += measurement.Quantity
		case coreusage.MetricCost:
			out.cost += measurement.Quantity
			if out.currency == "" {
				out.currency = measurement.Dimensions["currency"]
			}
		case coreusage.MetricRequests:
			out.requests += measurement.Quantity
		case coreusage.MetricNetworkBytes, coreusage.MetricFileBytes:
			out.bytes += measurement.Quantity
		case coreusage.MetricWallTime:
			out.wallTimeMillis += measurement.Quantity
		}
	}
	if out.totalTokens == 0 {
		out.totalTokens = out.inputTokens + out.cachedInputTokens + out.outputTokens + out.reasoningTokens
	}
	return out
}

func usageTitle(recorded coreusage.Recorded, totals totals) string {
	subject := strings.Trim(strings.TrimSpace(recorded.Subject.Provider+"/"+recorded.Subject.Name), "/")
	if subject == "" {
		subject = string(recorded.Subject.Kind)
	}
	parts := []string{"usage", subject}
	if totals.totalTokens > 0 {
		parts = append(parts, formatQuantity(totals.totalTokens)+" tokens")
	}
	if totals.cost > 0 {
		currency := totals.currency
		if currency == "" {
			currency = "USD"
		}
		parts = append(parts, formatQuantity(totals.cost)+" "+currency)
	}
	return strings.Join(parts, " ")
}

func usageContent(recorded coreusage.Recorded, totals totals) string {
	parts := []string{
		"source=" + recorded.Source,
		"subject_kind=" + string(recorded.Subject.Kind),
		"provider=" + recorded.Subject.Provider,
		"name=" + recorded.Subject.Name,
	}
	addPart := func(name string, value float64) {
		if value != 0 {
			parts = append(parts, name+"="+formatQuantity(value))
		}
	}
	addPart("input_tokens", totals.inputTokens)
	addPart("cached_input_tokens", totals.cachedInputTokens)
	addPart("output_tokens", totals.outputTokens)
	addPart("reasoning_tokens", totals.reasoningTokens)
	addPart("total_tokens", totals.totalTokens)
	addPart("cost", totals.cost)
	addPart("requests", totals.requests)
	addPart("bytes", totals.bytes)
	addPart("wall_time_ms", totals.wallTimeMillis)
	return strings.Join(nonEmptyParts(parts), "\n")
}

func recordID(threadID corethread.ID, sequence event.Sequence) string {
	return string(threadID) + ":" + string(EntityRecord) + ":" + strconv.FormatInt(int64(sequence), 10)
}

func validateEntity(entity coredatasource.EntityType) error {
	if entity == "" || entity == EntityRecord {
		return nil
	}
	return fmt.Errorf("usage: unsupported entity %q", entity)
}

func parseRecordID(id string) (string, int64, bool) {
	suffix := ":" + string(EntityRecord) + ":"
	index := strings.LastIndex(id, suffix)
	if index <= 0 {
		return "", 0, false
	}
	sequence, err := strconv.ParseInt(id[index+len(suffix):], 10, 64)
	return id[:index], sequence, err == nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func parseCursor(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("usage: invalid cursor %q", raw)
	}
	return offset, nil
}

func matchQuery(record coredatasource.Record, query string) bool {
	if query == "" {
		return true
	}
	haystack := strings.ToLower(record.Title + "\n" + record.Content + "\n" + strings.Join(metadataValues(record.Metadata), "\n"))
	return strings.Contains(haystack, query)
}

func matchFilters(record coredatasource.Record, filters map[string]string) bool {
	for key, want := range filters {
		want = strings.TrimSpace(want)
		if want == "" || key == "thread_id" {
			continue
		}
		switch key {
		case "since":
			if !afterOrEqual(recordTime(record), want) {
				return false
			}
		case "until":
			if !beforeOrEqual(recordTime(record), want) {
				return false
			}
		default:
			if !strings.EqualFold(record.Metadata[key], want) {
				return false
			}
		}
	}
	return true
}

func recordTime(record coredatasource.Record) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, record.Metadata["time"])
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func afterOrEqual(value time.Time, raw string) bool {
	parsed, ok := parseFilterTime(raw)
	return !ok || value.IsZero() || value.Equal(parsed) || value.After(parsed)
}

func beforeOrEqual(value time.Time, raw string) bool {
	parsed, ok := parseFilterTime(raw)
	return !ok || value.IsZero() || value.Equal(parsed) || value.Before(parsed)
}

func parseFilterTime(raw string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		parsed, err := time.Parse(layout, strings.TrimSpace(raw))
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func sortRecordsNewestFirst(records []coredatasource.Record) {
	sort.SliceStable(records, func(i, j int) bool {
		left := recordTime(records[i])
		right := recordTime(records[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return records[i].ID > records[j].ID
	})
}

func corpusDocument(record coredatasource.Record) coredatasource.CorpusDocument {
	body := strings.TrimSpace(record.Content)
	if metadata := strings.Join(metadataValues(record.Metadata), "\n"); metadata != "" {
		if body != "" {
			body += "\n\n"
		}
		body += metadata
	}
	return coredatasource.CorpusDocument{
		Ref: coredatasource.RecordRef{
			Datasource: record.Datasource,
			Entity:     record.Entity,
			ID:         record.ID,
			URL:        record.URL,
		},
		Title:       record.Title,
		Body:        body,
		URL:         record.URL,
		Metadata:    cloneMetadata(record.Metadata),
		Fingerprint: recordFingerprint(record),
	}
}

func recordFingerprint(record coredatasource.Record) string {
	encoded, err := json.Marshal(struct {
		Title    string            `json:"title"`
		Content  string            `json:"content"`
		URL      string            `json:"url"`
		Metadata map[string]string `json:"metadata"`
	}{
		Title:    record.Title,
		Content:  record.Content,
		URL:      record.URL,
		Metadata: record.Metadata,
	})
	if err != nil {
		encoded = []byte(record.Title + "\n" + record.Content + "\n" + record.URL)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func metadataValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key, value := range values {
		if value != "" {
			out = append(out, key+"="+value)
		}
	}
	sort.Strings(out)
	return out
}

func cloneMetadata(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func addQuantity(metadata map[string]string, key string, value float64) {
	if value != 0 {
		metadata[key] = formatQuantity(value)
	}
}

func formatQuantity(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func nonEmptyParts(parts []string) []string {
	out := parts[:0]
	for _, part := range parts {
		if strings.Trim(part, " =") != "" {
			out = append(out, part)
		}
	}
	return out
}
