package discovery

import (
	"context"
	"testing"

	corediscovery "github.com/fluxplane/engine/core/discovery"
)

func TestRegistryDiscoversFromMatchingProviders(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(staticProvider{name: "loki", products: []string{"loki"}, candidates: []corediscovery.Candidate{{ID: "b", Score: 1}, {ID: "a", Score: 2}}}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Register(staticProvider{name: "other", products: []string{"other"}, candidates: []corediscovery.Candidate{{ID: "other", Score: 10}}}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	result, err := registry.Discover(context.Background(), corediscovery.Request{Product: "loki", Limit: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].ID != "a" {
		t.Fatalf("candidates = %#v, want highest scoring loki candidate", result.Candidates)
	}
	status := registry.Status()
	if len(status) != 2 {
		t.Fatalf("status len = %d, want 2", len(status))
	}
}

type staticProvider struct {
	name       string
	products   []string
	candidates []corediscovery.Candidate
}

func (p staticProvider) Spec() ProviderSpec {
	return ProviderSpec{Name: p.name, Products: p.products}
}

func (p staticProvider) Discover(context.Context, corediscovery.Request) (corediscovery.Result, error) {
	return corediscovery.Result{Candidates: p.candidates}, nil
}
