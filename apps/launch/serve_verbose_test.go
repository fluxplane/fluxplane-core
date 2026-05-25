package launch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestShortLogValuePreservesUTF8RuneBoundaries regresses a bug where
// shortLogValue truncated with text[:limit-3] + "..." regardless of UTF-8
// rune boundaries. With non-ASCII content where the cut falls inside a
// multi-byte rune, the function emitted invalid UTF-8 followed by "...",
// corrupting verbose serve-event log lines.
func TestShortLogValuePreservesUTF8RuneBoundaries(t *testing.T) {
	// Build a string whose byte cut at limit-3 (117) lands inside a 3-byte rune.
	// 116 ASCII + 3-byte "€" + filler; byte 117 is the second continuation byte.
	value := strings.Repeat("a", 116) + "€" + strings.Repeat("b", 30)
	got := shortLogValue(value)
	if !utf8.ValidString(got) {
		t.Fatalf("shortLogValue produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("shortLogValue = %q, want trailing '...'", got)
	}
	// The partial "€" must be dropped; result is 116 'a's + "...".
	if got != strings.Repeat("a", 116)+"..." {
		t.Fatalf("shortLogValue = %q, want %d 'a's + '...'", got, 116)
	}
}

func TestShortLogValueLeavesShortStrings(t *testing.T) {
	if got := shortLogValue("hello € world"); got != "hello € world" {
		t.Fatalf("shortLogValue short = %q, want unchanged", got)
	}
}
