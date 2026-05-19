package codershell

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	mdbubble "github.com/codewandler/markdown/bubbleview"
	"github.com/codewandler/markdown/stream"
	mdterminal "github.com/codewandler/markdown/terminal"
	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/command"
	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	coreproject "github.com/fluxplane/agentruntime/core/project"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
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
	// CommandSpecs provide static command completion metadata for direct clients.
	CommandSpecs []command.Spec
	// Connect selects the shell endpoint. Empty uses the provided direct channel.
	// Supported values include fake, direct, unix://PATH, http(s)://URL, and future target URLs.
	Connect string
	// Provider is the selected model provider, when known by the launching CLI.
	Provider string
	// Model is the selected model name, when known by the launching CLI.
	Model string
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
	status.provider = strings.TrimSpace(opts.Provider)
	status.model = strings.TrimSpace(opts.Model)
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
			return NewDirectChannelClient(DirectChannelClientOptions{Client: opts.DirectClient, Session: agentruntime.SessionRef{Name: defaultSessionName}, Commands: opts.CommandSpecs})
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
	locale       string
	user         string
	provider     string
	model        string
	facets       []string
	taskCount    int
	projectCount int
	warningCount int
}

func loadStatus(ctx context.Context, sys system.System) shellStatus {
	status := shellStatus{}
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
	status.applyBaselineObservations(ctx)
	status.goVersion = detectGoVersion(ctx, sys)
	return status
}

func (s *shellStatus) applyBaselineObservations(ctx context.Context) {
	if s == nil {
		return
	}
	observations, err := runtimeenvironment.BaselineObserver().Observe(ctx, runtimeenvironment.ObservationRequest{
		Phase: coreenvironment.PhaseTurn,
	})
	if err != nil {
		return
	}
	for _, observation := range observations {
		content, ok := observation.Content.(map[string]any)
		if !ok {
			continue
		}
		switch observation.Kind {
		case runtimeenvironment.ObservationSystemLocale:
			s.locale = firstStringValue(content, "LC_ALL", "LC_CTYPE", "LANG")
		case runtimeenvironment.ObservationSystemUser:
			s.user = firstStringValue(content, "username")
		}
	}
}

func firstStringValue(content map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := content[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		case fmt.Stringer:
			if trimmed := strings.TrimSpace(typed.String()); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
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
			labels = append(labels, "go.mod")
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
		return ""
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
		return ""
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
	activeRuns     map[string]string
	activeOps      map[string]string
	usage          usageTotals
}

type askSubmittedMsg struct {
	sessionID string
	events    []TranscriptEvent
	err       error
}

type shellStreamEventMsg struct {
	sessionID string
	stream    ShellRunStream
	event     TranscriptEvent
}

type shellStreamDoneMsg struct {
	sessionID string
	done      ShellRunDone
}

func newModel(status shellStatus, client ShellClient, connection string) model {
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
	timeline := viewport.New(1, 1)
	timeline.MouseWheelEnabled = true
	m := model{status: status, shell: state, timeline: timeline, timelinePinned: true, activeRuns: map[string]string{}, activeOps: map[string]string{}}
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
	case shellStreamEventMsg:
		m.appendStreamTranscript(msg.sessionID, msg.event)
		m.syncTimelineViewport(msg.sessionID == m.activeSessionID())
		return m, waitShellStream(msg.sessionID, msg.stream)
	case shellStreamDoneMsg:
		m.finishStreamTranscript(msg.sessionID, msg.done)
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
			if tab != nil && value == "!" && tab.InputMode == InputModeAsk {
				tab.InputMode = InputModeShell
				m.mention = MentionState{}
				return m, nil
			}
			if tab != nil && value == "?" && tab.InputMode == InputModeShell {
				tab.InputMode = InputModeAsk
				m.mention = MentionState{}
				return m, nil
			}
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
			if cmd := m.submitActiveInputAsync(); cmd != nil {
				m.syncTimelineViewport(true)
				return m, cmd
			}
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
			if result, ok := m.mention.activeResult(); ok {
				tab := m.shell.ActiveTab()
				if tab != nil {
					switch m.mention.Kind {
					case completionCommand:
						tab.InputBuffer = replaceCommandFragment(tab.InputBuffer, result)
					case completionOption:
						tab.InputBuffer = replaceOptionFragment(tab.InputBuffer, result)
					default:
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
				}
				m.mention = MentionState{}
				m.syncTimelineViewport(true)
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *model) submitActiveInputAsync() tea.Cmd {
	if m == nil || m.shell == nil {
		return nil
	}
	tab := m.shell.ActiveTab()
	if tab == nil {
		return nil
	}
	line := strings.TrimSpace(tab.InputBuffer)
	if line == "" {
		tab.Transcript = append(tab.Transcript, TranscriptEvent{
			ID:        newEventID("input"),
			SessionID: tab.ID,
			Time:      time.Now(),
			Kind:      EventInputSubmitted,
			Summary:   "",
		})
		tab.InputBuffer = ""
		return nil
	}
	if tab.ID == disconnectedSessionID {
		err := fmt.Errorf("shell session is not connected: %s", lastSessionError(tab.Transcript))
		tab.Transcript = append(tab.Transcript, errorEvent(tab.ID, err))
		tab.InputBuffer = ""
		return nil
	}

	intent := classifyInput(line, tab.InputMode)
	if intent.Kind == IntentCD {
		result, err := m.shell.client.ChangeCWD(context.Background(), tab.ID, intent.Arg)
		if err != nil {
			tab.Transcript = append(tab.Transcript, errorEvent(tab.ID, err))
			return nil
		}
		tab.CWD = result.CWD
		tab.Transcript = append(tab.Transcript, TranscriptEvent{
			ID:        newEventID("cwd"),
			SessionID: tab.ID,
			Time:      time.Now(),
			Kind:      EventCWDChanged,
			Summary:   result.CWD,
			Data:      map[string]string{"target": intent.Arg},
		})
		tab.InputBuffer = ""
		return nil
	}

	sessionID := tab.ID
	cwd := tab.CWD
	var start TranscriptEvent
	var fallback func(context.Context) ([]TranscriptEvent, error)
	var stream func(context.Context, StreamingShellClient) (ShellRunStream, error)
	switch intent.Kind {
	case IntentAsk:
		projection := ProjectTranscript(tab.Transcript, m.shell.contextPolicy)
		start = TranscriptEvent{
			ID:        newEventID("ask"),
			SessionID: sessionID,
			Time:      time.Now(),
			Kind:      EventAskSubmitted,
			Summary:   line,
			Data:      map[string]string{"cwd": cwd, "context_items": fmt.Sprintf("%d", len(projection))},
		}
		req := AskRequest{Text: line, CWD: cwd, Context: projection}
		fallback = func(ctx context.Context) ([]TranscriptEvent, error) {
			return m.shell.client.SubmitAsk(ctx, sessionID, req)
		}
		stream = func(ctx context.Context, client StreamingShellClient) (ShellRunStream, error) {
			return client.SubmitAskStream(ctx, sessionID, req)
		}
	case IntentSlash:
		start = TranscriptEvent{ID: newEventID("slash"), SessionID: sessionID, Time: time.Now(), Kind: EventSlashSubmitted, Summary: intent.Text, Data: map[string]string{"cwd": cwd}}
		req := SlashRequest{Line: intent.Text, CWD: cwd}
		fallback = func(ctx context.Context) ([]TranscriptEvent, error) {
			return m.shell.client.SubmitSlash(ctx, sessionID, req)
		}
		stream = func(ctx context.Context, client StreamingShellClient) (ShellRunStream, error) {
			return client.SubmitSlashStream(ctx, sessionID, req)
		}
	default:
		start = TranscriptEvent{ID: newEventID("cmd-start"), SessionID: sessionID, Time: time.Now(), Kind: EventCommandStarted, Summary: intent.Text, Data: map[string]string{"cwd": cwd}}
		req := CommandRequest{Line: intent.Text, CWD: cwd}
		fallback = func(ctx context.Context) ([]TranscriptEvent, error) {
			return m.shell.client.SubmitCommand(ctx, sessionID, req)
		}
		stream = func(ctx context.Context, client StreamingShellClient) (ShellRunStream, error) {
			return client.SubmitCommandStream(ctx, sessionID, req)
		}
	}
	tab.Transcript = append(tab.Transcript, start)
	tab.InputBuffer = ""
	client := m.shell.client
	if m.activeRuns == nil {
		m.activeRuns = map[string]string{}
	}
	m.activeRuns[sessionID] = string(start.Kind)
	if streaming, ok := client.(StreamingShellClient); ok {
		streamHandle, err := stream(context.Background(), streaming)
		if err == nil {
			return waitShellStream(sessionID, streamHandle)
		}
		tab.Transcript = append(tab.Transcript, errorEvent(sessionID, err))
		delete(m.activeRuns, sessionID)
		return nil
	}
	return func() tea.Msg {
		if client == nil {
			return askSubmittedMsg{sessionID: sessionID, err: fmt.Errorf("shell client is unavailable")}
		}
		events, err := fallback(context.Background())
		return askSubmittedMsg{sessionID: sessionID, events: dropSubmittedStartEvent(events, start.Kind), err: err}
	}
}

func dropSubmittedStartEvent(events []TranscriptEvent, kind TranscriptKind) []TranscriptEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]TranscriptEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == kind {
			continue
		}
		out = append(out, event)
	}
	return out
}

func waitShellStream(sessionID string, stream ShellRunStream) tea.Cmd {
	return func() tea.Msg {
		if stream.Events != nil {
			if event, ok := <-stream.Events; ok {
				return shellStreamEventMsg{sessionID: sessionID, stream: stream, event: event}
			}
		}
		if stream.Done == nil {
			return shellStreamDoneMsg{sessionID: sessionID}
		}
		done, ok := <-stream.Done
		if !ok {
			return shellStreamDoneMsg{sessionID: sessionID}
		}
		return shellStreamDoneMsg{sessionID: sessionID, done: done}
	}
}

func (m *model) appendAsyncTranscript(sessionID string, events []TranscriptEvent, err error) {
	if m == nil || m.shell == nil {
		return
	}
	delete(m.activeRuns, sessionID)
	m.addUsageEvents(events)
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

func (m *model) appendStreamTranscript(sessionID string, event TranscriptEvent) {
	if m == nil || m.shell == nil {
		return
	}
	if event.SessionID == "" {
		event.SessionID = sessionID
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	if m.activeRuns == nil {
		m.activeRuns = map[string]string{}
	}
	m.activeRuns[sessionID] = streamActivity(event)
	if event.Kind == EventOperationStarted {
		if m.activeOps == nil {
			m.activeOps = map[string]string{}
		}
		if callID := event.Data["call_id"]; callID != "" {
			m.activeOps[callID] = firstNonEmptyString(event.Data["operation"], event.Summary)
		}
	}
	if event.Kind == EventOperationComplete || event.Kind == EventError {
		if callID := event.Data["call_id"]; callID != "" {
			delete(m.activeOps, callID)
		}
	}
	if event.Kind == EventUsageRecorded {
		m.usage.add(event)
	}
	for i := range m.shell.tabs {
		if m.shell.tabs[i].ID != sessionID {
			continue
		}
		m.shell.tabs[i].Transcript = append(m.shell.tabs[i].Transcript, event)
		return
	}
}

func (m *model) finishStreamTranscript(sessionID string, done ShellRunDone) {
	if m == nil || m.shell == nil {
		return
	}
	delete(m.activeRuns, sessionID)
	if len(m.activeRuns) == 0 {
		m.activeOps = map[string]string{}
	}
	m.addUsageEvents(done.Events)
	for i := range m.shell.tabs {
		if m.shell.tabs[i].ID != sessionID {
			continue
		}
		if done.Err != nil {
			m.shell.tabs[i].Transcript = append(m.shell.tabs[i].Transcript, errorEvent(sessionID, done.Err))
			return
		}
		m.shell.tabs[i].Transcript = append(m.shell.tabs[i].Transcript, done.Events...)
		return
	}
}

func (m *model) addUsageEvents(events []TranscriptEvent) {
	if m == nil {
		return
	}
	for _, event := range events {
		if event.Kind == EventUsageRecorded {
			m.usage.add(event)
		}
	}
}

func streamActivity(event TranscriptEvent) string {
	switch event.Kind {
	case EventAskDelta:
		return "agent streaming"
	case EventThinking:
		return "agent thinking"
	case EventOperationStarted:
		return "operation running"
	case EventProcessStarted, EventProcessOutput:
		return "process running"
	default:
		return "running"
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

	available := height - fixedHeight
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
		wrapped := wrap.Render(renderTimelineLine(line, width))
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
	if state, ok := slashCompletionQuery(tab.InputBuffer); ok {
		query := ResourceSearchQuery{Text: state.Query, Limit: 6, Kinds: []ResourceKind{ResourceCommand}, PrefixMode: "slash"}
		if state.Kind == completionOption {
			query.PrefixMode = "slash-option"
			query.CommandPath = state.CommandPath
		}
		results, err := m.shell.SearchResources(context.Background(), query)
		if err != nil {
			m.mention = MentionState{}
			return
		}
		state.Results = results
		m.mention = state
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
	m.mention = MentionState{Open: true, Kind: completionMention, Query: query, Results: results}
}

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
	footerStyle = lipgloss.NewStyle().Foreground(monokaiMuted).Padding(0, 1)
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
		lines = append(lines, prefix+subtleStyle.Render("["+string(result.Kind)+"] ")+style.Render(icon+result.Label)+detail)
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
	updated, cmd := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
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

type usageTotals struct {
	inputTokens      int64
	cacheWriteTokens int64
	cachedTokens     int64
	outputTokens     int64
	reasoningTokens  int64
	totalTokens      int64
	cost             float64
	currency         string
}

func (u *usageTotals) add(event TranscriptEvent) {
	if u == nil || event.Kind != EventUsageRecorded {
		return
	}
	u.inputTokens += parseIntData(event.Data, "input_tokens")
	u.cacheWriteTokens += parseIntData(event.Data, "cache_write_tokens")
	u.cachedTokens += parseIntData(event.Data, "cached_tokens")
	u.outputTokens += parseIntData(event.Data, "output_tokens")
	u.reasoningTokens += parseIntData(event.Data, "reasoning_tokens")
	u.totalTokens += parseIntData(event.Data, "total_tokens")
	u.cost += parseFloatData(event.Data, "cost")
	if currency := strings.TrimSpace(event.Data["currency"]); currency != "" {
		u.currency = currency
	}
}

func (u usageTotals) summary() string {
	parts := []string{}
	input := u.inputTokens + u.cacheWriteTokens
	if input > 0 {
		parts = append(parts, "in "+formatCompactInt(input))
	}
	if u.cachedTokens > 0 {
		parts = append(parts, "cached "+formatCompactInt(u.cachedTokens))
	}
	if u.outputTokens > 0 {
		parts = append(parts, "out "+formatCompactInt(u.outputTokens))
	}
	if u.reasoningTokens > 0 {
		parts = append(parts, "reason "+formatCompactInt(u.reasoningTokens))
	}
	if u.totalTokens > 0 && len(parts) == 0 {
		parts = append(parts, "total "+formatCompactInt(u.totalTokens))
	}
	if u.cost > 0 {
		parts = append(parts, formatUsageCost(u.cost, u.currency))
	}
	if len(parts) == 0 {
		return "usage --"
	}
	return "usage " + strings.Join(parts, "  ")
}

func usageSummaryFromData(data map[string]string) string {
	var totals usageTotals
	totals.add(TranscriptEvent{Kind: EventUsageRecorded, Data: data})
	return totals.summary()
}

func parseIntData(data map[string]string, key string) int64 {
	value := strings.TrimSpace(data[key])
	if value == "" {
		return 0
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil {
		return int64(parsed + 0.5)
	}
	return 0
}

func parseFloatData(data map[string]string, key string) float64 {
	value := strings.TrimSpace(data[key])
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func formatCompactInt(value int64) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	case value >= 10_000:
		return fmt.Sprintf("%.0fk", float64(value)/1_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func formatUsageCost(cost float64, currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || currency == "USD" {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("%.4f %s", cost, currency)
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
	box := promptBoxStyle.Width(width).Render(prompt)
	footer := footerStyle.Width(width).Align(lipgloss.Right).Render(m.usage.summary())
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
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
