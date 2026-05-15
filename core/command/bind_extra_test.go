package command

import (
	"encoding"
	"testing"
)

// customText implements encoding.TextUnmarshaler for setScalarValue coverage.
type customText struct {
	val string
}

func (c *customText) UnmarshalText(data []byte) error {
	c.val = string(data)
	return nil
}

var _ encoding.TextUnmarshaler = (*customText)(nil)

type bindEdgeCases struct {
	Tags       []string   `command:"arg"`
	Limit      int64      `command:"flag=limit"`
	Unexported string     // no command tag, skipped
	Ignored    string     `command:"-"`
	BadTag     string     `command:"unsupported=x"`
	Custom     customText `command:"flag=custom"`
}

func TestBindRejectsUnsupportedSource(t *testing.T) {
	// "unsupported=x" is a tag that produces an unrecognised source — should error.
	inv := Invocation{
		Input: map[string]any{"unsupported": "v"},
	}
	_, err := Bind[bindEdgeCases](inv)
	if err == nil {
		t.Fatal("Bind: want error for unsupported binding source")
	}
}

func TestBindIgnoresDashTag(t *testing.T) {
	type dashOnly struct {
		Skip string `command:"-"`
	}
	got, err := Bind[dashOnly](Invocation{})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got.Skip != "" {
		t.Fatalf("Bind: Skip should be empty, got %q", got.Skip)
	}
}

func TestBindPointerStruct(t *testing.T) {
	type target struct {
		Name string `command:"flag=name"`
	}
	inv := Invocation{Input: map[string]any{"name": "hello"}}
	got, err := Bind[*target](inv)
	if err != nil {
		t.Fatalf("Bind[*target]: %v", err)
	}
	if got == nil || got.Name != "hello" {
		t.Fatalf("Bind[*target]: got %v, want name=hello", got)
	}
}

func TestBindRejectsNonStructPointer(t *testing.T) {
	_, err := Bind[*string](Invocation{})
	if err == nil {
		t.Fatal("Bind[*string]: want error for non-struct pointer")
	}
}

func TestBindRejectsNonStruct(t *testing.T) {
	_, err := Bind[int](Invocation{})
	if err == nil {
		t.Fatal("Bind[int]: want error for non-struct type")
	}
}

func TestBindTextUnmarshaler(t *testing.T) {
	type withCustom struct {
		Custom customText `command:"flag=custom"`
	}
	inv := Invocation{Input: map[string]any{"custom": "my-value"}}
	got, err := Bind[withCustom](inv)
	if err != nil {
		t.Fatalf("Bind[withCustom]: %v", err)
	}
	if got.Custom.val != "my-value" {
		t.Fatalf("Bind[withCustom]: Custom.val = %q, want my-value", got.Custom.val)
	}
}

func TestBindFlagInputNonMapInput(t *testing.T) {
	// flagInput with non-map input returns empty string + false.
	raw, ok := flagInput("not-a-map", "key")
	if ok || raw != "" {
		t.Fatalf("flagInput(non-map): got %q, %v, want '', false", raw, ok)
	}
}

func TestBindFlagInputMissingKey(t *testing.T) {
	raw, ok := flagInput(map[string]any{"other": "v"}, "key")
	if ok || raw != "" {
		t.Fatalf("flagInput(missing): got %q, %v, want '', false", raw, ok)
	}
}

func TestBindFlagInputBoolValue(t *testing.T) {
	raw, ok := flagInput(map[string]any{"flag": true}, "flag")
	if !ok || raw != "true" {
		t.Fatalf("flagInput(bool): got %q, %v, want 'true', true", raw, ok)
	}
}

func TestBindFlagInputDefaultValue(t *testing.T) {
	raw, ok := flagInput(map[string]any{"num": 42}, "num")
	if !ok || raw == "" {
		t.Fatalf("flagInput(int): got %q, %v, want non-empty, true", raw, ok)
	}
}

func TestBindInt64Flag(t *testing.T) {
	type withInt64 struct {
		Limit int64 `command:"flag=limit"`
	}
	inv := Invocation{Input: map[string]any{"limit": "100"}}
	got, err := Bind[withInt64](inv)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got.Limit != 100 {
		t.Fatalf("Limit = %d, want 100", got.Limit)
	}
}

func TestBindInvalidInt64(t *testing.T) {
	type withInt64 struct {
		Limit int64 `command:"flag=limit"`
	}
	inv := Invocation{Input: map[string]any{"limit": "notanumber"}}
	_, err := Bind[withInt64](inv)
	if err == nil {
		t.Fatal("Bind: want error for invalid int64")
	}
}

func TestBindUnsupportedScalarType(t *testing.T) {
	type withFloat struct {
		Rate float64 `command:"flag=rate"`
	}
	inv := Invocation{Input: map[string]any{"rate": "1.5"}}
	_, err := Bind[withFloat](inv)
	if err == nil {
		t.Fatal("Bind: want error for unsupported scalar type float64")
	}
}

func TestParseCommandTagMalformedKey(t *testing.T) {
	// Tag with empty key before '=' should error.
	_, _, _, err := parseCommandTag("=value")
	if err == nil {
		t.Fatal("parseCommandTag('=value'): want error")
	}
}

func TestParseCommandTagMalformedValue(t *testing.T) {
	// Tag with empty value after '=' should error.
	_, _, _, err := parseCommandTag("flag=")
	if err == nil {
		t.Fatal("parseCommandTag('flag='): want error")
	}
}

func TestParseCommandTagEmptyString(t *testing.T) {
	source, name, ok, err := parseCommandTag("")
	if err != nil || ok || source != "" || name != "" {
		t.Fatalf("parseCommandTag(''): got %q,%q,%v,%v", source, name, ok, err)
	}
}

func TestParseCommandTagArgNoEquals(t *testing.T) {
	source, _, ok, err := parseCommandTag("arg")
	if err != nil || !ok || source != "arg" {
		t.Fatalf("parseCommandTag('arg'): got %q,%v,%v", source, ok, err)
	}
}

func TestParseCommandTagUnknownNoEquals(t *testing.T) {
	// Non-"arg" tag without '=' is malformed.
	_, _, _, err := parseCommandTag("flag")
	if err == nil {
		t.Fatal("parseCommandTag('flag'): want error for unknown tag without =")
	}
}
