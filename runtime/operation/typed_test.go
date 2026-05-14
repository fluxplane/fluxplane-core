package operationruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
)

type typedInput struct {
	Name string `json:"name" jsonschema:"description=Name to echo.,required"`
	Age  int    `json:"age,omitempty" jsonschema:"minimum=0"`
}

type typedOutput struct {
	Greeting string `json:"greeting" jsonschema:"required"`
}

func TestNewTypedGeneratesSchemaAndBindsInput(t *testing.T) {
	op := NewTyped(operation.Spec{
		Ref:         operation.Ref{Name: "greet"},
		Description: "Greet someone.",
	}, func(_ operation.Context, input typedInput) (typedOutput, error) {
		return typedOutput{Greeting: "hello " + input.Name}, nil
	})

	spec := op.Spec()
	if len(spec.Input.Schema.Data) == 0 {
		t.Fatal("input schema is empty")
	}
	if len(spec.Output.Schema.Data) == 0 {
		t.Fatal("output schema is empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(spec.Input.Schema.Data, &schema); err != nil {
		t.Fatalf("input schema json: %v", err)
	}
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "name" {
		t.Fatalf("required = %#v, want [name]", required)
	}

	result := op.Run(operation.NewContext(context.Background(), nil), map[string]any{"name": "Ada"})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	output, ok := result.Output.(typedOutput)
	if !ok || output.Greeting != "hello Ada" {
		t.Fatalf("output = %#v", result.Output)
	}
}

func TestNewTypedResultDerivesIntentFromTypedInput(t *testing.T) {
	op := NewTypedResult[typedInput, typedOutput](operation.Spec{
		Ref: operation.Ref{Name: "inspect"},
	}, func(_ operation.Context, input typedInput) operation.Result {
		return operation.OK(typedOutput{Greeting: input.Name})
	}, WithIntent(func(_ operation.Context, input typedInput) (operation.IntentSet, error) {
		return operation.IntentSet{Operations: []operation.IntentOperation{{
			Behavior:  operation.IntentFilesystemRead,
			Target:    operation.PathTarget{Path: operation.Path(input.Name)},
			Role:      operation.IntentRoleReadTarget,
			Certainty: operation.IntentCertain,
		}}}, nil
	}))

	provider, ok := op.(operation.IntentProvider)
	if !ok {
		t.Fatal("operation does not implement IntentProvider")
	}
	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), map[string]any{"name": "README.md"})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if len(intents.Operations) != 1 {
		t.Fatalf("intents = %#v, want one operation", intents)
	}
	target, ok := intents.Operations[0].Target.(operation.PathTarget)
	if !ok || target.Path != "README.md" {
		t.Fatalf("target = %#v, want README.md path", intents.Operations[0].Target)
	}
}
