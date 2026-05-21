package usage

import (
	"testing"

	"github.com/fluxplane/engine/core/llm"
	coreusage "github.com/fluxplane/engine/core/usage"
)

func TestEnrichCostsAppendsEstimatedCost(t *testing.T) {
	records := []coreusage.Recorded{{
		Subject: coreusage.Subject{Kind: coreusage.SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []coreusage.Measurement{{
			Metric:    coreusage.MetricLLMInputTokens,
			Quantity:  1000,
			Unit:      coreusage.UnitToken,
			Direction: coreusage.DirectionInput,
		}},
	}}
	enriched := EnrichCosts(records, []llm.PricingSpec{{
		Metric:    coreusage.MetricLLMInputTokens,
		Unit:      coreusage.UnitToken,
		Direction: coreusage.DirectionInput,
		Currency:  "USD",
		Price:     2,
		Per:       1000000,
	}})
	if len(enriched[0].Measurements) != 2 {
		t.Fatalf("measurements = %#v, want original plus cost", enriched[0].Measurements)
	}
	cost := enriched[0].Measurements[1]
	if cost.Metric != coreusage.MetricCost || cost.Quantity != 0.002 || cost.Unit != coreusage.UnitCurrency {
		t.Fatalf("cost measurement = %#v, want estimated USD cost", cost)
	}
	if cost.Dimensions["currency"] != "USD" || cost.Dimensions["estimated"] != "true" {
		t.Fatalf("cost dimensions = %#v, want currency and estimated", cost.Dimensions)
	}
}

func TestEnrichCostsLeavesMissingPricingRaw(t *testing.T) {
	records := []coreusage.Recorded{{
		Subject: coreusage.Subject{Kind: coreusage.SubjectNetwork, Provider: "openai", Name: "gpt-test"},
		Measurements: []coreusage.Measurement{{
			Metric:   coreusage.MetricNetworkBytes,
			Quantity: 10,
			Unit:     coreusage.UnitByte,
		}},
	}}
	enriched := EnrichCosts(records, nil)
	if len(enriched) != 1 || len(enriched[0].Measurements) != 1 {
		t.Fatalf("enriched = %#v, want raw record unchanged", enriched)
	}
}

func TestEnrichCostsDoesNotMutateInput(t *testing.T) {
	records := []coreusage.Recorded{{
		Subject: coreusage.Subject{Kind: coreusage.SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []coreusage.Measurement{{
			Metric:     coreusage.MetricCost,
			Quantity:   1,
			Unit:       coreusage.UnitCurrency,
			Dimensions: map[string]string{"currency": "USD"},
		}},
	}}
	enriched := EnrichCosts(records, []llm.PricingSpec{})
	enriched[0].Measurements[0].Dimensions["currency"] = "EUR"
	if records[0].Measurements[0].Dimensions["currency"] != "USD" {
		t.Fatalf("input was mutated")
	}
}
