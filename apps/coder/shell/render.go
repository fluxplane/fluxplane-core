package codershell

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	legacytea "github.com/charmbracelet/bubbletea"
	mdbubble "github.com/codewandler/markdown/bubbleview"
	"github.com/codewandler/markdown/stream"
	mdterminal "github.com/codewandler/markdown/terminal"
)

var (
	pageStyle = lipgloss.NewStyle()

	monokaiForeground = lipgloss.Color("#F8F8F2")
	monokaiBackground = lipgloss.Color("#272822")
	monokaiSurface    = lipgloss.Color("#3E3D32")
	monokaiComment    = lipgloss.Color("#75715E")
	monokaiMuted      = lipgloss.Color("#A59F85")
	monokaiPink       = lipgloss.Color("#F92672")
	monokaiGreen      = lipgloss.Color("#A6E22E")
	monokaiYellow     = lipgloss.Color("#E6DB74")
	monokaiOrange     = lipgloss.Color("#FD971F")
	monokaiPurple     = lipgloss.Color("#AE81FF")
	monokaiCyan       = lipgloss.Color("#66D9EF")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(monokaiBackground).
			Background(monokaiPink).
			Padding(0, 1)

	subtleStyle = lipgloss.NewStyle().Foreground(monokaiMuted)
	mutedStyle  = lipgloss.NewStyle().Foreground(monokaiComment)
	valueStyle  = lipgloss.NewStyle().Foreground(monokaiForeground)
	accentStyle = lipgloss.NewStyle().Foreground(monokaiPurple).Bold(true)
	askStyle    = lipgloss.NewStyle().Foreground(monokaiCyan).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(monokaiGreen)
	warnStyle   = lipgloss.NewStyle().Foreground(monokaiOrange)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(monokaiSurface).
			Padding(0, 1)

	chipStyle = lipgloss.NewStyle().
			Foreground(monokaiYellow).
			Padding(0, 1)

	promptBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(monokaiSurface).
			Padding(0, 1)
	promptStyle = lipgloss.NewStyle().Foreground(monokaiGreen).Bold(true)
	inputStyle  = lipgloss.NewStyle().Foreground(monokaiForeground)
	cursorStyle = lipgloss.NewStyle().Foreground(monokaiBackground).Background(monokaiYellow)
	footerStyle = lipgloss.NewStyle().Foreground(monokaiMuted).Padding(0, 1)
)

func (m model) View() tea.View {
	layout := m.layout()

	parts := []string{
		m.renderHeader(layout.contentWidth),
		m.renderStatus(layout.contentWidth),
		m.renderTimeline(layout),
	}
	if m.mention.Open {
		parts = append(parts, m.renderMentionPicker(layout.contentWidth))
	}
	parts = append(parts, m.renderPrompt(layout.contentWidth))
	view := tea.NewView(pageStyle.Render(lipgloss.JoinVertical(lipgloss.Left, parts...)))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m model) renderHeader(width int) string {
	project := m.status.projectName
	if project == "" {
		project = "workspace"
	}
	title := titleStyle.Render(" coder shell ")
	rightParts := []string{}
	if model := m.modelSummary(); model != "" {
		rightParts = append(rightParts, metaItem("model", model))
	}
	if m.shell != nil {
		if connection := strings.TrimSpace(m.shell.connection); connection != "" {
			rightParts = append(rightParts, metaItem("conn", connection))
		}
	}
	if tab := m.tabSummary(); tab != "" {
		rightParts = append(rightParts, metaItem("tab", tab))
	}
	lines := []string{alignLine(title, fitRightParts(width-lipgloss.Width(title)-2, rightParts), width)}

	projectParts := []string{accentStyle.Render(truncateStyled(project, projectWidth(width)))}
	if module := strings.TrimSpace(m.status.goModule); module != "" && module != project && width >= 72 {
		if moduleWidth := moduleWidth(width, project); moduleWidth > 0 {
			projectParts = append(projectParts, mutedStyle.Render(truncateStyled(module, moduleWidth)))
		}
	}
	lines = append(lines, fitParts(width, projectParts, mutedStyle.Render("  ")))

	if locationLine := m.renderLocationLine(width); locationLine != "" {
		lines = append(lines, locationLine)
	}
	return lipgloss.NewStyle().Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func (m model) renderStatus(width int) string {
	facts := m.observedFacts()
	if len(facts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(facts))
	for _, fact := range facts {
		parts = append(parts, fact.Render())
	}
	line := fitParts(width, parts, subtleStyle.Render("  "))
	return lipgloss.NewStyle().Width(width).Render(line)
}

func (m model) renderLocationLine(width int) string {
	activeCWD := strings.TrimSpace(m.status.cwd)
	if m.shell != nil {
		if active := m.shell.ActiveTab(); active != nil && strings.TrimSpace(active.CWD) != "" {
			activeCWD = active.CWD
		}
	}
	left := mutedStyle.Render(shortPath(activeCWD))
	if width >= 96 && strings.TrimSpace(activeCWD) != "" {
		left = mutedStyle.Render(activeCWD)
	}
	right := ""
	if m.shell != nil {
		if active := m.shell.ActiveTab(); active != nil {
			right = metaItem("session", active.ID)
		}
	}
	return alignLine(left, right, width)
}

func (m model) modelSummary() string {
	provider := strings.TrimSpace(m.status.provider)
	model := strings.TrimSpace(m.status.model)
	switch {
	case provider != "" && model != "":
		return provider + "/" + model
	case model != "":
		return model
	case provider != "":
		return provider
	default:
		return ""
	}
}

func (m model) tabSummary() string {
	if m.shell == nil || len(m.shell.tabs) == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d", m.shell.ActiveIndex()+1, len(m.shell.tabs))
}

type observedFact struct {
	Label string
	Value string
	Style lipgloss.Style
}

func (f observedFact) Render() string {
	value := strings.TrimSpace(f.Value)
	if value == "" {
		return ""
	}
	if strings.TrimSpace(f.Label) == "" {
		return f.Style.Render(value)
	}
	return subtleStyle.Render(f.Label) + " " + f.Style.Render(value)
}

func (m model) observedFacts() []observedFact {
	project := strings.TrimSpace(m.status.projectName)
	if project == "" {
		project = workspaceLabel(m.status.workspace, m.status.cwd)
	}
	facts := []observedFact{}
	if m.status.loading {
		facts = append(facts, observedFact{Value: "loading workspace...", Style: subtleStyle})
	}
	if len(m.activeRuns) > 0 {
		facts = append(facts, observedFact{Value: activeRunSummary(m.activeRuns), Style: accentStyle})
	}
	if len(m.activeOps) > 0 {
		facts = append(facts, observedFact{Label: "ops", Value: fmt.Sprintf("%d running", len(m.activeOps)), Style: valueStyle})
	}
	facts = append(facts, observedFact{Label: "workspace", Value: project, Style: valueStyle})
	if kind := strings.TrimSpace(m.status.projectKind); kind != "" {
		facts = append(facts, observedFact{Label: "kind", Value: kind, Style: valueStyle})
	}
	for _, facet := range m.status.facets {
		facts = append(facts, observedFact{Value: facet, Style: chipStyle})
	}
	if m.status.warningCount > 0 {
		facts = append(facts, observedFact{Value: fmt.Sprintf("%d warnings", m.status.warningCount), Style: warnStyle})
	} else if m.status.projectCount > 0 || len(m.status.facets) > 0 {
		facts = append(facts, observedFact{Value: "inventory ok", Style: okStyle})
	}
	if version := displayGoVersion(m.status.goVersion); version != "" {
		facts = append(facts, observedFact{Label: "go", Value: version, Style: valueStyle})
	}
	if locale := compactLocale(m.status.locale); locale != "" {
		facts = append(facts, observedFact{Label: "locale", Value: locale, Style: valueStyle})
	}
	if user := compactUser(m.status.user); user != "" {
		facts = append(facts, observedFact{Label: "user", Value: user, Style: valueStyle})
	}
	if m.status.taskCount > 0 {
		facts = append(facts, observedFact{Label: "tasks", Value: fmt.Sprintf("%d", m.status.taskCount), Style: valueStyle})
	}
	if m.status.projectCount > 1 {
		facts = append(facts, observedFact{Label: "projects", Value: fmt.Sprintf("%d", m.status.projectCount), Style: valueStyle})
	}
	return facts
}

func activeRunSummary(active map[string]string) string {
	for _, summary := range active {
		if strings.TrimSpace(summary) != "" {
			return summary
		}
	}
	if len(active) == 1 {
		return "running"
	}
	return fmt.Sprintf("%d runs", len(active))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func workspaceLabel(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		base := filepath.Base(value)
		if base != "." && base != string(filepath.Separator) && base != "" {
			return base
		}
	}
	return "workspace"
}

func displayGoVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "go version ")
	version = strings.TrimPrefix(version, "go")
	version = strings.TrimSpace(version)
	if version == "n/a" {
		return ""
	}
	return version
}

func compactLocale(locale string) string {
	locale = strings.TrimSpace(locale)
	if index := strings.Index(locale, "."); index > 0 {
		locale = locale[:index]
	}
	return locale
}

func compactUser(user string) string {
	user = strings.TrimSpace(user)
	if index := strings.LastIndex(user, string(filepath.Separator)); index >= 0 && index+1 < len(user) {
		user = user[index+1:]
	}
	if index := strings.LastIndex(user, "\\"); index >= 0 && index+1 < len(user) {
		user = user[index+1:]
	}
	return user
}

func metaItem(key, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return subtleStyle.Render(key) + " " + valueStyle.Render(value)
}

func fitRightParts(width int, parts []string) string {
	if width <= 0 || len(parts) == 0 {
		return ""
	}
	for count := len(parts); count > 0; count-- {
		candidate := strings.Join(parts[:count], "  ")
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return ""
}

func projectWidth(width int) int {
	if width < 44 {
		return width
	}
	return width / 2
}

func moduleWidth(width int, project string) int {
	remaining := width - projectWidth(width) - 2
	if remaining < 20 && strings.TrimSpace(project) != "" {
		return 0
	}
	return remaining
}

func alignLine(left, right string, width int) string {
	if width <= 0 {
		return left
	}
	left = strings.TrimRight(left, " ")
	right = strings.TrimSpace(right)
	if right == "" {
		return truncateStyled(left, width)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncateStyled(left+" "+right, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

func fitParts(width int, parts []string, sep string) string {
	if width <= 0 {
		return strings.Join(parts, sep)
	}
	line := ""
	hidden := 0
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		next := part
		if line != "" {
			next = line + sep + part
		}
		if lipgloss.Width(next) <= width {
			line = next
			continue
		}
		hidden++
	}
	if hidden == 0 {
		return line
	}
	more := mutedStyle.Render(fmt.Sprintf("+%d", hidden))
	if line == "" {
		return truncateStyled(more, width)
	}
	next := line + sep + more
	if lipgloss.Width(next) <= width {
		return next
	}
	return truncateStyled(line, width)
}

func (m model) renderMentionPicker(width int) string {
	if !m.mention.Open {
		return ""
	}
	header := "@ resources"
	switch m.mention.Kind {
	case completionCommand:
		header = "/ commands"
	case completionOption:
		header = "/ options " + m.mention.CommandPath
	}
	lines := []string{subtleStyle.Render(header) + " " + valueStyle.Render(m.mention.Query)}
	if len(m.mention.Results) == 0 {
		lines = append(lines, mutedStyle.Render("no results"))
	}
	for i, result := range m.mention.Results {
		prefix := "  "
		style := valueStyle
		if i == m.mention.Index {
			prefix = "❯ "
			style = accentStyle
		}
		icon := strings.TrimSpace(result.Icon)
		if icon != "" {
			icon += " "
		}
		detail := strings.TrimSpace(result.Detail)
		if detail != "" {
			detail = subtleStyle.Render("  " + detail)
		}
		kindPrefix := subtleStyle.Render("[" + string(result.Kind) + "] ")
		if m.mention.Kind == completionCommand || m.mention.Kind == completionOption {
			kindPrefix = ""
		}
		lines = append(lines, prefix+kindPrefix+style.Render(icon+result.Label)+detail)
	}
	return panelStyle.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func (m model) renderTimeline(layout shellLayout) string {
	timeline := m.timeline
	timeline.SetWidth(layout.timelineInnerWidth)
	timeline.SetHeight(layout.timelineInnerHeight)
	if m.timelinePinned || timeline.TotalLineCount() <= timeline.Height() {
		timeline.GotoBottom()
	}
	if layout.timelinePlain {
		return lipgloss.NewStyle().Width(layout.contentWidth).Height(layout.timelineOuterHeight).Render(timeline.View())
	}
	return panelStyle.Width(layout.contentWidth).Height(layout.timelineOuterHeight - 2).Render(timeline.View())
}

func renderTimelineLine(line string, width int) string {
	switch {
	case strings.HasPrefix(line, "? "):
		return askStyle.Render("? ") + valueStyle.Render(strings.TrimPrefix(line, "? "))
	case strings.HasPrefix(line, "> "):
		return promptStyle.Render("> ") + valueStyle.Render(strings.TrimPrefix(line, "> "))
	case strings.HasPrefix(line, "$ "):
		return promptStyle.Render("$ ") + valueStyle.Render(strings.TrimPrefix(line, "$ "))
	case strings.HasPrefix(line, "out:"):
		return subtleStyle.Render("↳ ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "out:")))
	case strings.HasPrefix(line, "exit:"):
		summary := strings.TrimSpace(strings.TrimPrefix(line, "exit:"))
		if summary == "0" || summary == "ok" {
			return okStyle.Render("✓ ") + valueStyle.Render(summary)
		}
		return warnStyle.Render("✗ ") + valueStyle.Render(summary)
	case strings.HasPrefix(line, "agent:"):
		return renderTimelineMarkdown("🤖 ", accentStyle, strings.TrimSpace(strings.TrimPrefix(line, "agent:")), width)
	case strings.HasPrefix(line, "thinking:"):
		return renderTimelineMarkdown("… ", subtleStyle, strings.TrimSpace(strings.TrimPrefix(line, "thinking:")), width)
	case strings.HasPrefix(line, "op:"):
		return accentStyle.Render("● ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "op:")))
	case strings.HasPrefix(line, "op-done:"):
		return okStyle.Render("✓ ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "op-done:")))
	case strings.HasPrefix(line, "proc:"):
		return subtleStyle.Render("process ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "proc:")))
	case strings.HasPrefix(line, "proc-out:"):
		return subtleStyle.Render("│ ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "proc-out:")))
	case strings.HasPrefix(line, "raw:"):
		return valueStyle.Render(strings.TrimPrefix(line, "raw: "))
	case strings.HasPrefix(line, "proc-exit:"):
		return subtleStyle.Render("exit ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "proc-exit:")))
	case strings.HasPrefix(line, "mention:"):
		return accentStyle.Render("@ ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "mention:")))
	case strings.HasPrefix(line, "slash:"):
		return promptStyle.Render("/ ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "slash:")))
	case strings.HasPrefix(line, "error:"):
		return warnStyle.Render("! ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "error:")))
	case strings.HasPrefix(line, "echo:"):
		return subtleStyle.Render("↳ ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "echo:")))
	case strings.HasPrefix(line, "project:"):
		return subtleStyle.Render("◆ ") + accentStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "project:")))
	case line == "":
		return ""
	default:
		return subtleStyle.Render("· ") + valueStyle.Render(line)
	}
}

func renderTimelineMarkdown(marker string, markerStyle lipgloss.Style, text string, width int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return markerStyle.Render(strings.TrimSpace(marker))
	}
	contentWidth := width - 3
	if contentWidth < 20 {
		contentWidth = width
	}
	rendered := renderMarkdownBubbleView(text, contentWidth)
	if strings.TrimSpace(rendered) == "" {
		rendered = text
	}
	lines := strings.Split(rendered, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return accentStyle.Render("🤖")
	}
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		prefix := "  "
		if i == 0 {
			prefix = markerStyle.Render(marker)
		}
		out = append(out, prefix+line)
	}
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}

func renderMarkdownBubbleView(text string, width int) string {
	if width <= 0 {
		width = 80
	}
	height := strings.Count(text, "\n") + 16
	if height < 24 {
		height = 24
	}
	if height > 240 {
		height = 240
	}
	model := mdbubble.NewStreamModel(
		mdbubble.WithWrapWidth(width),
		mdbubble.WithAnsi(mdterminal.AnsiOn),
		mdbubble.WithTheme(mdterminal.MonokaiTheme()),
		mdbubble.WithParserOptions(stream.WithGFMAutolinks()),
	)
	updated, cmd := model.Update(legacytea.WindowSizeMsg{Width: width, Height: height})
	if next, ok := updated.(mdbubble.StreamModel); ok {
		model = next
	}
	if cmd != nil {
		_ = cmd()
	}
	if cmd := model.Write([]byte(text)); cmd != nil {
		_ = cmd()
	}
	if cmd := model.Flush(); cmd != nil {
		_ = cmd()
	}
	return strings.TrimRight(model.View(), " \n")
}

func (m model) renderPrompt(width int) string {
	tab := m.shell.ActiveTab()
	input := ""
	marker := "❯ "
	if tab != nil {
		if tab.InputMode == InputModeAsk {
			marker = "🤖 "
		}
		input = renderInputWithCursor(tab)
	}
	prompt := promptStyle.Render(marker) + input
	box := promptBoxStyle.Width(width).Render(prompt)
	footer := footerStyle.Width(width).Align(lipgloss.Right).Render(m.usage.summary())
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

func renderInputWithCursor(tab *TabSession) string {
	if tab == nil {
		return cursorStyle.Render(" ")
	}
	runes := []rune(tab.InputBuffer)
	cursor := tab.inputCursor()
	if len(runes) == 0 {
		return cursorStyle.Render(" ")
	}
	if cursor >= len(runes) {
		return inputStyle.Render(tab.InputBuffer) + cursorStyle.Render(" ")
	}
	before := string(runes[:cursor])
	current := string(runes[cursor])
	after := string(runes[cursor+1:])
	return inputStyle.Render(before) + cursorStyle.Render(current) + inputStyle.Render(after)
}

func shortPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "."
	}
	base := filepath.Base(path)
	parent := filepath.Base(filepath.Dir(path))
	if parent == "." || parent == string(filepath.Separator) || parent == "" {
		return base
	}
	return filepath.Join(parent, base)
}

func truncateStyled(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	plain := lipgloss.NewStyle().Render(value)
	if len([]rune(plain)) <= width {
		return plain
	}
	runes := []rune(plain)
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}
