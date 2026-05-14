// Package sessionhistoryplugin exposes durable session history as a datasource.
package sessionhistoryplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
)

const (
	Name           = "session_history"
	DatasourceName = coredatasource.Name("session_history")

	EntityThread       = coredatasource.EntityType("session.thread")
	EntityMessage      = coredatasource.EntityType("session.message")
	EntityOperation    = coredatasource.EntityType("session.operation")
	EntityModelCall    = coredatasource.EntityType("session.model_call")
	EntityCompaction   = coredatasource.EntityType("session.compaction")
	EntityContinuation = coredatasource.EntityType("session.continuation")
	EntitySubagent     = coredatasource.EntityType("session.subagent")
	EntityUsage        = coredatasource.EntityType("session.usage")

	defaultSearchLimit = 10
	maxSearchLimit     = 100
)

var allEntities = []coredatasource.EntityType{
	EntityThread,
	EntityMessage,
	EntityOperation,
	EntityModelCall,
	EntityCompaction,
	EntityContinuation,
	EntitySubagent,
	EntityUsage,
}

// Plugin contributes a store-backed datasource over durable session history.
type Plugin struct {
	threads corethread.Store
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}

// New returns a session history plugin backed by the supplied thread store.
func New(threads corethread.Store) Plugin {
	return Plugin{threads: threads}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Session history datasource over persisted threads and events."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{Datasources: []coredatasource.Spec{DatasourceSpec()}}, nil
}

func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
	return []coredatasource.Provider{provider{threads: p.threads}}, nil
}

// DatasourceSpec returns the configured dev datasource.
func DatasourceSpec() coredatasource.Spec {
	return coredatasource.Spec{
		Name:        DatasourceName,
		Description: "Persisted local session threads, messages, operations, model calls, continuations, subagents, and usage.",
		Entities:    append([]coredatasource.EntityType(nil), allEntities...),
		Kind:        Name,
	}
}

type provider struct {
	threads corethread.Store
}

var _ coredatasource.Provider = provider{}

func (p provider) Kind() string { return Name }

func (p provider) Entities() []coredatasource.EntitySpec {
	return entitySpecs()
}

func (p provider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if p.threads == nil {
		return nil, fmt.Errorf("session_history: thread store is nil")
	}
	if spec.Kind != "" && spec.Kind != Name {
		return nil, fmt.Errorf("session_history: unsupported kind %q", spec.Kind)
	}
	return accessor{threads: p.threads, spec: spec}, nil
}

type accessor struct {
	threads corethread.Store
	spec    coredatasource.Spec
}

var _ coredatasource.Accessor = accessor{}
var _ coredatasource.Searcher = accessor{}
var _ coredatasource.Getter = accessor{}
var _ coredatasource.BatchGetter = accessor{}

func (a accessor) Spec() coredatasource.Spec { return a.spec }

func (a accessor) Entities() []coredatasource.EntitySpec {
	return entitySpecs()
}

func (a accessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := normalizeEntity(req.Entity)
	if entity == "" {
		return coredatasource.SearchResult{}, fmt.Errorf("session_history: entity is required")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	snapshots, err := a.snapshots(ctx, req.Filters)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	var matches []coredatasource.Record
	for _, snapshot := range snapshots {
		for _, record := range projectSnapshot(snapshot, entity) {
			if !matchFilters(record, req.Filters) || !matchQuery(record, query) {
				continue
			}
			matches = append(matches, record)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left := recordTime(matches[i])
		right := recordTime(matches[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return matches[i].ID > matches[j].ID
	})
	total := len(matches)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return coredatasource.SearchResult{
		Datasource: a.spec.Name,
		Entity:     entity,
		Records:    matches,
		Total:      total,
	}, nil
}

func (a accessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	entity := normalizeEntity(req.Entity)
	threadID, sequence, ok := parseRecordID(req.ID, entity)
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
	for _, record := range projectSnapshot(snapshot, entity) {
		if entity == EntityThread {
			if record.ID == req.ID || record.ID == threadID {
				return record, nil
			}
			continue
		}
		if record.Metadata["thread_id"] == threadID && record.Metadata["sequence"] == strconv.FormatInt(sequence, 10) {
			return record, nil
		}
	}
	return coredatasource.Record{}, coredatasource.ErrNotFound
}

func (a accessor) BatchGet(ctx context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	out := coredatasource.BatchGetResult{Datasource: a.spec.Name, Entity: req.Entity}
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

func (a accessor) snapshots(ctx context.Context, filters map[string]string) ([]corethread.Snapshot, error) {
	if threadID := strings.TrimSpace(filters["thread_id"]); threadID != "" {
		snapshot, err := a.threads.Read(ctx, corethread.ReadParams{ID: corethread.ID(threadID)})
		if errors.Is(err, corethread.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []corethread.Snapshot{snapshot}, nil
	}
	page, err := a.threads.List(ctx, corethread.ListParams{IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	return page.Threads, nil
}

func entitySpecs() []coredatasource.EntitySpec {
	caps := []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityGet}
	return []coredatasource.EntitySpec{
		entitySpec(EntityThread, "Persisted session threads.", caps),
		entitySpec(EntityMessage, "Inbound and outbound session messages.", caps),
		entitySpec(EntityOperation, "Operation requests and completions.", caps),
		entitySpec(EntityModelCall, "Model call lifecycle events.", caps),
		entitySpec(EntityCompaction, "Conversation compaction checkpoints.", caps),
		entitySpec(EntityContinuation, "Provider continuation handles.", caps),
		entitySpec(EntitySubagent, "Subagent lifecycle events.", caps),
		entitySpec(EntityUsage, "Usage metering events.", caps),
	}
}

func entitySpec(entity coredatasource.EntityType, description string, capabilities []coredatasource.EntityCapability) coredatasource.EntitySpec {
	return coredatasource.EntitySpec{
		Type:         entity,
		Description:  description,
		Capabilities: append([]coredatasource.EntityCapability(nil), capabilities...),
		Fields: []coredatasource.FieldSpec{
			{Name: "thread_id", Type: coredatasource.FieldString, Filterable: true, Identifier: entity == EntityThread},
			{Name: "sequence", Type: coredatasource.FieldNumber, Filterable: true, Identifier: entity != EntityThread},
			{Name: "time", Type: coredatasource.FieldString, Filterable: true, Sortable: true},
			{Name: "type", Type: coredatasource.FieldString, Filterable: true, Searchable: true},
			{Name: "status", Type: coredatasource.FieldString, Filterable: true, Searchable: true},
			{Name: "operation", Type: coredatasource.FieldString, Filterable: true, Searchable: true},
			{Name: "provider", Type: coredatasource.FieldString, Filterable: true, Searchable: true},
			{Name: "model", Type: coredatasource.FieldString, Filterable: true, Searchable: true},
		},
	}
}

func projectSnapshot(snapshot corethread.Snapshot, entity coredatasource.EntityType) []coredatasource.Record {
	if entity == EntityThread {
		return []coredatasource.Record{threadRecord(snapshot)}
	}
	var out []coredatasource.Record
	for _, stored := range snapshot.Events {
		record, ok := eventRecord(snapshot, stored, entity)
		if ok {
			out = append(out, record)
		}
	}
	return out
}

func threadRecord(snapshot corethread.Snapshot) coredatasource.Record {
	metadata := baseMetadata(snapshot, nil)
	metadata["type"] = "thread"
	if snapshot.Archived {
		metadata["status"] = "archived"
	} else {
		metadata["status"] = "active"
	}
	metadata["event_count"] = strconv.Itoa(len(snapshot.Events))
	metadata["time"] = snapshot.UpdatedAt.Format(time.RFC3339Nano)
	return coredatasource.Record{
		ID:         string(snapshot.ID),
		Datasource: DatasourceName,
		Entity:     EntityThread,
		Title:      fmt.Sprintf("thread %s", snapshot.ID),
		Content:    fmt.Sprintf("thread %s has %d persisted events", snapshot.ID, len(snapshot.Events)),
		Metadata:   metadata,
		Raw:        snapshot,
	}
}

func eventRecord(snapshot corethread.Snapshot, stored corethread.Record, entity coredatasource.EntityType) (coredatasource.Record, bool) {
	var payload any = stored.Event.Payload
	name := stored.Event.Name
	if runtimeEvent, ok := payload.(coresession.RuntimeEmitted); ok {
		payload = runtimeEvent.Payload
		name = runtimeEvent.Name
	} else if runtimeEvent, ok := payload.(*coresession.RuntimeEmitted); ok && runtimeEvent != nil {
		payload = runtimeEvent.Payload
		name = runtimeEvent.Name
	}

	target := classifyEvent(name, payload)
	if target != entity {
		return coredatasource.Record{}, false
	}

	metadata := baseMetadata(snapshot, &stored)
	metadata["type"] = typeName(entity)
	metadata["event"] = string(name)
	extractFields(metadata, name, payload)
	content := payloadText(payload)
	title := titleFor(entity, metadata, name)
	return coredatasource.Record{
		ID:         recordID(snapshot.ID, entity, stored.Sequence),
		Datasource: DatasourceName,
		Entity:     entity,
		Title:      title,
		Content:    content,
		Metadata:   metadata,
		Raw:        payload,
	}, true
}

func classifyEvent(name event.Name, payload any) coredatasource.EntityType {
	switch payload.(type) {
	case coresession.InputReceived, *coresession.InputReceived, coresession.OutboundProduced, *coresession.OutboundProduced:
		return EntityMessage
	case coresession.OperationRequested, *coresession.OperationRequested, coresession.OperationCompleted, *coresession.OperationCompleted:
		return EntityOperation
	}
	value := string(name)
	switch {
	case strings.HasPrefix(value, "llmagent.model_"):
		return EntityModelCall
	case value == "conversation.compaction.stored":
		return EntityCompaction
	case value == "conversation.continuation.stored":
		return EntityContinuation
	case strings.HasPrefix(value, "subagent."):
		return EntitySubagent
	case value == "usage.recorded":
		return EntityUsage
	default:
		return ""
	}
}

func extractFields(metadata map[string]string, name event.Name, payload any) {
	switch p := payload.(type) {
	case coresession.InputReceived:
		metadata["status"] = "received"
		metadata["conversation"] = p.Conversation.ID
		metadata["channel"] = string(p.Channel.Name)
	case *coresession.InputReceived:
		if p != nil {
			extractFields(metadata, name, *p)
		}
	case coresession.OutboundProduced:
		metadata["status"] = "produced"
	case *coresession.OutboundProduced:
		if p != nil {
			extractFields(metadata, name, *p)
		}
	case coresession.OperationRequested:
		metadata["status"] = "requested"
		metadata["operation"] = string(p.Operation.Name)
	case *coresession.OperationRequested:
		if p != nil {
			extractFields(metadata, name, *p)
		}
	case coresession.OperationCompleted:
		metadata["status"] = string(p.Result.Status)
		metadata["operation"] = string(p.Operation.Name)
	case *coresession.OperationCompleted:
		if p != nil {
			extractFields(metadata, name, *p)
		}
	default:
		extractFromJSON(metadata, payload)
	}
	if metadata["status"] == "" {
		metadata["status"] = strings.TrimPrefix(string(name), strings.Split(string(name), ".")[0]+".")
	}
}

func extractFromJSON(metadata map[string]string, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	var values struct {
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		Operation struct {
			Name string `json:"name"`
		} `json:"operation"`
		Status string `json:"status"`
	}
	if json.Unmarshal(encoded, &values) != nil {
		return
	}
	if values.Provider != "" {
		metadata["provider"] = values.Provider
	}
	if values.Model != "" {
		metadata["model"] = values.Model
	}
	if values.Operation.Name != "" {
		metadata["operation"] = values.Operation.Name
	}
	if values.Status != "" {
		metadata["status"] = values.Status
	}
}

func baseMetadata(snapshot corethread.Snapshot, stored *corethread.Record) map[string]string {
	metadata := map[string]string{
		"thread_id": string(snapshot.ID),
	}
	for key, value := range snapshot.Metadata {
		if value != "" {
			metadata[key] = value
		}
	}
	if stored != nil {
		metadata["sequence"] = strconv.FormatInt(int64(stored.Sequence), 10)
		metadata["branch_id"] = string(stored.BranchID)
		metadata["time"] = stored.Event.Time.Format(time.RFC3339Nano)
		if stored.Event.Scope.SessionID != "" {
			metadata["session"] = stored.Event.Scope.SessionID
		}
		if stored.Event.Scope.ChannelID != "" {
			metadata["channel"] = stored.Event.Scope.ChannelID
		}
		if stored.Event.Scope.UserID != "" {
			metadata["user"] = stored.Event.Scope.UserID
		}
	}
	return metadata
}

func recordID(threadID corethread.ID, entity coredatasource.EntityType, sequence event.Sequence) string {
	return string(threadID) + ":" + string(entity) + ":" + strconv.FormatInt(int64(sequence), 10)
}

func parseRecordID(id string, entity coredatasource.EntityType) (string, int64, bool) {
	if entity == EntityThread {
		id = strings.TrimSpace(id)
		return id, 0, id != ""
	}
	suffix := ":" + string(entity) + ":"
	index := strings.LastIndex(id, suffix)
	if index <= 0 {
		return "", 0, false
	}
	sequence, err := strconv.ParseInt(id[index+len(suffix):], 10, 64)
	return id[:index], sequence, err == nil
}

func normalizeEntity(entity coredatasource.EntityType) coredatasource.EntityType {
	for _, candidate := range allEntities {
		if candidate == entity {
			return entity
		}
	}
	switch strings.TrimSpace(string(entity)) {
	case "tool":
		return EntityOperation
	case "message":
		return EntityMessage
	default:
		return entity
	}
}

func titleFor(entity coredatasource.EntityType, metadata map[string]string, name event.Name) string {
	switch entity {
	case EntityOperation:
		if op := metadata["operation"]; op != "" {
			return metadata["status"] + " operation " + op
		}
	case EntityModelCall:
		model := strings.Trim(strings.TrimSpace(metadata["provider"]+"/"+metadata["model"]), "/")
		if model != "" {
			return string(name) + " " + model
		}
	case EntitySubagent:
		return string(name)
	}
	return string(name)
}

func typeName(entity coredatasource.EntityType) string {
	return strings.TrimPrefix(string(entity), "session.")
}

func payloadText(payload any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprint(payload)
	}
	return string(encoded)
}

func recordTime(record coredatasource.Record) time.Time {
	value := record.Metadata["time"]
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func matchQuery(record coredatasource.Record, query string) bool {
	if query == "" {
		return true
	}
	haystack := strings.ToLower(record.Title + "\n" + record.Content + "\n" + strings.Join(metadataValues(record.Metadata), "\n"))
	return strings.Contains(haystack, query)
}

func matchFilters(record coredatasource.Record, filters map[string]string) bool {
	if len(filters) == 0 {
		return true
	}
	for key, want := range filters {
		want = strings.TrimSpace(want)
		if want == "" {
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
		case "today":
			if want == "true" && !sameLocalDay(recordTime(record), time.Now()) {
				return false
			}
		case "type":
			if !matchType(record, want) {
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

func matchType(record coredatasource.Record, want string) bool {
	want = strings.TrimPrefix(strings.ToLower(want), "session.")
	if want == "tool" {
		want = "operation"
	}
	return strings.EqualFold(record.Metadata["type"], want)
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
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse("2006-01-02", raw); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func sameLocalDay(left, right time.Time) bool {
	if left.IsZero() {
		return false
	}
	ly, lm, ld := left.Local().Date()
	ry, rm, rd := right.Local().Date()
	return ly == ry && lm == rm && ld == rd
}

func metadataValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	sort.Strings(out)
	return out
}
