// Package lokiplugin contributes Loki log query operations and datasource
// access.
package loki

import (
	"context"
	"fmt"
	"strings"
	"time"

	coredata "github.com/fluxplane/engine/core/data"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	runtimediscovery "github.com/fluxplane/engine/runtime/discovery"
	runtimeendpoint "github.com/fluxplane/engine/runtime/endpoint"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
	"github.com/fluxplane/engine/runtime/system"
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
	URL              string             `json:"url,omitempty" yaml:"url,omitempty"`
	URLEnv           string             `json:"url_env,omitempty" yaml:"url_env,omitempty"`
	EndpointRef      string             `json:"endpoint_ref,omitempty" yaml:"endpoint_ref,omitempty"`
	TenantID         string             `json:"tenant_id,omitempty" yaml:"tenant_id,omitempty"`
	TenantIDEnv      string             `json:"tenant_id_env,omitempty" yaml:"tenant_id_env,omitempty"`
	DefaultNamespace string             `json:"default_namespace,omitempty" yaml:"default_namespace,omitempty"`
	DefaultSince     string             `json:"default_since,omitempty" yaml:"default_since,omitempty"`
	DefaultLimit     int                `json:"default_limit,omitempty" yaml:"default_limit,omitempty"`
	MaxLimit         int                `json:"max_limit,omitempty" yaml:"max_limit,omitempty"`
	Labels           []string           `json:"labels,omitempty" yaml:"labels,omitempty"`
	AutoDiscover     AutoDiscoverConfig `json:"auto_discover,omitempty" yaml:"auto_discover,omitempty"`
}

// AutoDiscoverConfig configures Loki endpoint discovery.
type AutoDiscoverConfig struct {
	Enabled       bool     `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Kubernetes    bool     `json:"kubernetes,omitempty" yaml:"kubernetes,omitempty"`
	Namespaces    []string `json:"namespaces,omitempty" yaml:"namespaces,omitempty"`
	PreferService bool     `json:"prefer_service,omitempty" yaml:"prefer_service,omitempty"`
	AllowPodIP    bool     `json:"allow_pod_ip,omitempty" yaml:"allow_pod_ip,omitempty"`
	ProbeTimeout  string   `json:"probe_timeout,omitempty" yaml:"probe_timeout,omitempty"`
}

// Plugin contributes Loki resources.
type Plugin struct {
	pluginhost.Configurable[Config]
	system    system.System
	ref       resource.PluginRef
	cfg       Config
	discovery *runtimediscovery.Registry
	endpoints *runtimeendpoint.Registry
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}

// New returns a Loki plugin.
func New(sys system.System) Plugin {
	return Plugin{system: sys, discovery: runtimediscovery.NewRegistry(), endpoints: runtimeendpoint.NewRegistry(15 * time.Minute)}
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
		p.discovery = runtimediscovery.NewRegistry()
	}
	if p.endpoints == nil {
		p.endpoints = runtimeendpoint.NewRegistry(15 * time.Minute)
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
	if p.system == nil {
		return nil, fmt.Errorf("lokiplugin: system is nil")
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
