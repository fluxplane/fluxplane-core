package codershell

import "charm.land/lipgloss/v2"

type shellLayout struct {
	contentWidth        int
	timelineOuterHeight int
	timelineInnerWidth  int
	timelineInnerHeight int
	header              string
	status              string
	mention             string
	prompt              string
}

func (m model) layout() shellLayout {
	width := m.width
	if width <= 0 {
		width = 96
	}
	contentWidth := width
	if contentWidth < 40 {
		contentWidth = width
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	header := m.renderHeader(contentWidth)
	status := m.renderStatus(contentWidth)
	prompt := m.renderPrompt(contentWidth)
	mention := ""
	if m.mention.Open {
		mention = m.renderMentionPicker(contentWidth)
	}
	fixedHeight := lipgloss.Height(header) + lipgloss.Height(status) + lipgloss.Height(prompt)
	if m.mention.Open {
		fixedHeight += lipgloss.Height(mention)
	}
	available := height - fixedHeight
	if available < 3 {
		available = 3
	}
	innerHeight := available
	if innerHeight < 1 {
		innerHeight = 1
	}
	innerWidth := contentWidth
	if innerWidth < 1 {
		innerWidth = 1
	}
	return shellLayout{
		contentWidth:        contentWidth,
		timelineOuterHeight: available,
		timelineInnerWidth:  innerWidth,
		timelineInnerHeight: innerHeight,
		header:              header,
		status:              status,
		mention:             mention,
		prompt:              prompt,
	}
}
