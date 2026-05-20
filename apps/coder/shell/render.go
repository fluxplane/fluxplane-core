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
	pageStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F8F8F2")).Background(lipgloss.Color("#272822"))

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
			Background(monokaiPink)

	subtleStyle      = lipgloss.NewStyle().Foreground(monokaiMuted)
	mutedStyle       = lipgloss.NewStyle().Foreground(monokaiComment)
	valueStyle       = lipgloss.NewStyle().Foreground(monokaiForeground)
	accentStyle      = lipgloss.NewStyle().Foreground(monokaiPurple).Bold(true)
	askStyle         = lipgloss.NewStyle().Foreground(monokaiCyan).Bold(true)
	okStyle          = lipgloss.NewStyle().Foreground(monokaiGreen)
	warnStyle        = lipgloss.NewStyle().Foreground(monokaiOrange)
	runningToolStyle = lipgloss.NewStyle().Foreground(monokaiCyan).Bold(true)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(monokaiSurface)

	chipStyle = lipgloss.NewStyle().Foreground(monokaiYellow)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(monokaiMuted).
				Background(monokaiSurface)
	activeTabStyle = lipgloss.NewStyle().
			Foreground(monokaiBackground).
			Background(monokaiGreen).
			Bold(true)
	selectedRowStyle = lipgloss.NewStyle().
				Foreground(monokaiForeground).
				Background(monokaiSurface)

	promptBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(monokaiSurface)
	promptStyle = lipgloss.NewStyle().Foreground(monokaiGreen).Bold(true)
	inputStyle  = lipgloss.NewStyle().Foreground(monokaiForeground)
	cursorStyle = lipgloss.NewStyle().Foreground(monokaiBackground).Background(monokaiYellow)
	footerStyle = lipgloss.NewStyle().Foreground(monokaiMuted)
)

func (m model) View() tea.View {
	layout := m.layout()

	parts := []string{
		layout.header,
		layout.status,
		m.renderTimeline(layout),
	}
	if m.mention.Open {
		parts = append(parts, layout.mention)
	}
	parts = append(parts, layout.prompt)
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
	title := titleStyle.Render("coder shell")
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
	projectLine := fitParts(width, projectParts, mutedStyle.Render("  "))
	if tabBar := m.renderTabBar(width - lipgloss.Width(projectLine) - 1); tabBar != "" {
		lines = append(lines, alignLine(projectLine, tabBar, width))
	} else {
		lines = append(lines, projectLine)
	}

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

func (m model) renderTabBar(width int) string {
	if m.shell == nil || len(m.shell.tabs) == 0 || width < 10 {
		return ""
	}
	active := m.shell.ActiveIndex()
	parts := make([]string, 0, len(m.shell.tabs))
	for i, tab := range m.shell.tabs {
		parts = append(parts, m.renderTabChip(i, tab, i == active))
	}
	prefix := subtleStyle.Render("tabs ")
	line := prefix + strings.Join(parts, " ")
	if lipgloss.Width(line) <= width {
		return line
	}
	if active >= 0 && active < len(parts) {
		hidden := len(parts) - 1
		line = prefix + parts[active]
		if hidden > 0 {
			line += " " + mutedStyle.Render(fmt.Sprintf("+%d", hidden))
		}
		if lipgloss.Width(line) <= width {
			return line
		}
	}
	return subtleStyle.Render("tabs ") + valueStyle.Render(m.tabSummary())
}

func (m model) renderTabChip(index int, tab TabSession, active bool) string {
	mode := "?"
	if tab.InputMode == InputModeShell {
		mode = "$"
	}
	label := fmt.Sprintf("%d:%s", index+1, mode)
	if active {
		if cwd := shortPath(tab.CWD); cwd != "" && cwd != "." {
			label += " " + cwd
		}
	}
	if m.activeRuns[tab.ID] != "" {
		label += " ●"
	}
	if active {
		return activeTabStyle.Render(" " + label + " ")
	}
	return inactiveTabStyle.Render(" " + label + " ")
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
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = width
	}
	header := "@ resources"
	switch m.mention.Kind {
	case completionCommand:
		header = "/ commands"
	case completionOption:
		header = "/ options " + m.mention.CommandPath
	}
	headerLine := subtleStyle.Render(header)
	if query := strings.TrimSpace(m.mention.Query); query != "" {
		headerLine += " " + valueStyle.Render(query)
	}
	if summary := mentionPickerSummary(m.mention); summary != "" {
		headerLine = alignLine(headerLine, subtleStyle.Render(summary), innerWidth)
	}
	lines := []string{headerLine}
	if m.mention.Loading {
		lines = append(lines, mutedStyle.Render("searching..."))
	} else if len(m.mention.Results) == 0 {
		lines = append(lines, mutedStyle.Render("no results"))
	}
	start, end := visibleMentionRange(m.mention, 8)
	if start > 0 {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("… %d earlier", start)))
	}
	for i := start; i < end; i++ {
		result := m.mention.Results[i]
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
		row := prefix + kindPrefix + style.Render(icon+result.Label) + detail
		if i == m.mention.Index {
			row = selectedRowStyle.Width(innerWidth).Render(row)
		}
		lines = append(lines, row)
	}
	if end < len(m.mention.Results) {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("… %d more", len(m.mention.Results)-end)))
	}
	if result, ok := m.mention.activeResult(); ok {
		if detail := firstNonEmptyString(result.Description, result.Detail, result.URI); detail != "" {
			lines = append(lines, subtleStyle.Render("info ")+valueStyle.Render(truncateStyled(detail, innerWidth-5)))
		}
	}
	lines = append(lines, mutedStyle.Render("tab accept · enter accept · ↑↓ select · esc close"))
	return panelStyle.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func visibleMentionRange(state MentionState, limit int) (int, int) {
	count := len(state.Results)
	if count == 0 {
		return 0, 0
	}
	if limit <= 0 || count <= limit {
		return 0, count
	}
	index := state.Index
	if index < 0 {
		index = 0
	}
	if index >= count {
		index = count - 1
	}
	start := index - limit/2
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > count {
		end = count
		start = end - limit
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func mentionPickerSummary(state MentionState) string {
	if state.Loading {
		return "loading"
	}
	if len(state.Results) == 0 {
		return "0 results"
	}
	index := state.Index + 1
	if index < 1 {
		index = 1
	}
	if index > len(state.Results) {
		index = len(state.Results)
	}
	return fmt.Sprintf("%d/%d results", index, len(state.Results))
}

func (m model) renderTimeline(layout shellLayout) string {
	timeline := m.timeline
	timeline.SetWidth(layout.timelineInnerWidth)
	timeline.SetHeight(layout.timelineInnerHeight)
	if m.timelinePinned || timeline.TotalLineCount() <= timeline.Height() {
		timeline.GotoBottom()
	}
	return lipgloss.NewStyle().Width(layout.contentWidth).Height(layout.timelineOuterHeight).Render(timeline.View())
}

func renderTimelineLine(line string, width int) string {
	return renderTimelineLineWithCache(line, width, nil)
}

func renderTimelineLineWithCache(line string, width int, markdownCache map[timelineCacheKey]string) string {
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
		return renderTimelineMarkdownCached("🤖 ", accentStyle, strings.TrimSpace(strings.TrimPrefix(line, "agent:")), width, markdownCache)
	case strings.HasPrefix(line, "thinking:"):
		return renderTimelineMarkdownCached("… ", subtleStyle, strings.TrimSpace(strings.TrimPrefix(line, "thinking:")), width, markdownCache)
	case strings.HasPrefix(line, "op:"):
		return renderToolLine("run", "⚙ ", runningToolStyle, strings.TrimSpace(strings.TrimPrefix(line, "op:")))
	case strings.HasPrefix(line, "op-done:"):
		return renderToolLine("done", "✓ ", okStyle, strings.TrimSpace(strings.TrimPrefix(line, "op-done:")))
	case strings.HasPrefix(line, "proc:"):
		return renderToolLine("proc", "⏵ ", subtleStyle, strings.TrimSpace(strings.TrimPrefix(line, "proc:")))
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
	case strings.HasPrefix(line, "canceled:"):
		return mutedStyle.Render("cancel ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "canceled:")))
	case strings.HasPrefix(line, "error:"):
		return warnStyle.Render("! ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "error:")))
	case strings.HasPrefix(line, "echo:"):
		return subtleStyle.Render("↳ ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "echo:")))
	case strings.HasPrefix(line, "project:"):
		return subtleStyle.Render("◆ ") + accentStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "project:")))
	case strings.HasPrefix(line, "tip:"):
		return subtleStyle.Render("tip ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "tip:")))
	case line == "":
		return ""
	default:
		return subtleStyle.Render("· ") + valueStyle.Render(line)
	}
}

func renderToolLine(label, marker string, markerStyle lipgloss.Style, summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return markerStyle.Render(marker) + subtleStyle.Render(label)
	}
	name, detail := splitToolSummary(summary)
	left := markerStyle.Render(marker) + subtleStyle.Render(label+" ") + accentStyle.Render(name)
	if detail == "" {
		return left
	}
	return left + subtleStyle.Render(" ┊ ") + valueStyle.Render(detail)
}

func splitToolSummary(summary string) (string, string) {
	fields := strings.Fields(summary)
	if len(fields) == 0 {
		return "tool", ""
	}
	if len(fields) >= 3 && fields[0] == "tool" && fields[1] == "call" {
		return fields[2], strings.Join(fields[3:], " ")
	}
	name := fields[0]
	if len(fields) == 1 {
		return name, ""
	}
	return name, strings.TrimSpace(strings.TrimPrefix(summary, name))
}

func renderTimelineMarkdownCached(marker string, markerStyle lipgloss.Style, text string, width int, cache map[timelineCacheKey]string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return markerStyle.Render(strings.TrimSpace(marker))
	}
	contentWidth := width - 3
	if contentWidth < 20 {
		contentWidth = width
	}
	key := timelineCacheKey{width: contentWidth, line: text}
	rendered, ok := cache[key]
	if !ok {
		rendered = renderMarkdownBubbleView(text, contentWidth)
		if cache != nil {
			cache[key] = rendered
		}
	}
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
	mode := InputModeShell
	editing := false
	if tab != nil {
		mode = tab.InputMode
		editing = tab.InputBuffer != ""
		if tab.InputMode == InputModeAsk {
			marker = "🤖 "
		}
		input = renderInputWithCursor(tab)
	}
	prompt := promptStyle.Render(marker) + input
	box := promptBoxStyle.Width(width).Render(prompt)
	footer := footerStyle.Width(width).Render(m.promptFooter(mode, width, editing))
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

func (m model) promptFooter(mode InputMode, width int, editing bool) string {
	usage := m.usage.summary()
	modeHint := "shell mode  ? ask · / command · @ mention"
	if mode == InputModeAsk {
		modeHint = "ask mode  ! shell · / command · @ mention"
	}
	navHint := "↑ history · pgup scroll"
	if editing {
		navHint = "ctrl+w word · ctrl+k tail"
	}
	if len(m.activeRuns) > 0 || len(m.activeCancels) > 0 {
		navHint = "esc cancel · pgup scroll"
	}
	hint := modeHint + " · " + navHint
	if lipgloss.Width(hint)+lipgloss.Width(usage)+1 <= width {
		return alignLine(mutedStyle.Render(hint), subtleStyle.Render(usage), width)
	}
	if lipgloss.Width(modeHint)+lipgloss.Width(usage)+1 <= width {
		return alignLine(mutedStyle.Render(modeHint), subtleStyle.Render(usage), width)
	}
	return subtleStyle.Render(usage)
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
