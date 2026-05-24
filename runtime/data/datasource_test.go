package data

import (
	"testing"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
)

func TestSourceFromDatasourceConvertsSchema(t *testing.T) {
	source := SourceFromDatasource("gitlab", "gitlab", []coredatasource.EntitySpec{{
		Type: "gitlab.user",
		Capabilities: []coredatasource.EntityCapability{
			coredatasource.EntityCapabilitySearch,
			coredatasource.EntityCapabilityIndex,
		},
		Fields: []coredatasource.FieldSpec{{
			Name:       "username",
			Type:       coredatasource.FieldString,
			Searchable: true,
		}},
		Relations: []coredatasource.RelationSpec{{
			Name:         "groups",
			TargetEntity: "gitlab.group",
			Exact:        true,
		}},
	}}, coredata.ViewSpec{Name: "gitlab.user", Source: "gitlab.user"})
	if err := source.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	entity := source.Entities[0]
	if entity.Capabilities[1] != coredata.CapabilityMaterialize {
		t.Fatalf("capabilities = %#v, want materialize", entity.Capabilities)
	}
	if entity.Relations[0].SourceEntity != "gitlab.user" || entity.Relations[0].TargetEntity != "gitlab.group" {
		t.Fatalf("relation = %#v, want source/target", entity.Relations[0])
	}
}
