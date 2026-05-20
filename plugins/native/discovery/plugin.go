package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreendpoint "github.com/fluxplane/agentruntime/core/endpoint"
	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimediscovery "github.com/fluxplane/agentruntime/runtime/discovery"
	runtimeendpoint "github.com/fluxplane/agentruntime/runtime/endpoint"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const (
	Name = "discovery"

	StatusOp       = "discovery_status"
	DiscoverOp     = "discovery_discover"
	ProvidersOp    = "discovery_providers"
	EndpointListOp = "endpoint_list"
	EndpointGetOp  = "endpoint_get"

	ObservationEndpointRegistry = "endpoint.registry"
	SignalEndpointAvailable     = "endpoint.available"
)

type Plugin struct {
	discovery  *runtimediscovery.Registry
	discoverer *runtimediscovery.Runner
	endpoints  *runtimeendpoint.Registry
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.SignalDeriverContributor = Plugin{}

func New() Plugin { return Plugin{} }

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Endpoint discovery introspection."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	p.discovery = ctx.Discovery
	p.discoverer = ctx.Discoverer
	p.endpoints = ctx.Endpoints
	return p, nil
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := operationSpecs()
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Discovery status and endpoint introspection.", Operations: operationRefs(specs)}},
		Operations:    specs,
		Observers: []coreenvironment.ObserverSpec{{
			Name:            "endpoint.registry",
			Description:     "Observes non-secret endpoint registry summaries for activation.",
			Environment:     coreenvironment.Ref{Name: "endpoint"},
			Phase:           coreenvironment.PhaseTurn,
			ObservableKinds: []string{ObservationEndpointRegistry},
			Dynamic:         true,
		}},
		SignalDerivers: []coreenvironment.SignalDeriverSpec{{
			Name:             "endpoint.signals",
			Description:      "Derives endpoint availability signals from endpoint registry observations.",
			ObservationKinds: []string{ObservationEndpointRegistry},
			Signals: []coreenvironment.SignalTemplate{
				{Kind: SignalEndpointAvailable},
			},
		}},
	}, nil
}

func (p Plugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeenvironment.Observer, error) {
	return []runtimeenvironment.Observer{endpointRegistryObserver{endpoints: p.endpoints}}, nil
}

func (Plugin) SignalDerivers(context.Context, pluginhost.Context) ([]runtimeenvironment.SignalDeriver, error) {
	return []runtimeenvironment.SignalDeriver{endpointSignalDeriver{}}, nil
}

func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	if p.discovery == nil {
		p.discovery = ctx.Discovery
	}
	if p.discoverer == nil {
		p.discoverer = ctx.Discoverer
	}
	if p.endpoints == nil {
		p.endpoints = ctx.Endpoints
	}
	if p.discovery == nil {
		return nil, fmt.Errorf("discoveryplugin: discovery registry is nil")
	}
	if p.endpoints == nil {
		return nil, fmt.Errorf("discoveryplugin: endpoint registry is nil")
	}
	return []operation.Operation{
		operationruntime.NewTyped[StatusInput, StatusOutput](statusSpec(), p.status),
		operationruntime.NewTyped[DiscoverInput, DiscoverOutput](discoverSpec(), p.discover),
		operationruntime.NewTyped[ProvidersInput, ProvidersOutput](providersSpec(), p.providers),
		operationruntime.NewTyped[EndpointListInput, EndpointListOutput](endpointListSpec(), p.endpointList),
		operationruntime.NewTyped[EndpointGetInput, EndpointGetOutput](endpointGetSpec(), p.endpointGet),
	}, nil
}

func operationSpecs() []operation.Spec {
	return []operation.Spec{statusSpec(), discoverSpec(), providersSpec(), endpointListSpec(), endpointGetSpec()}
}

func operationRefs(specs []operation.Spec) []operation.Ref {
	refs := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, spec.Ref)
	}
	return refs
}

type StatusInput struct{}

type StatusOutput struct {
	Providers []runtimediscovery.ProviderStatus `json:"providers,omitempty"`
	Runner    runtimediscovery.RunnerStatus     `json:"runner,omitempty"`
	Endpoints int                               `json:"endpoints"`
}

type DiscoverInput struct {
	Providers  []string `json:"providers,omitempty" jsonschema:"description=Provider names to refresh. Empty refreshes all providers."`
	Products   []string `json:"products,omitempty" jsonschema:"description=Product filters such as loki, prometheus, grafana, postgres, mysql, redis. Empty refreshes all products."`
	Namespaces []string `json:"namespaces,omitempty" jsonschema:"description=Namespace hints for providers that support namespace-scoped discovery."`
	Force      bool     `json:"force,omitempty" jsonschema:"description=Start a new refresh even if another run is already active."`
}

type DiscoverOutput struct {
	Run runtimediscovery.RunSummary `json:"run"`
}

type ProvidersInput struct{}

type ProvidersOutput struct {
	Providers []runtimediscovery.ProviderSpec `json:"providers,omitempty"`
}

type EndpointListInput struct {
	Product string `json:"product,omitempty"`
}

type EndpointSummary struct {
	Ref       coreendpoint.Ref       `json:"ref"`
	URL       string                 `json:"url,omitempty"`
	Product   string                 `json:"product,omitempty"`
	Source    coreendpoint.SourceRef `json:"source,omitempty"`
	Metadata  map[string]string      `json:"metadata,omitempty"`
	ExpiresAt string                 `json:"expires_at,omitempty"`
}

type EndpointListOutput struct {
	Endpoints []EndpointSummary `json:"endpoints,omitempty"`
}

type EndpointGetInput struct {
	Ref coreendpoint.Ref `json:"ref" jsonschema:"required,description=Endpoint ref such as @endpoint/loki-abc."`
}

type EndpointGetOutput struct {
	Endpoint EndpointSummary `json:"endpoint"`
}

type EndpointRegistryEvidence struct {
	Endpoints []EndpointSummary `json:"endpoints,omitempty"`
}

type endpointRegistryObserver struct {
	endpoints *runtimeendpoint.Registry
}

func (o endpointRegistryObserver) Spec() coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{
		Name:            "endpoint.registry",
		Description:     "Observes non-secret endpoint registry summaries for activation.",
		Environment:     coreenvironment.Ref{Name: "endpoint"},
		Phase:           coreenvironment.PhaseTurn,
		ObservableKinds: []string{ObservationEndpointRegistry},
		Dynamic:         true,
	}
}

func (o endpointRegistryObserver) Observe(_ context.Context, _ runtimeenvironment.ObservationRequest) ([]coreenvironment.Observation, error) {
	if o.endpoints == nil {
		return nil, nil
	}
	records := o.endpoints.List("")
	if len(records) == 0 {
		return nil, nil
	}
	evidence := EndpointRegistryEvidence{Endpoints: make([]EndpointSummary, 0, len(records))}
	for _, record := range records {
		summary := endpointSummary(record)
		if summary.Product == "" {
			continue
		}
		evidence.Endpoints = append(evidence.Endpoints, summary)
	}
	if len(evidence.Endpoints) == 0 {
		return nil, nil
	}
	return []coreenvironment.Observation{{
		ID:          "endpoint:registry",
		Environment: coreenvironment.Ref{Name: "endpoint"},
		Kind:        ObservationEndpointRegistry,
		Scope:       "runtime",
		Content:     evidence,
		At:          time.Now().UTC(),
	}}, nil
}

type endpointSignalDeriver struct{}

func (endpointSignalDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return coreenvironment.SignalDeriverSpec{
		Name:             "endpoint.signals",
		Description:      "Derives endpoint availability signals from endpoint registry observations.",
		ObservationKinds: []string{ObservationEndpointRegistry},
	}
}

func (endpointSignalDeriver) Derive(_ context.Context, req runtimeenvironment.SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	var out []coreenvironment.Signal
	for _, observation := range req.Observations {
		if observation.Kind != ObservationEndpointRegistry {
			continue
		}
		evidence, ok := endpointEvidenceFromObservation(observation.Content)
		if !ok {
			continue
		}
		for _, endpoint := range evidence.Endpoints {
			if !endpointAvailableForActivation(endpoint) {
				continue
			}
			out = append(out, coreenvironment.Signal{
				Kind:           SignalEndpointAvailable,
				Target:         endpoint.Product,
				Subject:        coreenvironment.Subject{Kind: coreenvironment.SubjectEndpoint, Name: endpoint.Product, ID: string(endpoint.Ref)},
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: observationIDs(observation.ID),
				Metadata: map[string]string{
					"endpoint_ref": string(endpoint.Ref),
					"source":       endpoint.Source.Kind,
				},
			})
		}
	}
	return out, nil
}

func endpointAvailableForActivation(endpoint EndpointSummary) bool {
	if strings.TrimSpace(endpoint.Product) == "" {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		endpoint.Metadata["readiness"],
		endpoint.Metadata["probe_status"],
		endpoint.Metadata["availability"],
		endpoint.Metadata["status"],
	)))
	switch status {
	case "configured", "probed", "ready", "reachable", "available", "ok":
		return true
	case "unprobed", "candidate", "failed", "unavailable":
		return false
	}
	// Explicit endpoint records do not have a discovery provider owner. Those are
	// intentional configuration and count as availability. Provider-owned records
	// need readiness metadata before they can activate product tools.
	return strings.TrimSpace(endpoint.Metadata["provider"]) == ""
}

func endpointEvidenceFromObservation(content any) (EndpointRegistryEvidence, bool) {
	switch typed := content.(type) {
	case EndpointRegistryEvidence:
		return typed, true
	case *EndpointRegistryEvidence:
		if typed == nil {
			return EndpointRegistryEvidence{}, false
		}
		return *typed, true
	default:
		return EndpointRegistryEvidence{}, false
	}
}

func observationIDs(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}

func (p Plugin) status(_ operation.Context, _ StatusInput) (StatusOutput, error) {
	var runner runtimediscovery.RunnerStatus
	if p.discoverer != nil {
		runner = p.discoverer.Status()
	}
	return StatusOutput{Providers: p.discovery.Status(), Runner: runner, Endpoints: len(p.endpoints.List(""))}, nil
}

func (p Plugin) discover(ctx operation.Context, in DiscoverInput) (DiscoverOutput, error) {
	if p.discoverer == nil {
		return DiscoverOutput{}, fmt.Errorf("discoveryplugin: discovery runner is nil")
	}
	run := p.discoverer.Trigger(ctx, runtimediscovery.RunRequest{
		Providers:  in.Providers,
		Products:   in.Products,
		Namespaces: in.Namespaces,
		Force:      in.Force,
		Reason:     "manual",
	})
	return DiscoverOutput{Run: run}, nil
}

func (p Plugin) providers(_ operation.Context, _ ProvidersInput) (ProvidersOutput, error) {
	return ProvidersOutput{Providers: p.discovery.Providers()}, nil
}

func (p Plugin) endpointList(_ operation.Context, in EndpointListInput) (EndpointListOutput, error) {
	records := p.endpoints.List(in.Product)
	out := make([]EndpointSummary, 0, len(records))
	for _, record := range records {
		out = append(out, endpointSummary(record))
	}
	return EndpointListOutput{Endpoints: out}, nil
}

func (p Plugin) endpointGet(_ operation.Context, in EndpointGetInput) (EndpointGetOutput, error) {
	resolved, ok := p.endpoints.Resolve(in.Ref)
	if !ok {
		return EndpointGetOutput{}, fmt.Errorf("endpoint %q not found", in.Ref)
	}
	summary := EndpointSummary{
		Ref:       resolved.Ref,
		URL:       resolved.URL,
		Source:    resolved.Source,
		Metadata:  cloneMap(resolved.Metadata),
		ExpiresAt: resolved.ExpiresAt,
	}
	summary.Product = summary.Metadata["product"]
	return EndpointGetOutput{Endpoint: summary}, nil
}

func endpointSummary(record runtimeendpoint.Record) EndpointSummary {
	resolved := record.Resolved
	if resolved.URL == "" {
		resolved.URL = record.Spec.URL
	}
	metadata := cloneMap(record.Metadata)
	if len(metadata) == 0 {
		metadata = cloneMap(resolved.Metadata)
	}
	return EndpointSummary{
		Ref:       resolved.Ref,
		URL:       resolved.URL,
		Product:   firstNonEmpty(record.Spec.Product, metadata["product"]),
		Source:    resolved.Source,
		Metadata:  metadata,
		ExpiresAt: resolved.ExpiresAt,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
