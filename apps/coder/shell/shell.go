package shell

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	coreproject "github.com/fluxplane/agentruntime/core/project"
	projectruntime "github.com/fluxplane/agentruntime/runtime/project"
	"github.com/fluxplane/agentruntime/runtime/system"
)

// Options configures the experimental coder shell TUI.
type Options struct {
	// Path is the workspace path displayed by the shell.
	Path string
	// System is the shared runtime system used by shell features. When nil, Run
	// creates a host-backed system rooted at Path.
	System system.System
	// In receives terminal input. When nil, Bubble Tea uses stdin.
	In io.Reader
	// Out receives terminal output. When nil, Bubble Tea uses stdout.
	Out io.Writer
}

// Run starts the first minimal coder shell TUI.
func Run(opts Options) error {
	ctx := context.Background()
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = "."
	}
	sharedSystem := opts.System
	if sharedSystem == nil {
		host, err := system.NewHost(system.Config{Root: path})
		if err != nil {
			return fmt.Errorf("coder shell: create host system: %w", err)
		}
		sharedSystem = host
	}
	status := loadStatus(ctx, sharedSystem)
	if status.cwd == "" {
		status.cwd = path
	}
	programOpts := []tea.ProgramOption{tea.WithAltScreen()}
	if opts.In != nil {
		programOpts = append(programOpts, tea.WithInput(opts.In))
	}
	if opts.Out != nil {
		programOpts = append(programOpts, tea.WithOutput(opts.Out))
	}
	_, err := tea.NewProgram(newModel(status), programOpts...).Run()
	return err
}

type shellStatus struct {
	cwd          string
	workspace    string
	projectName  string
	projectKind  string
	goModule     string
	goVersion    string
	facets       []string
	taskCount    int
	projectCount int
	warningCount int
}

func loadStatus(ctx context.Context, sys system.System) shellStatus {
	status := shellStatus{goVersion: "go n/a"}
	if sys == nil || sys.Workspace() == nil {
		return status
	}
	workspace := sys.Workspace()
	status.workspace = workspace.Root()
	status.cwd = workspace.Root()

	manager := projectruntime.NewManager(workspace)
	inventory, _, err := manager.Inventory(ctx, coreproject.InventoryQuery{MaxResults: 25, MaxBytes: 64 * 1024})
	if err == nil {
		status.projectCount = len(inventory.Projects)
		status.warningCount = len(inventory.Warnings)
		if len(inventory.Projects) > 0 {
			project := chooseStatusProject(inventory.Projects, workspace.Root())
			status.projectName = project.Name
			status.projectKind = project.Kind
			status.facets = compactFacetNames(project.Facets)
			status.taskCount = countTasks(project.Facets)
			status.goModule = goModuleName(project.Facets)
			status.warningCount += len(project.Warnings)
		}
	}
	status.goVersion = detectGoVersion(ctx, sys)
	return status
}

func chooseStatusProject(projects []coreproject.Project, root string) coreproject.Project {
	if len(projects) == 0 {
		return coreproject.Project{}
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		cleanRoot = root
	}
	for _, project := range projects {
		projectRoot := project.Root
		abs, err := filepath.Abs(projectRoot)
		if err == nil {
			projectRoot = abs
		}
		if projectRoot == cleanRoot || project.Root == "." {
			return project
		}
	}
	return projects[0]
}

func compactFacetNames(facets []coreproject.Facet) []string {
	labels := make([]string, 0, len(facets))
	for _, facet := range facets {
		switch facet.Kind {
		case coreproject.FacetGoModule:
			labels = append(labels, "go")
		case coreproject.FacetGoWorkspace:
			labels = append(labels, "go.work")
		case coreproject.FacetGitRepo:
			labels = append(labels, "git")
		case coreproject.FacetTaskfile:
			labels = append(labels, "task")
		case coreproject.FacetMakefile:
			labels = append(labels, "make")
		case coreproject.FacetMarkdownDocs:
			labels = append(labels, "docs")
		case coreproject.FacetAgentsDir:
			labels = append(labels, "agents")
		case coreproject.FacetClaudeDir:
			labels = append(labels, "claude")
		case coreproject.FacetNodePackage:
			labels = append(labels, "node")
		default:
			labels = append(labels, strings.TrimSpace(string(facet.Kind)))
		}
	}
	labels = uniqueStrings(labels)
	if len(labels) > 8 {
		labels = append(labels[:8], fmt.Sprintf("+%d", len(labels)-8))
	}
	return labels
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func countTasks(facets []coreproject.Facet) int {
	total := 0
	for _, facet := range facets {
		total += len(facet.Tasks)
	}
	return total
}

func goModuleName(facets []coreproject.Facet) string {
	for _, facet := range facets {
		if facet.Kind != coreproject.FacetGoModule {
			continue
		}
		if module := strings.TrimSpace(facet.Summary["module"]); module != "" {
			return module
		}
		if module := strings.TrimSpace(facet.Manifest.Summary["module"]); module != "" {
			return module
		}
		if name := strings.TrimSpace(facet.Name); name != "" {
			return name
		}
	}
	return ""
}

func detectGoVersion(ctx context.Context, sys system.System) string {
	if sys == nil || sys.Process() == nil {
		return "go n/a"
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := sys.Process().Run(runCtx, system.ProcessRequest{
		Command:   "go",
		Args:      []string{"version"},
		Timeout:   2 * time.Second,
		MaxStdout: 256,
		MaxStderr: 256,
	})
	if err != nil {
		return "go n/a"
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) >= 3 && fields[0] == "go" && fields[1] == "version" {
		return fields[2]
	}
	return strings.TrimSpace(result.Stdout)
}

type model struct {
	status shellStatus
	width  int
	height int
	input  string
	log    []string
}

func newModel(status shellStatus) model {
	project := status.projectName
	if project == "" {
		project = "no project inventory"
	}
	return model{
		status: status,
		input:  "",
		log: []string{
			"coder shell experimental TUI",
			fmt.Sprintf("project: %s", project),
			"type text and press enter to echo it",
			"press ctrl+c, esc, or q on an empty prompt to quit",
		},
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyRunes:
			m.input += msg.String()
			return m, nil
		case tea.KeySpace:
			m.input += " "
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			if m.input != "" {
				runes := []rune(m.input)
				m.input = string(runes[:len(runes)-1])
			}
			return m, nil
		case tea.KeyEnter:
			line := strings.TrimSpace(m.input)
			if line == "" {
				m.log = append(m.log, "")
			} else {
				m.log = append(m.log, fmt.Sprintf("$ %s", m.input), fmt.Sprintf("echo: %s", m.input))
			}
			m.input = ""
			return m, nil
		}
		if msg.String() == "q" && m.input == "" {
			return m, tea.Quit
		}
	}
	return m, nil
}

var (
	pageStyle = lipgloss.NewStyle().Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	subtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	valueStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)

	statusPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), true, false, false, false).
				BorderForeground(lipgloss.Color("238")).
				Padding(0, 1)

	badgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("57")).
			Padding(0, 1).
			MarginRight(1)

	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	inputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("231"))
)

func (m model) View() string {
	width := m.width
	if width <= 0 {
		width = 96
	}
	contentWidth := width - 4
	if contentWidth < 40 {
		contentWidth = width
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	bodyLines := height - 13
	if bodyLines < 4 {
		bodyLines = 4
	}

	parts := []string{
		m.renderHeader(contentWidth),
		m.renderStatus(contentWidth),
		m.renderTimeline(contentWidth, bodyLines),
		m.renderPrompt(contentWidth),
	}
	return pageStyle.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (m model) renderHeader(width int) string {
	project := m.status.projectName
	if project == "" {
		project = "workspace"
	}
	module := m.status.goModule
	if module == "" {
		module = project
	}

	left := lipgloss.JoinHorizontal(lipgloss.Center,
		titleStyle.Render(" coder shell "),
		" ",
		accentStyle.Render(project),
		mutedStyle.Render("  "+module),
	)
	rightParts := []string{}
	if m.status.goVersion != "" {
		rightParts = append(rightParts, statusPill("go", strings.TrimPrefix(m.status.goVersion, "go")))
	}
	if m.status.taskCount > 0 {
		rightParts = append(rightParts, statusPill("tasks", fmt.Sprintf("%d", m.status.taskCount)))
	}
	if m.status.projectCount > 0 {
		rightParts = append(rightParts, statusPill("projects", fmt.Sprintf("%d", m.status.projectCount)))
	}
	right := strings.Join(rightParts, " ")
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right
	return lipgloss.NewStyle().Width(width).Render(line)
}

func (m model) renderStatus(width int) string {
	leftItems := []string{
		statusItem("pwd", shortPath(m.status.cwd)),
	}
	if m.status.projectKind != "" {
		leftItems = append(leftItems, statusItem("kind", m.status.projectKind))
	}
	if m.status.warningCount > 0 {
		leftItems = append(leftItems, warnStyle.Render(fmt.Sprintf("▲ %d warnings", m.status.warningCount)))
	} else {
		leftItems = append(leftItems, okStyle.Render("● inventory ok"))
	}

	left := strings.Join(leftItems, subtleStyle.Render("  •  "))
	badges := make([]string, 0, len(m.status.facets))
	for _, facet := range m.status.facets {
		badges = append(badges, badgeStyle.Render(facet))
	}
	right := strings.Join(badges, "")
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right
	return statusPanelStyle.Width(width).Render(truncateStyled(line, width))
}

func (m model) renderTimeline(width, maxLines int) string {
	lines := visibleLines(m.log, maxLines)
	if len(lines) == 0 {
		lines = []string{"Ready."}
	}
	styled := make([]string, 0, len(lines))
	for _, line := range lines {
		styled = append(styled, renderTimelineLine(line))
	}
	content := lipgloss.JoinVertical(lipgloss.Left, styled...)
	return panelStyle.Width(width).Height(maxLines + 2).Render(content)
}

func renderTimelineLine(line string) string {
	switch {
	case strings.HasPrefix(line, "$ "):
		return promptStyle.Render("$ ") + valueStyle.Render(strings.TrimPrefix(line, "$ "))
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

func (m model) renderPrompt(width int) string {
	prompt := promptStyle.Render("❯ ") + inputStyle.Render(m.input)
	help := subtleStyle.Render("ctrl+c/esc quit")
	gap := width - lipgloss.Width(prompt) - lipgloss.Width(help)
	if gap < 1 {
		gap = 1
	}
	line := prompt + strings.Repeat(" ", gap) + help
	return lipgloss.NewStyle().Width(width).Padding(0, 1).Render(line)
}

func statusPill(key, value string) string {
	return subtleStyle.Render(key) + " " + valueStyle.Render(value)
}

func statusItem(key, value string) string {
	return subtleStyle.Render(key) + " " + valueStyle.Render(value)
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

func visibleLines(lines []string, max int) []string {
	if max <= 0 || len(lines) <= max {
		return lines
	}
	return lines[len(lines)-max:]
}
