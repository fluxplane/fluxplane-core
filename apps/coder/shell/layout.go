package codershell

import "charm.land/lipgloss/v2"

type shellLayout struct {
	contentWidth        int
	timelineOuterHeight int
	timelineInnerWidth  int
	timelineInnerHeight int
	timelinePlain       bool
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
	fixedHeight := lipgloss.Height(header) + lipgloss.Height(status) + lipgloss.Height(prompt)
	if m.mention.Open {
		fixedHeight += lipgloss.Height(m.renderMentionPicker(contentWidth))
	}
	plainTimeline := m.shellTimelinePlain()

	available := height - fixedHeight
	if available < 3 {
		available = 3
	}
	innerHeight := available - 2
	if plainTimeline {
		innerHeight = available
	}
	if innerHeight < 1 {
		innerHeight = 1
	}
	innerWidth := contentWidth - 4
	if plainTimeline {
		innerWidth = contentWidth
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return shellLayout{
		contentWidth:        contentWidth,
		timelineOuterHeight: available,
		timelineInnerWidth:  innerWidth,
		timelineInnerHeight: innerHeight,
		timelinePlain:       plainTimeline,
	}
}

func (m model) shellTimelinePlain() bool {
	if m.shell == nil {
		return false
	}
	tab := m.shell.ActiveTab()
	return tab != nil && tab.InputMode == InputModeShell
}
