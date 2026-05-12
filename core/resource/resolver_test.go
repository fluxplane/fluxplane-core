package resource

import "testing"

func TestResolverAllowsSameNameAcrossNamespaces(t *testing.T) {
	index := NewResourceIndex()
	index.Add(ResourceID{Kind: "operation", Origin: "embedded", Namespace: NewNamespace("plugins", "foo"), Name: "my-op"})
	index.Add(ResourceID{Kind: "operation", Origin: "embedded", Namespace: NewNamespace("plugins", "bar"), Name: "my-op"})
	resolver := NewResolver(ResolverConfig{Index: index, Policy: ErrorPolicy{}})

	foo, err := resolver.Resolve("operation", "foo:my-op")
	if err != nil {
		t.Fatalf("Resolve foo: %v", err)
	}
	if got, want := foo.Address(), "embedded:plugins/foo:my-op"; got != want {
		t.Fatalf("foo address = %q, want %q", got, want)
	}

	bar, err := resolver.Resolve("operation", "bar:my-op")
	if err != nil {
		t.Fatalf("Resolve bar: %v", err)
	}
	if got, want := bar.Address(), "embedded:plugins/bar:my-op"; got != want {
		t.Fatalf("bar address = %q, want %q", got, want)
	}

	if _, err := resolver.Resolve("operation", "my-op"); err == nil {
		t.Fatal("Resolve my-op error is nil, want ambiguity")
	}
}

func TestResolverResolveInScopePrefersSiblingResource(t *testing.T) {
	index := NewResourceIndex()
	fooOp := ResourceID{Kind: "operation", Origin: "embedded", Namespace: NewNamespace("plugins", "foo"), Name: "run"}
	barOp := ResourceID{Kind: "operation", Origin: "embedded", Namespace: NewNamespace("plugins", "bar"), Name: "run"}
	fooCommand := ResourceID{Kind: "command", Origin: "embedded", Namespace: NewNamespace("plugins", "foo"), Name: "run"}
	index.Add(fooOp)
	index.Add(barOp)
	index.Add(fooCommand)
	resolver := NewResolver(ResolverConfig{Index: index, Policy: ErrorPolicy{}})

	resolved, err := resolver.ResolveInScope("operation", "run", fooCommand)
	if err != nil {
		t.Fatalf("ResolveInScope: %v", err)
	}
	if !resolved.Equal(fooOp) {
		t.Fatalf("resolved = %s, want %s", resolved.Address(), fooOp.Address())
	}
}
