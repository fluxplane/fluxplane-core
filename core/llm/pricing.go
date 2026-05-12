package llm

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/usage"
)

// PricingSpec describes how one usage metric is priced. It is catalog data,
// not an evaluator.
type PricingSpec struct {
	Metric       usage.MetricName `json:"metric"`
	Unit         usage.Unit       `json:"unit"`
	Direction    usage.Direction  `json:"direction,omitempty"`
	Currency     string           `json:"currency"`
	Price        float64          `json:"price"`
	Per          float64          `json:"per"`
	EffectiveUTC string           `json:"effective_utc,omitempty"`
}

// Validate checks that a pricing spec can be interpreted by a cost evaluator.
func (s PricingSpec) Validate() error {
	if strings.TrimSpace(string(s.Metric)) == "" {
		return fmt.Errorf("metric is empty")
	}
	if strings.TrimSpace(string(s.Unit)) == "" {
		return fmt.Errorf("unit is empty")
	}
	if strings.TrimSpace(s.Currency) == "" {
		return fmt.Errorf("currency is empty")
	}
	if s.Per <= 0 {
		return fmt.Errorf("per must be > 0")
	}
	if s.Price < 0 {
		return fmt.Errorf("price must be >= 0")
	}
	return nil
}
