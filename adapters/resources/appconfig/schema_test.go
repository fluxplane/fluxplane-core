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
	pluginRefs := schemaMap["$defs"].(map[string]any)["app_pluginRefs"].(map[string]any)
	if !schemaContainsConst(pluginRefs, "chat") || !schemaContainsConst(pluginRefs, "search") {
		t.Fatalf("plugin refs schema = %#v, want chat and search plugin branches", pluginRefs)
	}
	if !schemaContainsProperty(pluginRefs, "auth") {
		t.Fatalf("plugin refs schema = %#v, want chat config auth schema", pluginRefs)
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
		"plugins": map[string]any{
			"search": nil,
			"chat": map[string]any{
				"auth": map[string]any{"method": "env"},
			},
			"new-chat-instance": map[string]any{
				"kind": "chat",
				"auth": map[string]any{"method": "env"},
			},
			"disabled-chat": map[string]any{
				"kind":    "chat",
				"enabled": false,
			},
		},
	}
	if err := compiled.Validate(valid); err != nil {
		t.Fatalf("Validate typed plugin config: %v", err)
	}
	collidingInstance := map[string]any{
		"kind": "app",
		"name": "sample",
		"plugins": map[string]any{
			"search": map[string]any{
				"kind": "chat",
				"auth": map[string]any{"method": "env"},
			},
		},
	}
	if err := compiled.Validate(collidingInstance); err != nil {
		t.Fatalf("Validate explicit plugin kind on colliding instance name: %v", err)
	}
	invalid := map[string]any{
		"kind":    "app",
		"name":    "sample",
		"plugins": map[string]any{"missing": map[string]any{"kind": "missing"}},
	}
	if err := compiled.Validate(invalid); err == nil {
		t.Fatal("Validate unknown plugin succeeded, want error")
	}
	listStyle := map[string]any{
		"kind":    "app",
		"name":    "sample",
		"plugins": []any{"search"},
	}
	if err := compiled.Validate(listStyle); err == nil {
		t.Fatal("Validate list-style plugins succeeded, want error")
	}
	stringOnly := map[string]any{
		"kind":    "app",
		"name":    "sample",
		"plugins": "search",
	}
	if err := compiled.Validate(stringOnly); err == nil {
		t.Fatal("Validate string-only plugins succeeded, want error")
	}
}

func TestManifestSchemaWithOptionsTypesAgentResourceSelectors(t *testing.T) {
	data, err := ManifestSchemaWithOptions(ManifestSchemaOptions{
		Resources: ResourceSchema{
			Operations:       []string{"send_report"},
			Tools:            []string{"image"},
			Datasources:      []string{"jira"},
			Skills:           []string{"review"},
			ContextProviders: []string{"identity"},
			ActivationSets:   []string{"channel", "datasource", "identity", "skills", "memory"},
		},
	})
	if err != nil {
		t.Fatalf("ManifestSchemaWithOptions: %v", err)
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	for property, value := range map[string]string{
		"operations":  "send_report",
		"tools":       "image",
		"datasources": "jira",
		"skills":      "review",
		"context":     "identity",
		"uses":        "datasource",
	} {
		if !schemaArrayPropertyEnumContains(schemaValue, property, value) {
			t.Fatalf("agentDoc.%s item enum missing %q", property, value)
		}
	}
	for _, value := range []string{"channel", "identity", "skills", "memory"} {
		if !schemaArrayPropertyEnumContains(schemaValue, "uses", value) {
			t.Fatalf("agentDoc.uses item enum missing %q", value)
		}
	}
	if schemaArrayPropertyEnumContains(schemaValue, "uses", "slack") {
		t.Fatal("agentDoc.uses item enum contains plugin instance slack, want capability-derived values only")
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
		"kind":        "agent",
		"name":        "helper",
		"uses":        []any{"channel", "datasource", "identity", "skills", "memory"},
		"operations":  []any{"send_report"},
		"tools":       []any{"image"},
		"datasources": []any{"jira"},
		"skills":      []any{"review"},
		"context":     []any{"identity"},
	}
	if err := compiled.Validate(valid); err != nil {
		t.Fatalf("Validate agent resource selectors: %v", err)
	}
	invalid := map[string]any{
		"kind":   "agent",
		"name":   "helper",
		"skills": []any{"unknown"},
	}
	if err := compiled.Validate(invalid); err == nil {
		t.Fatal("Validate unknown agent skill succeeded, want error")
	}
}

func TestManifestSchemaWithOptionsReusesModelSelectors(t *testing.T) {
	data, err := ManifestSchemaWithOptions(ManifestSchemaOptions{
		Resources: ResourceSchema{
			Models: []string{"smart_model", "openrouter/openai/gpt-5.5"},
		},
	})
	if err != nil {
		t.Fatalf("ManifestSchemaWithOptions: %v", err)
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if !schemaContainsEnum(schemaValue, "smart_model") {
		t.Fatalf("schema does not contain model selector enum")
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
		"models": map[string]any{
			"default": "smart_model",
		},
		"distribution": map[string]any{
			"deploy": map[string]any{
				"model": "smart_model",
			},
		},
	}
	if err := compiled.Validate(valid); err != nil {
		t.Fatalf("Validate reused model selectors: %v", err)
	}
	invalid := map[string]any{
		"kind": "app",
		"name": "sample",
		"models": map[string]any{
			"default": "unknown_model",
		},
	}
	if err := compiled.Validate(invalid); err == nil {
		t.Fatal("Validate unknown models.default succeeded, want error")
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
	if !schemaDatasourceBranchEntityEnum(datasourceDoc, "chat", "chat.message") || schemaDatasourceBranchEntityEnum(datasourceDoc, "chat", "tickets.issue") {
		t.Fatalf("DatasourceDoc chat branch should contain only chat entities")
	}
	if !schemaDatasourceBranchEntityEnum(datasourceDoc, "tickets", "tickets.issue") || schemaDatasourceBranchEntityEnum(datasourceDoc, "tickets", "chat.message") {
		t.Fatalf("DatasourceDoc tickets branch should contain only ticket entities")
	}
	if !schemaDatasourceBranchConfigProperty(datasourceDoc, "chat", "instance") || schemaDatasourceBranchConfigProperty(datasourceDoc, "chat", "url") {
		t.Fatalf("DatasourceDoc chat branch should contain only chat config")
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

func schemaContainsEnum(value any, want string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if rawEnum, ok := typed["enum"].([]any); ok {
			for _, value := range rawEnum {
				if value == want {
					return true
				}
			}
		}
		for _, nested := range typed {
			if schemaContainsEnum(nested, want) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if schemaContainsEnum(nested, want) {
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

func schemaArrayPropertyEnumContains(value any, property, want string) bool {
	def, ok := value.(map[string]any)
	if !ok {
		if values, ok := value.([]any); ok {
			for _, nested := range values {
				if schemaArrayPropertyEnumContains(nested, property, want) {
					return true
				}
			}
		}
		return false
	}
	if properties, ok := def["properties"].(map[string]any); ok {
		prop, ok := properties[property].(map[string]any)
		if ok {
			items, ok := prop["items"].(map[string]any)
			if ok {
				enum, ok := items["enum"].([]any)
				if ok {
					for _, value := range enum {
						if value == want {
							return true
						}
					}
				}
			}
		}
	}
	for _, nested := range def {
		if schemaArrayPropertyEnumContains(nested, property, want) {
			return true
		}
	}
	return false
}

func schemaDatasourceBranchEntityEnum(value any, kind, entity string) bool {
	def, ok := value.(map[string]any)
	if !ok {
		return false
	}
	oneOf, ok := def["oneOf"].([]any)
	if !ok {
		return false
	}
	for _, rawBranch := range oneOf {
		branch, ok := rawBranch.(map[string]any)
		if !ok || !schemaDatasourceBranchKind(branch, kind) {
			continue
		}
		for _, value := range schemaDirectEntitiesEnum(branch) {
			if value == entity {
				return true
			}
		}
	}
	return false
}

func schemaDatasourceBranchConfigProperty(value any, kind, property string) bool {
	def, ok := value.(map[string]any)
	if !ok {
		return false
	}
	oneOf, ok := def["oneOf"].([]any)
	if !ok {
		return false
	}
	for _, rawBranch := range oneOf {
		branch, ok := rawBranch.(map[string]any)
		if !ok || !schemaDatasourceBranchKind(branch, kind) {
			continue
		}
		properties, ok := branch["properties"].(map[string]any)
		if !ok {
			return false
		}
		config, ok := properties["config"].(map[string]any)
		if !ok {
			return false
		}
		configProperties, ok := config["properties"].(map[string]any)
		if !ok {
			return false
		}
		_, ok = configProperties[property]
		return ok
	}
	return false
}

func schemaDatasourceBranchKind(branch map[string]any, kind string) bool {
	properties, ok := branch["properties"].(map[string]any)
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
