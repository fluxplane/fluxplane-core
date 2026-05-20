package codershell

import (
	"fmt"
	"strconv"
	"strings"
)

type usageTotals struct {
	inputTokens      int64
	cacheWriteTokens int64
	cachedTokens     int64
	outputTokens     int64
	reasoningTokens  int64
	totalTokens      int64
	cost             float64
	currency         string
}

func (u *usageTotals) add(event TranscriptEvent) {
	if u == nil || event.Kind != EventUsageRecorded {
		return
	}
	u.inputTokens += parseIntData(event.Data, "input_tokens")
	u.cacheWriteTokens += parseIntData(event.Data, "cache_write_tokens")
	u.cachedTokens += parseIntData(event.Data, "cached_tokens")
	u.outputTokens += parseIntData(event.Data, "output_tokens")
	u.reasoningTokens += parseIntData(event.Data, "reasoning_tokens")
	u.totalTokens += parseIntData(event.Data, "total_tokens")
	u.cost += parseFloatData(event.Data, "cost")
	if currency := strings.TrimSpace(event.Data["currency"]); currency != "" {
		u.currency = currency
	}
}

func (u usageTotals) summary() string {
	parts := []string{}
	input := u.inputTokens + u.cacheWriteTokens
	if input > 0 {
		parts = append(parts, "in "+formatCompactInt(input))
	}
	if u.cachedTokens > 0 {
		parts = append(parts, "cached "+formatCompactInt(u.cachedTokens))
	}
	if u.outputTokens > 0 {
		parts = append(parts, "out "+formatCompactInt(u.outputTokens))
	}
	if u.reasoningTokens > 0 {
		parts = append(parts, "reason "+formatCompactInt(u.reasoningTokens))
	}
	if u.totalTokens > 0 && len(parts) == 0 {
		parts = append(parts, "total "+formatCompactInt(u.totalTokens))
	}
	if u.cost > 0 {
		parts = append(parts, formatUsageCost(u.cost, u.currency))
	}
	if len(parts) == 0 {
		return "usage --"
	}
	return "usage " + strings.Join(parts, "  ")
}

func usageSummaryFromData(data map[string]string) string {
	var totals usageTotals
	totals.add(TranscriptEvent{Kind: EventUsageRecorded, Data: data})
	return totals.summary()
}

func parseIntData(data map[string]string, key string) int64 {
	value := strings.TrimSpace(data[key])
	if value == "" {
		return 0
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil {
		return int64(parsed + 0.5)
	}
	return 0
}

func parseFloatData(data map[string]string, key string) float64 {
	value := strings.TrimSpace(data[key])
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func formatCompactInt(value int64) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	case value >= 10_000:
		return fmt.Sprintf("%.0fk", float64(value)/1_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func formatUsageCost(cost float64, currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || currency == "USD" {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("%.4f %s", cost, currency)
}
