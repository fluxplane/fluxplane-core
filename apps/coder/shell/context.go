package codershell

import "strings"

// ContextPolicy bounds transcript projection for future ask/model context.
type ContextPolicy struct {
	MaxEvents int
	MaxBytes  int
}

// ContextItem is a bounded model-facing transcript projection item.
type ContextItem struct {
	Kind    TranscriptKind
	Summary string
	Data    map[string]string
}

// ProjectTranscript produces a bounded, redaction-ready transcript projection.
func ProjectTranscript(events []TranscriptEvent, policy ContextPolicy) []ContextItem {
	maxEvents := policy.MaxEvents
	if maxEvents <= 0 || maxEvents > len(events) {
		maxEvents = len(events)
	}
	start := len(events) - maxEvents
	if start < 0 {
		start = 0
	}
	maxBytes := policy.MaxBytes
	out := make([]ContextItem, 0, maxEvents)
	used := 0
	for _, event := range events[start:] {
		summary := strings.TrimSpace(event.Summary)
		if summary == "" {
			continue
		}
		if maxBytes > 0 {
			remaining := maxBytes - used
			if remaining <= 0 {
				break
			}
			if len(summary) > remaining {
				summary = truncateRunes(summary, remaining)
			}
		}
		item := ContextItem{Kind: event.Kind, Summary: summary, Data: cloneStringMap(event.Data)}
		out = append(out, item)
		used += len(summary)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func truncateRunes(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	var b strings.Builder
	for _, r := range value {
		if b.Len()+len(string(r)) > maxBytes {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}
