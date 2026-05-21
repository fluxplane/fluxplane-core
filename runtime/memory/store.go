package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	coredata "github.com/fluxplane/engine/core/data"
	"github.com/fluxplane/engine/core/event"
	corememory "github.com/fluxplane/engine/core/memory"
	"github.com/fluxplane/engine/core/policy"
)

type Store struct {
	events event.Store
	data   coredata.Store
	now    func() time.Time
}

func NewStore(events event.Store, data coredata.Store) (*Store, error) {
	if events == nil {
		return nil, fmt.Errorf("memory: event store is nil")
	}
	return &Store{events: events, data: data, now: time.Now}, nil
}

func (s *Store) Memorize(ctx context.Context, req corememory.MemorizeRequest) (corememory.MemorizeResult, error) {
	if s == nil || s.events == nil {
		return corememory.MemorizeResult{}, fmt.Errorf("memory: store is nil")
	}
	if err := requireAccessScope(req.AccessScope); err != nil {
		return corememory.MemorizeResult{}, err
	}
	memory, err := normalizeMemory(req, s.now())
	if err != nil {
		return corememory.MemorizeResult{}, err
	}
	if err := s.append(ctx, memory.AccessScope, corememory.Memorized{Memory: memory}); err != nil {
		return corememory.MemorizeResult{}, err
	}
	if s.data != nil {
		if err := s.data.UpsertRecords(ctx, RecordFromMemory(memory)); err != nil {
			return corememory.MemorizeResult{}, err
		}
	}
	return corememory.MemorizeResult{Memory: memory}, nil
}

func (s *Store) Retrieve(ctx context.Context, req corememory.RetrieveRequest) (corememory.RetrieveResult, error) {
	if s == nil {
		return corememory.RetrieveResult{}, fmt.Errorf("memory: store is nil")
	}
	if err := requireAccessScope(req.AccessScope); err != nil {
		return corememory.RetrieveResult{}, err
	}
	var records []coredata.Record
	if s.data != nil {
		var err error
		records, err = s.queryDataRecords(ctx, req)
		if err != nil {
			return corememory.RetrieveResult{}, err
		}
	}
	if len(records) == 0 && s.events != nil {
		projected, err := s.project(ctx, req.AccessScope)
		if err != nil {
			return corememory.RetrieveResult{}, err
		}
		records = make([]coredata.Record, 0, len(projected))
		for _, memory := range projected {
			records = append(records, RecordFromMemory(memory))
		}
		if s.data != nil && len(records) > 0 {
			_ = s.data.UpsertRecords(context.WithoutCancel(ctx), records...)
		}
	}
	memories := make([]corememory.Memory, 0, len(records))
	for _, record := range records {
		memory, ok := MemoryFromRecord(record)
		if !ok || !includeStatus(memory.Status, req.IncludeArchived, req.IncludeForgotten) {
			continue
		}
		if len(req.Kinds) > 0 && !kindMatches(memory.Kind, req.Kinds) {
			continue
		}
		if len(req.Subjects) > 0 && !subjectsMatch(memory.Subjects, req.Subjects) {
			continue
		}
		if len(req.Tags) > 0 && !tagsMatch(memory.Tags, req.Tags) {
			continue
		}
		if !memoryMatchesRequest(memory, req) {
			continue
		}
		memories = append(memories, memory)
	}
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Provenance.UpdatedAt.After(memories[j].Provenance.UpdatedAt)
	})
	memories, nextCursor, complete := paginateMemories(memories, req.Limit, req.Cursor)
	return corememory.RetrieveResult{Memories: memories, NextCursor: nextCursor, Complete: complete}, nil
}

func (s *Store) queryDataRecords(ctx context.Context, req corememory.RetrieveRequest) ([]coredata.Record, error) {
	query := coredata.Query{
		Scope:    req.AccessScope,
		Sources:  []coredata.SourceName{corememory.SourceName},
		Entities: []coredata.EntityType{corememory.ItemEntity},
		Text:     req.Text,
		Limit:    1000,
		Filters:  retrieveFilters(req),
	}
	for _, id := range req.IDs {
		query.IDs = append(query.IDs, coredata.RecordID(id))
	}
	var records []coredata.Record
	for {
		result, err := s.data.QueryRecords(ctx, query)
		if err != nil {
			return nil, err
		}
		records = append(records, result.Records...)
		if result.Complete || result.NextCursor == "" {
			return records, nil
		}
		query.Cursor = result.NextCursor
	}
}

func (s *Store) Forget(ctx context.Context, req corememory.ForgetRequest) (corememory.ForgetResult, error) {
	if s == nil || s.events == nil {
		return corememory.ForgetResult{}, fmt.Errorf("memory: store is nil")
	}
	if err := requireAccessScope(req.AccessScope); err != nil {
		return corememory.ForgetResult{}, err
	}
	mode := req.Mode
	if mode == "" {
		mode = corememory.ForgetModeForget
	}
	status := corememory.StatusForgotten
	if mode == corememory.ForgetModeArchive {
		status = corememory.StatusArchived
	}
	retrieved, err := s.Retrieve(ctx, corememory.RetrieveRequest{
		AccessScope:      req.AccessScope,
		Subjects:         req.Subjects,
		Text:             req.Query,
		IDs:              req.IDs,
		Limit:            1000,
		IncludeArchived:  true,
		IncludeForgotten: true,
	})
	if err != nil {
		return corememory.ForgetResult{}, err
	}
	var affected []corememory.ID
	var records []coredata.Record
	now := s.now()
	for _, memory := range retrieved.Memories {
		memory.Status = status
		memory.Provenance.UpdatedAt = now
		if status == corememory.StatusForgotten {
			memory.Content = ""
			memory.Data = nil
			memory.BlobRefs = nil
		}
		affected = append(affected, memory.ID)
		records = append(records, RecordFromMemory(memory))
	}
	if len(affected) == 0 {
		return corememory.ForgetResult{Mode: mode, Status: status}, nil
	}
	if err := s.append(ctx, req.AccessScope, corememory.Forgotten{IDs: affected, Status: status, Mode: mode, Reason: req.Reason}); err != nil {
		return corememory.ForgetResult{}, err
	}
	if s.data != nil && len(records) > 0 {
		if err := s.data.UpsertRecords(ctx, records...); err != nil {
			return corememory.ForgetResult{}, err
		}
	}
	return corememory.ForgetResult{Affected: affected, Mode: mode, Status: status}, nil
}

func (s *Store) Organize(ctx context.Context, req corememory.OrganizeRequest) (corememory.OrganizeResult, error) {
	if s == nil || s.events == nil {
		return corememory.OrganizeResult{}, fmt.Errorf("memory: store is nil")
	}
	if err := requireAccessScope(req.AccessScope); err != nil {
		return corememory.OrganizeResult{}, err
	}
	if len(req.IDs) == 0 {
		return corememory.OrganizeResult{}, fmt.Errorf("memory: ids are required")
	}
	retrieved, err := s.Retrieve(ctx, corememory.RetrieveRequest{AccessScope: req.AccessScope, IDs: req.IDs, Limit: len(req.IDs), IncludeArchived: true})
	if err != nil {
		return corememory.OrganizeResult{}, err
	}
	now := s.now()
	memories := make([]corememory.Memory, 0, len(retrieved.Memories))
	for _, memory := range retrieved.Memories {
		memory.Provenance.UpdatedAt = now
		switch req.Action {
		case corememory.OrganizeRetag:
			memory.Tags = normalizeTags(req.Tags)
		case corememory.OrganizeArchive:
			memory.Status = corememory.StatusArchived
		case corememory.OrganizeSupersede:
			memory.Status = corememory.StatusSuperseded
		case corememory.OrganizeSummarize, corememory.OrganizeMerge:
			if strings.TrimSpace(req.Title) != "" {
				memory.Title = strings.TrimSpace(req.Title)
			}
			if strings.TrimSpace(req.Content) != "" {
				memory.Content = strings.TrimSpace(req.Content)
			}
			if len(req.Tags) > 0 {
				memory.Tags = normalizeTags(req.Tags)
			}
			if len(req.Subjects) > 0 {
				memory.Subjects = req.Subjects
			}
		default:
			return corememory.OrganizeResult{}, fmt.Errorf("memory: unsupported organize action %q", req.Action)
		}
		memories = append(memories, memory)
	}
	if len(memories) == 0 {
		return corememory.OrganizeResult{}, nil
	}
	if err := s.append(ctx, req.AccessScope, corememory.Organized{Memories: memories, Action: req.Action, Reason: req.Reason}); err != nil {
		return corememory.OrganizeResult{}, err
	}
	if s.data != nil {
		records := make([]coredata.Record, 0, len(memories))
		for _, memory := range memories {
			records = append(records, RecordFromMemory(memory))
		}
		if err := s.data.UpsertRecords(ctx, records...); err != nil {
			return corememory.OrganizeResult{}, err
		}
	}
	return corememory.OrganizeResult{Memories: memories}, nil
}

func (s *Store) append(ctx context.Context, scope coredata.Scope, payload event.Event) error {
	record := event.Record{
		Name:        payload.EventName(),
		Payload:     payload,
		Scope:       eventScope(scope),
		Sensitivity: policy.SensitivityRestricted,
		Source:      event.Source{Component: "runtime/memory"},
	}
	streams := StreamIDs(scope)
	requests := make([]event.AppendRequest, 0, len(streams))
	for _, stream := range streams {
		requests = append(requests, event.AppendRequest{
			Stream:  stream,
			Records: []event.Record{record},
		})
	}
	_, err := s.events.AppendBatch(ctx, requests...)
	return err
}

func StreamID(scope coredata.Scope) event.StreamID {
	streams := StreamIDs(scope)
	if len(streams) == 0 {
		return event.StreamID("memory/global")
	}
	return streams[0]
}

func StreamIDs(scope coredata.Scope) []event.StreamID {
	var streams []event.StreamID
	add := func(stream event.StreamID) {
		if stream == "" {
			return
		}
		for _, existing := range streams {
			if existing == stream {
				return
			}
		}
		streams = append(streams, stream)
	}
	switch {
	case scope.UserID != "":
		add(event.StreamID("memory/user/" + string(scope.UserID)))
	case scope.WorkspaceID != "":
		add(event.StreamID("memory/workspace/" + string(scope.WorkspaceID)))
	case scope.ThreadID != "":
		add(event.StreamID("memory/thread/" + string(scope.ThreadID)))
	case scope.SessionID != "":
		add(event.StreamID("memory/session/" + scope.SessionID))
	case scope.ChannelID != "":
		add(event.StreamID("memory/channel/" + scope.ChannelID))
	case scope.AgentID != "":
		add(event.StreamID("memory/agent/" + scope.AgentID))
	case scope.TenantID != "":
		add(event.StreamID("memory/tenant/" + scope.TenantID))
	}
	if scope.UserID != "" {
		add(event.StreamID("memory/user/" + string(scope.UserID)))
	}
	if scope.WorkspaceID != "" {
		add(event.StreamID("memory/workspace/" + string(scope.WorkspaceID)))
	}
	if scope.ThreadID != "" {
		add(event.StreamID("memory/thread/" + string(scope.ThreadID)))
	}
	if scope.SessionID != "" {
		add(event.StreamID("memory/session/" + scope.SessionID))
	}
	if scope.ChannelID != "" {
		add(event.StreamID("memory/channel/" + scope.ChannelID))
	}
	if scope.AgentID != "" {
		add(event.StreamID("memory/agent/" + scope.AgentID))
	}
	if scope.TenantID != "" {
		add(event.StreamID("memory/tenant/" + scope.TenantID))
	}
	if len(streams) == 0 {
		add(event.StreamID("memory/global"))
	}
	return streams
}

func (s *Store) project(ctx context.Context, scope coredata.Scope) ([]corememory.Memory, error) {
	byID := map[corememory.ID]corememory.Memory{}
	for _, stream := range StreamIDs(scope) {
		records, err := s.events.Load(ctx, stream, event.LoadOptions{})
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			switch payload := record.Record.Payload.(type) {
			case corememory.Memorized:
				byID[payload.Memory.ID] = payload.Memory
			case *corememory.Memorized:
				if payload != nil {
					byID[payload.Memory.ID] = payload.Memory
				}
			case corememory.Forgotten:
				projectForgotten(byID, payload)
			case *corememory.Forgotten:
				if payload != nil {
					projectForgotten(byID, *payload)
				}
			case corememory.Organized:
				for _, memory := range payload.Memories {
					byID[memory.ID] = memory
				}
			case *corememory.Organized:
				if payload != nil {
					for _, memory := range payload.Memories {
						byID[memory.ID] = memory
					}
				}
			}
		}
	}
	out := make([]corememory.Memory, 0, len(byID))
	for _, memory := range byID {
		if memory.AccessScope.Matches(scope) {
			out = append(out, memory)
		}
	}
	return out, nil
}

func projectForgotten(memories map[corememory.ID]corememory.Memory, event corememory.Forgotten) {
	for _, id := range event.IDs {
		memory, ok := memories[id]
		if !ok {
			continue
		}
		memory.Status = event.Status
		if event.Status == corememory.StatusForgotten {
			memory.Content = ""
			memory.Data = nil
			memory.BlobRefs = nil
		}
		memories[id] = memory
	}
}

func RecordFromMemory(memory corememory.Memory) coredata.Record {
	fields := map[string][]string{
		"kind":        {string(memory.Kind)},
		"status":      {string(memory.Status)},
		"visibility":  {string(memory.Visibility)},
		"sensitivity": {string(policy.NormalizeSensitivity(memory.Sensitivity))},
	}
	fields["tag"] = append(fields["tag"], memory.Tags...)
	for _, subject := range memory.Subjects {
		fields["subject.kind"] = append(fields["subject.kind"], string(subject.Kind))
		if subject.ID != "" {
			fields["subject.id"] = append(fields["subject.id"], subject.ID)
		}
		if subject.Name != "" {
			fields["subject.name"] = append(fields["subject.name"], subject.Name)
		}
		if subject.Path != "" {
			fields["subject.path"] = append(fields["subject.path"], subject.Path)
		}
		if subject.URL != "" {
			fields["subject.url"] = append(fields["subject.url"], subject.URL)
		}
	}
	return coredata.Record{
		Ref: coredata.Ref{
			Source: corememory.SourceName,
			Entity: corememory.ItemEntity,
			ID:     coredata.RecordID(memory.ID),
		},
		Scope:     memory.AccessScope,
		Title:     memory.Title,
		Content:   memory.Content,
		Fields:    fields,
		BlobRefs:  append([]coredata.BlobRef(nil), memory.BlobRefs...),
		Raw:       memory,
		Metadata:  map[string]string{"visibility": string(memory.Visibility), "status": string(memory.Status)},
		UpdatedAt: memory.Provenance.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func MemoryFromRecord(record coredata.Record) (corememory.Memory, bool) {
	if record.Ref.Source != corememory.SourceName || record.Ref.Entity != corememory.ItemEntity {
		return corememory.Memory{}, false
	}
	var memory corememory.Memory
	raw, err := json.Marshal(record.Raw)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return corememory.Memory{}, false
	}
	if err := json.Unmarshal(raw, &memory); err != nil {
		return corememory.Memory{}, false
	}
	return memory, memory.ID != ""
}

func normalizeMemory(req corememory.MemorizeRequest, now time.Time) (corememory.Memory, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return corememory.Memory{}, fmt.Errorf("memory: content is required")
	}
	kind := req.Kind
	if kind == "" {
		kind = corememory.KindFact
	}
	id := req.ID
	if id == "" {
		id = corememory.ID(newID())
	}
	visibility := req.Visibility
	if visibility == "" {
		visibility = corememory.VisibilityPrivateAgent
	}
	sensitivity := policy.NormalizeSensitivity(req.Sensitivity)
	return corememory.Memory{
		ID:          id,
		Kind:        kind,
		Status:      corememory.StatusActive,
		Visibility:  visibility,
		Subjects:    append([]corememory.Subject(nil), req.Subjects...),
		AccessScope: req.AccessScope,
		Title:       strings.TrimSpace(req.Title),
		Content:     content,
		Data:        req.Data,
		Tags:        normalizeTags(req.Tags),
		BlobRefs:    append([]coredata.BlobRef(nil), req.BlobRefs...),
		Supersedes:  append([]corememory.ID(nil), req.Supersedes...),
		Sensitivity: sensitivity,
		ExpiresAt:   req.ExpiresAt,
		Provenance: corememory.Provenance{
			SourceRefs: append([]corememory.SourceRef(nil), req.SourceRefs...),
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}, nil
}

func retrieveFilters(req corememory.RetrieveRequest) map[string]string {
	filters := map[string]string{}
	if len(req.Kinds) == 1 {
		filters["kind"] = string(req.Kinds[0])
	}
	if len(req.Tags) == 1 {
		filters["tag"] = req.Tags[0]
	}
	return filters
}

func kindMatches(value corememory.Kind, selectors []corememory.Kind) bool {
	for _, selector := range selectors {
		if selector == value {
			return true
		}
	}
	return false
}

func memoryMatchesRequest(memory corememory.Memory, req corememory.RetrieveRequest) bool {
	if len(req.IDs) > 0 {
		var matched bool
		for _, id := range req.IDs {
			if memory.ID == id {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	text := strings.ToLower(strings.TrimSpace(req.Text))
	if text == "" {
		return true
	}
	values := []string{string(memory.ID), string(memory.Kind), string(memory.Visibility), memory.Title, memory.Content}
	values = append(values, memory.Tags...)
	for _, subject := range memory.Subjects {
		values = append(values, string(subject.Kind), subject.ID, subject.Name, subject.Path, subject.URL, string(subject.Ref.Source), string(subject.Ref.Entity), string(subject.Ref.ID))
	}
	for _, ref := range memory.Provenance.SourceRefs {
		values = append(values, ref.Kind, ref.ID, ref.Name, ref.Path, ref.URL, ref.Description, string(ref.Ref.Source), string(ref.Ref.Entity), string(ref.Ref.ID))
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), text) {
			return true
		}
	}
	return false
}

func paginateMemories(memories []corememory.Memory, limit int, cursor string) ([]corememory.Memory, string, bool) {
	if limit <= 0 {
		limit = 20
	}
	offset := 0
	if cursor != "" {
		_, _ = fmt.Sscanf(cursor, "%d", &offset)
	}
	if offset >= len(memories) {
		return nil, "", true
	}
	memories = memories[offset:]
	next := ""
	if len(memories) > limit {
		memories = memories[:limit]
		next = fmt.Sprintf("%d", offset+limit)
	}
	return memories, next, next == ""
}

func includeStatus(status corememory.Status, includeArchived, includeForgotten bool) bool {
	switch status {
	case "", corememory.StatusActive:
		return true
	case corememory.StatusArchived, corememory.StatusSuperseded:
		return includeArchived
	case corememory.StatusForgotten:
		return includeForgotten
	default:
		return false
	}
}

func subjectsMatch(values, selectors []corememory.Subject) bool {
	for _, selector := range selectors {
		var matched bool
		for _, value := range values {
			if subjectMatches(value, selector) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func subjectMatches(value, selector corememory.Subject) bool {
	if selector.Kind != "" && value.Kind != selector.Kind {
		return false
	}
	if selector.ID != "" && value.ID != selector.ID {
		return false
	}
	if selector.Name != "" && value.Name != selector.Name {
		return false
	}
	if selector.Path != "" && value.Path != selector.Path {
		return false
	}
	if selector.URL != "" && value.URL != selector.URL {
		return false
	}
	if selector.Ref.Source != "" && value.Ref.Source != selector.Ref.Source {
		return false
	}
	if selector.Ref.Entity != "" && value.Ref.Entity != selector.Ref.Entity {
		return false
	}
	if selector.Ref.ID != "" && value.Ref.ID != selector.Ref.ID {
		return false
	}
	return true
}

func tagsMatch(values, selectors []string) bool {
	set := map[string]bool{}
	for _, value := range values {
		set[strings.ToLower(strings.TrimSpace(value))] = true
	}
	for _, selector := range selectors {
		if !set[strings.ToLower(strings.TrimSpace(selector))] {
			return false
		}
	}
	return true
}

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func eventScope(scope coredata.Scope) event.Scope {
	return event.Scope{
		TenantID:  scope.TenantID,
		AppID:     scope.AppID,
		SessionID: scope.SessionID,
		UserID:    string(scope.UserID),
		ChannelID: scope.ChannelID,
		AgentID:   scope.AgentID,
		ThreadID:  string(scope.ThreadID),
	}
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("mem_%d", time.Now().UnixNano())
	}
	return "mem_" + hex.EncodeToString(b[:])
}

func requireAccessScope(scope coredata.Scope) error {
	if !emptyScope(scope) {
		return nil
	}
	return fmt.Errorf("memory: access_scope must include at least one scope dimension")
}

func emptyScope(scope coredata.Scope) bool {
	if scope.TenantID != "" || scope.AppID != "" || scope.WorkspaceID != "" || scope.UserID != "" || scope.AgentID != "" || scope.SessionID != "" || scope.ThreadID != "" || scope.ChannelID != "" {
		return false
	}
	for key, value := range scope.Annotations {
		if strings.TrimSpace(key) != "" || strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}
