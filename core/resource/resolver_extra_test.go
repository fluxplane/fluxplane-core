package resource

import "testing"

func TestResolverNilReturnsError(t *testing.T) {
	var r *Resolver
	_, err := r.Resolve("agent", "main")
	if err == nil {
		t.Fatal("nil resolver Resolve: want error")
	}
}

func TestResolverEmptyRefReturnsError(t *testing.T) {
	r := NewResolver(ResolverConfig{})
	_, err := r.Resolve("agent", "")
	if err == nil {
		t.Fatal("empty ref Resolve: want error")
	}
}

func TestResolverMissingRefReturnsError(t *testing.T) {
	r := NewResolver(ResolverConfig{})
	_, err := r.Resolve("agent", "nonexistent")
	if err == nil {
		t.Fatal("missing ref Resolve: want error")
	}
}

func TestResolverResolvesWithAlias(t *testing.T) {
	index := NewResourceIndex()
	id := ResourceID{Kind: "agent", Name: "main", Origin: "embedded"}
	index.Add(id)
	r := NewResolver(ResolverConfig{
		Index:   index,
		Aliases: map[string]string{"default": "main"},
	})
	resolved, err := r.Resolve("agent", "default")
	if err != nil {
		t.Fatalf("Resolve(alias): %v", err)
	}
	if resolved.Name != "main" {
		t.Fatalf("resolved.Name = %q, want main", resolved.Name)
	}
}

func TestResolverSetAliasAndResolve(t *testing.T) {
	index := NewResourceIndex()
	id := ResourceID{Kind: "agent", Name: "assistant", Origin: "local"}
	index.Add(id)
	r := NewResolver(ResolverConfig{Index: index})
	r.SetAlias("default", "assistant")
	resolved, err := r.Resolve("agent", "default")
	if err != nil {
		t.Fatalf("Resolve after SetAlias: %v", err)
	}
	if resolved.Name != "assistant" {
		t.Fatalf("resolved.Name = %q, want assistant", resolved.Name)
	}
}

func TestResolverSetAliasNilSafe(t *testing.T) {
	var r *Resolver
	// Must not panic.
	r.SetAlias("a", "b")
}

func TestResolverIndexReturnsIndex(t *testing.T) {
	index := NewResourceIndex()
	r := NewResolver(ResolverConfig{Index: index})
	if r.Index() != index {
		t.Fatal("Index(): want same index back")
	}
}

func TestResolverIndexNilSafe(t *testing.T) {
	var r *Resolver
	if r.Index() != nil {
		t.Fatal("nil resolver Index(): want nil")
	}
}

func TestResolverCachesResolution(t *testing.T) {
	index := NewResourceIndex()
	id := ResourceID{Kind: "agent", Name: "main", Origin: "embedded"}
	index.Add(id)
	r := NewResolver(ResolverConfig{Index: index})

	// Resolve twice — second call uses the cache path.
	r1, err := r.Resolve("agent", "main")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	r2, err := r.Resolve("agent", "main")
	if err != nil {
		t.Fatalf("second Resolve (cached): %v", err)
	}
	if !r1.Equal(r2) {
		t.Fatalf("cached result differs: %v != %v", r1, r2)
	}
}

func TestResolverAmbiguityWithPrecedencePolicy(t *testing.T) {
	// Two candidates with known different origins — PrecedencePolicy picks first.
	index := NewResourceIndex()
	local := ResourceID{Kind: "agent", Name: "main", Origin: "local"}
	embedded := ResourceID{Kind: "agent", Name: "main", Origin: "embedded"}
	index.Add(local)
	index.Add(embedded)

	r := NewResolver(ResolverConfig{
		Index:  index,
		Policy: PrecedencePolicy{Order: []string{"local", "embedded"}},
	})
	resolved, err := r.Resolve("agent", "main")
	if err != nil {
		t.Fatalf("Resolve with precedence: %v", err)
	}
	if resolved.Origin != "local" {
		t.Fatalf("resolved.Origin = %q, want local", resolved.Origin)
	}
}

func TestResolverAmbiguityErrorPolicy(t *testing.T) {
	index := NewResourceIndex()
	index.Add(ResourceID{Kind: "agent", Name: "main", Origin: "a"})
	index.Add(ResourceID{Kind: "agent", Name: "main", Origin: "b"})
	r := NewResolver(ResolverConfig{Index: index, Policy: ErrorPolicy{}})
	_, err := r.Resolve("agent", "main")
	if err == nil {
		t.Fatal("Resolve ambiguous ref with ErrorPolicy: want error")
	}
}

func TestResolveInScopeNilFallsThrough(t *testing.T) {
	// nil resolver should propagate the inner Resolve error.
	var r *Resolver
	_, err := r.ResolveInScope("agent", "main", ResourceID{})
	if err == nil {
		t.Fatal("nil resolver ResolveInScope: want error")
	}
}

func TestResolveInScopeQualifiedRefSkipsScope(t *testing.T) {
	index := NewResourceIndex()
	id := ResourceID{Kind: "agent", Name: "assistant", Origin: "embedded", Namespace: NewNamespace("apps")}
	index.Add(id)
	r := NewResolver(ResolverConfig{Index: index})
	scope := ResourceID{Kind: "agent", Name: "main", Origin: "embedded", Namespace: NewNamespace("apps")}

	// A qualified ref (contains ":") bypasses scope and falls through to Resolve.
	resolved, err := r.ResolveInScope("agent", "embedded:apps:assistant", scope)
	if err != nil {
		t.Fatalf("ResolveInScope(qualified): %v", err)
	}
	if resolved.Name != "assistant" {
		t.Fatalf("resolved.Name = %q, want assistant", resolved.Name)
	}
}

func TestNewResolverNilIndexCreatesOne(t *testing.T) {
	r := NewResolver(ResolverConfig{Index: nil})
	if r.Index() == nil {
		t.Fatal("NewResolver(nil index): want index created automatically")
	}
}

func TestPrecedencePolicyTied(t *testing.T) {
	// Two candidates with the same rank → ambiguity error.
	candidates := []ResourceID{
		{Kind: "agent", Name: "main", Origin: "local"},
		{Kind: "agent", Name: "main", Origin: "local"},
	}
	p := PrecedencePolicy{Order: []string{"local"}}
	_, err := p.Resolve("agent", "main", candidates)
	if err == nil {
		t.Fatal("PrecedencePolicy tied: want ambiguity error")
	}
}

func TestPrecedencePolicyUnrankedOriginLoses(t *testing.T) {
	candidates := []ResourceID{
		{Kind: "agent", Name: "main", Origin: "unknown"},
		{Kind: "agent", Name: "main", Origin: "local"},
	}
	p := PrecedencePolicy{Order: []string{"local"}}
	resolved, err := p.Resolve("agent", "main", candidates)
	if err != nil {
		t.Fatalf("PrecedencePolicy unranked: %v", err)
	}
	if resolved.Origin != "local" {
		t.Fatalf("resolved.Origin = %q, want local", resolved.Origin)
	}
}
