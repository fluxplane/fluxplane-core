package usage

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/llm"
	coreusage "github.com/fluxplane/fluxplane-core/core/usage"
)

func TestEvaluatePricesMatchingUsageRecords(t *testing.T) {
	got := Evaluate([]coreusage.Recorded{{
		Subject: coreusage.Subject{Kind: coreusage.SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []coreusage.Measurement{{
			Metric:    coreusage.MetricLLMInputTokens,
			Quantity:  2_000,
			Unit:      coreusage.UnitToken,
			Direction: coreusage.DirectionInput,
		}},
	}}, []llm.PricingSpec{{
		Metric:    coreusage.MetricLLMInputTokens,
		Unit:      coreusage.UnitToken,
		Direction: coreusage.DirectionInput,
		Currency:  "USD",
		Price:     1.25,
		Per:       1_000_000,
	}})

	if got.Currency != "USD" {
		t.Fatalf("currency = %q, want USD", got.Currency)
	}
	if got.Total != 0.0025 {
		t.Fatalf("total = %v, want 0.0025", got.Total)
	}
	if len(got.Lines) != 1 {
		t.Fatalf("lines len = %d, want 1", len(got.Lines))
	}
}

func TestEvaluatePricesCacheWriteTokens(t *testing.T) {
	got := Evaluate([]coreusage.Recorded{{
		Subject: coreusage.Subject{Kind: coreusage.SubjectLLM, Provider: "anthropic", Name: "claude-test"},
		Measurements: []coreusage.Measurement{{
			Metric:    coreusage.MetricLLMCacheWriteTokens,
			Quantity:  2_000,
			Unit:      coreusage.UnitToken,
			Direction: coreusage.DirectionWrite,
		}},
	}}, []llm.PricingSpec{{
		Metric:    coreusage.MetricLLMCacheWriteTokens,
		Unit:      coreusage.UnitToken,
		Direction: coreusage.DirectionWrite,
		Currency:  "USD",
		Price:     3.75,
		Per:       1_000_000,
	}})

	if got.Total != 0.0075 {
		t.Fatalf("total = %v, want 0.0075", got.Total)
	}
}
