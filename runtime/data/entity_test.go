package data

import (
	"testing"

	coredata "github.com/fluxplane/engine/core/data"
)

type testSourceEntity struct {
	ID     string `json:"id" datasource:"id,filterable" jsonschema:"description=Stable id.,required"`
	Title  string `json:"title,omitempty" datasource:"searchable,sortable" corpus:"title" jsonschema:"description=Display title."`
	Hidden string `json:"-"`
}

type testView struct {
	ID     string             `json:"id" datasource:"id,filterable" jsonschema:"description=Stable id."`
	Groups []testViewGroupRef `json:"groups"`
}

type testViewGroupRef struct {
	ID   string `json:"id" datasource:"filterable" jsonschema:"description=Group id."`
	Path string `json:"path" datasource:"searchable,filterable" jsonschema:"description=Group path."`
	Name string `json:"name" datasource:"searchable" jsonschema:"description=Group name."`
}

func TestSourceEntityOfDerivesFieldSpecsFromTags(t *testing.T) {
	spec := SourceEntityOf[testSourceEntity]("test.entity", "Test entity.")
	if spec.Type != "test.entity" || spec.Description != "Test entity." {
		t.Fatalf("spec = %#v, want type and description", spec)
	}
	if len(spec.Fields) != 2 {
		t.Fatalf("fields len = %d, want 2: %#v", len(spec.Fields), spec.Fields)
	}
	id := spec.Fields[0]
	if id.Name != "id" || id.Type != coredata.FieldString || !id.Identifier || !id.Filterable || !id.Required || id.Description != "Stable id." {
		t.Fatalf("id field = %#v", id)
	}
	title := spec.Fields[1]
	if title.Name != "title" || !title.Searchable || !title.Sortable || !title.Corpus || title.Required {
		t.Fatalf("title field = %#v", title)
	}
}

func TestViewOfDerivesNestedMaterializedFields(t *testing.T) {
	view := ViewOf[testView](
		"test.user_with_groups",
		"test.user",
		WithViewEntity("test.user"),
		WithViewDescription("Users with groups."),
		WithViewIncludes(coredata.RelationIncludeSpec{Relation: "groups", Target: "test.group", Fields: []string{"id", "path", "name"}}),
		WithViewQueryHints(coredata.QueryList, coredata.QuerySearch, coredata.QueryRelation),
	)
	if err := (coredata.SourceSpec{Name: "test", Entities: []coredata.EntitySpec{{Type: "test.user"}}, Views: []coredata.ViewSpec{view}}).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	fields := map[string]coredata.FieldSpec{}
	for _, field := range view.Fields {
		fields[field.Name] = field
	}
	if _, ok := fields["groups"]; ok {
		t.Fatalf("fields = %#v, did not want container field for nested relation", view.Fields)
	}
	for _, name := range []string{"id", "groups.id", "groups.path", "groups.name"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("missing field %q in %#v", name, view.Fields)
		}
	}
	if !fields["groups.path"].Searchable || !fields["groups.path"].Filterable {
		t.Fatalf("groups.path = %#v, want searchable and filterable", fields["groups.path"])
	}
	if len(view.Includes) != 1 || view.Includes[0].Relation != "groups" {
		t.Fatalf("includes = %#v, want groups include", view.Includes)
	}
}
