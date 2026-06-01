package datasource

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	coredatasource "github.com/fluxplane/fluxplane-datasource"
)

// TestTruncateBytesPreservesUTF8RuneBoundaries regresses a bug in
// truncateBytes where the function returned value[:max] regardless of UTF-8
// rune boundaries. Source text passed to the regex detector could end with
// a dangling continuation byte; the matched substring it produced ended up
// as invalid UTF-8 in RecordRef.SourceText.
func TestTruncateBytesPreservesUTF8RuneBoundaries(t *testing.T) {
	// 9 ASCII bytes + one 3-byte rune ("€") so byte 10 falls inside the rune.
	value := strings.Repeat("a", 9) + "€" + strings.Repeat("b", 5)
	got := truncateBytes(value, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateBytes produced invalid UTF-8: %q", got)
	}
	if got != strings.Repeat("a", 9) {
		t.Fatalf("truncateBytes = %q, want %d 'a's with the partial rune dropped", got, 9)
	}
}

func TestTruncateBytesLeavesShortValues(t *testing.T) {
	if got := truncateBytes("hello € world", 100); got != "hello € world" {
		t.Fatalf("truncateBytes short = %q, want unchanged", got)
	}
}

type detectAccessor struct {
	spec   coredatasource.Spec
	entity coredatasource.EntitySpec
}

func (a detectAccessor) Spec() coredatasource.Spec { return a.spec }
func (a detectAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}

func TestDetectRegexTemplateDedupeAndCap(t *testing.T) {
	refs := Detect(context.Background(), coredatasource.DetectionInput{
		MaxRefs: 1,
		Sources: []coredatasource.DetectionSource{{
			Kind: "channel.message",
			Text: "See DEV-381 and DEV-381",
		}},
	}, []coredatasource.Accessor{detectAccessor{
		spec: coredatasource.Spec{Name: "issues", Kind: "memory", Entities: []coredatasource.EntityType{"issue"}},
		entity: coredatasource.EntitySpec{
			Type: "issue",
			Detectors: []coredatasource.DetectorSpec{{
				Name:       "issue_key",
				Kind:       coredatasource.DetectorRegex,
				Pattern:    `\b([A-Z]+-\d+)\b`,
				IDTemplate: "$1",
				Confidence: 0.9,
			}},
		},
	}}, DetectOptions{MaxRefs: 20})
	if len(refs) != 1 {
		t.Fatalf("refs = %#v, want one", refs)
	}
	if refs[0].Datasource != "issues" || refs[0].Entity != "issue" || refs[0].ID != "DEV-381" || refs[0].Confidence != 0.9 {
		t.Fatalf("ref = %#v, want normalized issue ref", refs[0])
	}
}

func TestDetectURLAndStructuredMetadata(t *testing.T) {
	accessor := detectAccessor{
		spec: coredatasource.Spec{Name: "links", Kind: "memory", Entities: []coredatasource.EntityType{"link"}},
		entity: coredatasource.EntitySpec{
			Type: "link",
			Detectors: []coredatasource.DetectorSpec{
				{
					Name:          "link_url",
					Kind:          coredatasource.DetectorURL,
					Pattern:       `https?://example.test/items/([a-z0-9-]+)`,
					IDTemplate:    "$1",
					QueryTemplate: "$1",
					URLTemplate:   "$0",
				},
				{
					Name:       "link_metadata",
					Kind:       coredatasource.DetectorStructured,
					Pattern:    "item_id",
					IDTemplate: "$0",
				},
			},
		},
	}
	refs := Detect(context.Background(), coredatasource.DetectionInput{
		Sources: []coredatasource.DetectionSource{
			{Kind: "text", Text: "Open https://example.test/items/alpha-1"},
			{Kind: "metadata", Metadata: map[string]string{"item_id": "beta-2"}},
		},
	}, []coredatasource.Accessor{accessor}, DetectOptions{})
	if len(refs) != 2 {
		t.Fatalf("refs = %#v, want two", refs)
	}
	if refs[0].ID != "alpha-1" || refs[0].URL == "" || refs[1].ID != "beta-2" {
		t.Fatalf("refs = %#v, want URL and structured refs", refs)
	}
}
