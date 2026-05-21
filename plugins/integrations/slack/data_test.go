package slack

import (
	"testing"

	coredata "github.com/fluxplane/engine/core/data"
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
		SlackUserView,
		SlackChannelView,
		SlackMessageView,
		SlackChannelWithMembersView,
	} {
		if _, ok := views[name]; !ok {
			t.Fatalf("missing view %q in %#v", name, spec.Views)
		}
	}
	withMembers := views[SlackChannelWithMembersView]
	if len(withMembers.Includes) != 1 || withMembers.Includes[0].Relation != "members" || withMembers.Includes[0].Target != coredata.EntityType(UserEntity) {
		t.Fatalf("channel_with_members includes = %#v, want members -> slack.user", withMembers.Includes)
	}
	fields := map[string]coredata.FieldSpec{}
	for _, field := range withMembers.Fields {
		fields[field.Name] = field
	}
	for _, name := range []string{"name", "members.name", "members.email", "members.display_name"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("missing field %q in %#v", name, withMembers.Fields)
		}
	}
	if !fields["members.email"].Searchable || !fields["members.email"].Filterable {
		t.Fatalf("members.email = %#v, want reflected searchable/filterable metadata", fields["members.email"])
	}
}
