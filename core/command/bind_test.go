package command

import "testing"

type typedCommandInput struct {
	Goal []string `command:"arg"`
	Max  int      `command:"flag=max"`
	Dry  bool     `command:"flag=dry-run"`
}

func TestBindUsesArgsAndFlags(t *testing.T) {
	inv := Invocation{
		Path: Path{"goal"},
		Args: []string{"foo bar", "baz"},
		Input: map[string]any{
			"max":     "40",
			"dry-run": true,
		},
	}

	got, err := Bind[typedCommandInput](inv)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got.Max != 40 || !got.Dry || len(got.Goal) != 2 || got.Goal[0] != "foo bar" || got.Goal[1] != "baz" {
		t.Fatalf("bound input = %#v, want args and flags", got)
	}
}

func TestBindRejectsInvalidFlagValue(t *testing.T) {
	_, err := Bind[typedCommandInput](Invocation{Input: map[string]any{"max": "nope"}})
	if err == nil {
		t.Fatal("Bind error = nil, want invalid int")
	}
}

func TestBindAppliesDefaultValues(t *testing.T) {
	type withDefaults struct {
		Limit int    `command:"flag=limit,default=10"`
		Name  string `command:"flag=name,default=default-name"`
		Dry   bool   `command:"flag=dry-run,default=true"`
	}
	got, err := Bind[withDefaults](Invocation{})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got.Limit != 10 || got.Name != "default-name" || !got.Dry {
		t.Fatalf("bound defaults = %#v, want limit/name/dry defaults", got)
	}
}

func TestBindExplicitValuesOverrideDefaults(t *testing.T) {
	type withDefaults struct {
		Limit int  `command:"flag=limit,default=10"`
		Dry   bool `command:"flag=dry-run,default=true"`
	}
	got, err := Bind[withDefaults](Invocation{Input: map[string]any{
		"limit":   0,
		"dry-run": false,
	}})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got.Limit != 0 || got.Dry {
		t.Fatalf("bound values = %#v, want explicit zero/false", got)
	}
}

func TestBindDefaultOnlyTag(t *testing.T) {
	type withDefault struct {
		Limit *int `command:"default=10"`
	}
	got, err := Bind[withDefault](Invocation{})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got.Limit == nil || *got.Limit != 10 {
		t.Fatalf("Limit = %#v, want pointer to 10", got.Limit)
	}
}
