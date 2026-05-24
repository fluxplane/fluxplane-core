package appconfig

import (
	"encoding/json"
	"strings"
	"testing"

	santhoshjsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestManifestSchemaCoversBaseDocuments(t *testing.T) {
	data, err := ManifestSchema()
	if err != nil {
		t.Fatalf("ManifestSchema: %v", err)
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	for _, forbidden := range []string{`"depends-on"`, `"error-policy"`, `"idempotency-key"`} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("schema contains dashed manifest field %s", forbidden)
		}
	}
	schemaMap, ok := schemaValue.(map[string]any)
	if !ok {
		t.Fatalf("schema = %T, want object", schemaValue)
	}
	if value, ok := schemaMap["$id"]; ok {
		t.Fatalf("$id = %#v, want omitted for local editor schema", value)
	}
	if _, ok := schemaMap["$defs"].(map[string]any)["app_DaemonConfig"]; !ok {
		t.Fatalf("root $defs missing app_DaemonConfig")
	}
	defs := schemaMap["$defs"].(map[string]any)
	assertEnum(t, defs, "app_identityGroupDoc", "trust", "public", "internal", "operator")
	assertEnum(t, defs, "app_identityMatchDoc", "resolution", "unresolved", "resolved")
	assertEnum(t, defs, "app_Grant", "required_trust", "untrusted", "verified", "privileged", "system")
	assertEnum(t, defs, "app_ResourceRef", "kind", "datasource", "workspace", "path", "process", "network", "channel", "task", "session", "admin", "model", "operation", "secret")
	assertEnum(t, defs, "app_Action", "kind", "activate_skill", "activate_reference", "enable_activation_set", "enable_operation_set", "enable_datasource", "enable_context_provider", "run_workflow", "run_operation", "run_command")
	assertEnum(t, defs, "app_workflowStepDoc", "kind", "operation", "agent")
	assertEnum(t, defs, "app_Spec", "kind", "startup", "schedule")
	assertEnum(t, defs, "app_RuntimeDataStoreDoc", "kind", "mem", "memory", "mysql")
	assertEnum(t, defs, "app_RuntimeEventStoreDoc", "kind", "jetstream", "local", "nats", "nats-jetstream", "sqlite")
	if !schemaContainsOneOf(defs["app_commandTargetDoc"]) || !schemaContainsOneOf(defs["app_workflowStepDoc"]) {
		t.Fatalf("command target and workflow step docs should expose oneOf branches")
	}
	for i, branch := range schemaMap["anyOf"].([]any) {
		if _, ok := branch.(map[string]any)["$defs"]; ok {
			t.Fatalf("anyOf[%d] has nested $defs, want root definitions", i)
		}
		if hasManifestSchemaIDRef(branch) {
			t.Fatalf("anyOf[%d] contains absolute schema-id ref, want local #/$defs refs", i)
		}
	}
	compiler := santhoshjsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaValue); err != nil {
		t.Fatalf("AddResource: %v", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	for name, value := range map[string]any{
		"app": map[string]any{
			"kind": "app",
			"name": "sample",
			"workflows": []any{
				map[string]any{
					"name": "nightly",
					"steps": []any{
						map[string]any{"id": "run", "operation": "echo"},
					},
				},
			},
		},
		"workflow": map[string]any{
			"kind": "workflow",
			"name": "nightly",
			"steps": []any{
				map[string]any{"id": "run", "operation": "echo"},
			},
		},
		"agent": map[string]any{
			"kind": "agent",
			"name": "helper",
		},
	} {
		if err := compiled.Validate(value); err != nil {
			t.Fatalf("Validate(%s): %v", name, err)
		}
	}

	if err := compiled.Validate(map[string]any{"kind": "unknown", "name": "nope"}); err == nil {
		t.Fatal("Validate unknown kind succeeded, want error")
	}
}

func TestManifestSchemaWithOptionsTypesPluginRefs(t *testing.T) {
	data, err := ManifestSchemaWithOptions(ManifestSchemaOptions{
		Plugins: []PluginSchema{
			{
				Kind:        "chat",
				Description: "Chat integration.",
				ConfigSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"auth": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"method": map[string]any{"type": "string"},
							},
							"additionalProperties": false,
						},
					},
					"additionalProperties": false,
				},
			},
			{Kind: "search", Description: "Search integration."},
		},
	})
	if err != nil {
		t.Fatalf("ManifestSchemaWithOptions: %v", err)
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	schemaMap := schemaValue.(map[string]any)
	pluginRef := schemaMap["$defs"].(map[string]any)["app_pluginRef"].(map[string]any)
	if !schemaContainsConst(pluginRef, "chat") || !schemaContainsConst(pluginRef, "search") {
		t.Fatalf("plugin ref schema = %#v, want chat and search plugin branches", pluginRef)
	}
	if !schemaContainsProperty(pluginRef, "auth") {
		t.Fatalf("plugin ref schema = %#v, want chat config auth schema", pluginRef)
	}

	compiler := santhoshjsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaValue); err != nil {
		t.Fatalf("AddResource: %v", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	valid := map[string]any{
		"kind": "app",
		"name": "sample",
		"plugins": []any{
			"search",
			map[string]any{
				"kind":     "chat",
				"instance": "new-chat-instance",
				"config": map[string]any{
					"auth": map[string]any{"method": "env"},
				},
			},
		},
	}
	if err := compiled.Validate(valid); err != nil {
		t.Fatalf("Validate typed plugin config: %v", err)
	}
	invalid := map[string]any{
		"kind":    "app",
		"name":    "sample",
		"plugins": []any{map[string]any{"kind": "missing"}},
	}
	if err := compiled.Validate(invalid); err == nil {
		t.Fatal("Validate unknown plugin succeeded, want error")
	}
}

func TestManifestSchemaWithOptionsScopesDatasourceEntitiesByKind(t *testing.T) {
	data, err := ManifestSchemaWithOptions(ManifestSchemaOptions{
		Datasources: []DatasourceSchema{
			{
				Kind:        "chat",
				Description: "Configuration for chat datasource credentials.",
				ConfigSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"instance": map[string]any{
							"type":        "string",
							"description": "Plugin instance that provides credentials for this datasource.",
						},
					},
					"additionalProperties": false,
				},
			},
		},
		Resources: ResourceSchema{
			DatasourceKinds: []string{"chat", "tickets"},
			EntitiesByKind: map[string][]string{
				"chat":    {"chat.channel", "chat.message"},
				"tickets": {"tickets.issue", "tickets.project"},
			},
		},
	})
	if err != nil {
		t.Fatalf("ManifestSchemaWithOptions: %v", err)
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	schemaMap := schemaValue.(map[string]any)
	datasourceDoc := schemaMap["$defs"].(map[string]any)["app_DatasourceDoc"].(map[string]any)
	if schemaDirectEntitiesEnum(datasourceDoc) != nil {
		t.Fatalf("DatasourceDoc.entities has a flat enum, want kind-scoped conditionals")
	}
	if !schemaConditionalEntityEnum(datasourceDoc, "chat", "chat.message") || schemaConditionalEntityEnum(datasourceDoc, "chat", "tickets.issue") {
		t.Fatalf("DatasourceDoc chat branch should contain only chat entities")
	}
	if !schemaConditionalEntityEnum(datasourceDoc, "tickets", "tickets.issue") || schemaConditionalEntityEnum(datasourceDoc, "tickets", "chat.message") {
		t.Fatalf("DatasourceDoc tickets branch should contain only ticket entities")
	}

	compiler := santhoshjsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaValue); err != nil {
		t.Fatalf("AddResource: %v", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	valid := map[string]any{
		"kind": "app",
		"name": "sample",
		"datasource": map[string]any{
			"datasources": []any{
				map[string]any{"name": "chat", "kind": "chat", "entities": []any{"chat.message"}, "config": map[string]any{"instance": "main"}},
			},
		},
	}
	if err := compiled.Validate(valid); err != nil {
		t.Fatalf("Validate scoped datasource entities: %v", err)
	}
	invalidEntity := map[string]any{
		"kind": "app",
		"name": "sample",
		"datasource": map[string]any{
			"datasources": []any{
				map[string]any{"name": "chat", "kind": "chat", "entities": []any{"tickets.issue"}, "config": map[string]any{"instance": "main"}},
			},
		},
	}
	if err := compiled.Validate(invalidEntity); err == nil {
		t.Fatal("Validate chat datasource with tickets entity succeeded, want error")
	}
	invalidConfig := map[string]any{
		"kind": "app",
		"name": "sample",
		"datasource": map[string]any{
			"datasources": []any{
				map[string]any{"name": "chat", "kind": "chat", "entities": []any{"chat.message"}, "config": map[string]any{"url": "https://example.com"}},
			},
		},
	}
	if err := compiled.Validate(invalidConfig); err == nil {
		t.Fatal("Validate chat datasource with unexpected config key succeeded, want error")
	}
}

func assertEnum(t *testing.T, defs map[string]any, defName, property string, want ...string) {
	t.Helper()
	def, ok := defs[defName].(map[string]any)
	if !ok {
		t.Fatalf("$defs[%s] missing", defName)
	}
	properties, ok := def["properties"].(map[string]any)
	if !ok {
		t.Fatalf("$defs[%s].properties missing", defName)
	}
	prop, ok := properties[property].(map[string]any)
	if !ok {
		t.Fatalf("$defs[%s].properties[%s] missing", defName, property)
	}
	if ref, ok := prop["$ref"].(string); ok {
		prop = referencedDef(t, defs, ref)
	}
	rawEnum, ok := prop["enum"].([]any)
	if !ok {
		t.Fatalf("$defs[%s].properties[%s].enum missing in %#v", defName, property, prop)
	}
	got := make([]string, 0, len(rawEnum))
	for _, value := range rawEnum {
		got = append(got, value.(string))
	}
	if len(got) != len(want) {
		t.Fatalf("%s.%s enum = %#v, want %#v", defName, property, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s.%s enum = %#v, want %#v", defName, property, got, want)
		}
	}
}

func referencedDef(t *testing.T, defs map[string]any, ref string) map[string]any {
	t.Helper()
	const prefix = "#/$defs/"
	if !strings.HasPrefix(ref, prefix) {
		t.Fatalf("unexpected schema ref %q", ref)
	}
	def, ok := defs[strings.TrimPrefix(ref, prefix)].(map[string]any)
	if !ok {
		t.Fatalf("schema ref %q missing target", ref)
	}
	return def
}

func schemaContainsConst(value any, want string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if typed["const"] == want {
			return true
		}
		for _, nested := range typed {
			if schemaContainsConst(nested, want) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if schemaContainsConst(nested, want) {
				return true
			}
		}
	}
	return false
}

func schemaContainsProperty(value any, want string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if properties, ok := typed["properties"].(map[string]any); ok {
			if _, ok := properties[want]; ok {
				return true
			}
		}
		for _, nested := range typed {
			if schemaContainsProperty(nested, want) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if schemaContainsProperty(nested, want) {
				return true
			}
		}
	}
	return false
}

func schemaContainsOneOf(value any) bool {
	typed, ok := value.(map[string]any)
	if !ok {
		return false
	}
	oneOf, ok := typed["oneOf"].([]any)
	return ok && len(oneOf) > 0
}

func schemaDirectEntitiesEnum(value any) []any {
	def, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	properties, ok := def["properties"].(map[string]any)
	if !ok {
		return nil
	}
	entities, ok := properties["entities"].(map[string]any)
	if !ok {
		return nil
	}
	items, ok := entities["items"].(map[string]any)
	if !ok {
		return nil
	}
	enum, _ := items["enum"].([]any)
	return enum
}

func schemaConditionalEntityEnum(value any, kind, entity string) bool {
	def, ok := value.(map[string]any)
	if !ok {
		return false
	}
	allOf, ok := def["allOf"].([]any)
	if !ok {
		return false
	}
	for _, rawBranch := range allOf {
		branch, ok := rawBranch.(map[string]any)
		if !ok || !schemaBranchKind(branch, kind) {
			continue
		}
		then, ok := branch["then"].(map[string]any)
		if !ok {
			return false
		}
		for _, value := range schemaDirectEntitiesEnum(then) {
			if value == entity {
				return true
			}
		}
	}
	return false
}

func schemaBranchKind(branch map[string]any, kind string) bool {
	ifSchema, ok := branch["if"].(map[string]any)
	if !ok {
		return false
	}
	properties, ok := ifSchema["properties"].(map[string]any)
	if !ok {
		return false
	}
	kindSchema, ok := properties["kind"].(map[string]any)
	if !ok {
		return false
	}
	return kindSchema["const"] == kind
}

func hasManifestSchemaIDRef(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == "$ref" && hasPrefix(nested, manifestSchemaID+"#/$defs/") {
				return true
			}
			if hasManifestSchemaIDRef(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if hasManifestSchemaIDRef(nested) {
				return true
			}
		}
	}
	return false
}

func hasPrefix(value any, prefix string) bool {
	text, ok := value.(string)
	return ok && len(text) >= len(prefix) && text[:len(prefix)] == prefix
}
