package command

import (
	"reflect"
	"testing"
)

func TestParseSlashIgnoresNonSlashText(t *testing.T) {
	invocation, ok, err := ParseSlash("hello /context")
	if err != nil {
		t.Fatalf("ParseSlash: %v", err)
	}
	if ok || invocation.Path.String() != "" {
		t.Fatalf("invocation = %#v ok=%v, want ignored", invocation, ok)
	}
}

func TestParseSlash(t *testing.T) {
	tests := []struct {
		name  string
		input string
		path  Path
		value map[string]any
		args  []string
	}{
		{
			name:  "context",
			input: "/context",
			path:  Path{"context"},
		},
		{
			name:  "boolean flag",
			input: "/context --fresh",
			path:  Path{"context"},
			value: map[string]any{"fresh": true},
		},
		{
			name:  "flag with separated value",
			input: "/context --key docs",
			path:  Path{"context"},
			value: map[string]any{"key": "docs"},
		},
		{
			name:  "flag with equals value",
			input: "/context --key=docs",
			path:  Path{"context"},
			value: map[string]any{"key": "docs"},
		},
		{
			name:  "subcommand",
			input: "/foo bar",
			path:  Path{"foo", "bar"},
		},
		{
			name:  "subcommand with flag value",
			input: "/foo bar --key docs",
			path:  Path{"foo", "bar"},
			value: map[string]any{"key": "docs"},
		},
		{
			name:  "nested subcommand with flag",
			input: "/foo bar baz --dry-run",
			path:  Path{"foo", "bar", "baz"},
			value: map[string]any{"dry-run": true},
		},
		{
			name:  "flag then quoted args",
			input: `/goal --max 40 "foo bar" baz`,
			path:  Path{"goal"},
			value: map[string]any{"max": "40"},
			args:  []string{"foo bar", "baz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invocation, ok, err := ParseSlash(tt.input)
			if err != nil {
				t.Fatalf("ParseSlash: %v", err)
			}
			if !ok {
				t.Fatal("ParseSlash ok = false, want true")
			}
			if !reflect.DeepEqual(invocation.Path, tt.path) {
				t.Fatalf("path = %#v, want %#v", invocation.Path, tt.path)
			}
			if !reflect.DeepEqual(invocation.Args, tt.args) {
				t.Fatalf("args = %#v, want %#v", invocation.Args, tt.args)
			}
			if len(tt.value) == 0 {
				if invocation.Input != nil {
					t.Fatalf("input = %#v, want nil", invocation.Input)
				}
				return
			}
			input, ok := invocation.Input.(map[string]any)
			if !ok {
				t.Fatalf("input = %T, want map", invocation.Input)
			}
			if !reflect.DeepEqual(input, tt.value) {
				t.Fatalf("input = %#v, want %#v", input, tt.value)
			}
		})
	}
}

func TestParseSlashRejectsInvalidInput(t *testing.T) {
	tests := []string{
		"/",
		"//foo",
		"/context --key=",
		`/context "unterminated`,
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, _, err := ParseSlash(input); err == nil {
				t.Fatal("ParseSlash error = nil, want error")
			}
		})
	}
}
