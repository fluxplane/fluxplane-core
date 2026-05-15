package resource

import (
	"encoding/json"
	"testing"
)

func TestNamespaceSegments(t *testing.T) {
	ns := NewNamespace("a", "b", "c")
	segs := ns.Segments()
	if len(segs) != 3 || segs[0] != "a" || segs[1] != "b" || segs[2] != "c" {
		t.Fatalf("Segments = %v, want [a b c]", segs)
	}
	// Returned slice must be a copy.
	segs[0] = "mutated"
	if ns.segments[0] != "a" {
		t.Fatal("Segments returned aliased slice")
	}
}

func TestNamespaceLast(t *testing.T) {
	if NewNamespace("x", "y").Last() != "y" {
		t.Fatal("Last() should return last segment")
	}
	if NewNamespace().Last() != "" {
		t.Fatal("Last() on empty namespace should return empty string")
	}
}

func TestNamespaceIsEmpty(t *testing.T) {
	if !NewNamespace().IsEmpty() {
		t.Fatal("IsEmpty() for empty namespace = false, want true")
	}
	if NewNamespace("x").IsEmpty() {
		t.Fatal("IsEmpty() for non-empty namespace = true, want false")
	}
}

func TestNamespaceEqual(t *testing.T) {
	a := NewNamespace("x", "y")
	b := NewNamespace("x", "y")
	c := NewNamespace("x")
	if !a.Equal(b) {
		t.Fatal("Equal should be true for identical namespaces")
	}
	if a.Equal(c) {
		t.Fatal("Equal should be false for different-length namespaces")
	}
}

func TestNamespaceSuffixMatch(t *testing.T) {
	ns := NewNamespace("plugins", "foo", "bar")
	if !ns.SuffixMatch([]string{"foo", "bar"}) {
		t.Fatal("SuffixMatch([foo bar]) = false, want true")
	}
	if !ns.SuffixMatch([]string{"bar"}) {
		t.Fatal("SuffixMatch([bar]) = false, want true")
	}
	if ns.SuffixMatch([]string{"plugins", "foo", "bar", "extra"}) {
		t.Fatal("SuffixMatch with longer suffix = true, want false")
	}
	if ns.SuffixMatch([]string{"baz"}) {
		t.Fatal("SuffixMatch([baz]) = true, want false")
	}
}

func TestNamespaceAppend(t *testing.T) {
	ns := NewNamespace("a").Append("b", "c")
	if ns.String() != "a/b/c" {
		t.Fatalf("Append result = %q, want a/b/c", ns.String())
	}
}

func TestNamespaceMarshalUnmarshalJSON(t *testing.T) {
	ns := NewNamespace("plugins", "foo")
	raw, err := json.Marshal(ns)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var ns2 Namespace
	if err := json.Unmarshal(raw, &ns2); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if !ns.Equal(ns2) {
		t.Fatalf("roundtrip mismatch: %v != %v", ns, ns2)
	}
}

func TestResourceIDString(t *testing.T) {
	id := ResourceID{Origin: "local", Namespace: NewNamespace("ns"), Name: "res"}
	got := id.String()
	if got != "local:ns:res" {
		t.Fatalf("String() = %q, want local:ns:res", got)
	}
}

func TestResourceIDAddressNoOrigin(t *testing.T) {
	id := ResourceID{Name: "res"}
	if got := id.Address(); got != "res" {
		t.Fatalf("Address() = %q, want res", got)
	}
}

func TestResourceIDAddressOriginNoNamespace(t *testing.T) {
	id := ResourceID{Origin: "local", Name: "res"}
	if got := id.Address(); got != "local:res" {
		t.Fatalf("Address() = %q, want local:res", got)
	}
}

func TestDeriveOriginCases(t *testing.T) {
	cases := []struct {
		scope     Scope
		ecosystem string
		want      string
	}{
		{ScopeProject, "", "local"},
		{ScopeUser, "", "user"},
		{ScopeEmbedded, "myeco", "myeco"},
		{ScopeEmbedded, "", "embedded"},
		{ScopeRemote, "", "remote"},
		{ScopeExplicit, "", "explicit"},
		{"", "myeco", "myeco"},
		{"", "", "unknown"},
	}
	for _, tc := range cases {
		got := DeriveOrigin(SourceRef{Scope: tc.scope, Ecosystem: tc.ecosystem})
		if got != tc.want {
			t.Errorf("DeriveOrigin(scope=%q, eco=%q) = %q, want %q", tc.scope, tc.ecosystem, got, tc.want)
		}
	}
}

func TestDeriveNamespaceCases(t *testing.T) {
	// ScopeUser => global.
	ns := DeriveNamespace(SourceRef{Scope: ScopeUser})
	if ns.String() != "global" {
		t.Fatalf("ScopeUser namespace = %q, want global", ns.String())
	}

	// ScopeProject with nested location.
	ns = DeriveNamespace(SourceRef{Scope: ScopeProject, Location: "some/dir/file.yaml"})
	if ns.String() != "dir" {
		t.Fatalf("ScopeProject namespace = %q, want dir", ns.String())
	}

	// ScopeEmbedded with location.
	ns = DeriveNamespace(SourceRef{Scope: ScopeEmbedded, Location: "./plugins/foo"})
	if ns.String() != "plugins/foo" {
		t.Fatalf("ScopeEmbedded namespace = %q, want plugins/foo", ns.String())
	}

	// Ref fallback.
	ns = DeriveNamespace(SourceRef{Ref: "my-ref"})
	if ns.String() != "my-ref" {
		t.Fatalf("Ref fallback namespace = %q, want my-ref", ns.String())
	}

	// Location fallback.
	ns = DeriveNamespace(SourceRef{Location: "some/loc"})
	if ns.String() != "some/loc" {
		t.Fatalf("Location fallback namespace = %q, want some/loc", ns.String())
	}

	// Empty.
	ns = DeriveNamespace(SourceRef{})
	if !ns.IsEmpty() {
		t.Fatalf("Empty source namespace = %q, want empty", ns.String())
	}
}

func TestResourceIndexAll(t *testing.T) {
	idx := NewResourceIndex()
	idx.Add(ResourceID{Kind: "op", Origin: "local", Name: "a"})
	idx.Add(ResourceID{Kind: "op", Origin: "local", Name: "b"})
	all := idx.All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
}

func TestResolverSetAlias(t *testing.T) {
	idx := NewResourceIndex()
	idx.Add(ResourceID{Kind: "op", Origin: "local", Name: "my-op"})
	r := NewResolver(ResolverConfig{Index: idx})
	r.SetAlias("alias-op", "my-op")
	got, err := r.Resolve("op", "alias-op")
	if err != nil {
		t.Fatalf("Resolve via alias: %v", err)
	}
	if got.Name != "my-op" {
		t.Fatalf("Resolve via alias name = %q, want my-op", got.Name)
	}
}

func TestNilResolverSetAlias(t *testing.T) {
	var r *Resolver
	r.SetAlias("a", "b") // should not panic
}

func TestNilResolverIndex(t *testing.T) {
	var r *Resolver
	if r.Index() != nil {
		t.Fatal("nil Resolver.Index() should return nil")
	}
}

func TestResolverIndex(t *testing.T) {
	r := NewResolver(ResolverConfig{})
	if r.Index() == nil {
		t.Fatal("Resolver.Index() should not be nil")
	}
}

func TestNilResolverResolve(t *testing.T) {
	var r *Resolver
	_, err := r.Resolve("op", "x")
	if err == nil {
		t.Fatal("nil Resolver.Resolve should return error")
	}
}

func TestResolverResolveMissing(t *testing.T) {
	r := NewResolver(ResolverConfig{})
	_, err := r.Resolve("op", "no-such")
	if err == nil {
		t.Fatal("Resolve for missing resource should return error")
	}
}

func TestResolverResolveEmptyRef(t *testing.T) {
	r := NewResolver(ResolverConfig{})
	_, err := r.Resolve("op", "")
	if err == nil {
		t.Fatal("Resolve with empty ref should return error")
	}
}

func TestResolverResolveAmbiguous(t *testing.T) {
	idx := NewResourceIndex()
	idx.Add(ResourceID{Kind: "op", Origin: "local", Name: "op"})
	idx.Add(ResourceID{Kind: "op", Origin: "user", Name: "op"})
	// ErrorPolicy always returns ambiguity error.
	r := NewResolver(ResolverConfig{Index: idx, Policy: ErrorPolicy{}})
	_, err := r.Resolve("op", "op")
	if err == nil {
		t.Fatal("Resolve ambiguous should return error")
	}
}

func TestPrecedencePolicyPicksBest(t *testing.T) {
	candidates := []ResourceID{
		{Kind: "op", Origin: "user", Name: "op"},
		{Kind: "op", Origin: "local", Name: "op"},
	}
	policy := PrecedencePolicy{Order: []string{"local", "user"}}
	got, err := policy.Resolve("op", "op", candidates)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Origin != "local" {
		t.Fatalf("Winner origin = %q, want local", got.Origin)
	}
}

func TestPrecedencePolicyAmbiguous(t *testing.T) {
	// Two candidates with same origin → ambiguity.
	candidates := []ResourceID{
		{Kind: "op", Origin: "local", Name: "a"},
		{Kind: "op", Origin: "local", Name: "b"},
	}
	policy := PrecedencePolicy{Order: []string{"local"}}
	_, err := policy.Resolve("op", "a", candidates)
	if err == nil {
		t.Fatal("Resolve with tied candidates should return error")
	}
}
