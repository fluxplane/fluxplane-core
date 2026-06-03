package data

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/core/user"
	"github.com/fluxplane/fluxplane-core/core/workspace"
	"github.com/fluxplane/fluxplane-operation"
)

// SourceName identifies one data source instance.
type SourceName string

// EntityType identifies a source or view entity type.
type EntityType string

// ViewName identifies one materialized query shape.
type ViewName string

// RecordID identifies one record within a source/view.
type RecordID string

// RelationName identifies one relation edge kind.
type RelationName string

// BlobID identifies one stored payload.
type BlobID string

// Scope carries queryable ownership and visibility metadata for stored data.
type Scope struct {
	TenantID    string            `json:"tenant_id,omitempty"`
	AppID       string            `json:"app_id,omitempty"`
	WorkspaceID workspace.ID      `json:"workspace_id,omitempty"`
	UserID      user.ID           `json:"user_id,omitempty"`
	AgentID     string            `json:"agent_id,omitempty"`
	SessionID   string            `json:"session_id,omitempty"`
	ThreadID    thread.ID         `json:"thread_id,omitempty"`
	ChannelID   string            `json:"channel_id,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Matches reports whether s satisfies every non-empty selector dimension.
func (s Scope) Matches(selector Scope) bool {
	if selector.TenantID != "" && s.TenantID != selector.TenantID {
		return false
	}
	if selector.AppID != "" && s.AppID != selector.AppID {
		return false
	}
	if selector.WorkspaceID != "" && s.WorkspaceID != selector.WorkspaceID {
		return false
	}
	if selector.UserID != "" && s.UserID != selector.UserID {
		return false
	}
	if selector.AgentID != "" && s.AgentID != selector.AgentID {
		return false
	}
	if selector.SessionID != "" && s.SessionID != selector.SessionID {
		return false
	}
	if selector.ThreadID != "" && s.ThreadID != selector.ThreadID {
		return false
	}
	if selector.ChannelID != "" && s.ChannelID != selector.ChannelID {
		return false
	}
	for key, value := range selector.Annotations {
		if s.Annotations[key] != value {
			return false
		}
	}
	return true
}

// FieldType is a coarse field value class for source and view schema.
type FieldType string

const (
	FieldString  FieldType = "string"
	FieldNumber  FieldType = "number"
	FieldBoolean FieldType = "boolean"
	FieldObject  FieldType = "object"
	FieldArray   FieldType = "array"
	FieldAny     FieldType = "any"
)

// FieldSpec describes one entity or view field.
type FieldSpec struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type,omitempty"`
	Description string    `json:"description,omitempty"`
	Required    bool      `json:"required,omitempty"`
	Searchable  bool      `json:"searchable,omitempty"`
	Filterable  bool      `json:"filterable,omitempty"`
	Sortable    bool      `json:"sortable,omitempty"`
	Identifier  bool      `json:"identifier,omitempty"`
	URL         bool      `json:"url,omitempty"`
	Corpus      bool      `json:"corpus,omitempty"`
}

// EntityCapability describes source or view actions.
type EntityCapability string

const (
	CapabilitySearch         EntityCapability = "search"
	CapabilityList           EntityCapability = "list"
	CapabilityGet            EntityCapability = "get"
	CapabilityRelation       EntityCapability = "relation"
	CapabilityMaterialize    EntityCapability = "materialize"
	CapabilitySemanticSearch EntityCapability = "semantic_search"
)

// EntitySpec describes one source entity schema.
type EntitySpec struct {
	Type         EntityType         `json:"type"`
	Description  string             `json:"description,omitempty"`
	Capabilities []EntityCapability `json:"capabilities,omitempty"`
	Fields       []FieldSpec        `json:"fields,omitempty"`
	Relations    []RelationSpec     `json:"relations,omitempty"`
	Annotations  map[string]string  `json:"annotations,omitempty"`
}

// RelationSpec describes source-schema relationship meaning.
type RelationSpec struct {
	Name         RelationName `json:"name"`
	Description  string       `json:"description,omitempty"`
	SourceEntity EntityType   `json:"source_entity,omitempty"`
	TargetEntity EntityType   `json:"target_entity"`
	Exact        bool         `json:"exact,omitempty"`
}

// SourceSpec describes one live or configured data source.
type SourceSpec struct {
	Name         SourceName        `json:"name"`
	Kind         string            `json:"kind,omitempty"`
	Description  string            `json:"description,omitempty"`
	Entities     []EntitySpec      `json:"entities,omitempty"`
	Views        []ViewSpec        `json:"views,omitempty"`
	Config       map[string]string `json:"config,omitempty"`
	ConfigSchema operation.Schema  `json:"config_schema,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// Validate checks the source spec is structurally usable.
func (s SourceSpec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("data: source name is empty")
	}
	seenEntities := map[EntityType]bool{}
	for i, entity := range s.Entities {
		if strings.TrimSpace(string(entity.Type)) == "" {
			return fmt.Errorf("data: source %s entities[%d] type is empty", s.Name, i)
		}
		if seenEntities[entity.Type] {
			return fmt.Errorf("data: source %s duplicate entity %q", s.Name, entity.Type)
		}
		seenEntities[entity.Type] = true
		if err := validateFields("entity "+string(entity.Type), entity.Fields); err != nil {
			return err
		}
	}
	seenViews := map[ViewName]bool{}
	for i, view := range s.Views {
		if strings.TrimSpace(string(view.Name)) == "" {
			return fmt.Errorf("data: source %s views[%d] name is empty", s.Name, i)
		}
		if seenViews[view.Name] {
			return fmt.Errorf("data: source %s duplicate view %q", s.Name, view.Name)
		}
		seenViews[view.Name] = true
		if strings.TrimSpace(string(view.Source)) == "" {
			return fmt.Errorf("data: source %s view %s source is empty", s.Name, view.Name)
		}
		if err := validateFields("view "+string(view.Name), view.Fields); err != nil {
			return err
		}
	}
	return nil
}

// QueryHint declares which query shapes a view is intended to serve.
type QueryHint string

const (
	QueryGet       QueryHint = "get"
	QueryList      QueryHint = "list"
	QuerySearch    QueryHint = "search"
	QueryRelation  QueryHint = "relation"
	QueryAggregate QueryHint = "aggregate"
)

// ViewSpec describes one configured materialized query shape.
type ViewSpec struct {
	Name        ViewName              `json:"name"`
	Entity      EntityType            `json:"entity,omitempty"`
	Source      EntityType            `json:"source"`
	Description string                `json:"description,omitempty"`
	Includes    []RelationIncludeSpec `json:"includes,omitempty"`
	Fields      []FieldSpec           `json:"fields,omitempty"`
	QueryHints  []QueryHint           `json:"query_hints,omitempty"`
	Freshness   string                `json:"freshness,omitempty"`
	RawPayload  bool                  `json:"raw_payload,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty"`
}

// RelationIncludeSpec describes related summaries embedded or indexed by a view.
type RelationIncludeSpec struct {
	Relation RelationName `json:"relation"`
	Target   EntityType   `json:"target"`
	Fields   []string     `json:"fields,omitempty"`
}

// Ref identifies one source/view record.
type Ref struct {
	Source SourceName `json:"source,omitempty"`
	Entity EntityType `json:"entity,omitempty"`
	View   ViewName   `json:"view,omitempty"`
	ID     RecordID   `json:"id,omitempty"`
}

// Record is one structured item stored in a data store.
type Record struct {
	Ref         Ref                  `json:"ref"`
	Scope       Scope                `json:"scope,omitempty"`
	Title       string               `json:"title,omitempty"`
	Content     string               `json:"content,omitempty"`
	URL         string               `json:"url,omitempty"`
	Fields      map[string][]string  `json:"fields,omitempty"`
	Relations   map[string][]Summary `json:"relations,omitempty"`
	BlobRefs    []BlobRef            `json:"blob_refs,omitempty"`
	Score       float64              `json:"score,omitempty"`
	Raw         any                  `json:"raw,omitempty"`
	Metadata    map[string]string    `json:"metadata,omitempty"`
	Fingerprint string               `json:"fingerprint,omitempty"`
	UpdatedAt   string               `json:"updated_at,omitempty"`
}

// Summary is a compact related record projection.
type Summary struct {
	Ref    Ref               `json:"ref"`
	Title  string            `json:"title,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

// Relation is one normalized edge between records.
type Relation struct {
	Source   Ref               `json:"source"`
	Name     RelationName      `json:"name"`
	Target   Ref               `json:"target"`
	Scope    Scope             `json:"scope,omitempty"`
	Summary  Summary           `json:"summary,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// BlobRef identifies a stored payload.
type BlobRef struct {
	ID        BlobID            `json:"id"`
	Scope     Scope             `json:"scope,omitempty"`
	MediaType string            `json:"media_type,omitempty"`
	Size      int64             `json:"size,omitempty"`
	Digest    string            `json:"digest,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Blob is a stored payload descriptor and optional content.
type Blob struct {
	Ref     BlobRef `json:"ref"`
	Content []byte  `json:"content,omitempty"`
}

// QueryMode selects a retrieval strategy.
type QueryMode string

const (
	QueryModeExact    QueryMode = "exact"
	QueryModeList     QueryMode = "list"
	QueryModeLexical  QueryMode = "lexical"
	QueryModeSemantic QueryMode = "semantic"
	QueryModeHybrid   QueryMode = "hybrid"
)

// RelationFilter constrains records by relation target fields.
type RelationFilter struct {
	Relation RelationName      `json:"relation"`
	Target   EntityType        `json:"target,omitempty"`
	Filters  map[string]string `json:"filters,omitempty"`
}

// Query describes a record lookup over stored data.
type Query struct {
	Scope           Scope             `json:"scope,omitempty"`
	Sources         []SourceName      `json:"sources,omitempty"`
	Entities        []EntityType      `json:"entities,omitempty"`
	Views           []ViewName        `json:"views,omitempty"`
	IDs             []RecordID        `json:"ids,omitempty"`
	Text            string            `json:"text,omitempty"`
	Filters         map[string]string `json:"filters,omitempty"`
	RelationFilters []RelationFilter  `json:"relation_filters,omitempty"`
	Limit           int               `json:"limit,omitempty"`
	Cursor          string            `json:"cursor,omitempty"`
	Mode            QueryMode         `json:"mode,omitempty"`
}

// QueryResult contains matched records and pagination state.
type QueryResult struct {
	Records    []Record `json:"records,omitempty"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Complete   bool     `json:"complete,omitempty"`
}

// RelationQuery describes a relation lookup.
type RelationQuery struct {
	Scope    Scope        `json:"scope,omitempty"`
	Sources  []SourceName `json:"sources,omitempty"`
	Views    []ViewName   `json:"views,omitempty"`
	Relation RelationName `json:"relation,omitempty"`
	Source   Ref          `json:"source,omitempty"`
	Target   Ref          `json:"target,omitempty"`
	Limit    int          `json:"limit,omitempty"`
	Cursor   string       `json:"cursor,omitempty"`
}

// RelationResult contains matched relation edges.
type RelationResult struct {
	Relations  []Relation `json:"relations,omitempty"`
	NextCursor string     `json:"next_cursor,omitempty"`
	Complete   bool       `json:"complete,omitempty"`
}

// Store is the provider-neutral data store port.
type Store interface {
	UpsertRecords(context.Context, ...Record) error
	DeleteRecords(context.Context, Scope, ...Ref) error
	GetRecord(context.Context, Scope, Ref) (Record, bool, error)
	BatchGetRecords(context.Context, Scope, ...Ref) ([]Record, error)
	QueryRecords(context.Context, Query) (QueryResult, error)
	UpsertRelations(context.Context, ...Relation) error
	QueryRelations(context.Context, RelationQuery) (RelationResult, error)
	PutBlob(context.Context, Blob) (BlobRef, error)
	GetBlob(context.Context, BlobRef) (Blob, bool, error)
}

func validateFields(owner string, fields []FieldSpec) error {
	seen := map[string]bool{}
	for i, field := range fields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			return fmt.Errorf("data: %s fields[%d] name is empty", owner, i)
		}
		if seen[name] {
			return fmt.Errorf("data: %s duplicate field %q", owner, name)
		}
		seen[name] = true
	}
	return nil
}
