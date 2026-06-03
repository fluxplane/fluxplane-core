// Package pluginbridge adapts fluxplane-plugin runtimes into Core plugin
// contributions. The dependency direction is intentionally Core -> plugin SDK.
package pluginbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-operation"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/pluginruntime"
	"github.com/fluxplane/fluxplane-plugin/protocol"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
)

const (
	AnnotationPluginName     = "fluxplane_plugin.name"
	AnnotationPluginInstance = "fluxplane_plugin.instance"
	AnnotationPluginAccess   = "fluxplane_plugin.access"
	AnnotationPluginCompact  = "fluxplane_plugin.compact"
	AnnotationPluginRender   = "fluxplane_plugin.render"
)

// HostCallerFactory builds an SDK host caller for one Core plugin instance.
type HostCallerFactory func(contributions.Context) protocol.HostCaller

// Option configures a bridged plugin.
type Option func(*Plugin)

// WithHostCallerFactory lets product/Core runtime code provide SDK host
// capabilities for plugin protocol calls.
func WithHostCallerFactory(factory HostCallerFactory) Option {
	return func(p *Plugin) {
		p.hostCaller = factory
	}
}

// Plugin is a Core contributions plugin backed by one fluxplane-plugin runtime.
type Plugin struct {
	manifest   sdkmanifest.PluginManifest
	runtime    pluginruntime.Plugin
	hostCaller HostCallerFactory
}

var _ contributions.Provider = Plugin{}
var _ contributions.OperationProvider = Plugin{}
var _ contributions.AuthMethodProvider = Plugin{}
var _ contributions.AuthTestProvider = Plugin{}
var _ contributions.DatasourceProviderProvider = Plugin{}
var _ contributions.ContextProviderProvider = Plugin{}

// New returns a Core contributions-compatible adapter for runtime.
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

// Manifest returns Core contributions metadata.
func (p Plugin) Manifest() contributions.Manifest {
	return contributions.Manifest{
		Name:        p.manifest.Name,
		Version:     p.manifest.Version,
		Description: p.manifest.Description,
	}
}

// Contributions returns inert Core contribution specs derived from the SDK
// manifest.
func (p Plugin) Contributions(context.Context, contributions.Context) (resource.ContributionBundle, error) {
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
	bundle := resource.ContributionBundle{Operations: ops, Datasources: datasourceSpecs(p.manifest.Name, p.manifest.Datasources), ContextProviders: contextSpecs(p.manifest.Context)}
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
func (p Plugin) Operations(_ context.Context, ctx contributions.Context) ([]operation.Operation, error) {
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

// AuthMethods exposes SDK manifest auth methods to Core contributions resolution.
func (p Plugin) AuthMethods(context.Context, contributions.Context) ([]auth.MethodSpec, error) {
	out := make([]auth.MethodSpec, 0, len(p.manifest.Auth))
	for _, method := range p.manifest.Auth {
		out = append(out, method.MethodSpec())
	}
	return out, nil
}

// TestConnection asks the SDK plugin runtime to test auth for one Core plugin
// instance.
func (p Plugin) TestConnection(ctx context.Context, pluginCtx contributions.Context, req contributions.AuthTestRequest, reports chan<- contributions.AuthTestReport) error {
	ref := req.Ref
	if ref.Name == "" {
		ref = pluginCtx.Ref
	}
	if ref.Name == "" {
		ref.Name = p.manifest.Name
	}
	method := firstNonEmpty(req.Method, firstAuthMethod(p.manifest.Auth))
	material, err := p.authMaterial(ctx, ref.InstanceName(), method, req.Secrets)
	if err != nil {
		sendAuthTestReport(reports, p.manifest.Name, ref.InstanceName(), method, "auth.test", "failed", err.Error(), nil)
		return nil
	}
	host, err := pluginruntime.NewHost(p.runtime)
	if err != nil {
		sendAuthTestReport(reports, p.manifest.Name, ref.InstanceName(), method, "auth.test", "failed", err.Error(), nil)
		return nil
	}
	options := []pluginruntime.InvokeOption{pluginruntime.WithInstance(ref.InstanceName())}
	if p.hostCaller != nil {
		options = append(options, pluginruntime.WithHostCaller(p.hostCaller(pluginCtx)))
	}
	resp, err := host.Invoke(ctx, p.manifest.Name, protocol.CommandAuthTest, material, options...)
	if err != nil {
		sendAuthTestReport(reports, p.manifest.Name, ref.InstanceName(), method, "auth.test", "failed", err.Error(), nil)
		return nil
	}
	if !resp.OK {
		sendAuthTestReport(reports, p.manifest.Name, ref.InstanceName(), method, "auth.test", "failed", pluginErrorMessage(resp.Error), nil)
		return nil
	}
	status, message, details := decodeAuthTestResult(resp.Result)
	sendAuthTestReport(reports, p.manifest.Name, ref.InstanceName(), method, "auth.test", status, message, details)
	return nil
}

func (p Plugin) authMaterial(ctx context.Context, instance, method string, resolver sharedsecret.Resolver) (sdkmanifest.AuthMaterial, error) {
	material := sdkmanifest.AuthMaterial{Method: strings.TrimSpace(method)}
	if resolver == nil {
		return material, nil
	}
	declared, ok := authMethodDeclaration(p.manifest.Auth, material.Method)
	if !ok {
		return material, nil
	}
	values := map[string]string{}
	for _, field := range declared.Fields {
		slot := strings.TrimSpace(field.Name)
		if slot == "" {
			continue
		}
		if value, ok, err := resolveSecretString(ctx, resolver, sharedsecret.Plugin(p.manifest.Name, instance, sharedsecret.Slot(slot))); err != nil {
			return material, err
		} else if ok {
			values[slot] = value
			continue
		}
		for _, env := range field.Env {
			if value, ok, err := resolveSecretString(ctx, resolver, sharedsecret.Env(env)); err != nil {
				return material, err
			} else if ok {
				values[slot] = value
				break
			}
		}
	}
	if len(values) > 0 {
		material.Values = values
	}
	return material, nil
}

// ContextProviders returns runtime context providers backed by plugin protocol
// context build calls.
func (p Plugin) ContextProviders(_ context.Context, ctx contributions.Context) ([]corecontext.Provider, error) {
	out := make([]corecontext.Provider, 0, len(p.manifest.Context))
	for _, declared := range p.manifest.Context {
		spec := contextSpec(declared)
		if spec.Name == "" {
			continue
		}
		out = append(out, bridgedContextProvider{
			spec:       spec,
			plugin:     p.manifest.Name,
			instance:   ctx.Ref.InstanceName(),
			runtime:    p.runtime,
			hostCaller: p.hostCaller,
			pluginCtx:  ctx,
		})
	}
	return out, nil
}

// DatasourceProviders returns runtime datasource providers backed by plugin
// protocol datasource calls.
func (p Plugin) DatasourceProviders(_ context.Context, ctx contributions.Context) ([]coredatasource.Provider, error) {
	if len(p.manifest.Datasources) == 0 {
		return nil, nil
	}
	return []coredatasource.Provider{bridgedDatasourceProvider{
		plugin:       p.manifest.Name,
		instance:     ctx.Ref.InstanceName(),
		runtime:      p.runtime,
		hostCaller:   p.hostCaller,
		pluginCtx:    ctx,
		declarations: append([]sdkmanifest.DatasourceSpec(nil), p.manifest.Datasources...),
	}}, nil
}

type bridgedOperation struct {
	plugin     string
	instance   string
	runtime    pluginruntime.Plugin
	hostCaller HostCallerFactory
	pluginCtx  contributions.Context
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
		if pluginErrorCode(resp.Error, "") == "canceled" {
			return operation.Canceled(pluginErrorMessage(resp.Error))
		}
		return operation.Failed(pluginErrorCode(resp.Error, "plugin_operation_failed"), pluginErrorMessage(resp.Error), nil)
	}
	output, err := decodeOperationOutput(resp.Result)
	if err != nil {
		return operation.Failed("plugin_result_decode_failed", err.Error(), nil)
	}
	return operation.OK(output)
}

type bridgedContextProvider struct {
	spec       corecontext.ProviderSpec
	plugin     string
	instance   string
	runtime    pluginruntime.Plugin
	hostCaller HostCallerFactory
	pluginCtx  contributions.Context
}

func (p bridgedContextProvider) Spec() corecontext.ProviderSpec {
	return p.spec
}

func (p bridgedContextProvider) Build(ctx context.Context, req corecontext.Request) ([]corecontext.Block, error) {
	portable := req.Portable()
	payload := pluginbinding.ContextBuildInput{
		ThreadID:      portable.ThreadID,
		BranchID:      portable.BranchID,
		TurnID:        portable.TurnID,
		Reason:        portable.Reason,
		InputText:     portable.InputText,
		RecentContext: portable.RecentContext,
		Scope:         cloneStringMap(portable.Scope),
		BudgetTokens:  portable.BudgetTokens,
		Query:         strings.TrimSpace(firstNonEmpty(portable.InputText, portable.RecentContext)),
		Limit:         portable.BudgetTokens,
	}
	if len(p.spec.Kinds) > 0 {
		kinds := make([]string, 0, len(p.spec.Kinds))
		for _, kind := range p.spec.Kinds {
			if strings.TrimSpace(string(kind)) != "" {
				kinds = append(kinds, string(kind))
			}
		}
		payload.Kinds = kinds
	}
	host, err := pluginruntime.NewHost(p.runtime)
	if err != nil {
		return nil, err
	}
	options := []pluginruntime.InvokeOption{pluginruntime.WithInstance(p.instance)}
	if p.hostCaller != nil {
		options = append(options, pluginruntime.WithHostCaller(p.hostCaller(p.pluginCtx)))
	}
	resp, err := host.Invoke(ctx, p.plugin, protocol.CommandContextBuild, payload, options...)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", pluginErrorMessage(resp.Error))
	}
	var result pluginbinding.ContextBuildResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	out := make([]corecontext.Block, 0, len(result.Blocks))
	for _, block := range result.Blocks {
		if block.Provider == "" {
			block.Provider = p.spec.Name
		}
		out = append(out, block)
	}
	return out, nil
}

type bridgedDatasourceProvider struct {
	plugin       string
	instance     string
	runtime      pluginruntime.Plugin
	hostCaller   HostCallerFactory
	pluginCtx    contributions.Context
	declarations []sdkmanifest.DatasourceSpec
}

func (p bridgedDatasourceProvider) Entities() []coredatasource.EntitySpec {
	out := make([]coredatasource.EntitySpec, 0, len(p.declarations))
	seen := map[coredatasource.EntityType]bool{}
	for _, declaration := range p.declarations {
		entity := entitySpec(declaration)
		if entity.Type == "" || seen[entity.Type] {
			continue
		}
		seen[entity.Type] = true
		out = append(out, entity)
	}
	return out
}

func (p bridgedDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	declared, ok := p.declarationFor(spec)
	if !ok {
		return nil, fmt.Errorf("pluginbridge: plugin %q does not expose datasource %q", p.plugin, spec.Name)
	}
	return bridgedDatasourceAccessor{
		spec:        spec,
		entity:      entitySpec(declared),
		plugin:      p.plugin,
		instance:    p.instance,
		runtime:     p.runtime,
		hostCaller:  p.hostCaller,
		pluginCtx:   p.pluginCtx,
		declaration: declared,
	}, nil
}

func (p bridgedDatasourceProvider) declarationFor(spec coredatasource.Spec) (sdkmanifest.DatasourceSpec, bool) {
	name := strings.TrimSpace(string(spec.Name))
	for _, declaration := range p.declarations {
		if strings.TrimSpace(declaration.Name) == name {
			return declaration, true
		}
	}
	if len(p.declarations) == 1 && name == "" {
		return p.declarations[0], true
	}
	return sdkmanifest.DatasourceSpec{}, false
}

type bridgedDatasourceAccessor struct {
	spec        coredatasource.Spec
	entity      coredatasource.EntitySpec
	plugin      string
	instance    string
	runtime     pluginruntime.Plugin
	hostCaller  HostCallerFactory
	pluginCtx   contributions.Context
	declaration sdkmanifest.DatasourceSpec
}

func (a bridgedDatasourceAccessor) Spec() coredatasource.Spec {
	return a.spec
}

func (a bridgedDatasourceAccessor) Entities() []coredatasource.EntitySpec {
	if a.entity.Type == "" {
		return nil
	}
	return []coredatasource.EntitySpec{a.entity}
}

func (a bridgedDatasourceAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	payload := map[string]any{
		"datasource": string(a.spec.Name),
		"entity":     string(firstEntity(req.Entity, a.entity.Type)),
		"query":      req.Query,
		"limit":      req.Limit,
	}
	if len(req.Filters) > 0 {
		payload["filters"] = req.Filters
	}
	raw, err := a.call(ctx, protocol.CommandDatasourcesSearch, payload)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	var result struct {
		Count   int               `json:"count"`
		Records []json.RawMessage `json:"records"`
		Errors  []datasourceError `json:"errors,omitempty"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return coredatasource.SearchResult{}, err
	}
	if len(result.Errors) > 0 {
		return coredatasource.SearchResult{}, fmt.Errorf("pluginbridge: datasource search failed: %s", result.Errors[0].Message)
	}
	records, err := decodeDatasourceRecords(a.spec.Name, firstEntity(req.Entity, a.entity.Type), result.Records)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: firstEntity(req.Entity, a.entity.Type), Records: records, Total: result.Count}, nil
}

func (a bridgedDatasourceAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	payload := map[string]any{
		"datasource": string(a.spec.Name),
		"entity":     string(firstEntity(req.Entity, a.entity.Type)),
		"id":         req.ID,
	}
	raw, err := a.call(ctx, protocol.CommandDatasourcesGet, payload)
	if err != nil {
		return coredatasource.Record{}, err
	}
	var result struct {
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return coredatasource.Record{}, err
	}
	record, err := decodeDatasourceRecord(a.spec.Name, firstEntity(req.Entity, a.entity.Type), result.Record)
	if err != nil {
		return coredatasource.Record{}, err
	}
	return record, nil
}

func (a bridgedDatasourceAccessor) call(ctx context.Context, command string, payload any) (json.RawMessage, error) {
	host, err := pluginruntime.NewHost(a.runtime)
	if err != nil {
		return nil, err
	}
	options := []pluginruntime.InvokeOption{pluginruntime.WithInstance(a.instance)}
	if a.hostCaller != nil {
		options = append(options, pluginruntime.WithHostCaller(a.hostCaller(a.pluginCtx)))
	}
	resp, err := host.Invoke(ctx, a.plugin, command, payload, options...)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", pluginErrorMessage(resp.Error))
	}
	return append(json.RawMessage(nil), resp.Result...), nil
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

func decodeOperationOutput(raw json.RawMessage) (any, error) {
	value, err := decodeValue(raw)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	text, _ := object["text"].(string)
	data, hasData := object["data"]
	if strings.TrimSpace(text) == "" || !hasData {
		return value, nil
	}
	return operation.Rendered{Text: text, Data: data}, nil
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

func contextSpecs(declarations []sdkmanifest.ContextSpec) []corecontext.ProviderSpec {
	out := make([]corecontext.ProviderSpec, 0, len(declarations))
	for _, declaration := range declarations {
		spec := contextSpec(declaration)
		if spec.Name != "" {
			out = append(out, spec)
		}
	}
	return out
}

func contextSpec(declaration sdkmanifest.ContextSpec) corecontext.ProviderSpec {
	name := strings.TrimSpace(string(declaration.Name))
	if name == "" {
		return corecontext.ProviderSpec{}
	}
	kinds := make([]corecontext.BlockKind, 0, len(declaration.Kinds))
	for _, kind := range declaration.Kinds {
		if strings.TrimSpace(string(kind)) != "" {
			kinds = append(kinds, corecontext.BlockKind(kind))
		}
	}
	return corecontext.ProviderSpec{Name: corecontext.ProviderName(name), Description: strings.TrimSpace(declaration.Description), Kinds: kinds}
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstAuthMethod(methods []sdkmanifest.AuthMethod) string {
	for _, method := range methods {
		if strings.TrimSpace(method.Name) != "" {
			return strings.TrimSpace(method.Name)
		}
	}
	return ""
}

func authMethodDeclaration(methods []sdkmanifest.AuthMethod, name string) (sdkmanifest.AuthMethod, bool) {
	trimmed := strings.TrimSpace(name)
	for _, method := range methods {
		if strings.TrimSpace(method.Name) == trimmed {
			return method, true
		}
	}
	if trimmed == "" && len(methods) == 1 {
		return methods[0], true
	}
	return sdkmanifest.AuthMethod{}, false
}

func resolveSecretString(ctx context.Context, resolver sharedsecret.Resolver, ref sharedsecret.Ref) (string, bool, error) {
	if resolver == nil {
		return "", false, nil
	}
	material, ok, err := resolver.ResolveSecret(ctx, ref)
	if err != nil || !ok {
		return "", ok, err
	}
	value := strings.TrimSpace(string(material.Value))
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func decodeAuthTestResult(raw json.RawMessage) (string, string, map[string]string) {
	var result struct {
		Status  string         `json:"status,omitempty"`
		Message string         `json:"message,omitempty"`
		Text    string         `json:"text,omitempty"`
		Summary string         `json:"summary,omitempty"`
		Data    map[string]any `json:"data,omitempty"`
		Details map[string]any `json:"details,omitempty"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &result)
	}
	status := firstNonEmpty(result.Status, stringFromAny(result.Data["status"]), "ok")
	message := firstNonEmpty(result.Message, result.Text, result.Summary, stringFromAny(result.Data["message"]))
	details := stringMap(result.Details)
	if len(details) == 0 {
		if nested, ok := result.Data["details"].(map[string]any); ok {
			details = stringMap(nested)
		}
	}
	return status, message, details
}

func sendAuthTestReport(reports chan<- contributions.AuthTestReport, plugin, instance, method, check, status, message string, details map[string]string) {
	if reports == nil {
		return
	}
	reports <- contributions.AuthTestReport{
		Plugin:   strings.TrimSpace(plugin),
		Instance: strings.TrimSpace(instance),
		Method:   strings.TrimSpace(method),
		Check:    strings.TrimSpace(check),
		Status:   strings.TrimSpace(status),
		Message:  strings.TrimSpace(message),
		Details:  details,
	}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func stringMap(values map[string]any) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range values {
		str := stringFromAny(value)
		if strings.TrimSpace(key) != "" && str != "" {
			out[strings.TrimSpace(key)] = str
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func entitySpec(declaration sdkmanifest.DatasourceSpec) coredatasource.EntitySpec {
	entity := strings.TrimSpace(declaration.Entity)
	if entity == "" {
		return coredatasource.EntitySpec{}
	}
	return coredatasource.EntitySpec{
		Type:         coredatasource.EntityType(entity),
		Description:  strings.TrimSpace(declaration.Description),
		Capabilities: datasourceCapabilities(declaration.Capabilities),
		Fields:       datasourceFields(declaration.EntitySchema),
		Relations:    datasourceRelations(declaration.Relations),
	}
}

func datasourceCapabilities(values []string) []coredatasource.EntityCapability {
	out := make([]coredatasource.EntityCapability, 0, len(values))
	for _, value := range values {
		switch strings.TrimSpace(value) {
		case "search":
			out = append(out, coredatasource.EntityCapabilitySearch)
		case "list":
			out = append(out, coredatasource.EntityCapabilityList)
		case "get":
			out = append(out, coredatasource.EntityCapabilityGet)
		case "index":
			out = append(out, coredatasource.EntityCapabilityIndex)
		case "relation":
			out = append(out, coredatasource.EntityCapabilityRelation)
		}
	}
	return out
}

func datasourceFields(schema *sdkmanifest.DatasourceEntitySchema) []coredatasource.FieldSpec {
	if schema == nil {
		return nil
	}
	out := make([]coredatasource.FieldSpec, 0, len(schema.Fields))
	for _, field := range schema.Fields {
		if strings.TrimSpace(field.Name) == "" {
			continue
		}
		out = append(out, coredatasource.FieldSpec{
			Name:        strings.TrimSpace(field.Name),
			Type:        coredatasource.FieldType(strings.TrimSpace(field.Type)),
			Description: strings.TrimSpace(field.Description),
			Identifier:  schema.IDField == field.Name,
		})
	}
	return out
}

func datasourceRelations(relations []sdkmanifest.DatasourceRelationSpec) []coredatasource.RelationSpec {
	out := make([]coredatasource.RelationSpec, 0, len(relations))
	for _, relation := range relations {
		name := strings.TrimSpace(relation.Name)
		entity := strings.TrimSpace(relation.Entity)
		if name == "" || entity == "" {
			continue
		}
		out = append(out, coredatasource.RelationSpec{Name: name, TargetEntity: coredatasource.EntityType(entity)})
	}
	return out
}

func firstEntity(requested, fallback coredatasource.EntityType) coredatasource.EntityType {
	if strings.TrimSpace(string(requested)) != "" {
		return requested
	}
	return fallback
}

type datasourceError struct {
	Message string `json:"message"`
}

func decodeDatasourceRecords(datasource coredatasource.Name, entity coredatasource.EntityType, values []json.RawMessage) ([]coredatasource.Record, error) {
	out := make([]coredatasource.Record, 0, len(values))
	for _, value := range values {
		record, err := decodeDatasourceRecord(datasource, entity, value)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func decodeDatasourceRecord(datasource coredatasource.Name, entity coredatasource.EntityType, raw json.RawMessage) (coredatasource.Record, error) {
	if len(raw) == 0 {
		return coredatasource.Record{}, nil
	}
	var record struct {
		ID       string            `json:"id"`
		Entity   string            `json:"entity,omitempty"`
		Title    string            `json:"title,omitempty"`
		Content  string            `json:"content,omitempty"`
		URL      string            `json:"url,omitempty"`
		Score    float64           `json:"score,omitempty"`
		Metadata map[string]string `json:"metadata,omitempty"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return coredatasource.Record{}, err
	}
	out := coredatasource.Record{
		ID:         record.ID,
		Datasource: datasource,
		Entity:     entity,
		Title:      record.Title,
		Content:    record.Content,
		URL:        record.URL,
		Score:      record.Score,
		Metadata:   record.Metadata,
		Raw:        json.RawMessage(raw),
	}
	if strings.TrimSpace(record.Entity) != "" {
		out.Entity = coredatasource.EntityType(record.Entity)
	}
	return out, nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
