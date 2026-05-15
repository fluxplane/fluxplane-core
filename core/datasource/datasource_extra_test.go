package datasource

import (
	"context"
	"testing"
)

// --- Spec.Validate edge cases ---

func TestSpecValidateRejectsEmptyName(t *testing.T) {
	spec := Spec{Entities: []EntityType{"file.document"}, Kind: "filesystem"}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty name")
	}
}

func TestSpecValidateRejectsEmptyEntityString(t *testing.T) {
	spec := Spec{Name: "docs", Entities: []EntityType{""}, Kind: "filesystem"}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty entity string")
	}
}

func TestSpecValidateRejectsDuplicateEntity(t *testing.T) {
	spec := Spec{Name: "docs", Entities: []EntityType{"file.document", "file.document"}, Kind: "filesystem"}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for duplicate entity")
	}
}

func TestSpecValidateRejectsEmptyConfigKey(t *testing.T) {
	spec := Spec{
		Name:     "docs",
		Entities: []EntityType{"file.document"},
		Kind:     "filesystem",
		Config:   map[string]string{"": "value"},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty config key")
	}
}

func TestSpecValidateRejectsEmptySemanticEntity(t *testing.T) {
	spec := Spec{
		Name:     "docs",
		Entities: []EntityType{"file.document"},
		Kind:     "filesystem",
		Semantic: SemanticSpec{
			Entities: map[EntityType]EntitySemantic{"": {}},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty semantic entity key")
	}
}

// --- EntitySpec.Validate edge cases ---

func TestEntityValidateRejectsDuplicateField(t *testing.T) {
	spec := EntitySpec{
		Type: "file.document",
		Fields: []FieldSpec{
			{Name: "id"},
			{Name: "id"},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for duplicate field")
	}
}

func TestEntityValidateRejectsEmptyFieldName(t *testing.T) {
	spec := EntitySpec{
		Type:   "file.document",
		Fields: []FieldSpec{{Name: ""}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty field name")
	}
}

func TestEntityValidateRejectsDuplicateDetector(t *testing.T) {
	spec := EntitySpec{
		Type: "issue",
		Detectors: []DetectorSpec{
			{Name: "url-detector", Kind: DetectorURL},
			{Name: "url-detector", Kind: DetectorURL},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for duplicate detector")
	}
}

func TestEntityValidateRejectsDetectorWithoutKind(t *testing.T) {
	spec := EntitySpec{
		Type:      "issue",
		Detectors: []DetectorSpec{{Name: "d"}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for detector missing kind")
	}
}

func TestEntityValidateRejectsEmptyDetectorName(t *testing.T) {
	spec := EntitySpec{
		Type:      "issue",
		Detectors: []DetectorSpec{{Name: "", Kind: DetectorRegex}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty detector name")
	}
}

// --- EntitySpec.Supports ---

func TestEntitySpecSupports(t *testing.T) {
	spec := EntitySpec{
		Type:         "issue",
		Capabilities: []EntityCapability{EntityCapabilitySearch, EntityCapabilityGet},
	}
	if !spec.Supports(EntityCapabilitySearch) {
		t.Error("Supports(search) = false, want true")
	}
	if !spec.Supports(EntityCapabilityGet) {
		t.Error("Supports(get) = false, want true")
	}
	if spec.Supports(EntityCapabilityRelation) {
		t.Error("Supports(relation) = true, want false")
	}
}

// --- Registry ---

func TestRegistryNilAccessor(t *testing.T) {
	_, err := NewRegistry([]Accessor{nil}, nil)
	if err == nil {
		t.Fatal("NewRegistry: want error for nil accessor")
	}
}

func TestRegistryInvalidEntitySpec(t *testing.T) {
	_, err := NewRegistry(nil, []EntitySpec{{Type: ""}})
	if err == nil {
		t.Fatal("NewRegistry: want error for invalid entity spec")
	}
}

func TestRegistryGetMissing(t *testing.T) {
	reg, err := NewRegistry(nil, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Fatal("Get: want false for missing datasource")
	}
}

func TestRegistryNilGet(t *testing.T) {
	var reg *Registry
	_, ok := reg.Get("any")
	if ok {
		t.Fatal("nil registry Get: want false")
	}
}

func TestRegistryNilAll(t *testing.T) {
	var reg *Registry
	if reg.All() != nil {
		t.Fatal("nil registry All: want nil")
	}
}

func TestRegistryNilEntity(t *testing.T) {
	var reg *Registry
	_, ok := reg.Entity("any")
	if ok {
		t.Fatal("nil registry Entity: want false")
	}
}

func TestRegistryNilEntities(t *testing.T) {
	var reg *Registry
	if reg.Entities() != nil {
		t.Fatal("nil registry Entities: want nil")
	}
}

func TestRegistryAll(t *testing.T) {
	spec := Spec{Name: "docs", Entities: []EntityType{"file.document"}, Kind: "filesystem"}
	accessor := testAccessor{spec: spec, entity: EntitySpec{Type: "file.document"}}
	reg, err := NewRegistry([]Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(reg.All()) != 1 {
		t.Fatalf("All() len = %d, want 1", len(reg.All()))
	}
}

func TestRegistryGetAndEntity(t *testing.T) {
	spec := Spec{Name: "docs", Entities: []EntityType{"file.document"}, Kind: "filesystem"}
	accessor := testAccessor{spec: spec, entity: EntitySpec{Type: "file.document"}}
	reg, err := NewRegistry([]Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	got, ok := reg.Get("docs")
	if !ok || got.Spec().Name != "docs" {
		t.Fatalf("Get(docs) = %v, ok=%v", got, ok)
	}
	entity, ok := reg.Entity("file.document")
	if !ok || entity.Type != "file.document" {
		t.Fatalf("Entity(file.document) = %v, ok=%v", entity, ok)
	}
}

func TestRegistryEntities(t *testing.T) {
	spec := Spec{Name: "docs", Entities: []EntityType{"file.document"}, Kind: "filesystem"}
	accessor := testAccessor{spec: spec, entity: EntitySpec{Type: "file.document"}}
	reg, err := NewRegistry([]Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	all := reg.Entities()
	if len(all) == 0 {
		t.Fatal("Entities(): want at least one entity")
	}
}

func TestRegistryAccessorInvalidSpec(t *testing.T) {
	// An accessor whose Spec() is invalid should fail NewRegistry.
	accessor := testAccessor{spec: Spec{}, entity: EntitySpec{}}
	_, err := NewRegistry([]Accessor{accessor}, nil)
	if err == nil {
		t.Fatal("NewRegistry: want error for accessor with invalid spec")
	}
}

// --- Context helpers ---

func TestDetectionInputContextRoundTrip(t *testing.T) {
	input := DetectionInput{
		Sources: []DetectionSource{{ID: "s1", Kind: "text", Text: "hello"}},
		MaxRefs: 5,
	}
	ctx := ContextWithDetectionInput(context.Background(), input)
	got, ok := DetectionInputFromContext(ctx)
	if !ok {
		t.Fatal("DetectionInputFromContext: want ok=true")
	}
	if got.MaxRefs != 5 || len(got.Sources) != 1 || got.Sources[0].ID != "s1" {
		t.Fatalf("DetectionInputFromContext = %+v, want %+v", got, input)
	}
}

func TestDetectionInputContextNil(t *testing.T) {
	_, ok := DetectionInputFromContext(nil)
	if ok {
		t.Fatal("DetectionInputFromContext(nil): want ok=false")
	}
}

func TestDetectionInputContextMissing(t *testing.T) {
	_, ok := DetectionInputFromContext(context.Background())
	if ok {
		t.Fatal("DetectionInputFromContext: want ok=false when not set")
	}
}

func TestContextWithDetectionInputNilCtx(t *testing.T) {
	// nil ctx should not panic — it becomes Background internally.
	ctx := ContextWithDetectionInput(nil, DetectionInput{MaxRefs: 1})
	got, ok := DetectionInputFromContext(ctx)
	if !ok || got.MaxRefs != 1 {
		t.Fatalf("ContextWithDetectionInput(nil,...): got=%+v ok=%v", got, ok)
	}
}

func TestDetectedRefsContextRoundTrip(t *testing.T) {
	refs := []RecordRef{
		{Datasource: "docs", Entity: "file.document", ID: "123"},
	}
	ctx := ContextWithDetectedRefs(context.Background(), refs)
	got, ok := DetectedRefsFromContext(ctx)
	if !ok || len(got) != 1 || got[0].ID != "123" {
		t.Fatalf("DetectedRefsFromContext = %v ok=%v", got, ok)
	}
	// Mutating original should not affect stored copy.
	refs[0].ID = "mutated"
	if got[0].ID != "123" {
		t.Fatal("DetectedRefsFromContext: stored refs should be independent of original")
	}
}

func TestDetectedRefsContextNil(t *testing.T) {
	_, ok := DetectedRefsFromContext(nil)
	if ok {
		t.Fatal("DetectedRefsFromContext(nil): want ok=false")
	}
}

func TestDetectedRefsContextMissing(t *testing.T) {
	_, ok := DetectedRefsFromContext(context.Background())
	if ok {
		t.Fatal("DetectedRefsFromContext: want ok=false when not set")
	}
}

func TestContextWithDetectedRefsNilCtx(t *testing.T) {
	ctx := ContextWithDetectedRefs(nil, []RecordRef{{ID: "x"}})
	got, ok := DetectedRefsFromContext(ctx)
	if !ok || len(got) != 1 {
		t.Fatalf("ContextWithDetectedRefs(nil,...): got=%v ok=%v", got, ok)
	}
}

func TestAccessPolicyContextNilCtx(t *testing.T) {
	ctx := ContextWithAccessPolicy(nil, AccessPolicy{Datasources: []Name{"x"}})
	policy, ok := AccessPolicyFromContext(ctx)
	if !ok || len(policy.Datasources) != 1 {
		t.Fatalf("ContextWithAccessPolicy(nil,...): got=%v ok=%v", policy, ok)
	}
}
