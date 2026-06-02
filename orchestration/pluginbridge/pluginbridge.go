// Package pluginbridge adapts fluxplane-plugin runtimes into Core plugin
// contributions. The dependency direction is intentionally Core -> plugin SDK.
package pluginbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginruntime"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

const (
	AnnotationPluginName     = "fluxplane_plugin.name"
	AnnotationPluginInstance = "fluxplane_plugin.instance"
	AnnotationPluginAccess   = "fluxplane_plugin.access"
	AnnotationPluginCompact  = "fluxplane_plugin.compact"
	AnnotationPluginRender   = "fluxplane_plugin.render"
)

// HostCallerFactory builds an SDK host caller for one Core plugin instance.
type HostCallerFactory func(pluginhost.Context) protocol.HostCaller

// Option configures a bridged plugin.
type Option func(*Plugin)

// WithHostCallerFactory lets product/Core runtime code provide SDK host
// capabilities for plugin protocol calls.
func WithHostCallerFactory(factory HostCallerFactory) Option {
	return func(p *Plugin) {
		p.hostCaller = factory
	}
}

// Plugin is a Core pluginhost plugin backed by one fluxplane-plugin runtime.
type Plugin struct {
	manifest   sdkmanifest.PluginManifest
	runtime    pluginruntime.Plugin
	hostCaller HostCallerFactory
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.AuthMethodContributor = Plugin{}

// New returns a Core pluginhost-compatible adapter for runtime.
func New(runtime pluginruntime.Plugin, manifest sdkmanifest.PluginManifest, opts ...Option) (Plugin, error) {
	if runtime == nil {
		return Plugin{}, fmt.Errorf("pluginbridge: runtime plugin is nil")
	}
	if strings.TrimSpace(manifest.Name) == "" {
		manifest.Name = runtime.Name()
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return Plugin{}, fmt.Errorf("pluginbridge: manifest name is required")
	}
	plugin := Plugin{manifest: manifest, runtime: runtime}
	for _, opt := range opts {
		if opt != nil {
			opt(&plugin)
		}
	}
	return plugin, nil
}

// Load asks runtime for its manifest, then creates a bridge.
func Load(ctx context.Context, runtime pluginruntime.Plugin, opts ...Option) (Plugin, error) {
	if runtime == nil {
		return Plugin{}, fmt.Errorf("pluginbridge: runtime plugin is nil")
	}
	host, err := pluginruntime.NewHost(runtime)
	if err != nil {
		return Plugin{}, err
	}
	manifest, err := host.Manifest(ctx, runtime.Name())
	if err != nil {
		return Plugin{}, err
	}
	return New(runtime, manifest, opts...)
}

// Manifest returns Core pluginhost metadata.
func (p Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{
		Name:        p.manifest.Name,
		Version:     p.manifest.Version,
		Description: p.manifest.Description,
	}
}

// Contributions returns inert Core contribution specs derived from the SDK
// manifest.
func (p Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	ops := make([]operation.Spec, 0, len(p.manifest.Operations))
	refs := make([]operation.Ref, 0, len(p.manifest.Operations))
	for _, declared := range p.manifest.Operations {
		spec := operationSpec(p.manifest.Name, declared)
		if spec.Ref.IsZero() {
			continue
		}
		ops = append(ops, spec)
		refs = append(refs, spec.Ref)
	}
	bundle := resource.ContributionBundle{Operations: ops, Datasources: datasourceSpecs(p.manifest.Name, p.manifest.Datasources)}
	if len(refs) > 0 {
		bundle.OperationSets = append(bundle.OperationSets, operation.Set{
			Name:        p.manifest.Name,
			Description: p.manifest.Description,
			Operations:  refs,
			Annotations: map[string]string{AnnotationPluginName: p.manifest.Name},
		})
	}
	return bundle, nil
}

// Operations returns executable Core operations backed by plugin protocol
// operation calls.
func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	instance := ctx.Ref.InstanceName()
	out := make([]operation.Operation, 0, len(p.manifest.Operations))
	for _, declared := range p.manifest.Operations {
		spec := operationSpec(p.manifest.Name, declared)
		if spec.Ref.IsZero() {
			continue
		}
		op := bridgedOperation{plugin: p.manifest.Name, instance: instance, runtime: p.runtime, hostCaller: p.hostCaller, pluginCtx: ctx, spec: spec}
		out = append(out, operationruntime.NewNamedInstance(p.manifest.Name, instance, op))
	}
	return out, nil
}

// AuthMethods exposes SDK manifest auth methods to Core pluginhost resolution.
func (p Plugin) AuthMethods(context.Context, pluginhost.Context) ([]auth.MethodSpec, error) {
	out := make([]auth.MethodSpec, 0, len(p.manifest.Auth))
	for _, method := range p.manifest.Auth {
		out = append(out, method.MethodSpec())
	}
	return out, nil
}

type bridgedOperation struct {
	plugin     string
	instance   string
	runtime    pluginruntime.Plugin
	hostCaller HostCallerFactory
	pluginCtx  pluginhost.Context
	spec       operation.Spec
}

func (o bridgedOperation) Spec() operation.Spec {
	return o.spec
}

func (o bridgedOperation) Run(ctx operation.Context, input operation.Value) operation.Result {
	raw, err := marshalInput(input)
	if err != nil {
		return operation.Failed("plugin_input_marshal_failed", err.Error(), nil)
	}
	host, err := pluginruntime.NewHost(o.runtime)
	if err != nil {
		return operation.Failed("plugin_runtime_failed", err.Error(), nil)
	}
	call := protocol.OperationCall{Name: string(o.spec.Ref.Name), Input: raw}
	options := []pluginruntime.InvokeOption{pluginruntime.WithInstance(o.instance)}
	if o.hostCaller != nil {
		options = append(options, pluginruntime.WithHostCaller(o.hostCaller(o.pluginCtx)))
	}
	resp, err := host.CallOperation(ctx, o.plugin, call, options...)
	if err != nil {
		return operation.Failed("plugin_operation_failed", err.Error(), nil)
	}
	if !resp.OK {
		return operation.Failed(pluginErrorCode(resp.Error, "plugin_operation_failed"), pluginErrorMessage(resp.Error), nil)
	}
	output, err := decodeValue(resp.Result)
	if err != nil {
		return operation.Failed("plugin_result_decode_failed", err.Error(), nil)
	}
	return operation.OK(output)
}

func operationSpec(plugin string, declared sdkmanifest.OperationSpec) operation.Spec {
	ref := operation.Ref{Name: operation.Name(strings.TrimSpace(declared.Name))}
	annotations := map[string]string{AnnotationPluginName: strings.TrimSpace(plugin)}
	if len(declared.Access) > 0 {
		annotations[AnnotationPluginAccess] = joinAccess(declared.Access)
	}
	if declared.Compact {
		annotations[AnnotationPluginCompact] = "true"
	}
	if declared.Render != nil && declared.Render.Preferred != "" {
		annotations[AnnotationPluginRender] = declared.Render.Preferred
	}
	if len(declared.AuthScopes) > 0 {
		annotations[operationruntime.AnnotationRequiredAuthScope] = strings.Join(declared.AuthScopes, ",")
	}
	return operation.Spec{
		Ref:         ref,
		Description: strings.TrimSpace(declared.Description),
		Input:       schemaType(string(ref.Name)+"_input", declared.Input),
		Output:      schemaType(string(ref.Name)+"_output", declared.Output),
		Semantics: operation.Semantics{
			Effects:     mapEffects(declared),
			Risk:        mapRisk(declared.Risk),
			Idempotency: mapIdempotency(declared.Idempotency),
		},
		Annotations: annotations,
	}
}

func schemaType(name string, raw json.RawMessage) operation.Type {
	if len(raw) == 0 {
		return operation.Type{}
	}
	return operation.Type{Name: name, Schema: operation.Schema{Format: "json-schema", Data: append(json.RawMessage(nil), raw...)}}
}

func mapEffects(declared sdkmanifest.OperationSpec) operation.EffectSet {
	if len(declared.Effects) == 0 {
		if declared.ReadOnly {
			return operation.EffectSet{operation.EffectReadExternal}
		}
		return nil
	}
	out := make(operation.EffectSet, 0, len(declared.Effects))
	for _, effect := range declared.Effects {
		switch effect {
		case sdkmanifest.OperationEffectRead:
			out = append(out, operation.EffectReadExternal)
		case sdkmanifest.OperationEffectWrite:
			out = append(out, operation.EffectWriteExternal)
		case sdkmanifest.OperationEffectNetwork:
			out = append(out, operation.EffectNetwork)
		case sdkmanifest.OperationEffectProcess, sdkmanifest.OperationEffectLocalSystem, sdkmanifest.OperationEffectBrowser:
			out = append(out, operation.EffectProcess)
		case sdkmanifest.OperationEffectFilesystem:
			out = append(out, operation.EffectFilesystem)
		default:
			out = append(out, operation.Effect(effect))
		}
	}
	return out
}

func mapRisk(risk sdkmanifest.OperationRisk) operation.RiskLevel {
	switch risk {
	case sdkmanifest.OperationRiskLow:
		return operation.RiskLow
	case sdkmanifest.OperationRiskMedium:
		return operation.RiskMedium
	case sdkmanifest.OperationRiskHigh:
		return operation.RiskHigh
	case sdkmanifest.OperationRiskDestructive:
		return operation.RiskCritical
	default:
		return operation.RiskUnknown
	}
}

func mapIdempotency(idempotency sdkmanifest.OperationIdempotency) operation.Idempotency {
	switch idempotency {
	case sdkmanifest.OperationIdempotent:
		return operation.IdempotencyIdempotent
	case sdkmanifest.OperationNonIdempotent:
		return operation.IdempotencyNonIdempotent
	default:
		return operation.IdempotencyUnknown
	}
}

func marshalInput(input operation.Value) (json.RawMessage, error) {
	if input == nil {
		return nil, nil
	}
	if raw, ok := input.(json.RawMessage); ok {
		return append(json.RawMessage(nil), raw...), nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func decodeValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func pluginErrorCode(err *protocol.Error, fallback string) string {
	if err != nil && strings.TrimSpace(err.Code) != "" {
		return err.Code
	}
	return fallback
}

func pluginErrorMessage(err *protocol.Error) string {
	if err != nil && strings.TrimSpace(err.Message) != "" {
		return err.Message
	}
	return "plugin operation failed"
}

func joinAccess(access []sdkmanifest.OperationAccess) string {
	values := make([]string, 0, len(access))
	for _, item := range access {
		if strings.TrimSpace(string(item)) != "" {
			values = append(values, string(item))
		}
	}
	return strings.Join(values, ",")
}

func datasourceSpecs(plugin string, declarations []sdkmanifest.DatasourceSpec) []coredatasource.Spec {
	out := make([]coredatasource.Spec, 0, len(declarations))
	for _, declaration := range declarations {
		name := strings.TrimSpace(declaration.Name)
		entity := strings.TrimSpace(declaration.Entity)
		if name == "" || entity == "" {
			continue
		}
		out = append(out, coredatasource.Spec{
			Name:        coredatasource.Name(name),
			Description: strings.TrimSpace(declaration.Description),
			Entities:    []coredatasource.EntityType{coredatasource.EntityType(entity)},
			Kind:        strings.TrimSpace(plugin),
			Annotations: map[string]string{AnnotationPluginName: strings.TrimSpace(plugin)},
		})
	}
	return out
}
