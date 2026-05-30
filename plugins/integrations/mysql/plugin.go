// Package mysqlplugin contributes MySQL endpoint query operations.
package mysql

import (
	"context"
	"strings"
	"time"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-policy"
)

const (
	Name    = "mysql"
	QueryOp = "mysql_query"
)

type Config struct {
	EndpointRef string `json:"endpoint_ref,omitempty" yaml:"endpoint_ref,omitempty" jsonschema:"description=Runtime endpoint reference for the MySQL server."`
	Database    string `json:"database,omitempty" yaml:"database,omitempty" jsonschema:"description=Default MySQL database/schema to query."`
	Timeout     string `json:"timeout,omitempty" yaml:"timeout,omitempty" jsonschema:"description=Query timeout using Go duration syntax such as 30s."`
	MaxRows     int    `json:"max_rows,omitempty" yaml:"max_rows,omitempty" jsonschema:"description=Maximum rows returned by one query."`
}

type Plugin struct {
	pluginhost.Configurable[Config]
	ref       resource.PluginRef
	cfg       Config
	endpoints *fpendpoint.Registry
	secrets   runtimesecret.Resolver
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

func New() Plugin {
	return Plugin{endpoints: fpendpoint.NewRegistry(15 * time.Minute), secrets: runtimesecret.NewRegistry()}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "MySQL query integration for discovered endpoints."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[Config](ctx)
	if err != nil {
		return nil, err
	}
	p.ref = ctx.Ref
	p.cfg = normalizeConfig(cfg)
	if ctx.Endpoints != nil {
		p.endpoints = ctx.Endpoints
	}
	if ctx.Secrets != nil {
		p.secrets = ctx.Secrets
	}
	if p.endpoints == nil {
		p.endpoints = fpendpoint.NewRegistry(15 * time.Minute)
	}
	if p.secrets == nil {
		p.secrets = runtimesecret.NewRegistry()
	}
	return p, nil
}

func (p Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := operationSpecs()
	return resource.ContributionBundle{
		DataSources:   []coredata.SourceSpec{DataSourceSpec()},
		OperationSets: []operation.Set{{Name: Name, Description: "MySQL database operations.", Operations: operationRefs(specs)}},
		Operations:    specs,
	}, nil
}

func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operationruntime.NewTypedResult[QueryInput, QueryOutput](querySpec(), p.query(), operationruntime.WithAccess(func(ctx operation.Context, in QueryInput) ([]operationruntime.AccessDescriptor, error) {
			return p.queryAccess(ctx, in)
		})),
	}, nil
}

func DataSourceSpec() coredata.SourceSpec {
	return coredata.SourceSpec{
		Name:        coredata.SourceName(Name),
		Kind:        Name,
		Description: "Discovered MySQL database endpoints.",
		Entities: []coredata.EntitySpec{
			{Type: coredata.EntityType("mysql.endpoint"), Description: "Discovered MySQL endpoint."},
		},
	}
}

func operationSpecs() []operation.Spec {
	return []operation.Spec{querySpec()}
}

func operationRefs(specs []operation.Spec) []operation.Ref {
	refs := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, spec.Ref)
	}
	return refs
}

func querySpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: QueryOp},
		Description: "Run one bounded read-only query against a discovered MySQL endpoint.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskMedium,
		},
	}
}

func normalizeConfig(cfg Config) Config {
	cfg.EndpointRef = strings.TrimSpace(cfg.EndpointRef)
	cfg.Database = strings.TrimSpace(cfg.Database)
	cfg.Timeout = strings.TrimSpace(cfg.Timeout)
	if cfg.Timeout == "" {
		cfg.Timeout = "10s"
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = 100
	}
	return cfg
}

func secretDescriptor(ref string) operationruntime.AccessDescriptor {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "*"
	}
	return operationruntime.AccessDescriptor{
		Resource: policy.ResourceRef{Kind: policy.ResourceSecret, Name: ref},
		Action:   policy.ActionSecretUse,
	}
}

func (p Plugin) queryAccess(_ operation.Context, in QueryInput) ([]operationruntime.AccessDescriptor, error) {
	endpoint, ok := p.resolveEndpoint(in.EndpointRef)
	target := "*"
	authRef := ""
	if ok {
		target = endpoint.URL
		if target == "" {
			target = endpoint.Metadata["host"]
		}
		authRef = endpoint.AuthRef
	}
	out := []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(target, policy.ActionNetworkConnect)}
	if authRef != "" {
		out = append(out, secretDescriptor(authRef))
	}
	return out, nil
}

func (p Plugin) resolveEndpoint(input string) (resolvedEndpoint, bool) {
	ref := strings.TrimSpace(firstNonEmpty(input, p.cfg.EndpointRef))
	if ref == "" || p.endpoints == nil {
		return resolvedEndpoint{}, false
	}
	resolved, ok := p.endpoints.Resolve(endpointRef(ref))
	if !ok {
		return resolvedEndpoint{}, false
	}
	return resolvedEndpoint{
		Ref:      string(resolved.Ref),
		URL:      resolved.URL,
		AuthRef:  resolved.AuthRef,
		Source:   resolved.Source,
		Metadata: resolved.Metadata,
	}, true
}
