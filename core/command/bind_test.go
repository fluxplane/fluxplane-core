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
