package usage

import (
	"github.com/fluxplane/engine/core/llm"
	coreusage "github.com/fluxplane/engine/core/usage"
)

// Cost is a cost evaluation result for one or more usage records.
type Cost struct {
	Currency string     `json:"currency,omitempty"`
	Total    float64    `json:"total,omitempty"`
	Lines    []CostLine `json:"lines,omitempty"`
}

// CostLine is one priced usage measurement.
type CostLine struct {
	Subject     coreusage.Subject     `json:"subject"`
	Measurement coreusage.Measurement `json:"measurement"`
	Pricing     llm.PricingSpec       `json:"pricing"`
	Amount      float64               `json:"amount"`
}

// Evaluate prices usage records using the supplied pricing specs. Missing
// prices are ignored; budget policy can decide later whether that is allowed.
func Evaluate(records []coreusage.Recorded, prices []llm.PricingSpec) Cost {
	index := pricingIndex(prices)
	out := Cost{}
	for _, record := range records {
		for _, measurement := range record.Measurements {
			price, ok := index[pricingKey{
				metric:    measurement.Metric,
				unit:      measurement.Unit,
				direction: measurement.Direction,
			}]
			if !ok {
				price, ok = index[pricingKey{metric: measurement.Metric, unit: measurement.Unit}]
			}
			if !ok || price.Per <= 0 {
				continue
			}
			amount := measurement.Quantity / price.Per * price.Price
			if out.Currency == "" {
				out.Currency = price.Currency
			}
			out.Total += amount
			out.Lines = append(out.Lines, CostLine{
				Subject:     record.Subject,
				Measurement: measurement,
				Pricing:     price,
				Amount:      amount,
			})
		}
	}
	return out
}

type pricingKey struct {
	metric    coreusage.MetricName
	unit      coreusage.Unit
	direction coreusage.Direction
}

func pricingIndex(prices []llm.PricingSpec) map[pricingKey]llm.PricingSpec {
	out := make(map[pricingKey]llm.PricingSpec, len(prices))
	for _, price := range prices {
		out[pricingKey{
			metric:    price.Metric,
			unit:      price.Unit,
			direction: price.Direction,
		}] = price
	}
	return out
}
