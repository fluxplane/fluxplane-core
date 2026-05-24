package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	corediscovery "github.com/fluxplane/fluxplane-core/core/discovery"
)

// ProviderSpec describes one registered discovery provider.
type ProviderSpec struct {
	Name        string   `json:"name"`
	Source      string   `json:"source,omitempty"`
	Products    []string `json:"products,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Provider discovers endpoint candidates for a request.
type Provider interface {
	Spec() ProviderSpec
	Discover(context.Context, corediscovery.Request) (corediscovery.Result, error)
}

// ProviderStatus is the current in-memory status for one provider.
type ProviderStatus struct {
	Spec        ProviderSpec `json:"spec"`
	LastRun     string       `json:"last_run,omitempty"`
	LastError   string       `json:"last_error,omitempty"`
	LastResults int          `json:"last_results,omitempty"`
}

// Registry stores endpoint discovery providers and their last-run status.
type Registry struct {
	mu        sync.RWMutex
	providers []Provider
	status    map[string]ProviderStatus
}

// NewRegistry returns an empty discovery provider registry.
func NewRegistry() *Registry {
	return &Registry{status: map[string]ProviderStatus{}}
}

// Register appends provider unless another provider with the same name exists.
func (r *Registry) Register(provider Provider) error {
	if r == nil {
		return fmt.Errorf("discovery: registry is nil")
	}
	if provider == nil {
		return fmt.Errorf("discovery: provider is nil")
	}
	spec := provider.Spec()
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("discovery: provider name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.providers {
		if existing.Spec().Name == spec.Name {
			return nil
		}
	}
	r.providers = append(r.providers, provider)
	if r.status == nil {
		r.status = map[string]ProviderStatus{}
	}
	r.status[spec.Name] = ProviderStatus{Spec: cloneSpec(spec)}
	return nil
}

// Providers returns registered provider specs.
func (r *Registry) Providers() []ProviderSpec {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderSpec, 0, len(r.providers))
	for _, provider := range r.providers {
		out = append(out, cloneSpec(provider.Spec()))
	}
	return out
}

// Status returns current provider status.
func (r *Registry) Status() []ProviderStatus {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderStatus, 0, len(r.status))
	for _, status := range r.status {
		status.Spec = cloneSpec(status.Spec)
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spec.Name < out[j].Spec.Name })
	return out
}

// Discover asks matching providers for endpoint candidates.
func (r *Registry) Discover(ctx context.Context, req corediscovery.Request) (corediscovery.Result, error) {
	if r == nil {
		return corediscovery.Result{}, fmt.Errorf("discovery: registry is nil")
	}
	providers := r.matchingProviders(req.Product)
	if len(req.Providers) > 0 {
		allowed := map[string]bool{}
		for _, name := range req.Providers {
			name = strings.TrimSpace(name)
			if name != "" {
				allowed[name] = true
			}
		}
		filtered := providers[:0]
		for _, provider := range providers {
			if allowed[provider.Spec().Name] {
				filtered = append(filtered, provider)
			}
		}
		providers = filtered
	}
	var out corediscovery.Result
	var firstErr error
	for _, provider := range providers {
		result, err := provider.Discover(ctx, req)
		r.recordRun(provider.Spec(), len(result.Candidates), err)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out.EndpointRefs = append(out.EndpointRefs, result.EndpointRefs...)
		out.Candidates = append(out.Candidates, result.Candidates...)
		out.Probes = append(out.Probes, result.Probes...)
	}
	sort.SliceStable(out.Candidates, func(i, j int) bool {
		if out.Candidates[i].Score == out.Candidates[j].Score {
			return out.Candidates[i].ID < out.Candidates[j].ID
		}
		return out.Candidates[i].Score > out.Candidates[j].Score
	})
	if req.Limit > 0 && len(out.Candidates) > req.Limit {
		out.Candidates = out.Candidates[:req.Limit]
	}
	if len(out.Candidates) == 0 && firstErr != nil {
		return out, firstErr
	}
	return out, nil
}

func (r *Registry) matchingProviders(product string) []Provider {
	product = strings.TrimSpace(product)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Provider
	for _, provider := range r.providers {
		spec := provider.Spec()
		if product == "" || supportsProduct(spec, product) {
			out = append(out, provider)
		}
	}
	return out
}

func (r *Registry) recordRun(spec ProviderSpec, results int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == nil {
		r.status = map[string]ProviderStatus{}
	}
	status := r.status[spec.Name]
	status.Spec = cloneSpec(spec)
	status.LastRun = time.Now().UTC().Format(time.RFC3339)
	status.LastResults = results
	status.LastError = ""
	if err != nil {
		status.LastError = err.Error()
	}
	r.status[spec.Name] = status
}

func supportsProduct(spec ProviderSpec, product string) bool {
	for _, candidate := range spec.Products {
		if strings.TrimSpace(candidate) == product {
			return true
		}
	}
	return false
}

func cloneSpec(spec ProviderSpec) ProviderSpec {
	spec.Products = append([]string(nil), spec.Products...)
	return spec
}
