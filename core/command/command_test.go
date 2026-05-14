package command

import (
	"reflect"
	"testing"
)

func TestPathString(t *testing.T) {
	tests := []struct {
		name string
		path Path
		want string
	}{
		{
			name: "empty path",
			path: Path{},
			want: "",
		},
		{
			name: "single segment",
			path: Path{"foo"},
			want: "/foo",
		},
		{
			name: "two segments",
			path: Path{"foo", "bar"},
			want: "/foo/bar",
		},
		{
			name: "multiple segments",
			path: Path{"foo", "bar", "baz", "qux"},
			want: "/foo/bar/baz/qux",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.path.String()
			if got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInvocationValidateEmpty(t *testing.T) {
	inv := Invocation{Path: Path{}}
	err := inv.Validate()
	if err == nil {
		t.Fatal("Validate() should fail for empty path")
	}
}

func TestInvocationValidateValid(t *testing.T) {
	inv := Invocation{Path: Path{"foo", "bar"}}
	err := inv.Validate()
	if err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
}

func TestInvocationValidateSingleSegment(t *testing.T) {
	inv := Invocation{Path: Path{"context"}}
	err := inv.Validate()
	if err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
}

func TestInvocationWithInput(t *testing.T) {
	inv := Invocation{
		Path: Path{"foo"},
		Input: map[string]any{
			"key": "value",
		},
	}

	err := inv.Validate()
	if err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}

	input, ok := inv.Input.(map[string]any)
	if !ok {
		t.Fatalf("Input = %T, want map[string]any", inv.Input)
	}
	if input["key"] != "value" {
		t.Fatalf("Input[key] = %v, want value", input["key"])
	}
}

func TestPathNil(t *testing.T) {
	var path Path
	if path != nil {
		t.Fatalf("Path nil literal should be nil")
	}
	str := path.String()
	if str != "" {
		t.Fatalf("nil.String() = %q, want empty", str)
	}
}

func TestSpecFields(t *testing.T) {
	spec := Spec{
		Path:        Path{"test"},
		Description: "test command",
		Annotations: map[string]string{
			"key": "value",
		},
	}

	if !reflect.DeepEqual(spec.Path, Path{"test"}) {
		t.Fatalf("Path = %v, want [test]", spec.Path)
	}
	if spec.Description != "test command" {
		t.Fatalf("Description = %q, want 'test command'", spec.Description)
	}
	if spec.Annotations["key"] != "value" {
		t.Fatalf("Annotations[key] = %q, want value", spec.Annotations["key"])
	}
}

func TestInvocationFields(t *testing.T) {
	inv := Invocation{
		Path: Path{"foo", "bar"},
		Input: map[string]any{
			"flag": true,
		},
	}

	if !reflect.DeepEqual(inv.Path, Path{"foo", "bar"}) {
		t.Fatalf("Path = %v, want [foo bar]", inv.Path)
	}
	input, ok := inv.Input.(map[string]any)
	if !ok {
		t.Fatalf("Input = %T, want map", inv.Input)
	}
	if input["flag"] != true {
		t.Fatalf("Input[flag] = %v, want true", input["flag"])
	}
}
