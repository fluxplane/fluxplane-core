package launch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/engine/core/agent"
	coredata "github.com/fluxplane/engine/core/data"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	coredistribution "github.com/fluxplane/engine/core/distribution"
	corellm "github.com/fluxplane/engine/core/llm"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/resource"
	coresession "github.com/fluxplane/engine/core/session"
	"github.com/fluxplane/engine/core/workflow"
	"github.com/fluxplane/engine/orchestration/distribution"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
)

type testDatasourceConfig struct {
	Instance string `json:"instance,omitempty" jsonschema:"description=Datasource plugin instance."`
}

func TestAppConfigSchemaCommandWritesDefaultSchemaFile(t *testing.T) {
	dir := t.TempDir()
	cmd := NewAppConfigCommand(nil, nil)
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"schema", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	path := filepath.Join(dir, ".fluxplane", "schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if schema["$schema"] == "" {
		t.Fatalf("schema missing $schema: %#v", schema)
	}
	if _, ok := schema["anyOf"].([]any); !ok {
		t.Fatalf("schema anyOf = %#v, want array", schema["anyOf"])
	}
	if !strings.Contains(out.String(), filepath.Join(".fluxplane", "schema.json")) {
		t.Fatalf("output = %q, want schema path", out.String())
	}
}

func TestAppConfigSchemaCommandCanWriteStdout(t *testing.T) {
	cmd := NewAppConfigCommand(nil, nil)
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"schema", "-o", "-"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(out.Bytes(), &schema); err != nil {
		t.Fatalf("stdout is not JSON schema: %v\n%s", err, out.String())
	}
	if schema["title"] != "Fluxplane manifest" {
		t.Fatalf("title = %#v, want Fluxplane manifest", schema["title"])
	}
}

func TestAppConfigSchemaCommandAddsManifestResourceEnums(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fluxplane.yaml"), []byte("kind: app\nname: sample\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	loader := func(ctx context.Context, path string) (distribution.Loaded, error) {
		return distribution.Loaded{
			Root:     path,
			Manifest: filepath.Join(path, "fluxplane.yaml"),
			Distribution: distribution.Distribution{
				Spec: coredistribution.Spec{
					DefaultSession: coresession.Ref{Name: "sample-session"},
					DefaultModel:   coredistribution.ModelDefault{Model: "smart_model"},
				},
				Bundles: []resource.ContributionBundle{{
					Agents:     []agent.Spec{{Name: "helper"}},
					Sessions:   []coresession.Spec{{Name: "sample-session"}},
					Workflows:  []workflow.Spec{{Name: "nightly"}},
					Operations: []operation.Spec{{Ref: operation.Ref{Name: "send_report"}}},
					Datasources: []coredatasource.Spec{{
						Name:     "jira",
						Kind:     "jira",
						Entities: []coredatasource.EntityType{"jira.issue"},
					}},
					DataSources: []coredata.SourceSpec{{
						Name:         "jira",
						Kind:         "jira",
						Entities:     []coredata.EntitySpec{{Type: "jira.issue"}},
						ConfigSchema: operationruntime.SchemaFor[testDatasourceConfig](),
					}},
					LLMProviders: []corellm.ProviderSpec{{
						Name: "openrouter",
						Models: []corellm.ModelSpec{{
							Ref:     corellm.ModelRef{Name: "openai/gpt-5.5"},
							Aliases: []corellm.ModelName{"smart_model"},
						}},
					}},
					Plugins: []resource.PluginRef{{Name: "slack", Instance: "slack-bot"}},
				}},
			},
			Launch: distribution.LaunchConfig{
				Listeners: []distribution.Listener{{Name: "http"}},
				Channels:  []distribution.Channel{{Name: "slack-main", Listener: "http", Session: "sample-session"}},
			},
		}, nil
	}
	cmd := NewAppConfigCommand(loader, nil)
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"schema", "-o", "-", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(out.Bytes(), &schema); err != nil {
		t.Fatalf("stdout is not JSON schema: %v", err)
	}
	if !schemaContainsEnum(schema, "helper") || !schemaContainsEnum(schema, "nightly") ||
		!schemaContainsEnum(schema, "send_report") || !schemaContainsEnum(schema, "jira") ||
		!schemaContainsEnum(schema, "smart_model") {
		t.Fatalf("schema does not contain expected context resource enums")
	}
	if !schemaContainsProperty(schema, "instance") {
		t.Fatalf("schema does not contain datasource config from final bundle")
	}
	if schemaHasInstanceEnum(schema) {
		t.Fatalf("schema hard-enumerates plugin instance declarations")
	}
}

func TestAppConfigValidateCommandLoadsManifest(t *testing.T) {
	loader := func(ctx context.Context, path string) (distribution.Loaded, error) {
		if path != "appdir" {
			t.Fatalf("path = %q, want appdir", path)
		}
		return distribution.Loaded{Root: path, Manifest: filepath.Join(path, "fluxplane.yaml")}, nil
	}
	cmd := NewAppConfigCommand(loader, nil)
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"validate", "appdir"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "valid appdir/fluxplane.yaml") {
		t.Fatalf("output = %q, want valid manifest path", out.String())
	}
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

func schemaHasInstanceEnum(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if properties, ok := typed["properties"].(map[string]any); ok {
			if instance, ok := properties["instance"].(map[string]any); ok {
				if _, ok := instance["enum"].([]any); ok {
					return true
				}
			}
		}
		for _, nested := range typed {
			if schemaHasInstanceEnum(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if schemaHasInstanceEnum(nested) {
				return true
			}
		}
	}
	return false
}

func TestAppConfigValidateCommandFailsOnErrorDiagnostics(t *testing.T) {
	loader := func(ctx context.Context, path string) (distribution.Loaded, error) {
		return distribution.Loaded{
			Root:     path,
			Manifest: filepath.Join(path, "fluxplane.yaml"),
			Diagnostics: []resource.Diagnostic{{
				Severity: resource.SeverityError,
				Message:  "broken resource",
			}},
		}, nil
	}
	cmd := NewAppConfigCommand(loader, nil)
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"validate", "appdir"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute succeeded, want error")
	}
	if !strings.Contains(out.String(), "broken resource") {
		t.Fatalf("output = %q, want diagnostic", out.String())
	}
}
