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

func TestAccessPolicyContextRoundTrip(t *testing.T) {
	ctx := ContextWithAccessPolicy(context.Background(), AccessPolicy{Datasources: []Name{"docs"}})
	policy, ok := AccessPolicyFromContext(ctx)
	if !ok || len(policy.Datasources) != 1 || policy.Datasources[0] != "docs" {
		t.Fatalf("policy = %#v ok=%v", policy, ok)
	}
}
