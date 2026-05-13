package datasourceplugin

import (
	"context"
	"fmt"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
)

// BuildRegistry materializes configured datasource specs through providers.
func BuildRegistry(ctx context.Context, specs []coredatasource.Spec, providers []coredatasource.Provider) (*coredatasource.Registry, error) {
	var accessors []coredatasource.Accessor
	var entities []coredatasource.EntitySpec
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

func openDatasource(ctx context.Context, spec coredatasource.Spec, providers []coredatasource.Provider) (coredatasource.Accessor, error) {
	var lastErr error
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		accessor, err := provider.Open(ctx, spec)
		if err == nil {
			return accessor, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no datasource provider is available")
}
