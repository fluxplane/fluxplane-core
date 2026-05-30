// Package lokiplugin contributes Loki log query operations and datasource
// access.
package loki

import (
	"context"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"strings"
	"time"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
)

const (
	Name = "loki"

	TestOp       = "loki_test"
	LabelsOp     = "loki_labels"
	QueryOp      = "loki_query"
	RecentLogsOp = "loki_recent_logs"

	LogEntryEntity         coredatasource.EntityType = "loki.log_entry"
	StreamEntity           coredatasource.EntityType = "loki.stream"
	LabelEntity            coredatasource.EntityType = "loki.label"
	DetectedEndpointEntity coredatasource.EntityType = "loki.detected_endpoint"
)

// Config is the per-instance Loki plugin configuration.
type Config struct {
	URL              string             `json:"url,omitempty" yaml:"url,omitempty" jsonschema:"description=Loki base URL."`
	URLEnv           string             `json:"url_env,omitempty" yaml:"url_env,omitempty" jsonschema:"description=Environment variable containing the Loki base URL."`
	EndpointRef      string             `json:"endpoint_ref,omitempty" yaml:"endpoint_ref,omitempty" jsonschema:"description=Runtime endpoint reference for a discovered Loki service."`
	TenantID         string             `json:"tenant_id,omitempty" yaml:"tenant_id,omitempty" jsonschema:"description=Loki tenant id for multi-tenant deployments."`
	TenantIDEnv      string             `json:"tenant_id_env,omitempty" yaml:"tenant_id_env,omitempty" jsonschema:"description=Environment variable containing the Loki tenant id."`
	DefaultNamespace string             `json:"default_namespace,omitempty" yaml:"default_namespace,omitempty" jsonschema:"description=Default Kubernetes namespace filter for Loki queries."`
	DefaultSince     string             `json:"default_since,omitempty" yaml:"default_since,omitempty" jsonschema:"description=Default lookback duration for recent log queries, such as 15m."`
	DefaultLimit     int                `json:"default_limit,omitempty" yaml:"default_limit,omitempty" jsonschema:"description=Default maximum log entries returned."`
	MaxLimit         int                `json:"max_limit,omitempty" yaml:"max_limit,omitempty" jsonschema:"description=Hard maximum log entries accepted from manifest or operation input."`
	Labels           []string           `json:"labels,omitempty" yaml:"labels,omitempty" jsonschema:"description=Loki labels exposed for filtering and datasource records."`
	AutoDiscover     AutoDiscoverConfig `json:"auto_discover,omitempty" yaml:"auto_discover,omitempty" jsonschema:"description=Options for discovering Loki endpoints from Kubernetes."`
}

// AutoDiscoverConfig configures Loki endpoint discovery.
type AutoDiscoverConfig struct {
	Enabled       bool     `json:"enabled,omitempty" yaml:"enabled,omitempty" jsonschema:"description=Enable automatic Loki endpoint discovery."`
	Kubernetes    bool     `json:"kubernetes,omitempty" yaml:"kubernetes,omitempty" jsonschema:"description=Discover Loki endpoints from Kubernetes services and pods."`
	Namespaces    []string `json:"namespaces,omitempty" yaml:"namespaces,omitempty" jsonschema:"description=Kubernetes namespaces searched during Loki discovery."`
	PreferService bool     `json:"prefer_service,omitempty" yaml:"prefer_service,omitempty" jsonschema:"description=Prefer Kubernetes service endpoints over pod IPs when both are available."`
	AllowPodIP    bool     `json:"allow_pod_ip,omitempty" yaml:"allow_pod_ip,omitempty" jsonschema:"description=Allow direct pod IP endpoints during discovery."`
	PortForward   bool     `json:"port_forward,omitempty" yaml:"port_forward,omitempty" jsonschema:"description=Use Kubernetes port-forwarding for discovered Loki endpoints."`
	ProbeTimeout  string   `json:"probe_timeout,omitempty" yaml:"probe_timeout,omitempty" jsonschema:"description=Timeout for probing discovered Loki endpoints using Go duration syntax such as 5s."`
}

// Plugin contributes Loki resources.
type Plugin struct {
	pluginhost.Configurable[Config]
	process     fpsystem.ProcessManager
	network     fpsystem.Network
	environment fpsystem.Environment
	ref         resource.PluginRef
	cfg         Config
	discovery   *fpendpoint.DiscoveryRegistry
	endpoints   *fpendpoint.Registry
}

// Boundaries are the host capabilities used by the Loki plugin.
type Boundaries struct {
	Process     fpsystem.ProcessManager
	Network     fpsystem.Network
	Environment fpsystem.Environment
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}

// New returns a Loki plugin.
func New(sys fpsystem.System) Plugin {
	return NewWithBoundaries(boundariesFromSystem(sys))
}

func NewWithBoundaries(boundaries Boundaries) Plugin {
	return Plugin{process: boundaries.Process, network: boundaries.Network, environment: boundaries.Environment, discovery: fpendpoint.NewDiscoveryRegistry(), endpoints: fpendpoint.NewRegistry(15 * time.Minute)}
}

func boundariesFromSystem(sys fpsystem.System) Boundaries {
	if sys == nil {
		return Boundaries{}
	}
	return Boundaries{Process: sys.Process(), Network: sys.Network(), Environment: sys.Environment()}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Loki log query integration."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[Config](ctx)
	if err != nil {
		return nil, err
	}
	p.ref = ctx.Ref
	p.cfg = normalizeConfig(cfg)
	if ctx.Discovery != nil {
		p.discovery = ctx.Discovery
	}
	if ctx.Endpoints != nil {
		p.endpoints = ctx.Endpoints
	}
	if p.discovery == nil {
		p.discovery = fpendpoint.NewDiscoveryRegistry()
	}
	if p.endpoints == nil {
		p.endpoints = fpendpoint.NewRegistry(15 * time.Minute)
	}
	return p, nil
}

func (p Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := operationSpecs()
	return resource.ContributionBundle{
		DataSources:   []coredata.SourceSpec{DataSourceSpec()},
		OperationSets: []operation.Set{{Name: Name, Description: "Loki log operations.", Operations: operationRefs(specs)}},
		Operations:    specs,
	}, nil
}

func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.network == nil {
		return nil, fmt.Errorf("lokiplugin: network is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[TestInput, TestOutput](testSpec(), p.test(), operationruntime.WithAccess(func(ctx operation.Context, in TestInput) ([]operationruntime.AccessDescriptor, error) {
			return p.lokiNetworkAccess(ctx, in)
		})),
		operationruntime.NewTypedResult[LabelsInput, LabelsOutput](labelsSpec(), p.labels(), operationruntime.WithAccess(func(ctx operation.Context, in LabelsInput) ([]operationruntime.AccessDescriptor, error) {
			return p.lokiNetworkAccess(ctx, in)
		})),
		operationruntime.NewTypedResult[QueryInput, QueryOutput](querySpec(), p.query(), operationruntime.WithAccess(func(ctx operation.Context, in QueryInput) ([]operationruntime.AccessDescriptor, error) {
			return p.lokiNetworkAccess(ctx, in)
		})),
		operationruntime.NewTypedResult[RecentLogsInput, QueryOutput](recentLogsSpec(), p.recentLogs(), operationruntime.WithAccess(func(ctx operation.Context, in RecentLogsInput) ([]operationruntime.AccessDescriptor, error) {
			return p.lokiNetworkAccess(ctx, in)
		})),
	}, nil
}

func operationSpecs() []operation.Spec {
	return []operation.Spec{testSpec(), labelsSpec(), querySpec(), recentLogsSpec()}
}

func operationRefs(specs []operation.Spec) []operation.Ref {
	refs := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, spec.Ref)
	}
	return refs
}

func normalizeConfig(cfg Config) Config {
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.URLEnv = strings.TrimSpace(cfg.URLEnv)
	cfg.EndpointRef = strings.TrimSpace(cfg.EndpointRef)
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	cfg.TenantIDEnv = strings.TrimSpace(cfg.TenantIDEnv)
	cfg.DefaultNamespace = strings.TrimSpace(cfg.DefaultNamespace)
	cfg.DefaultSince = strings.TrimSpace(cfg.DefaultSince)
	if cfg.DefaultSince == "" {
		cfg.DefaultSince = "1h"
	}
	if cfg.DefaultLimit <= 0 {
		cfg.DefaultLimit = 100
	}
	if cfg.MaxLimit <= 0 {
		cfg.MaxLimit = 1000
	}
	cfg.AutoDiscover.Namespaces = normalizeStrings(cfg.AutoDiscover.Namespaces)
	if cfg.AutoDiscover.Enabled && !cfg.AutoDiscover.Kubernetes {
		cfg.AutoDiscover.Kubernetes = true
	}
	if cfg.URL == "" && cfg.EndpointRef == "" {
		cfg.AutoDiscover.Enabled = true
		cfg.AutoDiscover.Kubernetes = true
	}
	if cfg.AutoDiscover.Enabled && cfg.AutoDiscover.Kubernetes {
		cfg.AutoDiscover.PortForward = true
	}
	return cfg
}

func normalizeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}
