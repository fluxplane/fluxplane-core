// Package datasource defines inert specs and runtime-facing contracts for
// searchable data boundaries.
package datasource

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned when a datasource record cannot be retrieved.
var ErrNotFound = errors.New("datasource: record not found")

// Name identifies one configured datasource instance.
type Name string

// EntityType identifies a logical entity shape such as work.item.
type EntityType string

// FieldType is a coarse field value class for datasource metadata.
type FieldType string

const (
	FieldString  FieldType = "string"
	FieldNumber  FieldType = "number"
	FieldBoolean FieldType = "boolean"
	FieldObject  FieldType = "object"
	FieldArray   FieldType = "array"
	FieldAny     FieldType = "any"
)

// EntityCapability describes an action supported for one datasource entity.
type EntityCapability string

const (
	EntityCapabilitySearch EntityCapability = "search"
	EntityCapabilityGet    EntityCapability = "get"
)

// DetectorKind classifies one local, provider-neutral reference detector.
type DetectorKind string

const (
	DetectorRegex      DetectorKind = "regex"
	DetectorURL        DetectorKind = "url"
	DetectorStructured DetectorKind = "structured"
)

// Ref grants access to one datasource by name.
type Ref struct {
	Name Name `json:"name"`
}

// Spec describes one configured datasource instance.
type Spec struct {
	Name        Name              `json:"name"`
	Description string            `json:"description,omitempty"`
	Entities    []EntityType      `json:"entities,omitempty"`
	Connector   string            `json:"connector,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Config      map[string]string `json:"config,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Validate checks the datasource spec is structurally usable.
func (s Spec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("datasource: spec name is empty")
	}
	if len(s.Entities) == 0 {
		return fmt.Errorf("datasource: entities is empty")
	}
	seenEntities := map[EntityType]bool{}
	for i, entity := range s.Entities {
		if strings.TrimSpace(string(entity)) == "" {
			return fmt.Errorf("datasource: entities[%d] is empty", i)
		}
		if seenEntities[entity] {
			return fmt.Errorf("datasource: duplicate entity %q", entity)
		}
		seenEntities[entity] = true
	}
	if strings.TrimSpace(s.Connector) == "" && strings.TrimSpace(s.Kind) == "" {
		return fmt.Errorf("datasource: connector or kind is required")
	}
	for key := range s.Config {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("datasource: config contains an empty key")
		}
	}
	return nil
}

// EntitySpec describes the fields and capabilities of one entity type.
type EntitySpec struct {
	Type         EntityType         `json:"type"`
	Description  string             `json:"description,omitempty"`
	Capabilities []EntityCapability `json:"capabilities,omitempty"`
	Detectors    []DetectorSpec     `json:"detectors,omitempty"`
	Fields       []FieldSpec        `json:"fields,omitempty"`
}

// Supports reports whether the entity declares a capability.
func (s EntitySpec) Supports(capability EntityCapability) bool {
	for _, candidate := range s.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

// DetectorSpec describes a local reference detector for one entity.
type DetectorSpec struct {
	Name          string            `json:"name,omitempty"`
	Kind          DetectorKind      `json:"kind,omitempty"`
	Pattern       string            `json:"pattern,omitempty"`
	IDTemplate    string            `json:"id_template,omitempty"`
	QueryTemplate string            `json:"query_template,omitempty"`
	URLTemplate   string            `json:"url_template,omitempty"`
	Confidence    float64           `json:"confidence,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// FieldSpec describes one entity field.
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
}

// Validate checks the entity spec is structurally usable.
func (s EntitySpec) Validate() error {
	if strings.TrimSpace(string(s.Type)) == "" {
		return fmt.Errorf("datasource: entity type is empty")
	}
	seen := map[string]bool{}
	for i, field := range s.Fields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			return fmt.Errorf("datasource: entity %s fields[%d] name is empty", s.Type, i)
		}
		if seen[name] {
			return fmt.Errorf("datasource: entity %s duplicate field %q", s.Type, name)
		}
		seen[name] = true
	}
	seenDetectors := map[string]bool{}
	for i, detector := range s.Detectors {
		if strings.TrimSpace(detector.Name) == "" {
			return fmt.Errorf("datasource: entity %s detectors[%d] name is empty", s.Type, i)
		}
		if seenDetectors[detector.Name] {
			return fmt.Errorf("datasource: entity %s duplicate detector %q", s.Type, detector.Name)
		}
		seenDetectors[detector.Name] = true
		if detector.Kind == "" {
			return fmt.Errorf("datasource: entity %s detector %q kind is empty", s.Type, detector.Name)
		}
	}
	return nil
}

// SearchRequest describes one provider search.
type SearchRequest struct {
	Entity  EntityType        `json:"entity,omitempty"`
	Query   string            `json:"query,omitempty"`
	Limit   int               `json:"limit,omitempty"`
	Filters map[string]string `json:"filters,omitempty"`
}

// GetRequest describes one provider record lookup.
type GetRequest struct {
	Entity EntityType `json:"entity"`
	ID     string     `json:"id"`
}

// SearchResult is the normalized result for one datasource search.
type SearchResult struct {
	Datasource Name       `json:"datasource"`
	Entity     EntityType `json:"entity"`
	Records    []Record   `json:"records,omitempty"`
	Total      int        `json:"total,omitempty"`
}

// Record is one normalized entity instance returned by a datasource.
type Record struct {
	ID         string            `json:"id,omitempty"`
	Datasource Name              `json:"datasource,omitempty"`
	Entity     EntityType        `json:"entity,omitempty"`
	Title      string            `json:"title,omitempty"`
	Content    string            `json:"content,omitempty"`
	URL        string            `json:"url,omitempty"`
	Score      float64           `json:"score,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Links      []RecordRef       `json:"links,omitempty"`
	Raw        any               `json:"raw,omitempty"`
}

// RecordRef is a local reference to a datasource entity or candidate lookup.
type RecordRef struct {
	Datasource  Name              `json:"datasource,omitempty"`
	Entity      EntityType        `json:"entity,omitempty"`
	ID          string            `json:"id,omitempty"`
	Query       string            `json:"query,omitempty"`
	URL         string            `json:"url,omitempty"`
	Confidence  float64           `json:"confidence,omitempty"`
	SourceText  string            `json:"source_text,omitempty"`
	SourceKind  string            `json:"source_kind,omitempty"`
	Detector    string            `json:"detector,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// DetectionInput is the turn-scoped text/metadata scanned by local detectors.
type DetectionInput struct {
	Sources []DetectionSource `json:"sources,omitempty"`
	MaxRefs int               `json:"max_refs,omitempty"`
}

// DetectionSource is one local source scanned for datasource references.
type DetectionSource struct {
	ID       string            `json:"id,omitempty"`
	Kind     string            `json:"kind,omitempty"`
	Text     string            `json:"text,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Accessor is the base runtime handle for one configured datasource.
type Accessor interface {
	Spec() Spec
	Entities() []EntitySpec
}

// Searcher is implemented by accessors that support text search.
type Searcher interface {
	Search(context.Context, SearchRequest) (SearchResult, error)
}

// Getter is implemented by accessors that support direct record retrieval.
type Getter interface {
	Get(context.Context, GetRequest) (Record, error)
}

// Provider materializes datasource accessors for supported specs.
type Provider interface {
	Entities() []EntitySpec
	Open(context.Context, Spec) (Accessor, error)
}

// Registry is an immutable collection of configured datasource accessors.
type Registry struct {
	accessors []Accessor
	byName    map[Name]Accessor
	entities  map[EntityType]EntitySpec
}

// NewRegistry builds an immutable datasource registry.
func NewRegistry(accessors []Accessor, entities []EntitySpec) (*Registry, error) {
	out := &Registry{
		byName:   map[Name]Accessor{},
		entities: map[EntityType]EntitySpec{},
	}
	for _, entity := range entities {
		if err := entity.Validate(); err != nil {
			return nil, err
		}
		if _, exists := out.entities[entity.Type]; !exists {
			out.entities[entity.Type] = entity
		}
	}
	for _, accessor := range accessors {
		if accessor == nil {
			return nil, fmt.Errorf("datasource: accessor is nil")
		}
		spec := accessor.Spec()
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		if _, exists := out.byName[spec.Name]; exists {
			return nil, fmt.Errorf("datasource: duplicate datasource %q", spec.Name)
		}
		for _, entity := range accessor.Entities() {
			if isZeroEntity(entity) {
				continue
			}
			if err := entity.Validate(); err != nil {
				return nil, err
			}
			if _, exists := out.entities[entity.Type]; !exists {
				out.entities[entity.Type] = entity
			}
		}
		out.byName[spec.Name] = accessor
		out.accessors = append(out.accessors, accessor)
	}
	return out, nil
}

// Get resolves one configured datasource by name.
func (r *Registry) Get(name Name) (Accessor, bool) {
	if r == nil {
		return nil, false
	}
	accessor, ok := r.byName[name]
	return accessor, ok
}

// All returns configured datasource accessors in registration order.
func (r *Registry) All() []Accessor {
	if r == nil {
		return nil
	}
	out := make([]Accessor, len(r.accessors))
	copy(out, r.accessors)
	return out
}

// Entity returns metadata for an entity type.
func (r *Registry) Entity(typ EntityType) (EntitySpec, bool) {
	if r == nil {
		return EntitySpec{}, false
	}
	entity, ok := r.entities[typ]
	return entity, ok
}

// Entities returns all known entity specs.
func (r *Registry) Entities() []EntitySpec {
	if r == nil {
		return nil
	}
	out := make([]EntitySpec, 0, len(r.entities))
	for _, entity := range r.entities {
		out = append(out, entity)
	}
	return out
}

// AccessPolicy carries datasource grants for the currently executing agent.
type AccessPolicy struct {
	Datasources []Name
}

type accessPolicyKey struct{}
type detectionInputKey struct{}
type detectedRefsKey struct{}

// ContextWithAccessPolicy stores datasource access policy on ctx.
func ContextWithAccessPolicy(ctx context.Context, policy AccessPolicy) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, accessPolicyKey{}, policy)
}

// AccessPolicyFromContext returns the datasource access policy on ctx.
func AccessPolicyFromContext(ctx context.Context) (AccessPolicy, bool) {
	if ctx == nil {
		return AccessPolicy{}, false
	}
	policy, ok := ctx.Value(accessPolicyKey{}).(AccessPolicy)
	return policy, ok
}

// ContextWithDetectionInput stores turn-local detector input on ctx.
func ContextWithDetectionInput(ctx context.Context, input DetectionInput) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, detectionInputKey{}, input)
}

// DetectionInputFromContext returns turn-local detector input from ctx.
func DetectionInputFromContext(ctx context.Context) (DetectionInput, bool) {
	if ctx == nil {
		return DetectionInput{}, false
	}
	input, ok := ctx.Value(detectionInputKey{}).(DetectionInput)
	return input, ok
}

// ContextWithDetectedRefs stores turn-local detected references on ctx.
func ContextWithDetectedRefs(ctx context.Context, refs []RecordRef) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	copied := append([]RecordRef(nil), refs...)
	return context.WithValue(ctx, detectedRefsKey{}, copied)
}

// DetectedRefsFromContext returns turn-local detected references from ctx.
func DetectedRefsFromContext(ctx context.Context) ([]RecordRef, bool) {
	if ctx == nil {
		return nil, false
	}
	refs, ok := ctx.Value(detectedRefsKey{}).([]RecordRef)
	return append([]RecordRef(nil), refs...), ok
}

func isZeroEntity(spec EntitySpec) bool {
	return spec.Type == "" && spec.Description == "" && len(spec.Capabilities) == 0 && len(spec.Detectors) == 0 && len(spec.Fields) == 0
}
