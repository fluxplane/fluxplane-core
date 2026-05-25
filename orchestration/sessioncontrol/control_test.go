package sessioncontrol

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateTextPreservesUTF8RuneBoundaries regresses a UTF-8 truncation
// bug: truncateText returned text[:max] regardless of rune boundaries.
// truncateText feeds LLM stop-condition prompts (capped at 2000 / 1000 bytes
// in finalizerDescription / evaluator output), and some tokenizers behave
// poorly on a dangling continuation byte.
func TestTruncateTextPreservesUTF8RuneBoundaries(t *testing.T) {
	// 9 ASCII + 3-byte "€" + filler; cap 10 lands inside the rune.
	value := strings.Repeat("a", 9) + "€" + strings.Repeat("b", 5)
	got := truncateText(value, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateText produced invalid UTF-8: %q", got)
	}
	if got != strings.Repeat("a", 9) {
		t.Fatalf("truncateText = %q, want %d 'a's with the partial rune dropped", got, 9)
	}
}

func TestTruncateTextLeavesShortValues(t *testing.T) {
	if got := truncateText("hello € world", 100); got != "hello € world" {
		t.Fatalf("truncateText short = %q, want unchanged", got)
	}
}

func TestTruncateTextHonorsZeroAndNegativeMax(t *testing.T) {
	for _, max := range []int{0, -1, -100} {
		if got := truncateText("hello", max); got != "hello" {
			t.Fatalf("truncateText with max=%d = %q, want unchanged", max, got)
		}
	}
}
