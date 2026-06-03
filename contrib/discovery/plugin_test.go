package discovery

import (
	"context"
	"testing"

	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	coreevidence "github.com/fluxplane/fluxplane-evidence"
)

func TestDiscoveryPluginListsProvidersAndEndpoints(t *testing.T) {
	discovery := fpendpoint.NewDiscoveryRegistry()
	endpoints := fpendpoint.NewRegistry(0)
	if err := discovery.Register(testProvider{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	ref, err := endpoints.Put(fpendpoint.RuntimeRecord{Spec: fpendpoint.Spec{Name: "dev-loki", URL: "http://loki:3100", Product: "loki"}})
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
	endpoints := fpendpoint.NewRegistry(0)
	ref, err := endpoints.Put(fpendpoint.RuntimeRecord{Spec: fpendpoint.Spec{Name: "dev-loki", URL: "http://loki:3100", Product: "loki"}})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	observer := endpointRegistryObserver{endpoints: endpoints}
	observations, diagnostics := runtimeevidence.RunObservers(context.Background(), []runtimeevidence.Observer{observer}, runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(observations) != 1 || observations[0].Kind != ObservationEndpointRegistry {
		t.Fatalf("observations = %#v", observations)
	}
	deriver := endpointAssertionDeriver{}
	assertions, diagnostics := runtimeevidence.DeriveAssertions(context.Background(), []runtimeevidence.AssertionDeriver{deriver}, runtimeevidence.AssertionDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(assertions) != 1 || assertions[0].Kind != AssertionEndpointAvailable || assertions[0].Target != "loki" {
		t.Fatalf("assertions = %#v, want endpoint.available loki", assertions)
	}
	if assertions[0].Metadata["endpoint_ref"] != string(ref) {
		t.Fatalf("assertion metadata = %#v, want endpoint ref %q", assertions[0].Metadata, ref)
	}
}

func TestEndpointRegistryEvidenceDoesNotDeriveAvailabilityForUnprobedProviderCandidates(t *testing.T) {
	endpoints := fpendpoint.NewRegistry(0)
	_, err := endpoints.Put(fpendpoint.RuntimeRecord{
		Spec: fpendpoint.Spec{Name: "kubernetes-loki", URL: "http://loki:3100", Product: "loki"},
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
	observations, diagnostics := runtimeevidence.RunObservers(context.Background(), []runtimeevidence.Observer{observer}, runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(observations) != 1 || observations[0].Kind != ObservationEndpointRegistry {
		t.Fatalf("observations = %#v", observations)
	}
	deriver := endpointAssertionDeriver{}
	assertions, diagnostics := runtimeevidence.DeriveAssertions(context.Background(), []runtimeevidence.AssertionDeriver{deriver}, runtimeevidence.AssertionDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(assertions) != 0 {
		t.Fatalf("assertions = %#v, want no endpoint availability for unprobed provider candidate", assertions)
	}
}

func TestEndpointRegistryEvidenceDerivesAvailabilityForReadyProviderCandidates(t *testing.T) {
	endpoints := fpendpoint.NewRegistry(0)
	_, err := endpoints.Put(fpendpoint.RuntimeRecord{
		Spec: fpendpoint.Spec{Name: "kubernetes-loki", URL: "http://loki:3100", Product: "loki"},
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
	observations, diagnostics := runtimeevidence.RunObservers(context.Background(), []runtimeevidence.Observer{observer}, runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	deriver := endpointAssertionDeriver{}
	assertions, diagnostics := runtimeevidence.DeriveAssertions(context.Background(), []runtimeevidence.AssertionDeriver{deriver}, runtimeevidence.AssertionDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(assertions) != 1 || assertions[0].Kind != AssertionEndpointAvailable || assertions[0].Target != "loki" {
		t.Fatalf("assertions = %#v, want endpoint.available loki", assertions)
	}
}

type testProvider struct{}

func (testProvider) Spec() fpendpoint.ProviderSpec {
	return fpendpoint.ProviderSpec{Name: "test", Products: []string{"loki"}}
}

func (testProvider) Discover(ctx context.Context, req fpendpoint.DiscoveryRequest) (fpendpoint.DiscoveryResult, error) {
	return fpendpoint.DiscoveryResult{}, nil
}
