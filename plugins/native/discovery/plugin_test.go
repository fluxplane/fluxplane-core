package discovery

import (
	"context"
	"testing"

	corediscovery "github.com/fluxplane/agentruntime/core/discovery"
	coreendpoint "github.com/fluxplane/agentruntime/core/endpoint"
	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	runtimediscovery "github.com/fluxplane/agentruntime/runtime/discovery"
	runtimeendpoint "github.com/fluxplane/agentruntime/runtime/endpoint"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
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

func TestEndpointRegistryEvidenceDerivesEndpointAvailability(t *testing.T) {
	endpoints := runtimeendpoint.NewRegistry(0)
	ref, err := endpoints.Put(runtimeendpoint.Record{Spec: coreendpoint.Spec{Name: "dev-loki", URL: "http://loki:3100", Product: "loki"}})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	observer := endpointRegistryObserver{endpoints: endpoints}
	observations, diagnostics := runtimeenvironment.RunObservers(context.Background(), []runtimeenvironment.Observer{observer}, runtimeenvironment.ObservationRequest{Phase: coreenvironment.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(observations) != 1 || observations[0].Kind != ObservationEndpointRegistry {
		t.Fatalf("observations = %#v", observations)
	}
	deriver := endpointSignalDeriver{}
	signals, diagnostics := runtimeenvironment.DeriveSignals(context.Background(), []runtimeenvironment.SignalDeriver{deriver}, runtimeenvironment.SignalDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(signals) != 1 || signals[0].Kind != SignalEndpointAvailable || signals[0].Target != "loki" {
		t.Fatalf("signals = %#v, want endpoint.available loki", signals)
	}
	if signals[0].Metadata["endpoint_ref"] != string(ref) {
		t.Fatalf("signal metadata = %#v, want endpoint ref %q", signals[0].Metadata, ref)
	}
}

func TestEndpointRegistryEvidenceDoesNotDeriveAvailabilityForUnprobedProviderCandidates(t *testing.T) {
	endpoints := runtimeendpoint.NewRegistry(0)
	_, err := endpoints.Put(runtimeendpoint.Record{
		Spec: coreendpoint.Spec{Name: "kubernetes-loki", URL: "http://loki:3100", Product: "loki"},
		Metadata: map[string]string{
			"provider": "kubernetes",
			"product":  "loki",
		},
		Owner: "kubernetes",
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	observer := endpointRegistryObserver{endpoints: endpoints}
	observations, diagnostics := runtimeenvironment.RunObservers(context.Background(), []runtimeenvironment.Observer{observer}, runtimeenvironment.ObservationRequest{Phase: coreenvironment.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(observations) != 1 || observations[0].Kind != ObservationEndpointRegistry {
		t.Fatalf("observations = %#v", observations)
	}
	deriver := endpointSignalDeriver{}
	signals, diagnostics := runtimeenvironment.DeriveSignals(context.Background(), []runtimeenvironment.SignalDeriver{deriver}, runtimeenvironment.SignalDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(signals) != 0 {
		t.Fatalf("signals = %#v, want no endpoint availability for unprobed provider candidate", signals)
	}
}

func TestEndpointRegistryEvidenceDerivesAvailabilityForReadyProviderCandidates(t *testing.T) {
	endpoints := runtimeendpoint.NewRegistry(0)
	_, err := endpoints.Put(runtimeendpoint.Record{
		Spec: coreendpoint.Spec{Name: "kubernetes-loki", URL: "http://loki:3100", Product: "loki"},
		Metadata: map[string]string{
			"provider":     "kubernetes",
			"product":      "loki",
			"probe_status": "ready",
		},
		Owner: "kubernetes",
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	observer := endpointRegistryObserver{endpoints: endpoints}
	observations, diagnostics := runtimeenvironment.RunObservers(context.Background(), []runtimeenvironment.Observer{observer}, runtimeenvironment.ObservationRequest{Phase: coreenvironment.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	deriver := endpointSignalDeriver{}
	signals, diagnostics := runtimeenvironment.DeriveSignals(context.Background(), []runtimeenvironment.SignalDeriver{deriver}, runtimeenvironment.SignalDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(signals) != 1 || signals[0].Kind != SignalEndpointAvailable || signals[0].Target != "loki" {
		t.Fatalf("signals = %#v, want endpoint.available loki", signals)
	}
}

type testProvider struct{}

func (testProvider) Spec() runtimediscovery.ProviderSpec {
	return runtimediscovery.ProviderSpec{Name: "test", Products: []string{"loki"}}
}

func (testProvider) Discover(ctx context.Context, req corediscovery.Request) (corediscovery.Result, error) {
	return corediscovery.Result{}, nil
}
