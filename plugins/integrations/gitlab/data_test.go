package gitlab

import (
	"testing"

	coredata "github.com/fluxplane/agentruntime/core/data"
)

func TestDataSourceSpecDeclaresViews(t *testing.T) {
	spec := DataSourceSpec()
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if spec.Kind != Name {
		t.Fatalf("kind = %q, want %q", spec.Kind, Name)
	}
	views := map[coredata.ViewName]coredata.ViewSpec{}
	for _, view := range spec.Views {
		views[view.Name] = view
	}
	for _, name := range []coredata.ViewName{
		GitLabProjectView,
		GitLabUserView,
		GitLabGroupView,
		MembershipDataView,
		GitLabUserWithGroupsView,
	} {
		if _, ok := views[name]; !ok {
			t.Fatalf("missing view %q in %#v", name, spec.Views)
		}
	}
	withGroups := views[GitLabUserWithGroupsView]
	if len(withGroups.Includes) != 1 || withGroups.Includes[0].Relation != "groups" || withGroups.Includes[0].Target != coredata.EntityType(GroupEntity) {
		t.Fatalf("user_with_groups includes = %#v, want groups -> gitlab.group", withGroups.Includes)
	}
	fields := map[string]coredata.FieldSpec{}
	for _, field := range withGroups.Fields {
		fields[field.Name] = field
	}
	for _, name := range []string{"username", "groups.path", "groups.full_path", "groups.name"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("missing field %q in %#v", name, withGroups.Fields)
		}
	}
	if !fields["groups.path"].Searchable || !fields["groups.path"].Filterable {
		t.Fatalf("groups.path = %#v, want reflected searchable/filterable metadata", fields["groups.path"])
	}
}
