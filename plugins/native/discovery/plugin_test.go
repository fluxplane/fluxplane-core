package discovery

import (
	"context"
	"testing"

	corediscovery "github.com/fluxplane/agentruntime/core/discovery"
	coreendpoint "github.com/fluxplane/agentruntime/core/endpoint"
	runtimediscovery "github.com/fluxplane/agentruntime/runtime/discovery"
	runtimeendpoint "github.com/fluxplane/agentruntime/runtime/endpoint"
)

func TestDiscoveryPluginListsProvidersAndEndpoints(t *testing.T) {
	discovery := runtimediscovery.NewRegistry()
	endpoints := runtimeendpoint.NewRegistry(0)
	if err := discovery.Register(testProvider{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	ref, err := endpoints.Put(runtimeendpoint.Record{Spec: coreendpoint.Spec{Name: "dev-loki", URL: "http://loki:3100", Product: "loki"}})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	plugin := Plugin{discovery: discovery, endpoints: endpoints}
	providers, err := plugin.providers(nil, ProvidersInput{})
	if err != nil {
		t.Fatalf("providers() error = %v", err)
	}
	if len(providers.Providers) != 1 || providers.Providers[0].Name != "test" {
		t.Fatalf("providers = %#v", providers)
	}
	list, err := plugin.endpointList(nil, EndpointListInput{Product: "loki"})
	if err != nil {
		t.Fatalf("endpointList() error = %v", err)
	}
	if len(list.Endpoints) != 1 || list.Endpoints[0].Ref != ref {
		t.Fatalf("endpoints = %#v, want ref %q", list.Endpoints, ref)
	}
	got, err := plugin.endpointGet(nil, EndpointGetInput{Ref: ref})
	if err != nil {
		t.Fatalf("endpointGet() error = %v", err)
	}
	if got.Endpoint.URL != "http://loki:3100" {
		t.Fatalf("endpoint = %#v", got.Endpoint)
	}
}

type testProvider struct{}

func (testProvider) Spec() runtimediscovery.ProviderSpec {
	return runtimediscovery.ProviderSpec{Name: "test", Products: []string{"loki"}}
}

func (testProvider) Discover(ctx context.Context, req corediscovery.Request) (corediscovery.Result, error) {
	return corediscovery.Result{}, nil
}
