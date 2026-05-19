package datasourceplugin

import (
	"context"
	"fmt"

	coredata "github.com/fluxplane/agentruntime/core/data"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
)

// RegistryOptions configures datasource registry construction.
type RegistryOptions struct {
	SemanticIndex *semantic.Index
	DataSources   []coredata.SourceSpec
}

// BuildRegistry materializes configured datasource specs through providers.
func BuildRegistry(ctx context.Context, specs []coredatasource.Spec, providers []coredatasource.Provider) (*coredatasource.Registry, error) {
	return BuildRegistryWithOptions(ctx, specs, providers, RegistryOptions{})
}

// BuildRegistryWithOptions materializes configured datasource specs through
// providers and passes optional runtime services to providers that accept them.
func BuildRegistryWithOptions(ctx context.Context, specs []coredatasource.Spec, providers []coredatasource.Provider, opts RegistryOptions) (*coredatasource.Registry, error) {
	var accessors []coredatasource.Accessor
	var entities []coredatasource.EntitySpec
	providers = providersWithOptions(providers, opts)
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		entities = append(entities, provider.Entities()...)
	}
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		accessor, err := openDatasource(ctx, spec, providers)
		if err != nil {
			return nil, fmt.Errorf("datasource %q: %w", spec.Name, err)
		}
		accessors = append(accessors, accessor)
	}
	return coredatasource.NewRegistry(accessors, entities)
}

func providersWithOptions(providers []coredatasource.Provider, opts RegistryOptions) []coredatasource.Provider {
	if opts.SemanticIndex == nil {
		return providers
	}
	out := append([]coredatasource.Provider(nil), providers...)
	for i, provider := range out {
		if provider == nil {
			continue
		}
		indexAware, ok := provider.(interface {
			WithSemanticIndex(*semantic.Index) coredatasource.Provider
		})
		if ok {
			out[i] = indexAware.WithSemanticIndex(opts.SemanticIndex)
		}
	}
	return out
}

func openDatasource(ctx context.Context, spec coredatasource.Spec, providers []coredatasource.Provider) (coredatasource.Accessor, error) {
	var lastErr error
	var matched bool
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		if !providerSupportsEntities(provider, spec.Entities) {
			continue
		}
		matched = true
		accessor, err := provider.Open(ctx, spec)
		if err == nil {
			return accessor, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if !matched {
		return nil, fmt.Errorf("unsupported entities %q", spec.Entities)
	}
	return nil, fmt.Errorf("no datasource provider is available")
}

func providerSupportsEntities(provider coredatasource.Provider, entities []coredatasource.EntityType) bool {
	if len(entities) == 0 {
		return false
	}
	available := map[coredatasource.EntityType]bool{}
	for _, entity := range provider.Entities() {
		if entity.Type != "" {
			available[entity.Type] = true
		}
	}
	for _, entity := range entities {
		if !available[entity] {
			return false
		}
	}
	return true
}
