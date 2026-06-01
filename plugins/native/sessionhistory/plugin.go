// Package sessionhistoryplugin exposes durable session history as a datasource.
package sessionhistory

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

	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-event"
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
	EntitySessionAgent = coredatasource.EntityType("session.session_agent")
	EntityUsage        = coredatasource.EntityType("session.usage")

	defaultSearchLimit = 10
	maxSearchLimit     = 100

	corpusChunkChars   = 900
	corpusOverlapChars = 120
)

var allEntities = []coredatasource.EntityType{
	EntityThread,
	EntityMessage,
	EntityOperation,
	EntityModelCall,
	EntityCompaction,
	EntityContinuation,
	EntitySessionAgent,
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
	return []coredatasource.Provider{provider(p)}, nil
}

// DatasourceSpec returns the configured dev datasource.
func DatasourceSpec() coredatasource.Spec {
	return coredatasource.Spec{
		Name:        DatasourceName,
		Description: "Persisted local session threads, messages, operations, model calls, continuations, session agents, and usage.",
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
var _ coredatasource.CorpusProvider = accessor{}

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
	sortRecordsNewestFirst(matches)
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

func (a accessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	entity := normalizeEntity(req.Entity)
	if entity == "" {
		return coredatasource.CorpusPage{}, fmt.Errorf("session_history: entity is required")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = maxSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	offset, err := parseCursor(req.Cursor)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	snapshots, err := a.snapshots(ctx, nil)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	var records []coredatasource.Record
	for _, snapshot := range snapshots {
		records = append(records, projectSnapshot(snapshot, entity)...)
	}
	sortRecordsNewestFirst(records)
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
	caps := []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilitySemanticSearch,
	}
	return []coredatasource.EntitySpec{
		entitySpec(EntityThread, "Persisted session threads.", caps),
		entitySpec(EntityMessage, "Inbound and outbound session messages.", caps),
		entitySpec(EntityOperation, "Operation requests and completions.", caps),
		entitySpec(EntityModelCall, "Model call lifecycle events.", caps),
		entitySpec(EntityCompaction, "Conversation compaction checkpoints.", caps),
		entitySpec(EntityContinuation, "Provider continuation handles.", caps),
		entitySpec(EntitySessionAgent, "Command session-agent lifecycle events.", caps),
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
		URL:        sessionURL(string(snapshot.ID), EntityThread, 0),
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
		URL:        sessionURL(string(snapshot.ID), entity, stored.Sequence),
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
	case strings.HasPrefix(value, "session_agent."):
		return EntitySessionAgent
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

func sessionURL(threadID string, entity coredatasource.EntityType, sequence event.Sequence) string {
	if entity == EntityThread {
		return "session://" + threadID + "/" + string(entity)
	}
	return "session://" + threadID + "/" + string(entity) + "/" + strconv.FormatInt(int64(sequence), 10)
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
	case EntitySessionAgent:
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
		Chunks:      corpusChunks(record.Title, body),
	}
}

func corpusChunks(title, body string) []coredatasource.CorpusChunk {
	parts := boundedTextChunks(body, corpusChunkChars, corpusOverlapChars)
	out := make([]coredatasource.CorpusChunk, 0, len(parts))
	for i, part := range parts {
		out = append(out, coredatasource.CorpusChunk{
			ID:      strconv.Itoa(i),
			Title:   title,
			Text:    part.Text,
			Ordinal: i,
			Start:   part.Start,
			End:     part.End,
		})
	}
	return out
}

type textPart struct {
	Text  string
	Start int
	End   int
}

func boundedTextChunks(text string, target, overlap int) []textPart {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return nil
	}
	if target <= 0 {
		target = corpusChunkChars
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= target {
		overlap = target / 5
	}
	var out []textPart
	for start := 0; start < len(runes); {
		end := start + target
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, textPart{Text: strings.TrimSpace(string(runes[start:end])), Start: start, End: end})
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	return out
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

func parseCursor(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("session_history: invalid corpus cursor %q", raw)
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
