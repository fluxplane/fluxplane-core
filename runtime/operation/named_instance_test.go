package operationruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
)

// TestNamedInstanceRejectsNonStringInstanceWithStableMessage regresses a bug
// where selectInstance used fmt.Sprint(values["instance"]) to derive the
// instance name. For a missing key fmt.Sprint(nil) returned "<nil>" and for
// non-string values produced "true"/"42"/etc - none of which match an actual
// instance and none of which the "instance is required" check could catch.
// The user got a confusing `unknown instance "<nil>"` (or "42", etc.) error
// instead of the clear "instance is required" message.
func TestNamedInstanceRejectsNonStringInstanceWithStableMessage(t *testing.T) {
	op := AggregateNamedInstances("gitlab", []NamedInstanceBinding{
		{Instance: "a", Operation: namedInstanceTestOperation("a")},
		{Instance: "b", Operation: namedInstanceTestOperation("b")},
	})
	cases := []struct {
		name  string
		input map[string]any
	}{
		{"missing key", map[string]any{"name": "Ada"}},
		{"nil value", map[string]any{"instance": nil, "name": "Ada"}},
		{"boolean value", map[string]any{"instance": true, "name": "Ada"}},
		{"number value", map[string]any{"instance": 42, "name": "Ada"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := op.Run(operation.NewContext(context.Background(), nil), tc.input)
			if !result.IsError() || result.Error == nil {
				t.Fatalf("result = %#v, want error", result)
			}
			if !strings.Contains(result.Error.Message, "instance is required") {
				t.Fatalf("error message = %q, want to contain 'instance is required'", result.Error.Message)
			}
		})
	}
}

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
