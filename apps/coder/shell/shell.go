package codershell

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	agentruntime "github.com/fluxplane/agentruntime"
	coreproject "github.com/fluxplane/agentruntime/core/project"
	projectruntime "github.com/fluxplane/agentruntime/runtime/project"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	defaultDirectEndpoint = "http://127.0.0.1:8080"
	defaultSessionName    = "coder"
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
	// DirectClient can provide an already opened channel client for tests or embedders.
	DirectClient agentruntime.ChannelClient
	// Connect selects the shell endpoint. Empty uses the provided direct channel.
	// Supported values include fake, direct, unix://PATH, http(s)://URL, and future target URLs.
	Connect string
}

// Config configures a shell presentation instance.
type Config = Options

// Shell is the coder shell presentation layer.
type Shell struct {
	opts Options
}

// New creates a shell presentation instance.
func New(cfg Config) *Shell {
	return &Shell{opts: Options(cfg)}
}

// Run starts the shell presentation.
func (s *Shell) Run(ctx context.Context) error {
	if s == nil {
		return Run(Options{})
	}
	return run(ctx, s.opts)
}

// Run starts the first minimal coder shell TUI.
func Run(opts Options) error {
	return run(context.Background(), opts)
}

func run(ctx context.Context, opts Options) error {
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
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
	programOpts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}
	if opts.In != nil {
		programOpts = append(programOpts, tea.WithInput(opts.In))
	}
	if opts.Out != nil {
		programOpts = append(programOpts, tea.WithOutput(opts.Out))
	}
	client := newClient(sharedSystem, opts)
	connection := connectionDescription(client, opts.Connect)
	_, err := tea.NewProgram(newModel(status, client, connection), programOpts...).Run()
	return err
}

func newClient(sys system.System, opts Options) ShellClient {
	if strings.TrimSpace(opts.Connect) == "" || strings.TrimSpace(opts.Connect) == "direct" {
		if opts.DirectClient != nil {
			return NewDirectChannelClient(DirectChannelClientOptions{Client: opts.DirectClient, Session: agentruntime.SessionRef{Name: defaultSessionName}})
		}
		client, err := newRemoteDirectChannelClient(defaultDirectEndpoint)
		if err == nil {
			return client
		}
		return NewRemoteClient(RemoteClientOptions{Endpoint: defaultDirectEndpoint, ParseError: err})
	}
	return newClientFromEndpoint(sys, opts.Connect)
}

func connectionDescription(client ShellClient, fallback string) string {
	if described, ok := client.(interface{ ConnectionDescription() string }); ok {
		if value := strings.TrimSpace(described.ConnectionDescription()); value != "" {
			return value
		}
	}
	if strings.TrimSpace(fallback) == "" {
		return "direct"
	}
	return strings.TrimSpace(fallback)
}

func newClientFromEndpoint(sys system.System, endpoint string) ShellClient {
	parsed, err := parseConnectEndpoint(endpoint)
	if err != nil {
		return NewRemoteClient(RemoteClientOptions{Endpoint: strings.TrimSpace(endpoint), ParseError: err})
	}
	switch parsed.Mode {
	case ClientModeFake:
		return NewFakeClient()
	case ClientModeDirect:
		if client, err := newRemoteDirectChannelClient(defaultDirectEndpoint); err == nil {
			return client
		}
		return NewRemoteClient(RemoteClientOptions{Endpoint: defaultDirectEndpoint})
	case ClientModeLocal:
		if client, err := newRemoteDirectChannelClient(parsed.Endpoint); err == nil {
			return client
		}
		return NewRemoteClient(RemoteClientOptions{Endpoint: parsed.Endpoint})
	case ClientModeRemote:
		if client, err := newRemoteDirectChannelClient(parsed.Endpoint); err == nil {
			return client
		}
		return NewRemoteClient(RemoteClientOptions{Endpoint: parsed.Endpoint})
	default:
		return NewLocalClient(sys)
	}
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
	status         shellStatus
	width          int
	height         int
	shell          *ShellObject
	mention        MentionState
	timeline       viewport.Model
	timelinePinned bool
}

type askSubmittedMsg struct {
	sessionID string
	events    []TranscriptEvent
	err       error
}

func newModel(status shellStatus, client ShellClient, connection string) model {
	project := status.projectName
	if project == "" {
		project = "no project inventory"
	}
	state, err := NewShellObject(context.Background(), ShellObjectOptions{Client: client, CWD: status.cwd, Connection: connection})
	if err != nil {
		state = &ShellObject{client: client, tabs: []TabSession{{
			ID:        disconnectedSessionID,
			Label:     "1",
			CWD:       status.cwd,
			InputMode: InputModeShell,
			Transcript: []TranscriptEvent{
				transcriptConnectionEvent(disconnectedSessionID, connection),
				{
					ID:      newEventID("error"),
					Kind:    EventError,
					Summary: err.Error(),
				},
			},
		}}}
	}
	active := state.ActiveTab()
	if active != nil {
		active.Transcript = append(active.Transcript,
			TranscriptEvent{ID: newEventID("intro"), SessionID: active.ID, Time: time.Now(), Summary: "coder shell experimental TUI"},
			TranscriptEvent{ID: newEventID("project"), SessionID: active.ID, Time: time.Now(), Summary: fmt.Sprintf("project: %s", project)},
			TranscriptEvent{ID: newEventID("hint"), SessionID: active.ID, Time: time.Now(), Summary: "type text and press enter to submit through the selected shell client"},
			TranscriptEvent{ID: newEventID("quit"), SessionID: active.ID, Time: time.Now(), Summary: "press ctrl+c, esc, or q on an empty prompt to quit"},
		)
	}
	timeline := viewport.New(1, 1)
	timeline.MouseWheelEnabled = true
	m := model{status: status, shell: state, timeline: timeline, timelinePinned: true}
	m.syncTimelineViewport(true)
	return m
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncTimelineViewport(false)
		return m, nil
	case askSubmittedMsg:
		m.appendAsyncTranscript(msg.sessionID, msg.events, msg.err)
		m.syncTimelineViewport(msg.sessionID == m.activeSessionID())
		return m, nil
	case tea.MouseMsg:
		m.syncTimelineViewport(false)
		var cmd tea.Cmd
		m.timeline, cmd = m.timeline.Update(msg)
		m.timelinePinned = m.timeline.AtBottom()
		return m, cmd
	case tea.KeyMsg:
		tab := m.shell.ActiveTab()
		switch msg.Type {
		case tea.KeyPgUp:
			m.syncTimelineViewport(false)
			m.timeline.PageUp()
			m.timelinePinned = false
			return m, nil
		case tea.KeyPgDown:
			m.syncTimelineViewport(false)
			m.timeline.PageDown()
			m.timelinePinned = m.timeline.AtBottom()
			return m, nil
		case tea.KeyHome:
			m.syncTimelineViewport(false)
			m.timeline.GotoTop()
			m.timelinePinned = false
			return m, nil
		case tea.KeyEnd:
			m.syncTimelineViewport(false)
			m.timeline.GotoBottom()
			m.timelinePinned = true
			return m, nil
		case tea.KeyCtrlU:
			m.syncTimelineViewport(false)
			m.timeline.HalfPageUp()
			m.timelinePinned = false
			return m, nil
		case tea.KeyCtrlD:
			m.syncTimelineViewport(false)
			m.timeline.HalfPageDown()
			m.timelinePinned = m.timeline.AtBottom()
			return m, nil
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyCtrlT:
			cwd := m.status.cwd
			if tab != nil && strings.TrimSpace(tab.CWD) != "" {
				cwd = tab.CWD
			}
			_, _ = m.shell.NewTab(context.Background(), cwd)
			m.mention = MentionState{}
			m.syncTimelineViewport(true)
			return m, nil
		case tea.KeyRunes:
			value := msg.String()
			if value == "q" && tab != nil && tab.InputBuffer == "" {
				return m, tea.Quit
			}
			if len(value) == 1 && value[0] >= '1' && value[0] <= '9' && msg.Alt {
				m.shell.SelectTab(int(value[0] - '1'))
				m.syncTimelineViewport(true)
				return m, nil
			}
			m.shell.AppendInput(value)
			m.refreshMention()
			return m, nil
		case tea.KeySpace:
			m.shell.AppendInput(" ")
			m.mention = MentionState{}
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			m.shell.BackspaceInput()
			m.refreshMention()
			return m, nil
		case tea.KeyEnter:
			m.mention = MentionState{}
			if cmd := m.submitAskAsync(); cmd != nil {
				m.syncTimelineViewport(true)
				return m, cmd
			}
			_ = m.shell.SubmitActiveInput(context.Background())
			m.syncTimelineViewport(true)
			return m, nil
		case tea.KeyUp:
			if m.mention.Open && m.mention.Index > 0 {
				m.mention.Index--
			}
			return m, nil
		case tea.KeyDown:
			if m.mention.Open && m.mention.Index+1 < len(m.mention.Results) {
				m.mention.Index++
			}
			return m, nil
		case tea.KeyTab:
			if msg.Alt {
				m.shell.ToggleInputMode()
				return m, nil
			}
			if result, ok := m.mention.activeResult(); ok {
				tab := m.shell.ActiveTab()
				if tab != nil {
					tab.InputBuffer = replaceMentionFragment(tab.InputBuffer, result)
					tab.Mentions = append(tab.Mentions, ResourceMention{
						Kind:       result.Kind,
						ID:         result.ID,
						Label:      result.Label,
						URI:        result.URI,
						InsertText: result.InsertText,
						Metadata:   result.Metadata,
					})
					tab.Transcript = append(tab.Transcript, TranscriptEvent{
						ID:        newEventID("mention"),
						SessionID: tab.ID,
						Time:      time.Now(),
						Kind:      EventResourceMentioned,
						Summary:   string(result.Kind) + ": " + result.Label,
					})
				}
				m.mention = MentionState{}
				m.syncTimelineViewport(true)
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *model) submitAskAsync() tea.Cmd {
	if m == nil || m.shell == nil {
		return nil
	}
	tab := m.shell.ActiveTab()
	if tab == nil || tab.InputMode != InputModeAsk {
		return nil
	}
	line := strings.TrimSpace(tab.InputBuffer)
	if line == "" || classifyInput(line, tab.InputMode).Kind != IntentAsk {
		return nil
	}
	if tab.ID == disconnectedSessionID {
		err := fmt.Errorf("shell session is not connected: %s", lastSessionError(tab.Transcript))
		tab.Transcript = append(tab.Transcript, errorEvent(tab.ID, err))
		tab.InputBuffer = ""
		return nil
	}
	sessionID := tab.ID
	cwd := tab.CWD
	projection := ProjectTranscript(tab.Transcript, m.shell.contextPolicy)
	start := TranscriptEvent{
		ID:        newEventID("ask"),
		SessionID: sessionID,
		Time:      time.Now(),
		Kind:      EventAskSubmitted,
		Summary:   line,
		Data:      map[string]string{"cwd": cwd, "context_items": fmt.Sprintf("%d", len(projection))},
	}
	tab.Transcript = append(tab.Transcript, start)
	tab.InputBuffer = ""
	client := m.shell.client
	return func() tea.Msg {
		if client == nil {
			return askSubmittedMsg{sessionID: sessionID, err: fmt.Errorf("shell client is unavailable")}
		}
		events, err := client.SubmitAsk(context.Background(), sessionID, AskRequest{Text: line, CWD: cwd, Context: projection})
		return askSubmittedMsg{sessionID: sessionID, events: dropSubmittedAskEvent(events), err: err}
	}
}

func dropSubmittedAskEvent(events []TranscriptEvent) []TranscriptEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]TranscriptEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == EventAskSubmitted {
			continue
		}
		out = append(out, event)
	}
	return out
}

func (m *model) appendAsyncTranscript(sessionID string, events []TranscriptEvent, err error) {
	if m == nil || m.shell == nil {
		return
	}
	for i := range m.shell.tabs {
		if m.shell.tabs[i].ID != sessionID {
			continue
		}
		if err != nil {
			m.shell.tabs[i].Transcript = append(m.shell.tabs[i].Transcript, errorEvent(sessionID, err))
			return
		}
		m.shell.tabs[i].Transcript = append(m.shell.tabs[i].Transcript, events...)
		return
	}
}

func (m model) activeSessionID() string {
	if m.shell == nil {
		return ""
	}
	if tab := m.shell.ActiveTab(); tab != nil {
		return tab.ID
	}
	return ""
}

type shellLayout struct {
	contentWidth        int
	timelineOuterHeight int
	timelineInnerWidth  int
	timelineInnerHeight int
}

func (m model) layout() shellLayout {
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

	header := m.renderHeader(contentWidth)
	status := m.renderStatus(contentWidth)
	prompt := m.renderPrompt(contentWidth)
	fixedHeight := lipgloss.Height(header) + lipgloss.Height(status) + lipgloss.Height(prompt)
	if m.mention.Open {
		fixedHeight += lipgloss.Height(m.renderMentionPicker(contentWidth))
	}

	available := height - 2 - fixedHeight
	if available < 3 {
		available = 3
	}
	innerHeight := available - 2
	if innerHeight < 1 {
		innerHeight = 1
	}
	innerWidth := contentWidth - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	return shellLayout{
		contentWidth:        contentWidth,
		timelineOuterHeight: available,
		timelineInnerWidth:  innerWidth,
		timelineInnerHeight: innerHeight,
	}
}

func (m *model) syncTimelineViewport(follow bool) {
	if m == nil {
		return
	}
	layout := m.layout()
	pinned := follow || m.timelinePinned || m.timeline.AtBottom() || m.timeline.TotalLineCount() == 0
	m.timeline.Width = layout.timelineInnerWidth
	m.timeline.Height = layout.timelineInnerHeight
	m.timeline.SetContent(m.timelineContent(layout.timelineInnerWidth))
	if pinned {
		m.timeline.GotoBottom()
	}
	m.timelinePinned = m.timeline.AtBottom()
}

func (m model) timelineContent(width int) string {
	tab := m.shell.ActiveTab()
	lines := []string{"Ready."}
	if tab != nil {
		lines = TimelineLines(tab.Transcript)
	}
	if len(lines) == 0 {
		lines = []string{"Ready."}
	}
	rendered := make([]string, 0, len(lines))
	wrap := lipgloss.NewStyle().Width(width)
	for _, line := range lines {
		wrapped := wrap.Render(renderTimelineLine(line))
		rendered = append(rendered, strings.Split(wrapped, "\n")...)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rendered...)
}

func (m *model) refreshMention() {
	tab := m.shell.ActiveTab()
	if tab == nil {
		m.mention = MentionState{}
		return
	}
	query, ok := mentionQuery(tab.InputBuffer)
	if !ok {
		m.mention = MentionState{}
		return
	}
	results, err := m.shell.SearchResources(context.Background(), ResourceSearchQuery{Text: query, Limit: 6, Mention: true})
	if err != nil {
		m.mention = MentionState{}
		return
	}
	m.mention = MentionState{Open: true, Query: query, Results: results}
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
	if active := m.shell.ActiveTab(); active != nil {
		rightParts = append(rightParts, statusPill("session", active.ID))
	}
	if len(m.shell.tabs) > 0 {
		rightParts = append(rightParts, statusPill("tabs", fmt.Sprintf("%d", len(m.shell.tabs))))
	}
	if connection := strings.TrimSpace(m.shell.connection); connection != "" {
		rightParts = append(rightParts, statusPill("conn", connection))
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
	activeCWD := m.status.cwd
	if active := m.shell.ActiveTab(); active != nil && strings.TrimSpace(active.CWD) != "" {
		activeCWD = active.CWD
	}
	leftItems := []string{
		statusItem("pwd", shortPath(activeCWD)),
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

func (m model) renderMentionPicker(width int) string {
	if !m.mention.Open {
		return ""
	}
	lines := []string{subtleStyle.Render("@ resources") + " " + valueStyle.Render(m.mention.Query)}
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
		lines = append(lines, prefix+subtleStyle.Render("["+string(result.Kind)+"] ")+style.Render(icon+result.Label))
	}
	return panelStyle.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func (m model) renderTimeline(layout shellLayout) string {
	timeline := m.timeline
	timeline.Width = layout.timelineInnerWidth
	timeline.Height = layout.timelineInnerHeight
	timeline.SetContent(m.timelineContent(layout.timelineInnerWidth))
	if m.timelinePinned || timeline.TotalLineCount() <= timeline.Height {
		timeline.GotoBottom()
	}
	return panelStyle.Width(layout.contentWidth).Height(layout.timelineOuterHeight - 2).Render(timeline.View())
}

func renderTimelineLine(line string) string {
	switch {
	case strings.HasPrefix(line, "? "):
		return accentStyle.Render("? ") + valueStyle.Render(strings.TrimPrefix(line, "? "))
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
		return accentStyle.Render("🤖 ") + valueStyle.Render(strings.TrimSpace(strings.TrimPrefix(line, "agent:")))
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

func (m model) renderPrompt(width int) string {
	tab := m.shell.ActiveTab()
	input := ""
	marker := "❯ "
	if tab != nil {
		input = tab.InputBuffer
		if tab.InputMode == InputModeAsk {
			marker = "🤖 "
		}
	}
	prompt := promptStyle.Render(marker) + inputStyle.Render(input)
	help := subtleStyle.Render("ctrl+t new tab · alt+tab mode · ctrl+c/esc quit")
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
