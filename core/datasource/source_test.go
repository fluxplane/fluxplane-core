package datasource

import (
	"testing"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
)

func TestSpecFromDataSourceDerivesCatalogSpec(t *testing.T) {
	source := coredata.SourceSpec{
		Name:        " jira ",
		Kind:        " jira ",
		Description: " Jira issues. ",
		Config:      map[string]string{"instance": "work"},
		Annotations: map[string]string{"owner": "team"},
		Entities: []coredata.EntitySpec{
			{Type: "jira.issue"},
			{Type: " "},
			{Type: "jira.project"},
		},
	}

	spec := SpecFromDataSource(source)
	spec.Config["instance"] = "changed"
	spec.Annotations["owner"] = "changed"

	if spec.Name != "jira" || spec.Kind != "jira" || spec.Description != "Jira issues." {
		t.Fatalf("spec identity = %#v, want trimmed source values", spec)
	}
	if len(spec.Entities) != 2 || spec.Entities[0] != EntityType("jira.issue") || spec.Entities[1] != EntityType("jira.project") {
		t.Fatalf("entities = %#v, want non-empty source entities", spec.Entities)
	}
	if source.Config["instance"] != "work" || source.Annotations["owner"] != "team" {
		t.Fatalf("source maps mutated: %#v %#v", source.Config, source.Annotations)
	}
}
