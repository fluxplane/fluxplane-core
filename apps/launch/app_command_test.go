package launch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/agent"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	coreskill "github.com/fluxplane/fluxplane-core/core/skill"
	coretool "github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/core/workflow"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/spf13/cobra"
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

func TestAppTargetsCommandListsBuildAndDeployTargets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fluxplane.yaml"), []byte(`
kind: app
name: sample
distribution:
  build:
    targets:
      docs:
        kind: documentation
        output: docs/capabilities.md
  deploy:
    targets:
      local:
        kind: docker-compose
        build: [docs]
        compose_file: docker-compose.yaml
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cmd := NewAppTargetsCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"Build targets", "docs", "documentation", "Deploy targets", "local", "docker-compose"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("targets output missing %q:\n%s", want, out.String())
		}
	}
}

func TestAppTargetsCommandCanOutputBuildTargetsAsJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fluxplane.yaml"), []byte(`
kind: app
name: sample
distribution:
  build:
    targets:
      docs:
        kind: documentation
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cmd := NewAppTargetsCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{dir, "--kind", "build", "--output", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result struct {
		Build  []map[string]any `json:"build"`
		Deploy []map[string]any `json:"deploy"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("targets JSON: %v\n%s", err, out.String())
	}
	if len(result.Build) != 1 || result.Build[0]["name"] != "docs" {
		t.Fatalf("build targets = %#v", result.Build)
	}
	if result.Deploy != nil {
		t.Fatalf("deploy targets = %#v, want omitted from build listing", result.Deploy)
	}
}

func TestAppBuildAndDeployHelpStayTargetFocused(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{name: "build", cmd: NewAppBuildCommand()},
		{name: "deploy", cmd: NewAppDeployCommand()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := bytes.Buffer{}
			tc.cmd.SetOut(&out)
			tc.cmd.SetErr(&out)
			tc.cmd.SetArgs([]string{"--help"})

			if err := tc.cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			help := out.String()
			for _, want := range []string{"--target", "--profile", "--dry-run", "--force", "--list-targets"} {
				if !strings.Contains(help, want) {
					t.Fatalf("%s help missing %q:\n%s", tc.name, want, help)
				}
			}
			for _, old := range []string{"--image", "--tag", "--platform", "--push", "--base-image", "--auth-path", "--allow-plugin-auth-env", "--provider", "--model", "--effort", "--output"} {
				if strings.Contains(help, old) {
					t.Fatalf("%s help still contains %q:\n%s", tc.name, old, help)
				}
			}
		})
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
					Skills:           []coreskill.Spec{{Name: "review"}},
					ContextProviders: []corecontext.ProviderSpec{{Name: "identity"}},
					ToolSets:         []coretool.Set{{Action: &coretool.ActionProjection{Tool: "image"}}},
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
		!schemaContainsEnum(schema, "smart_model") || !schemaContainsEnum(schema, "review") ||
		!schemaContainsEnum(schema, "identity") || !schemaContainsEnum(schema, "image") ||
		!schemaContainsEnum(schema, "datasource") {
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
	dir := t.TempDir()
	manifest := filepath.Join(dir, "fluxplane.yaml")
	if err := os.WriteFile(manifest, []byte("kind: app\nname: sample\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	loader := func(ctx context.Context, path string) (distribution.Loaded, error) {
		if path != dir {
			t.Fatalf("path = %q, want %s", path, dir)
		}
		return distribution.Loaded{Root: path, Manifest: manifest}, nil
	}
	cmd := NewAppConfigCommand(loader, nil)
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"validate", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "valid "+manifest) {
		t.Fatalf("output = %q, want valid manifest path", out.String())
	}
}

func TestAppConfigValidateCommandChecksAllManifestDocuments(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "fluxplane.yaml")
	if err := os.WriteFile(manifest, []byte(`
kind: app
name: sample
---
kind: agent
name: helper
datasources:
  - missing
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	loader := func(ctx context.Context, path string) (distribution.Loaded, error) {
		return distribution.Loaded{
			Root:     path,
			Manifest: manifest,
			Distribution: distribution.Distribution{
				Bundles: []resource.ContributionBundle{{
					Datasources: []coredatasource.Spec{{
						Name:     "known",
						Kind:     "memory",
						Entities: []coredatasource.EntityType{"memory.item"},
					}},
				}},
			},
		}, nil
	}
	cmd := NewAppConfigCommand(loader, nil)
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"validate", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute succeeded, want schema validation error")
	}
	if !strings.Contains(err.Error(), "document 2 schema validation failed") {
		t.Fatalf("error = %v, want second document schema validation failure", err)
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
