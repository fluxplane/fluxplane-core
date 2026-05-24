package appconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	coreevidence "github.com/fluxplane/engine/core/evidence"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
	corereaction "github.com/fluxplane/engine/core/reaction"
	coretrigger "github.com/fluxplane/engine/core/trigger"
	"github.com/fluxplane/engine/core/user"
	"github.com/fluxplane/engine/core/workflow"
	invjsonschema "github.com/invopop/jsonschema"
)

const manifestSchemaID = "https://fluxplane.dev/schemas/manifest.schema.json"

// ManifestSchemaOptions controls context-aware additions to the base manifest
// schema.
type ManifestSchemaOptions struct {
	Plugins     []PluginSchema
	Datasources []DatasourceSchema
	Resources   ResourceSchema
}

// PluginSchema describes one plugin available to the generated manifest schema.
type PluginSchema struct {
	Kind         string
	Description  string
	ConfigSchema map[string]any
}

// DatasourceSchema describes one datasource kind available to the generated
// manifest schema.
type DatasourceSchema struct {
	Kind         string
	Description  string
	ConfigSchema map[string]any
	Entities     []string
}

// ResourceSchema describes resources available in the current manifest context.
type ResourceSchema struct {
	Agents          []string
	Sessions        []string
	Workflows       []string
	Operations      []string
	Datasources     []string
	DatasourceKinds []string
	Entities        []string
	EntitiesByKind  map[string][]string
	Channels        []string
	Listeners       []string
	Models          []string
}

// ManifestSchema returns the base JSON Schema for Fluxplane app manifests.
// The schema covers the app document and all resource document kinds accepted
// by this adapter. It does not include project-specific resource completions.
func ManifestSchema() ([]byte, error) {
	return ManifestSchemaWithOptions(ManifestSchemaOptions{})
}

// ManifestSchemaWithOptions returns the base JSON Schema plus context-aware
// completions supplied by the caller.
func ManifestSchemaWithOptions(opts ManifestSchemaOptions) ([]byte, error) {
	docs := []manifestSchemaDoc{
		{title: "Fluxplane app manifest", key: "app", kind: "app", schema: schemaMapFor[Manifest]},
		{title: "Fluxplane agent resource", key: "agent", kind: "agent", requireKind: true, schema: schemaMapFor[agentDoc]},
		{title: "Fluxplane session resource", key: "session", kind: "session", requireKind: true, schema: schemaMapFor[sessionDoc]},
		{title: "Fluxplane command resource", key: "command", kind: "command", requireKind: true, schema: schemaMapFor[commandDoc]},
		{title: "Fluxplane workflow resource", key: "workflow", kind: "workflow", requireKind: true, schema: schemaMapFor[workflowDoc]},
		{title: "Fluxplane operation resource", key: "operation", kind: "operation", requireKind: true, schema: schemaMapFor[operationDoc]},
		{title: "Fluxplane datasource resource", key: "datasource", kind: "datasource", requireKind: true, schema: schemaMapFor[DatasourceDoc]},
		{title: "Fluxplane observer resource", key: "observer", kind: "observer", requireKind: true, schema: schemaMapFor[observerDoc]},
		{title: "Fluxplane assertion deriver resource", key: "assertion_deriver", kind: "assertion_deriver", requireKind: true, schema: schemaMapFor[assertionDeriverDoc]},
		{title: "Fluxplane reaction resource", key: "reaction", kind: "reaction", requireKind: true, schema: schemaMapFor[reactionDoc]},
		{title: "Fluxplane LLM provider resource", key: "llm_provider", kind: "llm_provider", requireKind: true, schema: schemaMapFor[llmProviderDoc]},
	}

	root := map[string]any{
		"$schema":     invjsonschema.Version,
		"title":       "Fluxplane manifest",
		"description": "Base schema for fluxplane.yaml app and resource documents.",
	}
	rootDefs := map[string]any{}
	var anyOf []any
	for _, doc := range docs {
		schema, err := doc.schema()
		if err != nil {
			return nil, err
		}
		delete(schema, "$schema")
		delete(schema, "$id")
		liftDefinitions(rootDefs, schema, doc.key)
		if doc.title != "" {
			schema["title"] = doc.title
		}
		constrainKind(schema, doc.kind, doc.requireKind)
		anyOf = append(anyOf, schema)
	}
	root["$defs"] = rootDefs
	root["anyOf"] = anyOf
	applyPluginSchemas(rootDefs, opts.Plugins)
	applyResourceSchemas(root, opts.Resources)
	applyDatasourceSchemas(rootDefs, opts.Datasources, opts.Resources)
	applyDiscriminatedUnions(rootDefs)

	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(root); err != nil {
		return nil, fmt.Errorf("marshal manifest schema: %w", err)
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

type manifestSchemaDoc struct {
	title       string
	key         string
	kind        string
	requireKind bool
	schema      func() (map[string]any, error)
}

func schemaMapFor[T any]() (map[string]any, error) {
	data, err := schemaDataFor[T]()
	if err != nil {
		return nil, err
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("decode generated schema: %w", err)
	}
	return schema, nil
}

func schemaEnumMapper(t reflect.Type) *invjsonschema.Schema {
	switch t {
	case reflect.TypeOf(user.TrustLevel("")):
		return enumSchema(user.TrustLevels())
	case reflect.TypeOf(user.ResolutionState("")):
		return enumSchema(user.ResolutionStates())
	case reflect.TypeOf(policy.Action("")):
		return enumSchema(policy.Actions())
	case reflect.TypeOf(policy.CallerKind("")):
		return enumSchema(policy.CallerKinds())
	case reflect.TypeOf(policy.SubjectKind("")):
		return enumSchema(policy.SubjectKinds())
	case reflect.TypeOf(policy.ResourceKind("")):
		return enumSchema(policy.ResourceKinds())
	case reflect.TypeOf(policy.TrustLevel("")):
		return enumSchema(policy.TrustLevels())
	case reflect.TypeOf(policy.TrustKind("")):
		return enumSchema(policy.TrustKinds())
	case reflect.TypeOf(policy.Sensitivity("")):
		return enumSchema(policy.Sensitivities())
	case reflect.TypeOf(policy.Decision("")):
		return enumSchema(policy.Decisions())
	case reflect.TypeOf(coreevidence.ObservationPhase("")):
		return enumSchema(coreevidence.ObservationPhases())
	case reflect.TypeOf(coreevidence.SubjectKind("")):
		return enumSchema(coreevidence.SubjectKinds())
	case reflect.TypeOf(operation.Determinism("")):
		return enumSchema(operation.Determinisms())
	case reflect.TypeOf(operation.Idempotency("")):
		return enumSchema(operation.Idempotencies())
	case reflect.TypeOf(operation.RiskLevel("")):
		return enumSchema(operation.RiskLevels())
	case reflect.TypeOf(operation.Effect("")):
		return enumSchema(operation.Effects())
	case reflect.TypeOf(corereaction.Mode("")):
		return enumSchema(corereaction.Modes())
	case reflect.TypeOf(corereaction.ActionKind("")):
		return enumSchema(corereaction.ActionKinds())
	case reflect.TypeOf(coretrigger.Kind("")):
		return enumSchema(coretrigger.Kinds())
	case reflect.TypeOf(workflow.StepKind("")):
		return enumSchema(workflow.StepKinds())
	case reflect.TypeOf(workflow.StepErrorPolicy("")):
		return enumSchema(workflow.StepErrorPolicies())
	default:
		return nil
	}
}

func enumSchema[T ~string](values []T) *invjsonschema.Schema {
	enum := make([]any, 0, len(values))
	for _, value := range values {
		if string(value) != "" {
			enum = append(enum, string(value))
		}
	}
	return &invjsonschema.Schema{Type: "string", Enum: enum}
}

func applyPluginSchemas(defs map[string]any, plugins []PluginSchema) {
	plugins = normalizedPluginSchemas(plugins)
	if len(plugins) == 0 {
		return
	}
	for name := range defs {
		if name == "app_pluginRef" || strings.HasSuffix(name, "_pluginRef") {
			defs[name] = pluginRefSchema(plugins)
		}
	}
}

func normalizedPluginSchemas(plugins []PluginSchema) []PluginSchema {
	byKind := map[string]PluginSchema{}
	for _, plugin := range plugins {
		plugin.Kind = strings.TrimSpace(plugin.Kind)
		if plugin.Kind == "" {
			continue
		}
		if _, exists := byKind[plugin.Kind]; exists {
			continue
		}
		byKind[plugin.Kind] = plugin
	}
	kinds := make([]string, 0, len(byKind))
	for kind := range byKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	out := make([]PluginSchema, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, byKind[kind])
	}
	return out
}

func pluginRefSchema(plugins []PluginSchema) map[string]any {
	kinds := make([]any, 0, len(plugins))
	for _, plugin := range plugins {
		kinds = append(kinds, plugin.Kind)
	}
	oneOf := []any{
		map[string]any{
			"type":        "string",
			"enum":        kinds,
			"description": "Plugin kind shortcut. Use the object form when an instance name or config is needed.",
		},
	}
	for _, plugin := range plugins {
		oneOf = append(oneOf, pluginObjectSchema(plugin))
	}
	return map[string]any{
		"oneOf":       oneOf,
		"description": "Plugin reference. Selects one plugin linked into this Fluxplane binary.",
	}
}

func pluginObjectSchema(plugin PluginSchema) map[string]any {
	properties := map[string]any{
		"kind": map[string]any{
			"type":        "string",
			"const":       plugin.Kind,
			"description": "Plugin kind.",
		},
		"instance": map[string]any{
			"type":        "string",
			"description": "Optional plugin instance name. Defaults to the plugin kind.",
		},
	}
	if plugin.ConfigSchema != nil {
		config := cloneSchemaMap(plugin.ConfigSchema)
		delete(config, "$schema")
		delete(config, "$id")
		addDescription(config, "Configuration for the "+plugin.Kind+" plugin.")
		properties["config"] = config
	}
	description := strings.TrimSpace(plugin.Description)
	if description == "" {
		description = "Configures the " + plugin.Kind + " plugin."
	}
	return map[string]any{
		"type":                 "object",
		"title":                plugin.Kind + " plugin",
		"description":          description,
		"properties":           properties,
		"required":             []any{"kind"},
		"additionalProperties": false,
	}
}

func cloneSchemaMap(schema map[string]any) map[string]any {
	data, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func cloneSchemaAny(schema any) any {
	data, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func addDescription(schema map[string]any, description string) {
	if strings.TrimSpace(description) == "" {
		return
	}
	if current, ok := schema["description"].(string); ok && strings.TrimSpace(current) != "" {
		return
	}
	schema["description"] = description
}

func applyResourceSchemas(root map[string]any, resources ResourceSchema) {
	enums := map[string][]string{
		"agent":      resources.Agents,
		"session":    resources.Sessions,
		"workflow":   resources.Workflows,
		"operation":  resources.Operations,
		"datasource": resources.Datasources,
		"channel":    resources.Channels,
		"listener":   resources.Listeners,
		"model":      resources.Models,
	}
	applyPropertyEnums(root, enums)
	if defs, ok := root["$defs"].(map[string]any); ok {
		for name, raw := range defs {
			def, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch {
			case strings.HasSuffix(name, "_agentRef"):
				applyRefNameEnum(def, resources.Agents)
			case strings.Contains(strings.ToLower(name), "modelref"):
				applyRefNameEnum(def, resources.Models)
			case strings.HasSuffix(name, "_DatasourceDoc"):
				applyObjectPropertyEnum(def, "kind", resources.DatasourceKinds)
			}
		}
	}
}

func applyDatasourceSchemas(defs map[string]any, datasources []DatasourceSchema, resources ResourceSchema) {
	datasources = normalizedDatasourceSchemas(datasources, resources)
	for name, raw := range defs {
		if !strings.HasSuffix(name, "_DatasourceDoc") {
			continue
		}
		def, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		addDatasourceConditionals(def, datasources)
	}
}

func normalizedDatasourceSchemas(datasources []DatasourceSchema, resources ResourceSchema) []DatasourceSchema {
	byKind := map[string]DatasourceSchema{}
	for _, datasource := range datasources {
		datasource.Kind = strings.TrimSpace(datasource.Kind)
		if datasource.Kind == "" {
			continue
		}
		if len(datasource.Entities) == 0 {
			datasource.Entities = append([]string(nil), resources.EntitiesByKind[datasource.Kind]...)
		}
		if _, exists := byKind[datasource.Kind]; exists {
			continue
		}
		byKind[datasource.Kind] = datasource
	}
	for kind, entities := range resources.EntitiesByKind {
		kind = strings.TrimSpace(kind)
		if kind == "" || len(entities) == 0 {
			continue
		}
		if _, exists := byKind[kind]; exists {
			continue
		}
		byKind[kind] = DatasourceSchema{
			Kind:     kind,
			Entities: append([]string(nil), entities...),
		}
	}
	kinds := make([]string, 0, len(byKind))
	for kind := range byKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	out := make([]DatasourceSchema, 0, len(kinds))
	for _, kind := range kinds {
		datasource := byKind[kind]
		datasource.Entities = sortedUnique(datasource.Entities)
		out = append(out, datasource)
	}
	return out
}

func addDatasourceConditionals(def map[string]any, datasources []DatasourceSchema) {
	allOf, _ := def["allOf"].([]any)
	for _, datasource := range datasources {
		thenProperties := map[string]any{}
		if datasource.ConfigSchema != nil {
			config := cloneSchemaMap(datasource.ConfigSchema)
			delete(config, "$schema")
			delete(config, "$id")
			description := strings.TrimSpace(datasource.Description)
			if description == "" {
				description = "Configuration for the " + datasource.Kind + " datasource provider."
			}
			addDescription(config, description)
			thenProperties["config"] = config
		}
		if len(datasource.Entities) > 0 {
			thenProperties["entities"] = stringArraySchema(datasource.Entities, "Entities supported by the "+datasource.Kind+" datasource provider.")
		}
		if len(thenProperties) == 0 {
			continue
		}
		allOf = append(allOf, map[string]any{
			"if": map[string]any{
				"properties": map[string]any{
					"kind": map[string]any{"const": datasource.Kind},
				},
				"required": []any{"kind"},
			},
			"then": map[string]any{
				"properties": thenProperties,
			},
		})
	}
	if len(allOf) > 0 {
		def["allOf"] = allOf
	}
}

func stringArraySchema(values []string, description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items": map[string]any{
			"type":        "string",
			"description": "One datasource entity type.",
			"enum":        stringsEnum(values),
		},
	}
}

func applyDiscriminatedUnions(defs map[string]any) {
	if def, ok := defs["app_commandTargetDoc"].(map[string]any); ok {
		applyCommandTargetUnion(def)
	}
	if def, ok := defs["command_commandTargetDoc"].(map[string]any); ok {
		applyCommandTargetUnion(def)
	}
	if def, ok := defs["app_workflowStepDoc"].(map[string]any); ok {
		applyWorkflowStepUnion(def)
	}
	if def, ok := defs["workflow_workflowStepDoc"].(map[string]any); ok {
		applyWorkflowStepUnion(def)
	}
}

func applyCommandTargetUnion(def map[string]any) {
	properties, ok := def["properties"].(map[string]any)
	if !ok {
		return
	}
	def["oneOf"] = []any{
		objectBranch(properties, []string{"operation", "input"}, []string{"operation"}, "Command target that invokes an operation."),
		objectBranch(properties, []string{"workflow", "input"}, []string{"workflow"}, "Command target that starts a workflow."),
	}
	def["description"] = "Command target. Specify exactly one of operation or workflow."
}

func applyWorkflowStepUnion(def map[string]any) {
	properties, ok := def["properties"].(map[string]any)
	if !ok {
		return
	}
	common := []string{"id", "kind", "input", "input_map", "depends_on", "when", "retry", "timeout", "error_policy", "idempotency_key"}
	operationFields := append(append([]string(nil), common...), "operation")
	agentFields := append(append([]string(nil), common...), "agent")
	operationBranch := objectBranch(properties, operationFields, []string{"id", "operation"}, "Workflow step that invokes an operation.")
	agentBranch := objectBranch(properties, agentFields, []string{"id", "agent"}, "Workflow step that submits work to an agent.")
	setConstIfPresent(operationBranch, "kind", string(workflow.StepOperation))
	setConstIfPresent(agentBranch, "kind", string(workflow.StepAgent))
	def["oneOf"] = []any{operationBranch, agentBranch}
	def["description"] = "Workflow step. Specify either operation or agent."
}

func objectBranch(properties map[string]any, names, required []string, description string) map[string]any {
	branchProperties := map[string]any{}
	for _, name := range names {
		if prop, ok := properties[name]; ok {
			branchProperties[name] = cloneSchemaAny(prop)
		}
	}
	branch := map[string]any{
		"type":                 "object",
		"description":          description,
		"properties":           branchProperties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		req := make([]any, 0, len(required))
		for _, name := range required {
			req = append(req, name)
		}
		branch["required"] = req
	}
	return branch
}

func setConstIfPresent(branch map[string]any, property, value string) {
	properties, ok := branch["properties"].(map[string]any)
	if !ok {
		return
	}
	prop, ok := properties[property].(map[string]any)
	if !ok {
		return
	}
	delete(prop, "enum")
	prop["const"] = value
}

func applyObjectPropertyEnum(schema map[string]any, property string, values []string) {
	if len(values) == 0 {
		return
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}
	prop, ok := properties[property].(map[string]any)
	if !ok || prop["type"] != "string" {
		return
	}
	prop["enum"] = stringsEnum(values)
}

func applyPropertyEnums(value any, enums map[string][]string) {
	switch typed := value.(type) {
	case map[string]any:
		if properties, ok := typed["properties"].(map[string]any); ok {
			for name, raw := range properties {
				prop, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				values := enums[name]
				if len(values) == 0 || prop["type"] != "string" {
					continue
				}
				prop["enum"] = stringsEnum(values)
			}
		}
		for _, nested := range typed {
			applyPropertyEnums(nested, enums)
		}
	case []any:
		for _, nested := range typed {
			applyPropertyEnums(nested, enums)
		}
	}
}

func applyRefNameEnum(schema map[string]any, values []string) {
	if len(values) == 0 {
		return
	}
	if schema["type"] == "string" {
		schema["enum"] = stringsEnum(values)
	}
	if oneOf, ok := schema["oneOf"].([]any); ok {
		for _, branch := range oneOf {
			branchMap, ok := branch.(map[string]any)
			if !ok {
				continue
			}
			if branchMap["type"] == "string" {
				branchMap["enum"] = stringsEnum(values)
			}
			if properties, ok := branchMap["properties"].(map[string]any); ok {
				if name, ok := properties["name"].(map[string]any); ok && name["type"] == "string" {
					name["enum"] = stringsEnum(values)
				}
			}
		}
	}
}

func stringsEnum(values []string) []any {
	values = sortedUnique(values)
	enum := make([]any, 0, len(values))
	for _, value := range values {
		enum = append(enum, value)
	}
	return enum
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func liftDefinitions(rootDefs map[string]any, schema map[string]any, prefix string) {
	defs, _ := schema["$defs"].(map[string]any)
	delete(schema, "$defs")
	rewriteRefs(schema, prefix)
	for name, def := range defs {
		rewriteRefs(def, prefix)
		rootDefs[prefixedDef(prefix, name)] = def
	}
}

func rewriteRefs(value any, prefix string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == "$ref" {
				if ref, ok := nested.(string); ok {
					typed[key] = rewriteRef(ref, prefix)
				}
				continue
			}
			rewriteRefs(nested, prefix)
		}
	case []any:
		for _, nested := range typed {
			rewriteRefs(nested, prefix)
		}
	}
}

func rewriteRef(ref, prefix string) string {
	const defsPrefix = "#/$defs/"
	if len(ref) <= len(defsPrefix) || ref[:len(defsPrefix)] != defsPrefix {
		return ref
	}
	return "#/$defs/" + prefixedDef(prefix, ref[len(defsPrefix):])
}

func prefixedDef(prefix, name string) string {
	return prefix + "_" + name
}

func constrainKind(schema map[string]any, kind string, required bool) {
	if kind == "" {
		return
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	properties["kind"] = map[string]any{
		"type":  "string",
		"const": kind,
	}
	if required {
		schema["required"] = appendRequired(schema["required"], "kind")
	}
}

func appendRequired(raw any, field string) []any {
	var out []any
	if values, ok := raw.([]any); ok {
		out = append(out, values...)
	}
	for _, value := range out {
		if value == field {
			return out
		}
	}
	return append(out, field)
}
