package resource

import "testing"

func TestResourceIndexAddNilIgnored(t *testing.T) {
	var idx *ResourceIndex
	// Should not panic.
	idx.Add(ResourceID{Name: "agent", Kind: "agent"})
}

func TestResourceIndexAddEmptyName(t *testing.T) {
	idx := NewResourceIndex()
	// ID with empty Name should be silently dropped.
	idx.Add(ResourceID{Kind: "agent", Name: ""})
	if len(idx.All()) != 0 {
		t.Fatal("Add(emptyName): want index to stay empty")
	}
}

func TestResourceIndexAddDeduplicate(t *testing.T) {
	idx := NewResourceIndex()
	id := ResourceID{Kind: "agent", Name: "main", Origin: "bundle"}
	idx.Add(id)
	idx.Add(id) // duplicate
	if len(idx.All()) != 1 {
		t.Fatalf("Add(duplicate): want 1 entry, got %d", len(idx.All()))
	}
}

func TestResourceIndexLookupNilReturnsNil(t *testing.T) {
	var idx *ResourceIndex
	result := idx.Lookup("agent", "main")
	if result != nil {
		t.Fatal("Lookup on nil index: want nil")
	}
}

func TestResourceIndexLookupByKind(t *testing.T) {
	idx := NewResourceIndex()
	idx.Add(ResourceID{Kind: "agent", Name: "main", Origin: "bundle"})
	idx.Add(ResourceID{Kind: "workflow", Name: "main", Origin: "bundle"})

	agents := idx.Lookup("agent", "main")
	if len(agents) != 1 || agents[0].Kind != "agent" {
		t.Fatalf("Lookup(agent,main): got %v, want 1 agent", agents)
	}
	all := idx.Lookup("", "main")
	if len(all) != 2 {
		t.Fatalf("Lookup('',main): got %d, want 2", len(all))
	}
}

func TestResourceIndexAllNilReturnsNil(t *testing.T) {
	var idx *ResourceIndex
	if idx.All() != nil {
		t.Fatal("All on nil index: want nil")
	}
}

func TestResourceIndexAllEmpty(t *testing.T) {
	idx := NewResourceIndex()
	if len(idx.All()) != 0 {
		t.Fatal("All on empty index: want empty")
	}
}

func TestResourceIndexLookupRef(t *testing.T) {
	idx := NewResourceIndex()
	id := ResourceID{Kind: "agent", Name: "main", Origin: "bundle", Namespace: NewNamespace("apps")}
	idx.Add(id)

	results := idx.LookupRef("agent", "main")
	if len(results) == 0 {
		t.Fatal("LookupRef(agent,main): want at least one match")
	}
}

func TestResourceIndexLookupRefEmptyRef(t *testing.T) {
	idx := NewResourceIndex()
	results := idx.LookupRef("agent", "")
	if results != nil {
		t.Fatalf("LookupRef(agent,''): want nil, got %v", results)
	}
}
