package command

import (
	"testing"
)

func TestBindPointerFieldInStruct(t *testing.T) {
	// setFieldValue: pointer branch — *string field bound via flag.
	type withPtr struct {
		Name *string `command:"flag=name"`
	}
	inv := Invocation{Input: map[string]any{"name": "hello"}}
	got, err := Bind[withPtr](inv)
	if err != nil {
		t.Fatalf("Bind[withPtr]: %v", err)
	}
	if got.Name == nil || *got.Name != "hello" {
		t.Fatalf("Name = %v, want pointer to 'hello'", got.Name)
	}
}

func TestBindSliceStringFlag(t *testing.T) {
	// setFieldValue: slice branch — []string bound via flag (single value).
	type withSlice struct {
		Tags []string `command:"flag=tags"`
	}
	inv := Invocation{Input: map[string]any{"tags": "go"}}
	got, err := Bind[withSlice](inv)
	if err != nil {
		t.Fatalf("Bind[withSlice]: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "go" {
		t.Fatalf("Tags = %v, want ['go']", got.Tags)
	}
}
