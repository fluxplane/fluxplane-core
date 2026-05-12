package resource

import "testing"

func TestResourceIDMatchesRef(t *testing.T) {
	id := ResourceID{
		Kind:      "operation",
		Origin:    "embedded",
		Namespace: NewNamespace("plugins", "foo"),
		Name:      "my-op",
	}

	for _, ref := range []string{
		"my-op",
		"foo:my-op",
		"plugins:foo:my-op",
		"embedded:plugins/foo:my-op",
		"embedded:plugins:foo:my-op",
		"embedded:my-op",
	} {
		if !id.MatchesRef(ref) {
			t.Fatalf("MatchesRef(%q) = false, want true", ref)
		}
	}
	for _, ref := range []string{"bar:my-op", "local:my-op", "other"} {
		if id.MatchesRef(ref) {
			t.Fatalf("MatchesRef(%q) = true, want false", ref)
		}
	}
}

func TestDeriveResourceIDForPluginSource(t *testing.T) {
	id := DeriveResourceID(SourceRef{
		Ecosystem: "embedded",
		Scope:     ScopeEmbedded,
		Location:  "plugins/foo",
	}, "operation", "my-op")
	if got, want := id.Address(), "embedded:plugins/foo:my-op"; got != want {
		t.Fatalf("Address = %q, want %q", got, want)
	}
}
