package operationruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
)

func TestNamedInstanceOperationAddsInstanceOnlyForMultipleInstances(t *testing.T) {
	base := namedInstanceTestOperation("base")
	single := NewNamedInstance("gitlab", "main", base)
	if hasSchemaProperty(t, single.Spec().Input, "instance") {
		t.Fatal("single instance operation advertised instance input")
	}

	multi := AggregateNamedInstances("gitlab", []NamedInstanceBinding{
		{Instance: "b", Operation: namedInstanceTestOperation("b")},
		{Instance: "a", Operation: namedInstanceTestOperation("a")},
	})
	if !hasSchemaProperty(t, multi.Spec().Input, "instance") {
		t.Fatal("multi instance operation did not advertise instance input")
	}

	result := multi.Run(operation.NewContext(context.Background(), nil), map[string]any{"instance": "a", "name": "Ada"})
	if result.IsError() {
		t.Fatalf("run error = %#v", result.Error)
	}
	output, ok := result.Output.(typedOutput)
	if !ok || output.Greeting != "a:Ada" {
		t.Fatalf("output = %#v, want a:Ada", result.Output)
	}
}

func TestFilterNamedInstancesCanRemoveAllInstances(t *testing.T) {
	op := AggregateNamedInstances("gitlab", []NamedInstanceBinding{
		{Instance: "a", Operation: namedInstanceTestOperation("a")},
	})
	if got := FilterNamedInstances(op, map[string]bool{}); got != nil {
		t.Fatalf("filtered op = %#v, want nil", got)
	}
}

func TestFilterNamedInstancesKeepsInstanceInputWhenSourceWasMultiInstance(t *testing.T) {
	op := AggregateNamedInstances("gitlab", []NamedInstanceBinding{
		{Instance: "a", Operation: namedInstanceTestOperation("a")},
		{Instance: "b", Operation: namedInstanceTestOperation("b")},
	})
	filtered := FilterNamedInstances(op, map[string]bool{"a": true})
	if filtered == nil {
		t.Fatal("filtered op is nil")
	}
	if !hasSchemaProperty(t, filtered.Spec().Input, "instance") {
		t.Fatal("filtered multi-instance operation did not keep instance input")
	}
	if result := filtered.Run(operation.NewContext(context.Background(), nil), map[string]any{"name": "Ada"}); !result.IsError() {
		t.Fatalf("missing instance result = %#v, want error", result)
	}
	if result := filtered.Run(operation.NewContext(context.Background(), nil), map[string]any{"instance": "b", "name": "Ada"}); !result.IsError() {
		t.Fatalf("mismatched instance result = %#v, want error", result)
	}
	result := filtered.Run(operation.NewContext(context.Background(), nil), map[string]any{"instance": "a", "name": "Ada"})
	if result.IsError() {
		t.Fatalf("matching instance result error = %#v", result.Error)
	}
	output, ok := result.Output.(typedOutput)
	if !ok || output.Greeting != "a:Ada" {
		t.Fatalf("output = %#v, want a:Ada", result.Output)
	}
}

func namedInstanceTestOperation(prefix string) operation.Operation {
	return NewTypedResult[typedInput, typedOutput](operation.Spec{
		Ref: operation.Ref{Name: "gitlab_mr"},
	}, func(_ operation.Context, input typedInput) operation.Result {
		return operation.OK(typedOutput{Greeting: prefix + ":" + input.Name})
	})
}

func hasSchemaProperty(t *testing.T, typ operation.Type, name string) bool {
	t.Helper()
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(typ.Schema.Data, &schema); err != nil {
		t.Fatalf("schema json: %v", err)
	}
	_, ok := schema.Properties[name]
	return ok
}
