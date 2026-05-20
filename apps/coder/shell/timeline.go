package codershell

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	maxRenderedTimelineEvents = 2500
	maxRenderedTimelineBlocks = 2500
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
	entries  map[timelineCacheKey]string
	markdown map[timelineCacheKey]string
	order    []timelineCacheKey
	content  timelineContentSnapshot
	lines    timelineLineSnapshot
}

type timelineContentSnapshot struct {
	tabID   string
	width   int
	lines   []string
	blocks  [][]string
	content string
}

type timelineLineSnapshot struct {
	tabID       string
	eventCount  int
	lastEventID string
	lines       []string
	agentDelta  string
	thinking    string
}

func newTimelineRenderCache() *timelineRenderCache {
	return &timelineRenderCache{entries: map[timelineCacheKey]string{}, markdown: map[timelineCacheKey]string{}}
}

func (c *timelineRenderCache) render(line string, width int) string {
	if c == nil {
		return renderTimelineLine(line, width)
	}
	key := timelineCacheKey{width: width, line: line}
	if rendered, ok := c.entries[key]; ok {
		return rendered
	}
	rendered := renderTimelineLineWithCache(line, width, c.markdown)
	if len(c.entries) > 4096 {
		c.entries = map[timelineCacheKey]string{}
		c.markdown = map[timelineCacheKey]string{}
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

func (c *timelineRenderCache) linesFor(tabID string, events []TranscriptEvent) []string {
	if c == nil {
		return TimelineLines(events)
	}
	events, hidden := visibleTimelineEvents(events)
	previous := c.lines
	appendOnly := previous.tabID == tabID &&
		previous.eventCount <= len(events) &&
		(previous.eventCount == 0 || events[previous.eventCount-1].ID == previous.lastEventID)
	if !appendOnly {
		previous = timelineLineSnapshot{tabID: tabID}
	}
	lines, agentDelta, thinking := timelineLinesIncremental(previous, events[previous.eventCount:])
	lastID := ""
	if len(events) > 0 {
		lastID = events[len(events)-1].ID
	}
	c.lines = timelineLineSnapshot{
		tabID:       tabID,
		eventCount:  len(events),
		lastEventID: lastID,
		lines:       lines,
		agentDelta:  agentDelta,
		thinking:    thinking,
	}
	out := finalizedTimelineLines(lines, agentDelta, thinking)
	if hidden > 0 {
		prefix := fmt.Sprintf("project: %d older events hidden", hidden)
		out = append([]string{prefix}, out...)
	}
	return out
}

func visibleTimelineEvents(events []TranscriptEvent) ([]TranscriptEvent, int) {
	if len(events) <= maxRenderedTimelineEvents {
		return events, 0
	}
	hidden := len(events) - maxRenderedTimelineEvents
	return events[hidden:], hidden
}

func timelineLinesIncremental(previous timelineLineSnapshot, events []TranscriptEvent) ([]string, string, string) {
	lines := append([]string(nil), previous.lines...)
	agentDelta := previous.agentDelta
	thinking := previous.thinking
	flushAgentDelta := func() {
		if agentDelta == "" {
			return
		}
		lines = append(lines, "agent: "+agentDelta)
		agentDelta = ""
	}
	flushThinking := func() {
		text := strings.TrimSpace(thinking)
		if text == "" {
			thinking = ""
			return
		}
		lines = append(lines, "thinking: "+text)
		thinking = ""
	}
	for _, event := range events {
		summary := event.Summary
		switch event.Kind {
		case EventAskDelta:
			flushThinking()
			agentDelta += summary
			continue
		case EventThinking:
			flushAgentDelta()
			if strings.TrimSpace(summary) != "" && summary != "thinking..." {
				thinking += summary
			}
			continue
		case EventUsageRecorded:
			continue
		}
		flushThinking()
		flushAgentDelta()
		if line, ok := timelineLineForEvent(event, summary); ok {
			lines = append(lines, line)
		}
	}
	return lines, agentDelta, thinking
}

func finalizedTimelineLines(lines []string, agentDelta string, thinking string) []string {
	out := append([]string(nil), lines...)
	if text := strings.TrimSpace(thinking); text != "" {
		out = append(out, "thinking: "+text)
	}
	if agentDelta != "" {
		out = append(out, "agent: "+agentDelta)
	}
	if len(out) > maxRenderedTimelineBlocks {
		hidden := len(out) - maxRenderedTimelineBlocks
		out = append([]string{fmt.Sprintf("project: %d older rendered blocks hidden", hidden)}, out[hidden:]...)
	}
	return out
}

func (m *model) timelineContent(width int) string {
	tab := m.shell.ActiveTab()
	tabID := ""
	lines := []string{"Ready."}
	if tab != nil {
		tabID = tab.ID
		lines = m.timelineCache.linesFor(tabID, tab.Transcript)
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
