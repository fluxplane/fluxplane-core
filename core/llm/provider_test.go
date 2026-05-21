package llm

import (
	"strings"
	"testing"

	"github.com/fluxplane/engine/core/usage"
)

func TestProviderSpecValidateAcceptsModelCatalog(t *testing.T) {
	spec := ProviderSpec{
		Name: "openai",
		Models: []ModelSpec{{
			Ref:             ModelRef{Name: "gpt-test"},
			ContextTokens:   128000,
			MaxOutputTokens: 4096,
			Capabilities:    CapabilitySet{CapabilityToolCalling, CapabilityStreaming},
			Pricing: []PricingSpec{{
				Metric:    usage.MetricLLMInputTokens,
				Unit:      usage.UnitToken,
				Direction: usage.DirectionInput,
				Currency:  "USD",
				Price:     0.25,
				Per:       1_000_000,
			}},
		}},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestProviderSpecValidateRejectsDuplicateModels(t *testing.T) {
	spec := ProviderSpec{
		Name: "openai",
		Models: []ModelSpec{
			{Ref: ModelRef{Name: "gpt-test"}},
			{Ref: ModelRef{Name: "gpt-test"}},
		},
	}
	err := spec.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate model") {
		t.Fatalf("Validate error = %v, want duplicate model", err)
	}
}
