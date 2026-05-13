package pluginhost

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
)

func TestHostResolvesPluginContributions(t *testing.T) {
	host, err := New(fakePlugin{name: "echo"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "echo"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Bundles) != 1 || len(resolution.Bundles[0].Commands) != 1 {
		t.Fatalf("resolution = %#v", resolution)
	}
}

func TestHostRejectsUnknownPlugin(t *testing.T) {
	host, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = host.Resolve(context.Background(), resource.PluginRef{Name: "missing"})
	if err == nil {
		t.Fatal("Resolve error is nil, want unknown plugin error")
	}
}

type fakePlugin struct {
	name string
}

func (p fakePlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakePlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Commands: []command.Spec{{Path: command.Path{"echo"}}},
	}, nil
}

func TestHostResolvesPluginOperations(t *testing.T) {
	host, err := New(fakeOperationPlugin{name: "echo"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "echo"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Operations) != 1 {
		t.Fatalf("operations len = %d, want 1", len(resolution.Operations))
	}
	if resolution.Operations[0].Operation.Spec().Ref.Name != "echo" {
		t.Fatalf("operation = %#v", resolution.Operations[0].Operation.Spec().Ref)
	}
	if resolution.Operations[0].Source.ID != "plugin:echo" {
		t.Fatalf("source ID = %q, want plugin:echo", resolution.Operations[0].Source.ID)
	}
}

func TestHostResolvesConnectorProviders(t *testing.T) {
	host, err := New(fakeConnectorProviderPlugin{name: "openai"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "openai"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.ConnectorProviders) != 1 {
		t.Fatalf("connector providers len = %d, want 1", len(resolution.ConnectorProviders))
	}
	if resolution.ConnectorProviders[0].Provider.Name != "openai" {
		t.Fatalf("provider = %#v, want openai", resolution.ConnectorProviders[0].Provider)
	}
	if resolution.ConnectorProviders[0].Source.ID != "plugin:openai" {
		t.Fatalf("source ID = %q, want plugin:openai", resolution.ConnectorProviders[0].Source.ID)
	}
}

func TestHostResolvesDatasourceProviders(t *testing.T) {
	host, err := New(fakeDatasourceProviderPlugin{name: "docs"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "docs"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.DatasourceProviders) != 1 {
		t.Fatalf("datasource providers len = %d, want 1", len(resolution.DatasourceProviders))
	}
	if len(resolution.DatasourceProviders[0].Provider.Entities()) != 1 {
		t.Fatalf("provider = %#v, want one entity", resolution.DatasourceProviders[0].Provider)
	}
	if resolution.DatasourceProviders[0].Source.ID != "plugin:docs" {
		t.Fatalf("source ID = %q, want plugin:docs", resolution.DatasourceProviders[0].Source.ID)
	}
}

func TestHostResolvesContextProviders(t *testing.T) {
	host, err := New(fakeContextProviderPlugin{name: "docs"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "docs"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.ContextProviders) != 1 {
		t.Fatalf("context providers len = %d, want 1", len(resolution.ContextProviders))
	}
	if resolution.ContextProviders[0].Provider.Spec().Name != "docs.catalog" {
		t.Fatalf("provider = %#v, want docs.catalog", resolution.ContextProviders[0].Provider.Spec())
	}
	if resolution.ContextProviders[0].Source.ID != "plugin:docs" {
		t.Fatalf("source ID = %q, want plugin:docs", resolution.ContextProviders[0].Source.ID)
	}
}

type fakeOperationPlugin struct {
	name string
}

func (p fakeOperationPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeOperationPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeOperationPlugin) Operations(context.Context, Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operation.New(operation.Spec{Ref: operation.Ref{Name: operation.Name(p.name)}}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(input)
		}),
	}, nil
}

type fakeConnectorProviderPlugin struct {
	name string
}

func (p fakeConnectorProviderPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeConnectorProviderPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeConnectorProviderPlugin) ConnectorProviders(context.Context, Context) ([]ConnectorProvider, error) {
	return []ConnectorProvider{{Name: p.name}}, nil
}

type fakeDatasourceProviderPlugin struct {
	name string
}

func (p fakeDatasourceProviderPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeDatasourceProviderPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeDatasourceProviderPlugin) DatasourceProviders(context.Context, Context) ([]coredatasource.Provider, error) {
	return []coredatasource.Provider{fakeDatasourceProvider{}}, nil
}

type fakeDatasourceProvider struct{}

func (fakeDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{{Type: "docs.page"}}
}

func (fakeDatasourceProvider) Open(context.Context, coredatasource.Spec) (coredatasource.Accessor, error) {
	return nil, nil
}

type fakeContextProviderPlugin struct {
	name string
}

func (p fakeContextProviderPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeContextProviderPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeContextProviderPlugin) ContextProviders(context.Context, Context) ([]corecontext.Provider, error) {
	return []corecontext.Provider{fakeContextProvider{}}, nil
}

type fakeContextProvider struct{}

func (fakeContextProvider) Spec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{Name: "docs.catalog"}
}

func (fakeContextProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	return []corecontext.Block{{Provider: "docs.catalog", Kind: corecontext.BlockText, Content: "docs"}}, nil
}
