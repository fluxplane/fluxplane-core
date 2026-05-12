package resource

import "sync"

// ResourceIndex indexes resource IDs by local name while allowing collisions
// across origins and namespaces.
type ResourceIndex struct {
	mu     sync.RWMutex
	byName map[string][]ResourceID
}

// NewResourceIndex returns an empty resource index.
func NewResourceIndex() *ResourceIndex {
	return &ResourceIndex{byName: map[string][]ResourceID{}}
}

// Add inserts id. Duplicate canonical IDs are ignored.
func (idx *ResourceIndex) Add(id ResourceID) {
	if idx == nil || id.Name == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.byName == nil {
		idx.byName = map[string][]ResourceID{}
	}
	existing := idx.byName[id.Name]
	for _, current := range existing {
		if current.Equal(id) {
			return
		}
	}
	idx.byName[id.Name] = append(existing, id)
}

// Lookup returns IDs with matching kind and name. Empty kind matches all kinds.
func (idx *ResourceIndex) Lookup(kind, name string) []ResourceID {
	if idx == nil {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	candidates := idx.byName[name]
	out := make([]ResourceID, 0, len(candidates))
	for _, candidate := range candidates {
		if kind == "" || candidate.Kind == kind {
			out = append(out, candidate)
		}
	}
	return out
}

// LookupRef returns IDs matching kind and user/local ref.
func (idx *ResourceIndex) LookupRef(kind, ref string) []ResourceID {
	parts := splitRef(ref)
	if len(parts) == 0 {
		return nil
	}
	name := parts[len(parts)-1]
	candidates := idx.Lookup(kind, name)
	out := make([]ResourceID, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.MatchesRef(ref) {
			out = append(out, candidate)
		}
	}
	return out
}

// All returns all resource IDs.
func (idx *ResourceIndex) All() []ResourceID {
	if idx == nil {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var out []ResourceID
	for _, ids := range idx.byName {
		out = append(out, ids...)
	}
	return out
}
