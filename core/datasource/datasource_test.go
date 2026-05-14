package datasource

import (
	"context"
	"testing"
)

type testAccessor struct {
	spec   Spec
	entity EntitySpec
}

func (a testAccessor) Spec() Spec { return a.spec }
func (a testAccessor) Entities() []EntitySpec {
	return []EntitySpec{a.entity}
}

func TestSpecValidateRequiresEntitiesAndBoundary(t *testing.T) {
	tests := []Spec{
		{Name: "docs", Kind: "filesystem"},
		{Name: "docs", Entities: []EntityType{"file.document"}},
	}
	for _, spec := range tests {
		if err := spec.Validate(); err == nil {
			t.Fatalf("Validate(%#v) error is nil, want error", spec)
		}
	}
}

func TestRegistryRejectsDuplicateDatasource(t *testing.T) {
	spec := Spec{Name: "docs", Entities: []EntityType{"file.document"}, Kind: "filesystem"}
	entity := EntitySpec{Type: "file.document"}
	_, err := NewRegistry([]Accessor{
		testAccessor{spec: spec, entity: entity},
		testAccessor{spec: spec, entity: entity},
	}, nil)
	if err == nil {
		t.Fatal("NewRegistry error is nil, want duplicate error")
	}
}

func TestEntityValidateRejectsInvalidRelations(t *testing.T) {
	tests := []EntitySpec{
		{Type: "team.group", Relations: []RelationSpec{{TargetEntity: "team.user"}}},
		{Type: "team.group", Relations: []RelationSpec{{Name: "members"}}},
		{Type: "team.group", Relations: []RelationSpec{{Name: "members", TargetEntity: "team.user"}, {Name: "members", TargetEntity: "team.user"}}},
	}
	for _, spec := range tests {
		if err := spec.Validate(); err == nil {
			t.Fatalf("Validate(%#v) error is nil, want error", spec)
		}
	}
}

func TestEntityValidateAcceptsRelation(t *testing.T) {
	spec := EntitySpec{
		Type: "team.group",
		Relations: []RelationSpec{{
			Name:         "members",
			TargetEntity: "team.user",
			Exact:        true,
		}},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestAccessPolicyContextRoundTrip(t *testing.T) {
	ctx := ContextWithAccessPolicy(context.Background(), AccessPolicy{Datasources: []Name{"docs"}})
	policy, ok := AccessPolicyFromContext(ctx)
	if !ok || len(policy.Datasources) != 1 || policy.Datasources[0] != "docs" {
		t.Fatalf("policy = %#v ok=%v", policy, ok)
	}
}

func TestAccessPolicyContextMissing(t *testing.T) {
	ctx := context.Background()
	policy, ok := AccessPolicyFromContext(ctx)
	if ok || policy.Datasources != nil {
		t.Fatalf("policy = %#v ok=%v, want missing", policy, ok)
	}
}

func TestSpecValidateAllowsValidSpec(t *testing.T) {
	spec := Spec{
		Name:       "docs",
		Kind:       "filesystem",
		Entities:   []EntityType{"file.document"},
		Connector:  "filesystem",
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestEntityValidateRejectsEmptyType(t *testing.T) {
	spec := EntitySpec{Type: ""}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for empty type")
	}
}

func TestEntityValidateRejectsEmptyRelationName(t *testing.T) {
	spec := EntitySpec{
		Type: "team.group",
		Relations: []RelationSpec{{
			Name:         "",
			TargetEntity: "team.user",
		}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for empty relation name")
	}
}

func TestEntitySpecValidateAllowsNoRelations(t *testing.T) {
	spec := EntitySpec{Type: "team.group"}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
