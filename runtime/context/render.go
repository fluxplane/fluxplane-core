package context

import (
	"encoding/xml"
	"sort"
	"strings"
)

// RenderDiff renders the provider-visible context diff for one placement.
func RenderDiff(result BuildResult, placement Placement) (string, bool) {
	placement = NormalizePlacement(placement)
	var b strings.Builder
	var hasDiff bool
	for _, provider := range result.Providers {
		added := filterBlocks(provider.Added, placement)
		updated := filterBlocks(provider.Updated, placement)
		removed := filterRemoved(provider.Removed, placement)
		if len(added) == 0 && len(updated) == 0 && len(removed) == 0 {
			continue
		}
		if !hasDiff {
			b.WriteString("<system-context>\n")
			hasDiff = true
		}
		writeXMLStart(&b, "provider", map[string]string{"key": string(provider.Provider)}, 1)
		for _, block := range added {
			writeBlockDiff(&b, "added", block, 2)
		}
		for _, block := range updated {
			writeBlockDiff(&b, "updated", block, 2)
		}
		for _, removedBlock := range removed {
			writeRemoved(&b, removedBlock, 2)
		}
		writeXMLEnd(&b, "provider", 1)
	}
	if !hasDiff {
		return "", false
	}
	b.WriteString("</system-context>")
	return b.String(), true
}

func filterBlocks(blocks []Block, placement Placement) []Block {
	out := make([]Block, 0, len(blocks))
	for _, block := range blocks {
		if NormalizePlacement(block.Placement) == placement {
			out = append(out, block)
		}
	}
	return out
}

func filterRemoved(blocks []BlockRemoved, placement Placement) []BlockRemoved {
	out := make([]BlockRemoved, 0, len(blocks))
	for _, block := range blocks {
		if NormalizePlacement(block.Placement) == placement {
			out = append(out, block)
		}
	}
	return out
}

func writeBlockDiff(b *strings.Builder, tag string, block Block, indent int) {
	attrs := map[string]string{
		"block":       block.ID,
		"provider":    string(block.Provider),
		"kind":        string(block.Kind),
		"placement":   string(NormalizePlacement(block.Placement)),
		"sensitivity": string(block.Sensitivity),
		"freshness":   string(block.Freshness),
		"fingerprint": BlockFingerprint(block),
	}
	writeXMLStart(b, tag, attrs, indent)
	if content := strings.TrimSpace(block.Content); content != "" {
		writeXMLText(b, content, indent+1)
	}
	writeXMLEnd(b, tag, indent)
}

func writeRemoved(b *strings.Builder, removed BlockRemoved, indent int) {
	writeXMLStart(b, "removed", map[string]string{
		"block":                removed.ID,
		"provider":             string(removed.Provider),
		"placement":            string(NormalizePlacement(removed.Placement)),
		"previous_fingerprint": removed.PreviousFingerprint,
	}, indent)
	writeXMLEnd(b, "removed", indent)
}

func writeXMLStart(b *strings.Builder, tag string, attrs map[string]string, indent int) {
	b.WriteString(strings.Repeat("  ", indent))
	b.WriteByte('<')
	b.WriteString(tag)
	keys := make([]string, 0, len(attrs))
	for key, value := range attrs {
		if strings.TrimSpace(value) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteByte(' ')
		b.WriteString(key)
		b.WriteString("=\"")
		var escaped strings.Builder
		_ = xml.EscapeText(&escaped, []byte(attrs[key]))
		b.WriteString(escaped.String())
		b.WriteByte('"')
	}
	b.WriteString(">\n")
}

func writeXMLEnd(b *strings.Builder, tag string, indent int) {
	b.WriteString(strings.Repeat("  ", indent))
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteString(">\n")
}

func writeXMLText(b *strings.Builder, text string, indent int) {
	b.WriteString(strings.Repeat("  ", indent))
	var escaped strings.Builder
	_ = xml.EscapeText(&escaped, []byte(text))
	b.WriteString(escaped.String())
	b.WriteByte('\n')
}
