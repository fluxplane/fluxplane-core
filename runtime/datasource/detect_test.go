package datasource

import (
	"context"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
)

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
