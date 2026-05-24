package usage

import (
	"github.com/fluxplane/fluxplane-core/core/llm"
	coreusage "github.com/fluxplane/fluxplane-core/core/usage"
)

// EnrichCosts appends estimated cost measurements to usage records using the
// supplied pricing specs. Records without matching pricing are left unchanged.
func EnrichCosts(records []coreusage.Recorded, prices []llm.PricingSpec) []coreusage.Recorded {
	if len(records) == 0 {
		return nil
	}
	out := cloneRecords(records)
	if len(prices) == 0 {
		return out
	}
	for i := range out {
		if hasCostMeasurement(out[i]) {
			continue
		}
		cost := Evaluate([]coreusage.Recorded{out[i]}, prices)
		if cost.Total <= 0 {
			continue
		}
		currency := cost.Currency
		if currency == "" {
			currency = "USD"
		}
		out[i].Measurements = append(out[i].Measurements, coreusage.Measurement{
			Metric:   coreusage.MetricCost,
			Quantity: cost.Total,
			Unit:     coreusage.UnitCurrency,
			Dimensions: map[string]string{
				"currency":  currency,
				"estimated": "true",
			},
		})
	}
	return out
}

func hasCostMeasurement(record coreusage.Recorded) bool {
	for _, measurement := range record.Measurements {
		if measurement.Metric == coreusage.MetricCost {
			return true
		}
	}
	return false
}

func cloneRecords(records []coreusage.Recorded) []coreusage.Recorded {
	out := make([]coreusage.Recorded, len(records))
	for i, record := range records {
		out[i] = coreusage.Recorded{
			Source:       record.Source,
			Subject:      cloneSubject(record.Subject),
			Measurements: cloneMeasurements(record.Measurements),
		}
	}
	return out
}

func cloneSubject(subject coreusage.Subject) coreusage.Subject {
	out := subject
	if subject.Attributes != nil {
		out.Attributes = map[string]string{}
		for key, value := range subject.Attributes {
			out.Attributes[key] = value
		}
	}
	return out
}

func cloneMeasurements(measurements []coreusage.Measurement) []coreusage.Measurement {
	if len(measurements) == 0 {
		return nil
	}
	out := make([]coreusage.Measurement, len(measurements))
	for i, measurement := range measurements {
		out[i] = measurement
		if measurement.Dimensions != nil {
			out[i].Dimensions = map[string]string{}
			for key, value := range measurement.Dimensions {
				out[i].Dimensions[key] = value
			}
		}
	}
	return out
}
