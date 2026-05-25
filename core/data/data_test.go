package data

import (
	"context"
	"testing"
)

func TestSourceSpecValidateRejectsDuplicateViews(t *testing.T) {
	spec := SourceSpec{
		Name: "gitlab",
		Entities: []EntitySpec{{
			Type: "gitlab.user",
		}},
		Views: []ViewSpec{
			{Name: "gitlab.user", Source: "gitlab.user"},
			{Name: "gitlab.user", Source: "gitlab.user"},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want duplicate view error")
	}
}

func TestScopeMatchesSelector(t *testing.T) {
	scope := Scope{TenantID: "tenant-a", AppID: "app-a", SessionID: "session-a"}
	if !scope.Matches(Scope{TenantID: "tenant-a", AppID: "app-a"}) {
		t.Fatal("Matches: want true for matching selector")
	}
	if scope.Matches(Scope{TenantID: "tenant-b"}) {
		t.Fatal("Matches: want false for mismatched tenant")
	}
}

func TestStoreInterfaceShape(t *testing.T) {
	var _ Store = noopStore{}
}

type noopStore struct{}

func (noopStore) UpsertRecords(context.Context, ...Record) error { return nil }
func (noopStore) DeleteRecords(context.Context, Scope, ...Ref) error {
	return nil
}
func (noopStore) GetRecord(context.Context, Scope, Ref) (Record, bool, error) {
	return Record{}, false, nil
}
func (noopStore) BatchGetRecords(context.Context, Scope, ...Ref) ([]Record, error) {
	return nil, nil
}
func (noopStore) QueryRecords(context.Context, Query) (QueryResult, error) {
	return QueryResult{}, nil
}
func (noopStore) UpsertRelations(context.Context, ...Relation) error { return nil }
func (noopStore) QueryRelations(context.Context, RelationQuery) (RelationResult, error) {
	return RelationResult{}, nil
}
func (noopStore) PutBlob(context.Context, Blob) (BlobRef, error) { return BlobRef{}, nil }
func (noopStore) GetBlob(context.Context, BlobRef) (Blob, bool, error) {
	return Blob{}, false, nil
}

func TestSourceSpecValidateRejectsEmptyName(t *testing.T) {
	spec := SourceSpec{Name: ""}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty source name")
	} else if err.Error() != "data: source name is empty" {
		t.Errorf("Validate error = %q, want %q", err.Error(), "data: source name is empty")
	}
}

func TestSourceSpecValidateRejectsDuplicateEntities(t *testing.T) {
	spec := SourceSpec{
		Name: "gitlab",
		Entities: []EntitySpec{
			{Type: "gitlab.user"},
			{Type: "gitlab.user"},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for duplicate entity")
	}
}

func TestSourceSpecValidateRejectsEmptyEntityType(t *testing.T) {
	spec := SourceSpec{
		Name:     "gitlab",
		Entities: []EntitySpec{{Type: ""}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty entity type")
	}
}

func TestSourceSpecValidateRejectsEmptyViewName(t *testing.T) {
	spec := SourceSpec{
		Name:  "gitlab",
		Views: []ViewSpec{{Name: "", Source: "gitlab.user"}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty view name")
	}
}

func TestSourceSpecValidateRejectsEmptyViewSource(t *testing.T) {
	spec := SourceSpec{
		Name:  "gitlab",
		Views: []ViewSpec{{Name: "gitlab.user", Source: ""}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty view source")
	}
}

func TestSourceSpecValidateRejectsDuplicateFields(t *testing.T) {
	spec := SourceSpec{
		Name: "gitlab",
		Entities: []EntitySpec{{
			Type: "gitlab.user",
			Fields: []FieldSpec{
				{Name: "id"},
				{Name: "id"},
			},
		}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for duplicate field")
	}
}

func TestSourceSpecValidateRejectsEmptyFieldName(t *testing.T) {
	spec := SourceSpec{
		Name: "gitlab",
		Entities: []EntitySpec{{
			Type:   "gitlab.user",
			Fields: []FieldSpec{{Name: ""}},
		}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for empty field name")
	}
}

func TestSourceSpecValidateRejectsDuplicateViewFields(t *testing.T) {
	spec := SourceSpec{
		Name: "gitlab",
		Views: []ViewSpec{{
			Name:   "gitlab.user",
			Source: "gitlab.user",
			Fields: []FieldSpec{
				{Name: "id"},
				{Name: "id"},
			},
		}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate: want error for duplicate view field")
	}
}

func TestSourceSpecValidateValid(t *testing.T) {
	spec := SourceSpec{
		Name: "gitlab",
		Entities: []EntitySpec{{
			Type:   "gitlab.user",
			Fields: []FieldSpec{{Name: "id", Type: FieldString, Required: true}},
		}},
		Views: []ViewSpec{{
			Name:   "gitlab.user",
			Source: "gitlab.user",
			Fields: []FieldSpec{{Name: "id"}},
		}},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestFieldTypeConstants(t *testing.T) {
	if FieldString != "string" {
		t.Errorf("FieldString = %q, want %q", FieldString, "string")
	}
	if FieldNumber != "number" {
		t.Errorf("FieldNumber = %q, want %q", FieldNumber, "number")
	}
	if FieldBoolean != "boolean" {
		t.Errorf("FieldBoolean = %q, want %q", FieldBoolean, "boolean")
	}
	if FieldObject != "object" {
		t.Errorf("FieldObject = %q, want %q", FieldObject, "object")
	}
	if FieldArray != "array" {
		t.Errorf("FieldArray = %q, want %q", FieldArray, "array")
	}
	if FieldAny != "any" {
		t.Errorf("FieldAny = %q, want %q", FieldAny, "any")
	}
}

func TestEntityCapabilityConstants(t *testing.T) {
	caps := []struct {
		got  string
		want string
	}{
		{string(CapabilitySearch), "search"},
		{string(CapabilityList), "list"},
		{string(CapabilityGet), "get"},
		{string(CapabilityRelation), "relation"},
		{string(CapabilityMaterialize), "materialize"},
		{string(CapabilitySemanticSearch), "semantic_search"},
	}
	for _, c := range caps {
		if c.got != c.want {
			t.Errorf("EntityCapability %q = %q, want %q", c.want, c.got, c.want)
		}
	}
}

func TestQueryHintConstants(t *testing.T) {
	if QueryGet != "get" {
		t.Errorf("QueryGet = %q, want %q", QueryGet, "get")
	}
	if QueryList != "list" {
		t.Errorf("QueryList = %q, want %q", QueryList, "list")
	}
	if QuerySearch != "search" {
		t.Errorf("QuerySearch = %q, want %q", QuerySearch, "search")
	}
	if QueryRelation != "relation" {
		t.Errorf("QueryRelation = %q, want %q", QueryRelation, "relation")
	}
	if QueryAggregate != "aggregate" {
		t.Errorf("QueryAggregate = %q, want %q", QueryAggregate, "aggregate")
	}
}

func TestQueryModeConstants(t *testing.T) {
	if QueryModeExact != "exact" {
		t.Errorf("QueryModeExact = %q, want %q", QueryModeExact, "exact")
	}
	if QueryModeList != "list" {
		t.Errorf("QueryModeList = %q, want %q", QueryModeList, "list")
	}
	if QueryModeLexical != "lexical" {
		t.Errorf("QueryModeLexical = %q, want %q", QueryModeLexical, "lexical")
	}
	if QueryModeSemantic != "semantic" {
		t.Errorf("QueryModeSemantic = %q, want %q", QueryModeSemantic, "semantic")
	}
	if QueryModeHybrid != "hybrid" {
		t.Errorf("QueryModeHybrid = %q, want %q", QueryModeHybrid, "hybrid")
	}
}

func TestScopeMatchesAnnotations(t *testing.T) {
	scope := Scope{Annotations: map[string]string{"env": "prod"}}
	if !scope.Matches(Scope{Annotations: map[string]string{"env": "prod"}}) {
		t.Fatal("Matches: want true for matching annotations")
	}
	if scope.Matches(Scope{Annotations: map[string]string{"env": "dev"}}) {
		t.Fatal("Matches: want false for mismatched annotation value")
	}
}

func TestScopeMatchesAllDimensions(t *testing.T) {
	scope := Scope{TenantID: "t1", AppID: "app1", WorkspaceID: "ws1", UserID: "u1", AgentID: "ag1", SessionID: "s1", ThreadID: "th1", ChannelID: "ch1"}
	if !scope.Matches(Scope{}) {
		t.Fatal("Matches: empty selector should match")
	}
}

func TestScopeMatchesAgentID(t *testing.T) {
	scope := Scope{AgentID: "agent-1"}
	if !scope.Matches(Scope{AgentID: "agent-1"}) {
		t.Fatal("Matches: want true for matching agent")
	}
	if scope.Matches(Scope{AgentID: "agent-2"}) {
		t.Fatal("Matches: want false for mismatched agent")
	}
}

func TestRef(t *testing.T) {
	r := Ref{Source: "gitlab", Entity: "gitlab.user", View: "default", ID: "123"}
	if r.Source != "gitlab" || r.ID != "123" {
		t.Errorf("Ref = %#v", r)
	}
}

func TestRecord(t *testing.T) {
	rec := Record{
		Ref:      Ref{Source: "gitlab", Entity: "gitlab.user", ID: "123"},
		Scope:    Scope{TenantID: "t1"},
		Title:    "Alice",
		Content:  "User record",
		URL:      "https://gitlab.example/users/123",
		Fields:   map[string][]string{"name": {"Alice"}},
		Score:    0.95,
		Metadata: map[string]string{"source": "gitlab"},
	}
	if rec.Title != "Alice" || rec.Score != 0.95 {
		t.Errorf("Record = %#v", rec)
	}
}

func TestSummary(t *testing.T) {
	sum := Summary{
		Ref:    Ref{Source: "gitlab", Entity: "gitlab.user", ID: "123"},
		Title:  "Alice",
		Fields: map[string]string{"name": "Alice"},
	}
	if sum.Title != "Alice" {
		t.Errorf("Summary = %#v", sum)
	}
}

func TestRelation(t *testing.T) {
	rel := Relation{
		Source: Ref{Source: "gitlab", Entity: "gitlab.user", ID: "1"},
		Name:   "member",
		Target: Ref{Source: "gitlab", Entity: "gitlab.group", ID: "2"},
		Scope:  Scope{TenantID: "t1"},
	}
	if rel.Name != "member" {
		t.Errorf("Relation = %#v", rel)
	}
}

func TestBlobRef(t *testing.T) {
	br := BlobRef{
		ID:        "blob-1",
		Scope:     Scope{TenantID: "t1"},
		MediaType: "image/png",
		Size:      1024,
		Digest:    "sha256:abc",
	}
	if br.MediaType != "image/png" || br.Size != 1024 {
		t.Errorf("BlobRef = %#v", br)
	}
}

func TestBlob(t *testing.T) {
	blob := Blob{
		Ref:     BlobRef{ID: "blob-1", MediaType: "text/plain"},
		Content: []byte("hello world"),
	}
	if string(blob.Content) != "hello world" {
		t.Errorf("Blob = %#v", blob)
	}
}

func TestQuery(t *testing.T) {
	q := Query{
		Scope:    Scope{TenantID: "t1"},
		Sources:  []SourceName{"gitlab"},
		Entities: []EntityType{"gitlab.user"},
		Text:     "alice",
		Limit:    50,
		Mode:     QueryModeSemantic,
	}
	if q.Text != "alice" || q.Limit != 50 {
		t.Errorf("Query = %#v", q)
	}
}

func TestQueryResult(t *testing.T) {
	res := QueryResult{
		Records:    []Record{{Ref: Ref{ID: "1"}}},
		NextCursor: "cursor-1",
		Complete:   false,
	}
	if len(res.Records) != 1 || res.NextCursor != "cursor-1" {
		t.Errorf("QueryResult = %#v", res)
	}
}

func TestRelationQuery(t *testing.T) {
	rq := RelationQuery{
		Scope:    Scope{TenantID: "t1"},
		Relation: "member",
		Limit:    100,
	}
	if rq.Relation != "member" {
		t.Errorf("RelationQuery = %#v", rq)
	}
}

func TestRelationResult(t *testing.T) {
	res := RelationResult{
		Relations:  []Relation{{Name: "member"}},
		NextCursor: "cursor-1",
		Complete:   true,
	}
	if len(res.Relations) != 1 {
		t.Errorf("RelationResult = %#v", res)
	}
}

func TestRelationIncludeSpec(t *testing.T) {
	ris := RelationIncludeSpec{
		Relation: "members",
		Target:   "gitlab.group",
		Fields:   []string{"id", "name"},
	}
	if ris.Relation != "members" || len(ris.Fields) != 2 {
		t.Errorf("RelationIncludeSpec = %#v", ris)
	}
}

func TestRelationFilter(t *testing.T) {
	rf := RelationFilter{
		Relation: "owner",
		Target:   "gitlab.user",
		Filters:  map[string]string{"role": "admin"},
	}
	if rf.Relation != "owner" {
		t.Errorf("RelationFilter = %#v", rf)
	}
}
