package codershell

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func (m *model) syncTimelineViewport(follow bool) {
	if m == nil {
		return
	}
	layout := m.layout()
	pinned := follow || m.timelinePinned || m.timeline.AtBottom() || m.timeline.TotalLineCount() == 0
	m.timeline.SetWidth(layout.timelineInnerWidth)
	m.timeline.SetHeight(layout.timelineInnerHeight)
	m.timeline.SetContent(m.timelineContent(layout.timelineInnerWidth))
	if pinned {
		m.timeline.GotoBottom()
	}
	m.timelinePinned = m.timeline.AtBottom()
}

type timelineCacheKey struct {
	width int
	line  string
}

type timelineRenderCache struct {
	entries map[timelineCacheKey]string
	order   []timelineCacheKey
	content timelineContentSnapshot
}

type timelineContentSnapshot struct {
	tabID   string
	width   int
	lines   []string
	blocks  [][]string
	content string
}

func newTimelineRenderCache() *timelineRenderCache {
	return &timelineRenderCache{entries: map[timelineCacheKey]string{}}
}

func (c *timelineRenderCache) render(line string, width int) string {
	if c == nil {
		return renderTimelineLine(line, width)
	}
	key := timelineCacheKey{width: width, line: line}
	if rendered, ok := c.entries[key]; ok {
		return rendered
	}
	rendered := renderTimelineLine(line, width)
	if len(c.entries) > 4096 {
		c.entries = map[timelineCacheKey]string{}
		c.order = c.order[:0]
	}
	c.entries[key] = rendered
	c.order = append(c.order, key)
	return rendered
}

func (c *timelineRenderCache) contentFor(tabID string, width int, lines []string) string {
	if c == nil {
		return renderTimelineLines(nil, width, lines)
	}
	previous := c.content
	if previous.tabID == tabID && previous.width == width && stringSlicesEqual(previous.lines, lines) {
		return previous.content
	}
	prefix := 0
	if previous.tabID == tabID && previous.width == width {
		limit := len(previous.lines)
		if len(lines) < limit {
			limit = len(lines)
		}
		for prefix < limit && previous.lines[prefix] == lines[prefix] {
			prefix++
		}
	}
	blocks := make([][]string, 0, len(lines))
	if prefix > 0 {
		blocks = append(blocks, previous.blocks[:prefix]...)
	}
	blocks = append(blocks, renderTimelineBlocks(c, width, lines[prefix:])...)
	rendered := flattenTimelineBlocks(blocks)
	content := lipgloss.JoinVertical(lipgloss.Left, rendered...)
	c.content = timelineContentSnapshot{
		tabID:   tabID,
		width:   width,
		lines:   append([]string(nil), lines...),
		blocks:  blocks,
		content: content,
	}
	return content
}

func (m *model) timelineContent(width int) string {
	tab := m.shell.ActiveTab()
	tabID := ""
	lines := []string{"Ready."}
	if tab != nil {
		tabID = tab.ID
		lines = TimelineLines(tab.Transcript)
	}
	if len(lines) == 0 {
		lines = []string{"Ready."}
	}
	return m.timelineCache.contentFor(tabID, width, lines)
}

func renderTimelineLines(cache *timelineRenderCache, width int, lines []string) string {
	return lipgloss.JoinVertical(lipgloss.Left, flattenTimelineBlocks(renderTimelineBlocks(cache, width, lines))...)
}

func renderTimelineBlocks(cache *timelineRenderCache, width int, lines []string) [][]string {
	rendered := make([][]string, 0, len(lines))
	wrap := lipgloss.NewStyle().Width(width)
	for _, line := range lines {
		wrapped := wrap.Render(cache.render(line, width))
		rendered = append(rendered, strings.Split(wrapped, "\n"))
	}
	return rendered
}

func flattenTimelineBlocks(blocks [][]string) []string {
	total := 0
	for _, block := range blocks {
		total += len(block)
	}
	out := make([]string, 0, total)
	for _, block := range blocks {
		out = append(out, block...)
	}
	return out
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
