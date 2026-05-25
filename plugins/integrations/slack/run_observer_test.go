package slack

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCompactTextPreservesUTF8RuneBoundaries regresses a bug where compactText
// truncated at byte `max` regardless of UTF-8 rune boundaries. If a multi-byte
// rune straddled the cut, the result was an invalid UTF-8 string with a
// dangling continuation byte followed by "..." - Slack's API renders that as
// replacement characters in the message body.
func TestCompactTextPreservesUTF8RuneBoundaries(t *testing.T) {
	// 9 ASCII + one 3-byte rune ("€" = 0xe2 0x82 0xac) so byte 10 falls inside it.
	value := strings.Repeat("a", 9) + "€" + strings.Repeat("b", 20)
	got := compactText(value, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("compactText produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("compactText = %q, want suffix '...'", got)
	}
	// The partial "€" must be dropped; result should be 9 'a's + "..."
	if got != strings.Repeat("a", 9)+"..." {
		t.Fatalf("compactText = %q, want %q", got, strings.Repeat("a", 9)+"...")
	}
}

func TestCompactTextLeavesShortValidStrings(t *testing.T) {
	if got := compactText("hello € world", 100); got != "hello € world" {
		t.Fatalf("compactText short = %q, want unchanged", got)
	}
}
