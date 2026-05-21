package datasource

import (
	"testing"

	coredatasource "github.com/fluxplane/engine/core/datasource"
)

type testEntity struct {
	ID     string `json:"id" datasource:"id,filterable" jsonschema:"description=Stable id.,required"`
	Title  string `json:"title,omitempty" datasource:"searchable,sortable" corpus:"title" jsonschema:"description=Display title."`
	Hidden string `json:"-"`
}

func TestEntityOfDerivesFieldSpecsFromTags(t *testing.T) {
	spec := EntityOf[testEntity]("test.entity", "Test entity.")
	if spec.Type != coredatasource.EntityType("test.entity") {
		t.Fatalf("type = %q, want test.entity", spec.Type)
	}
	if len(spec.Fields) != 2 {
		t.Fatalf("fields len = %d, want 2: %#v", len(spec.Fields), spec.Fields)
	}
	id := spec.Fields[0]
	if id.Name != "id" || !id.Identifier || !id.Filterable || !id.Required || id.Description != "Stable id." {
		t.Fatalf("id field = %#v", id)
	}
	title := spec.Fields[1]
	if title.Name != "title" || !title.Searchable || !title.Sortable || !title.Corpus || title.Required {
		t.Fatalf("title field = %#v", title)
	}
}
