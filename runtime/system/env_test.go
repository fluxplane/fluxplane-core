package system

import (
	"testing"
)

// TestUnescapeDoubleQuotedEnvPreservesUnknownEscapes regresses a bug where
// the default case in unescapeDoubleQuotedEnv dropped the leading backslash
// for unknown escape sequences. With this bug, `KEY="hi \$world"` parsed to
// "hi $world" instead of the intended "hi \$world" - convention in dotenv
// parsers (and in shell-like quoting) is to keep unknown escapes literal.
func TestUnescapeDoubleQuotedEnvPreservesUnknownEscapes(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"known newline", `hello\nworld`, "hello\nworld"},
		{"known tab", `hello\tworld`, "hello\tworld"},
		{"known carriage return", `hello\rworld`, "hello\rworld"},
		{"escaped quote", `say \"hi\"`, `say "hi"`},
		{"escaped backslash", `path\\name`, `path\name`},
		{"unknown dollar preserved", `hi \$world`, `hi \$world`},
		{"unknown letter preserved", `\x marks the spot`, `\x marks the spot`},
		{"no escapes", `plain text`, `plain text`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := unescapeDoubleQuotedEnv(tc.in)
			if err != nil {
				t.Fatalf("unescapeDoubleQuotedEnv(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("unescapeDoubleQuotedEnv(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnescapeDoubleQuotedEnvRejectsTrailingEscape(t *testing.T) {
	if _, err := unescapeDoubleQuotedEnv(`bad\`); err == nil {
		t.Fatal("expected error for trailing backslash")
	}
}
