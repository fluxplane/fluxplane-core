package resource

import (
	"fmt"
	"strings"
	"sync"
)

// ResolverPolicy chooses a winner when a ref matches multiple resources.
type ResolverPolicy interface {
	Resolve(kind string, ref string, candidates []ResourceID) (ResourceID, error)
}

// PrecedencePolicy chooses the candidate whose origin appears earliest in
// Order. Ties fall back to ambiguity errors.
type PrecedencePolicy struct {
	Order []string
}

// DefaultPrecedenceOrder is the default origin precedence.
var DefaultPrecedenceOrder = []string{"explicit", "local", "user", "embedded"}

// Resolve chooses one candidate by origin precedence.
func (p PrecedencePolicy) Resolve(kind string, ref string, candidates []ResourceID) (ResourceID, error) {
	order := p.Order
	if len(order) == 0 {
		order = DefaultPrecedenceOrder
	}
	rank := make(map[string]int, len(order))
	for i, origin := range order {
		rank[origin] = i
	}
	best := -1
	bestRank := len(order) + 1
	tied := false
	for i, candidate := range candidates {
		candidateRank, ok := rank[candidate.Origin]
		if !ok {
			candidateRank = len(order)
		}
		if candidateRank < bestRank {
			best = i
			bestRank = candidateRank
			tied = false
			continue
		}
		if candidateRank == bestRank {
			tied = true
		}
	}
	if best < 0 || tied {
		return ResourceID{}, ambiguityError(kind, ref, candidates)
	}
	return candidates[best], nil
}

// ErrorPolicy rejects ambiguous refs.
type ErrorPolicy struct{}

// Resolve always returns an ambiguity error.
func (ErrorPolicy) Resolve(kind string, ref string, candidates []ResourceID) (ResourceID, error) {
	return ResourceID{}, ambiguityError(kind, ref, candidates)
}

// Resolver resolves local/user refs to canonical resource IDs.
type Resolver struct {
	mu       sync.RWMutex
	index    *ResourceIndex
	policy   ResolverPolicy
	aliases  map[string]string
	resolved map[string]string
}

// ResolverConfig configures a resource resolver.
type ResolverConfig struct {
	Index   *ResourceIndex
	Policy  ResolverPolicy
	Aliases map[string]string
}

// NewResolver returns a resolver.
func NewResolver(cfg ResolverConfig) *Resolver {
	index := cfg.Index
	if index == nil {
		index = NewResourceIndex()
	}
	policy := cfg.Policy
	if policy == nil {
		policy = PrecedencePolicy{}
	}
	aliases := make(map[string]string, len(cfg.Aliases))
	for key, value := range cfg.Aliases {
		aliases[key] = value
	}
	return &Resolver{
		index:    index,
		policy:   policy,
		aliases:  aliases,
		resolved: map[string]string{},
	}
}

// Resolve maps kind/ref to a canonical resource ID.
func (r *Resolver) Resolve(kind, ref string) (ResourceID, error) {
	if r == nil {
		return ResourceID{}, fmt.Errorf("resource: resolver is nil")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ResourceID{}, fmt.Errorf("resource: empty %s ref", kind)
	}
	r.mu.RLock()
	if alias, ok := r.aliases[ref]; ok {
		ref = alias
	}
	cacheKey := kind + ":" + ref
	if cached, ok := r.resolved[cacheKey]; ok {
		candidates := r.index.LookupRef(kind, cached)
		r.mu.RUnlock()
		if len(candidates) == 1 {
			return candidates[0], nil
		}
	} else {
		r.mu.RUnlock()
	}

	candidates := r.index.LookupRef(kind, ref)
	switch len(candidates) {
	case 0:
		return ResourceID{}, fmt.Errorf("no %s found matching %q", kind, ref)
	case 1:
		r.cacheResolution(cacheKey, candidates[0].Address())
		return candidates[0], nil
	default:
		winner, err := r.policy.Resolve(kind, ref, candidates)
		if err != nil {
			return ResourceID{}, err
		}
		r.cacheResolution(cacheKey, winner.Address())
		return winner, nil
	}
}

// ResolveInScope resolves ref, preferring the same origin/namespace as scope
// when ref is unqualified.
func (r *Resolver) ResolveInScope(kind string, ref string, scope ResourceID) (ResourceID, error) {
	if r == nil || len(splitRef(ref)) > 1 || scope.IsZero() {
		return r.Resolve(kind, ref)
	}
	candidates := r.index.Lookup(kind, strings.TrimSpace(ref))
	var scoped []ResourceID
	for _, candidate := range candidates {
		if candidate.Origin == scope.Origin && candidate.Namespace.Equal(scope.Namespace) {
			scoped = append(scoped, candidate)
		}
	}
	if len(scoped) == 1 {
		return scoped[0], nil
	}
	return r.Resolve(kind, ref)
}

// SetAlias adds or replaces an alias.
func (r *Resolver) SetAlias(name, target string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.aliases == nil {
		r.aliases = map[string]string{}
	}
	r.aliases[name] = target
}

// Index returns the underlying index.
func (r *Resolver) Index() *ResourceIndex {
	if r == nil {
		return nil
	}
	return r.index
}

func (r *Resolver) cacheResolution(key, address string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolved == nil {
		r.resolved = map[string]string{}
	}
	r.resolved[key] = address
}

func ambiguityError(kind string, ref string, candidates []ResourceID) error {
	var b strings.Builder
	fmt.Fprintf(&b, "ambiguous %s %q matches %d resources:", kind, ref, len(candidates))
	for _, candidate := range candidates {
		fmt.Fprintf(&b, "\n  - %s", candidate.Address())
	}
	return fmt.Errorf("%s", b.String())
}
