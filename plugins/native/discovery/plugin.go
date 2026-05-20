package discovery

import (
	"context"
	"fmt"

	coreendpoint "github.com/fluxplane/agentruntime/core/endpoint"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimediscovery "github.com/fluxplane/agentruntime/runtime/discovery"
	runtimeendpoint "github.com/fluxplane/agentruntime/runtime/endpoint"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const (
	Name = "discovery"

	StatusOp       = "discovery_status"
	DiscoverOp     = "discovery_discover"
	ProvidersOp    = "discovery_providers"
	EndpointListOp = "endpoint_list"
	EndpointGetOp  = "endpoint_get"
)

type Plugin struct {
	discovery  *runtimediscovery.Registry
	discoverer *runtimediscovery.Runner
	endpoints  *runtimeendpoint.Registry
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

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
	}, nil
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
