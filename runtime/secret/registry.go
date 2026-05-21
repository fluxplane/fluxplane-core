package secret

import (
	"context"
	"sync"

	coresecret "github.com/fluxplane/engine/core/secret"
)

// Registry is a mutable resolver chain shared across plugin contribution
// resolution and operation execution.
type Registry struct {
	mu        sync.RWMutex
	resolvers []Resolver
}

// NewRegistry returns an empty secret resolver registry.
func NewRegistry(resolvers ...Resolver) *Registry {
	registry := &Registry{}
	for _, resolver := range resolvers {
		registry.Register(resolver)
	}
	return registry
}

// Register adds resolver to the chain.
func (r *Registry) Register(resolver Resolver) {
	if r == nil || resolver == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolvers = append(r.resolvers, resolver)
}

// ResolveSecret implements Resolver.
func (r *Registry) ResolveSecret(ctx context.Context, ref coresecret.Ref) (coresecret.Material, bool, error) {
	if r == nil {
		return coresecret.Material{}, false, nil
	}
	r.mu.RLock()
	resolvers := append([]Resolver(nil), r.resolvers...)
	r.mu.RUnlock()
	return ChainResolver(resolvers).ResolveSecret(ctx, ref)
}
