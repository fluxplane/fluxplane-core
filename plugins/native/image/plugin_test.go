package image

import (
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestPluginContributesImageOperationsAndActionToolSet(t *testing.T) {
	bundle, err := New(nil).Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Operations) != 3 {
		t.Fatalf("operations len = %d, want 3", len(bundle.Operations))
	}
	if len(bundle.OperationSets) != 3 {
		t.Fatalf("operation sets = %#v, want image sets", bundle.OperationSets)
	}
	if !imageOperationSetContains(bundle.OperationSets, Name, GenerateOp) ||
		!imageOperationSetContains(bundle.OperationSets, GenerationSet, GenerateOp) ||
		!imageOperationSetContains(bundle.OperationSets, UnderstandingSet, UnderstandOp) {
		t.Fatalf("operation sets = %#v, want full and capability-specific image sets", bundle.OperationSets)
	}
	if len(bundle.ToolSets) != 1 || bundle.ToolSets[0].Action == nil {
		t.Fatalf("tool sets = %#v, want action tool set", bundle.ToolSets)
	}
	action := bundle.ToolSets[0].Action
	if action.Tool != tool.Name(Name) || len(action.Cases) != 3 {
		t.Fatalf("action projection = %#v, want image with three cases", action)
	}
	schema := string(action.Input.Schema.Data)
	if strings.Contains(schema, `"oneOf"`) || !strings.Contains(schema, `"action"`) || !strings.Contains(schema, `"generate"`) {
		t.Fatalf("schema = %s, want provider-safe action object schema", schema)
	}
}

func imageOperationSetContains(sets []operation.Set, setName, operationName string) bool {
	for _, set := range sets {
		if set.Name != setName {
			continue
		}
		for _, ref := range set.Operations {
			if ref.Name == operation.Name(operationName) {
				return true
			}
		}
	}
	return false
}

func TestProvidersOperationReportsMissingAndKeylessProviders(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := findOp(t, ops, ProvidersOp).Run(operation.NewContext(context.Background(), nil), map[string]any{"action": "info"})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %T, want operation.Rendered", result.Output)
	}
	if !strings.Contains(rendered.Text, "pollinations [configured]") {
		t.Fatalf("text = %q, want configured pollinations", rendered.Text)
	}
	if !strings.Contains(rendered.Text, "ANTHROPIC_API_KEY") {
		t.Fatalf("text = %q, want missing anthropic key", rendered.Text)
	}
}

func TestGenerateOperationUsesRequestedProvider(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	plugin := New(sys, WithGenerationProvider(fakeGenerator{}))
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := findOp(t, ops, GenerateOp).Run(operation.NewContext(context.Background(), nil), map[string]any{
		"provider": "fake",
		"prompt":   "a square",
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered := result.Output.(operation.Rendered)
	if !strings.Contains(rendered.Text, "fake") {
		t.Fatalf("text = %q, want fake provider", rendered.Text)
	}
}

func TestUnderstandOperationUsesRequestedProvider(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	plugin := New(sys, WithUnderstandingProvider(fakeUnderstander{}))
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := findOp(t, ops, UnderstandOp).Run(operation.NewContext(context.Background(), nil), map[string]any{
		"provider": "fake",
		"images":   []string{"data:image/png;base64,iVBORw0KGgo="},
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered := result.Output.(operation.Rendered)
	if rendered.Text != "fake description" {
		t.Fatalf("text = %q, want fake description", rendered.Text)
	}
}

func findOp(t *testing.T, ops []operation.Operation, name operation.Name) operation.Operation {
	t.Helper()
	for _, op := range ops {
		if op.Spec().Ref.Name == name {
			return op
		}
	}
	t.Fatalf("operation %q not found", name)
	return nil
}

type fakeGenerator struct{}

func (fakeGenerator) Info(context.Context, system.System) ProviderInfo {
	return ProviderInfo{Name: "fake", Capabilities: []string{"generate"}, Configured: true}
}

func (fakeGenerator) Generate(context.Context, system.System, GenerateRequest) (GenerateResult, error) {
	return GenerateResult{Provider: "fake", Model: "test", FilePath: "/tmp/fake.png", ContentType: "image/png", SizeBytes: 3}, nil
}

type fakeUnderstander struct{}

func (fakeUnderstander) Info(context.Context, system.System) ProviderInfo {
	return ProviderInfo{Name: "fake", Capabilities: []string{"understand"}, Configured: true}
}

func (fakeUnderstander) Understand(context.Context, system.System, UnderstandRequest) (UnderstandResult, error) {
	return UnderstandResult{Provider: "fake", Model: "test", Text: "fake description"}, nil
}
