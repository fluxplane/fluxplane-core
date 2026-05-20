package openapiplugin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	coredata "github.com/fluxplane/agentruntime/core/data"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	runtimedata "github.com/fluxplane/agentruntime/runtime/data"
	"github.com/getkin/kin-openapi/openapi3"
)

const (
	OperationEntity      coredatasource.EntityType = "openapi.operation"
	SchemaEntity         coredatasource.EntityType = "openapi.schema"
	ParameterEntity      coredatasource.EntityType = "openapi.parameter"
	ResponseEntity       coredatasource.EntityType = "openapi.response"
	SecuritySchemeEntity coredatasource.EntityType = "openapi.security_scheme"
)

type generatedSpec struct {
	Operations      []operation.Spec
	OperationSet    operation.Set
	Datasources     []coredatasource.Spec
	DataSources     []coredata.SourceSpec
	Docs            []docRecord
	Executable      []operationDefinition
	AuthMethods     []coresecret.AuthMethodSpec
	AuthByScheme    map[string]coresecret.AuthMethodSpec
	SecuritySchemes map[string]*openapi3.SecurityScheme
}

type operationDefinition struct {
	Spec            operation.Spec
	Instance        string
	Name            string
	Method          string
	Path            string
	Server          string
	Parameters      []*openapi3.Parameter
	RequestBody     *openapi3.RequestBodyRef
	Security        openapi3.SecurityRequirements
	AuthByScheme    map[string]coresecret.AuthMethodSpec
	SecuritySchemes map[string]*openapi3.SecurityScheme
}

func generateAll(ref resource.PluginRef, loaded []loadedSpec) (generatedSpec, error) {
	out := generatedSpec{AuthByScheme: map[string]coresecret.AuthMethodSpec{}, SecuritySchemes: map[string]*openapi3.SecurityScheme{}}
	seenOps := map[string]bool{}
	for _, spec := range loaded {
		generated, err := generateOne(ref, spec)
		if err != nil {
			return generatedSpec{}, err
		}
		for _, op := range generated.Operations {
			name := string(op.Ref.Name)
			if seenOps[name] {
				return generatedSpec{}, fmt.Errorf("duplicate generated operation %q", name)
			}
			seenOps[name] = true
		}
		out.Operations = append(out.Operations, generated.Operations...)
		out.Executable = append(out.Executable, generated.Executable...)
		out.Datasources = append(out.Datasources, generated.Datasources...)
		out.DataSources = append(out.DataSources, generated.DataSources...)
		out.Docs = append(out.Docs, generated.Docs...)
		out.AuthMethods = append(out.AuthMethods, generated.AuthMethods...)
		for name, method := range generated.AuthByScheme {
			out.AuthByScheme[name] = method
		}
		for name, scheme := range generated.SecuritySchemes {
			out.SecuritySchemes[name] = scheme
		}
	}
	out.OperationSet = operation.Set{Name: Name, Description: "Generated OpenAPI operations.", Operations: operationRefs(out.Operations)}
	return out, nil
}

func generateOne(ref resource.PluginRef, loaded loadedSpec) (generatedSpec, error) {
	doc := loaded.Doc
	if doc == nil {
		return generatedSpec{}, fmt.Errorf("openapi document is nil")
	}
	authMethods, authByScheme, securitySchemes := authMethodsFor(ref, loaded.Config, doc)
	out := generatedSpec{AuthMethods: authMethods, AuthByScheme: authByScheme, SecuritySchemes: securitySchemes}
	if loaded.Config.Datasource.Name != "" {
		ds := coredatasource.Spec{
			Name:        coredatasource.Name(loaded.Config.Datasource.Name),
			Description: firstNonEmpty(docDescription(doc), "OpenAPI documentation."),
			Kind:        Name,
			Entities:    []coredatasource.EntityType{OperationEntity, SchemaEntity, ParameterEntity, ResponseEntity, SecuritySchemeEntity},
			Index:       coredatasource.IndexSpec{Enabled: loaded.Config.Datasource.Index.Enabled, Freshness: loaded.Config.Datasource.Index.Freshness},
		}
		out.Datasources = append(out.Datasources, ds)
		out.DataSources = append(out.DataSources, runtimedata.SourceFromDatasource(coredata.SourceName(ds.Name), Name, entitySpecs()))
		out.Docs = append(out.Docs, docsForSpec(ds.Name, doc)...)
	}
	for _, path := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Value(path)
		if item == nil {
			continue
		}
		methods := sortedOperationMethods(item.Operations())
		for _, method := range methods {
			op := item.GetOperation(method)
			if op == nil || !selectedOperation(loaded.Config.Operations, method, path, op) {
				continue
			}
			name := operationName(loaded.Config.Operations, method, path, op)
			params := append([]*openapi3.Parameter{}, parametersFromRefs(item.Parameters)...)
			params = append(params, parametersFromRefs(op.Parameters)...)
			spec := operationSpec(name, method, path, op, params)
			servers := doc.Servers
			if op.Servers != nil && len(*op.Servers) > 0 {
				servers = *op.Servers
			} else if len(item.Servers) > 0 {
				servers = item.Servers
			}
			security := doc.Security
			if op.Security != nil {
				security = *op.Security
			}
			out.Operations = append(out.Operations, spec)
			out.Executable = append(out.Executable, operationDefinition{
				Spec: spec, Instance: ref.InstanceName(), Name: name, Method: strings.ToUpper(method), Path: path, Server: firstServerURL(servers),
				Parameters: params, RequestBody: op.RequestBody, Security: security, AuthByScheme: authByScheme, SecuritySchemes: securitySchemes,
			})
		}
	}
	return out, nil
}

func operationSpec(name, method, path string, op *openapi3.Operation, params []*openapi3.Parameter) operation.Spec {
	description := firstNonEmpty(op.Description, op.Summary, strings.ToUpper(method)+" "+path)
	input := operation.Type{Name: name + "_input", Description: "OpenAPI request input.", Schema: operation.Schema{Format: "json-schema", Data: operationInputSchema(params, op.RequestBody)}}
	output := operation.Type{Name: name + "_output", Description: "HTTP response.", Schema: rawSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status":  map[string]any{"type": "integer"},
			"headers": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"body":    map[string]any{},
		},
	})}
	return operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Input:       input,
		Output:      output,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: idempotencyFor(method),
			Risk:        operation.RiskMedium,
		},
		Annotations: map[string]string{"openapi.method": strings.ToUpper(method), "openapi.path": path},
	}
}

func operationInputSchema(params []*openapi3.Parameter, body *openapi3.RequestBodyRef) []byte {
	envelope := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path":    objectSchema(),
			"query":   objectSchema(),
			"headers": objectSchema(),
			"cookies": objectSchema(),
			"body":    map[string]any{},
		},
	}
	for _, param := range params {
		if param == nil {
			continue
		}
		location := param.In
		switch location {
		case "cookie":
			location = "cookies"
		case "header":
			location = "headers"
		}
		props, ok := envelope["properties"].(map[string]any)[location].(map[string]any)
		if !ok {
			continue
		}
		paramProps, _ := props["properties"].(map[string]any)
		if paramProps == nil {
			paramProps = map[string]any{}
			props["properties"] = paramProps
		}
		paramProps[param.Name] = schemaValue(param.Schema)
		if param.Required {
			props["required"] = appendString(props["required"], param.Name)
		}
	}
	if body != nil && body.Value != nil {
		if schema := firstContentSchema(body.Value.Content); schema != nil {
			envelope["properties"].(map[string]any)["body"] = schemaValue(schema)
		}
	}
	raw, _ := json.Marshal(envelope)
	return raw
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func rawSchema(value map[string]any) operation.Schema {
	raw, _ := json.Marshal(value)
	return operation.Schema{Format: "json-schema", Data: raw}
}

func schemaValue(ref *openapi3.SchemaRef) map[string]any {
	if ref == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		return map[string]any{}
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return map[string]any{}
	}
	return value
}

func appendString(raw any, value string) []string {
	var out []string
	if existing, ok := raw.([]string); ok {
		out = append(out, existing...)
	}
	out = append(out, value)
	return out
}

func selectedOperation(cfg OperationsConfig, method, path string, op *openapi3.Operation) bool {
	candidates := operationSelectors(method, path, op, operationBaseName(method, path, op))
	if len(cfg.Include) > 0 && !matchesAny(cfg.Include, candidates) {
		return false
	}
	return !matchesAny(cfg.Exclude, candidates)
}

func operationName(cfg OperationsConfig, method, path string, op *openapi3.Operation) string {
	base := operationBaseName(method, path, op)
	if override, ok := cfg.Overrides[base]; ok && strings.TrimSpace(override.Name) != "" {
		return sanitizeName(override.Name)
	}
	if strings.TrimSpace(cfg.Prefix) == "" {
		return sanitizeName(base)
	}
	return sanitizeName(cfg.Prefix + "_" + base)
}

func operationBaseName(method, path string, op *openapi3.Operation) string {
	if op != nil && strings.TrimSpace(op.OperationID) != "" {
		return sanitizeName(op.OperationID)
	}
	return sanitizeName(strings.ToLower(method) + "_" + path)
}

func operationSelectors(method, path string, op *openapi3.Operation, name string) []string {
	out := []string{name, strings.ToLower(method) + " " + path, strings.ToUpper(method) + " " + path}
	if op != nil {
		if op.OperationID != "" {
			out = append(out, op.OperationID)
		}
		out = append(out, op.Tags...)
	}
	return out
}

func matchesAny(patterns, candidates []string) bool {
	for _, pattern := range patterns {
		for _, candidate := range candidates {
			if strings.EqualFold(pattern, candidate) {
				return true
			}
		}
	}
	return false
}

var nameRE = regexp.MustCompile(`[^A-Za-z0-9_]+`)
var camelRE = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func sanitizeName(value string) string {
	value = camelRE.ReplaceAllString(value, "${1}_${2}")
	value = strings.Trim(strings.ToLower(nameRE.ReplaceAllString(value, "_")), "_")
	if value == "" {
		return "openapi_operation"
	}
	return value
}

func parametersFromRefs(refs openapi3.Parameters) []*openapi3.Parameter {
	var out []*openapi3.Parameter
	for _, ref := range refs {
		if ref != nil && ref.Value != nil {
			out = append(out, ref.Value)
		}
	}
	return out
}

func firstContentSchema(content openapi3.Content) *openapi3.SchemaRef {
	if media := content.Get("application/json"); media != nil {
		return media.Schema
	}
	keys := make([]string, 0, len(content))
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if content[key] != nil && content[key].Schema != nil {
			return content[key].Schema
		}
	}
	return nil
}

func firstServerURL(servers openapi3.Servers) string {
	for _, server := range servers {
		if server != nil && strings.TrimSpace(server.URL) != "" {
			return strings.TrimRight(strings.TrimSpace(server.URL), "/")
		}
	}
	return ""
}

func sortedOperationMethods(ops map[string]*openapi3.Operation) []string {
	methods := make([]string, 0, len(ops))
	for method := range ops {
		methods = append(methods, method)
	}
	sort.Slice(methods, func(i, j int) bool { return strings.ToLower(methods[i]) < strings.ToLower(methods[j]) })
	return methods
}

func idempotencyFor(method string) operation.Idempotency {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return operation.IdempotencyIdempotent
	default:
		return operation.IdempotencyUnknown
	}
}

func operationRefs(specs []operation.Spec) []operation.Ref {
	refs := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, spec.Ref)
	}
	return refs
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
