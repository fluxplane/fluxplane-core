package codershell

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/command"
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
	status := initialStatus(sharedSystem, path, opts)
	programOpts := []tea.ProgramOption{}
	if opts.In != nil {
		programOpts = append(programOpts, tea.WithInput(opts.In))
	}
	if opts.Out != nil {
		programOpts = append(programOpts, tea.WithOutput(opts.Out))
	}
	client := newClient(sharedSystem, opts)
	connection := connectionDescription(client, opts.Connect)
	_, err := tea.NewProgram(newModelWithContext(ctx, status, client, connection, loadStatusCmd(ctx, sharedSystem, status)), programOpts...).Run()
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

type model struct {
	status         shellStatus
	width          int
	height         int
	shell          *ShellObject
	mention        MentionState
	mentionSeq     int
	mentionCancel  context.CancelFunc
	timeline       viewport.Model
	timelinePinned bool
	timelineCache  *timelineRenderCache
	activeRuns     map[string]string
	activeRunKinds map[string]TranscriptKind
	activeRunKeys  map[string]string
	activeCancels  map[string]context.CancelFunc
	canceledRuns   map[string]string
	activeOps      map[string]string
	usage          usageTotals
	ctx            context.Context
	cancel         context.CancelFunc
	statusCmd      tea.Cmd
}

type askSubmittedMsg struct {
	sessionID string
	runKey    string
	events    []TranscriptEvent
	err       error
}

type shellStreamEventsMsg struct {
	sessionID string
	runKey    string
	stream    ShellRunStream
	events    []TranscriptEvent
}

type shellStreamDoneMsg struct {
	sessionID string
	runKey    string
	done      ShellRunDone
}

type mentionRefreshMsg struct {
	seq     int
	input   string
	state   MentionState
	results []ResourceSearchResult
	err     error
}

const (
	mentionDebounceDelay  = 80 * time.Millisecond
	streamFrameDelay      = 16 * time.Millisecond
	streamMaxEventsPerMsg = 64
)

func newModel(status shellStatus, client ShellClient, connection string) model {
	return newModelWithContext(context.Background(), status, client, connection, nil)
}

func newModelWithContext(ctx context.Context, status shellStatus, client ShellClient, connection string, statusCmd tea.Cmd) model {
	if ctx == nil {
		ctx = context.Background()
	}
	modelCtx, cancel := context.WithCancel(ctx)
	state, err := NewShellObject(modelCtx, ShellObjectOptions{Client: client, CWD: status.cwd, Connection: connection})
	if err != nil {
		state = &ShellObject{client: client, tabs: []TabSession{{
			ID:          disconnectedSessionID,
			Label:       "1",
			CWD:         status.cwd,
			InputMode:   defaultInputMode,
			InputCursor: -1,
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
	timeline := viewport.New(viewport.WithWidth(1), viewport.WithHeight(1))
	timeline.MouseWheelEnabled = true
	m := model{
		status:         status,
		shell:          state,
		timeline:       timeline,
		timelinePinned: true,
		timelineCache:  newTimelineRenderCache(),
		activeRuns:     map[string]string{},
		activeRunKinds: map[string]TranscriptKind{},
		activeRunKeys:  map[string]string{},
		activeCancels:  map[string]context.CancelFunc{},
		canceledRuns:   map[string]string{},
		activeOps:      map[string]string{},
		ctx:            modelCtx,
		cancel:         cancel,
		statusCmd:      statusCmd,
	}
	m.syncTimelineViewport(true)
	return m
}

func (m model) Init() tea.Cmd { return m.statusCmd }

func isPlainTextKey(key tea.Key) bool {
	return !key.Mod.Contains(tea.ModAlt | tea.ModCtrl | tea.ModMeta | tea.ModHyper | tea.ModSuper)
}

func hasNonTextModifier(key tea.Key, keystroke string) bool {
	return !isPlainTextKey(key) ||
		strings.Contains(keystroke, "alt+") ||
		strings.Contains(keystroke, "ctrl+") ||
		strings.Contains(keystroke, "meta+") ||
		strings.Contains(keystroke, "hyper+") ||
		strings.Contains(keystroke, "super+")
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncTimelineViewport(false)
		return m, nil
	case shellStatusLoadedMsg:
		if msg.err == nil {
			m.status = msg.status
		}
		m.syncTimelineViewport(false)
		return m, nil
	case mentionRefreshMsg:
		if msg.seq != m.mentionSeq {
			return m, nil
		}
		if msg.err != nil {
			m.mention = MentionState{}
			return m, nil
		}
		state := msg.state
		if state.Kind == completionCommand {
			if len(msg.results) == 0 || slashCommandInputComplete(msg.input, state.Query, msg.results) {
				m.mention = MentionState{}
				return m, nil
			}
		}
		if state.Kind == completionOption && len(msg.results) == 0 {
			m.mention = MentionState{}
			return m, nil
		}
		state.Results = msg.results
		state.Loading = false
		m.mention = state
		return m, nil
	case askSubmittedMsg:
		m.appendAsyncTranscript(msg.sessionID, msg.runKey, msg.events, msg.err)
		m.syncTimelineViewport(msg.sessionID == m.activeSessionID())
		return m, nil
	case shellStreamEventsMsg:
		m.appendStreamTranscripts(msg.sessionID, msg.runKey, msg.events)
		m.syncTimelineViewport(msg.sessionID == m.activeSessionID())
		return m, waitShellStream(msg.sessionID, msg.runKey, msg.stream)
	case shellStreamDoneMsg:
		m.finishStreamTranscript(msg.sessionID, msg.runKey, msg.done)
		m.syncTimelineViewport(msg.sessionID == m.activeSessionID())
		return m, nil
	case tea.MouseWheelMsg:
		m.syncTimelineViewport(false)
		var cmd tea.Cmd
		m.timeline, cmd = m.timeline.Update(msg)
		m.timelinePinned = m.timeline.AtBottom()
		return m, cmd
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseMotionMsg:
		return m, nil
	case tea.KeyPressMsg:
		tab := m.shell.ActiveTab()
		key := msg.Key()
		switch {
		case key.Code == tea.KeyPgUp:
			m.syncTimelineViewport(false)
			m.timeline.PageUp()
			m.timelinePinned = false
			return m, nil
		case key.Code == tea.KeyPgDown:
			m.syncTimelineViewport(false)
			m.timeline.PageDown()
			m.timelinePinned = m.timeline.AtBottom()
			return m, nil
		case key.Code == tea.KeyHome && key.Mod.Contains(tea.ModCtrl):
			m.syncTimelineViewport(false)
			m.timeline.GotoTop()
			m.timelinePinned = false
			return m, nil
		case key.Code == tea.KeyEnd && key.Mod.Contains(tea.ModCtrl):
			m.syncTimelineViewport(false)
			m.timeline.GotoBottom()
			m.timelinePinned = true
			return m, nil
		case key.Code == tea.KeyHome || msg.Keystroke() == "ctrl+a":
			m.shell.MoveInputCursorStart()
			m.clearMention()
			return m, nil
		case key.Code == tea.KeyEnd || msg.Keystroke() == "ctrl+e":
			m.shell.MoveInputCursorEnd()
			return m, m.queueMentionRefresh()
		case key.Code == tea.KeyLeft && key.Mod.Contains(tea.ModAlt):
			m.shell.MoveInputCursorWordLeft()
			m.clearMention()
			return m, nil
		case key.Code == tea.KeyRight && key.Mod.Contains(tea.ModAlt):
			m.shell.MoveInputCursorWordRight()
			return m, m.queueMentionRefresh()
		case key.Code == tea.KeyLeft:
			m.shell.MoveInputCursorLeft()
			m.clearMention()
			return m, nil
		case key.Code == tea.KeyRight:
			m.shell.MoveInputCursorRight()
			return m, m.queueMentionRefresh()
		case msg.Keystroke() == "ctrl+u":
			m.syncTimelineViewport(false)
			m.timeline.HalfPageUp()
			m.timelinePinned = false
			return m, nil
		case msg.Keystroke() == "ctrl+d":
			m.syncTimelineViewport(false)
			m.timeline.HalfPageDown()
			m.timelinePinned = m.timeline.AtBottom()
			return m, nil
		case msg.Keystroke() == "ctrl+c" || key.Code == tea.KeyEscape:
			if key.Code == tea.KeyEscape && m.mention.Open {
				m.clearMention()
				return m, nil
			}
			if m.cancelActiveRuns() {
				m.syncTimelineViewport(true)
				return m, nil
			}
			return m, m.quitCmd()
		case msg.Keystroke() == "ctrl+t":
			cwd := m.status.cwd
			if tab != nil && strings.TrimSpace(tab.CWD) != "" {
				cwd = tab.CWD
			}
			_, _ = m.shell.NewTab(context.Background(), cwd)
			m.clearMention()
			m.syncTimelineViewport(true)
			return m, nil
		case key.Text != "":
			value := key.Text
			if tab != nil && tab.inputCursor() == 0 && value == "!" && tab.InputMode == InputModeAsk {
				tab.InputMode = InputModeShell
				tab.resetHistoryNavigation()
				m.clearMention()
				return m, nil
			}
			if tab != nil && tab.inputCursor() == 0 && value == "?" && tab.InputMode == InputModeShell {
				tab.InputMode = InputModeAsk
				tab.resetHistoryNavigation()
				m.clearMention()
				return m, nil
			}
			if value == "q" && tab != nil && tab.InputBuffer == "" {
				if m.cancelActiveRuns() {
					m.syncTimelineViewport(true)
					return m, nil
				}
				return m, m.quitCmd()
			}
			if len(value) == 1 && value[0] >= '1' && value[0] <= '9' && key.Mod.Contains(tea.ModAlt) {
				m.shell.SelectTab(int(value[0] - '1'))
				m.syncTimelineViewport(true)
				return m, nil
			}
			if hasNonTextModifier(key, msg.Keystroke()) {
				return m, nil
			}
			m.shell.AppendInput(value)
			return m, m.queueMentionRefresh()
		case key.Code == tea.KeySpace:
			m.shell.AppendInput(" ")
			m.clearMention()
			return m, nil
		case key.Code == tea.KeyBackspace || msg.Keystroke() == "ctrl+h":
			m.shell.BackspaceInput()
			return m, m.queueMentionRefresh()
		case key.Code == tea.KeyDelete:
			m.shell.DeleteInput()
			return m, m.queueMentionRefresh()
		case msg.Keystroke() == "ctrl+k":
			m.shell.DeleteInputToEnd()
			return m, m.queueMentionRefresh()
		case msg.Keystroke() == "ctrl+w":
			m.shell.DeleteInputPreviousWord()
			return m, m.queueMentionRefresh()
		case key.Code == tea.KeyEnter || key.Code == tea.KeyReturn:
			if m.acceptMentionSelection() {
				m.syncTimelineViewport(true)
				return m, nil
			}
			m.clearMention()
			if cmd := m.submitActiveInputAsync(); cmd != nil {
				m.syncTimelineViewport(true)
				return m, cmd
			}
			m.syncTimelineViewport(true)
			return m, nil
		case key.Code == tea.KeyUp:
			if m.mention.Open && m.mention.Index > 0 {
				m.mention.Index--
				return m, nil
			}
			if m.shell.PreviousInputHistory() {
				return m, m.queueMentionRefresh()
			}
			return m, nil
		case key.Code == tea.KeyDown:
			if m.mention.Open && m.mention.Index+1 < len(m.mention.Results) {
				m.mention.Index++
				return m, nil
			}
			if m.shell.NextInputHistory() {
				return m, m.queueMentionRefresh()
			}
			return m, nil
		case key.Code == tea.KeyTab:
			if m.acceptMentionSelection() {
				m.syncTimelineViewport(true)
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *model) acceptMentionSelection() bool {
	if m == nil || m.shell == nil {
		return false
	}
	result, ok := m.mention.activeResult()
	if !ok {
		return false
	}
	tab := m.shell.ActiveTab()
	if tab == nil {
		m.clearMention()
		return false
	}
	switch m.mention.Kind {
	case completionCommand:
		tab.InputBuffer = replaceCommandFragment(tab.InputBuffer, result)
		tab.InputCursor = len([]rune(tab.InputBuffer))
	case completionOption:
		tab.InputBuffer = replaceOptionFragment(tab.InputBuffer, result)
		tab.InputCursor = len([]rune(tab.InputBuffer))
	default:
		tab.InputBuffer = replaceMentionFragment(tab.InputBuffer, result)
		tab.InputCursor = len([]rune(tab.InputBuffer))
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
	m.clearMention()
	return true
}

func (m *model) submitActiveInputAsync() tea.Cmd {
	if m == nil || m.shell == nil {
		return nil
	}
	submission, ok, err := m.shell.startActiveSubmission(m.ctx)
	if err != nil || !ok {
		return nil
	}
	client := m.shell.client
	if m.activeRuns == nil {
		m.activeRuns = map[string]string{}
	}
	if m.activeCancels == nil {
		m.activeCancels = map[string]context.CancelFunc{}
	}
	runCtx, cancel := context.WithCancel(m.ctx)
	m.activeRuns[submission.sessionID] = string(submission.start.Kind)
	if m.activeRunKinds == nil {
		m.activeRunKinds = map[string]TranscriptKind{}
	}
	if m.activeRunKeys == nil {
		m.activeRunKeys = map[string]string{}
	}
	m.activeRunKinds[submission.sessionID] = submission.start.Kind
	m.activeRunKeys[submission.sessionID] = submission.runKey
	m.activeCancels[submission.sessionID] = cancel

	if streaming, ok := client.(StreamingShellClient); ok {
		streamHandle, err := submission.stream(runCtx, streaming)
		if err == nil {
			return waitShellStream(submission.sessionID, submission.runKey, streamHandle)
		}
		if tab := m.shell.ActiveTab(); tab != nil && tab.ID == submission.sessionID {
			tab.Transcript = append(tab.Transcript, errorEvent(submission.sessionID, err))
		}
		cancel()
		delete(m.activeRuns, submission.sessionID)
		delete(m.activeRunKeys, submission.sessionID)
		delete(m.activeCancels, submission.sessionID)
		return nil
	}
	return func() tea.Msg {
		defer cancel()
		if client == nil {
			return askSubmittedMsg{sessionID: submission.sessionID, runKey: submission.runKey, err: fmt.Errorf("shell client is unavailable")}
		}
		events, err := submission.fallback(runCtx)
		return askSubmittedMsg{sessionID: submission.sessionID, runKey: submission.runKey, events: dropSubmittedStartEvent(events, submission.start.Kind), err: err}
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

func waitShellStream(sessionID, runKey string, stream ShellRunStream) tea.Cmd {
	return func() tea.Msg {
		events := make([]TranscriptEvent, 0, streamMaxEventsPerMsg)
		drain := func(first TranscriptEvent) tea.Msg {
			events = append(events, first)
			deadline := time.NewTimer(streamFrameDelay)
			defer deadline.Stop()
			for len(events) < streamMaxEventsPerMsg {
				select {
				case event, ok := <-stream.Events:
					if !ok {
						stream.Events = nil
						return shellStreamEventsMsg{sessionID: sessionID, runKey: runKey, stream: stream, events: events}
					}
					events = append(events, event)
				case <-deadline.C:
					return shellStreamEventsMsg{sessionID: sessionID, runKey: runKey, stream: stream, events: events}
				default:
					return shellStreamEventsMsg{sessionID: sessionID, runKey: runKey, stream: stream, events: events}
				}
			}
			return shellStreamEventsMsg{sessionID: sessionID, runKey: runKey, stream: stream, events: events}
		}
		if stream.Events != nil {
			select {
			case event, ok := <-stream.Events:
				if !ok {
					stream.Events = nil
					break
				}
				return drain(event)
			default:
			}
		}
		switch {
		case stream.Events != nil && stream.Done != nil:
			select {
			case event, ok := <-stream.Events:
				if ok {
					return drain(event)
				}
				stream.Events = nil
				done, ok := <-stream.Done
				if !ok {
					return shellStreamDoneMsg{sessionID: sessionID, runKey: runKey}
				}
				return shellStreamDoneMsg{sessionID: sessionID, runKey: runKey, done: done}
			case done, ok := <-stream.Done:
				if !ok {
					return shellStreamDoneMsg{sessionID: sessionID, runKey: runKey}
				}
				return shellStreamDoneMsg{sessionID: sessionID, runKey: runKey, done: done}
			}
		case stream.Events != nil:
			event, ok := <-stream.Events
			if !ok {
				return shellStreamDoneMsg{sessionID: sessionID, runKey: runKey}
			}
			return drain(event)
		case stream.Done == nil:
			return shellStreamDoneMsg{sessionID: sessionID, runKey: runKey}
		default:
			done, ok := <-stream.Done
			if !ok {
				return shellStreamDoneMsg{sessionID: sessionID, runKey: runKey}
			}
			return shellStreamDoneMsg{sessionID: sessionID, runKey: runKey, done: done}
		}
	}
}

func (m *model) appendStreamTranscripts(sessionID, runKey string, events []TranscriptEvent) {
	for _, event := range events {
		m.appendStreamTranscript(sessionID, runKey, event)
	}
}

func (m *model) appendAsyncTranscript(sessionID, runKey string, events []TranscriptEvent, err error) {
	if m == nil || m.shell == nil {
		return
	}
	if m.canceledRuns[sessionID] == runKey {
		delete(m.canceledRuns, sessionID)
		return
	}
	if m.activeRunKeys[sessionID] != runKey {
		return
	}
	isAskRun := m.activeRunKinds[sessionID] == EventAskSubmitted
	delete(m.activeRuns, sessionID)
	delete(m.activeRunKinds, sessionID)
	delete(m.activeRunKeys, sessionID)
	if cancel := m.activeCancels[sessionID]; cancel != nil {
		cancel()
	}
	delete(m.activeCancels, sessionID)
	if isAskRun {
		events = dropSuccessfulCompletionEvents(events)
	}
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

func (m *model) appendStreamTranscript(sessionID, runKey string, event TranscriptEvent) {
	if m == nil || m.shell == nil {
		return
	}
	if runKey != "" && (m.canceledRuns[sessionID] == runKey || m.activeRunKeys[sessionID] != runKey) {
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

func (m *model) finishStreamTranscript(sessionID, runKey string, done ShellRunDone) {
	if m == nil || m.shell == nil {
		return
	}
	if m.canceledRuns[sessionID] == runKey {
		delete(m.canceledRuns, sessionID)
		return
	}
	if m.activeRunKeys[sessionID] != runKey {
		return
	}
	isAskRun := m.activeRunKinds[sessionID] == EventAskSubmitted
	delete(m.activeRuns, sessionID)
	delete(m.activeRunKinds, sessionID)
	delete(m.activeRunKeys, sessionID)
	if cancel := m.activeCancels[sessionID]; cancel != nil {
		cancel()
	}
	delete(m.activeCancels, sessionID)
	if len(m.activeRuns) == 0 {
		m.activeOps = map[string]string{}
	}
	if isAskRun {
		done.Events = dropSuccessfulCompletionEvents(done.Events)
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

func dropSuccessfulCompletionEvents(events []TranscriptEvent) []TranscriptEvent {
	if len(events) == 0 {
		return nil
	}
	out := events[:0]
	for _, event := range events {
		if event.Kind == EventCommandComplete && isSuccessfulCompletionSummary(event.Summary) {
			continue
		}
		out = append(out, event)
	}
	return out
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

func (m *model) cancelActiveRuns() bool {
	if m == nil || len(m.activeCancels) == 0 {
		return false
	}
	now := time.Now()
	for sessionID, cancel := range m.activeCancels {
		if m.canceledRuns == nil {
			m.canceledRuns = map[string]string{}
		}
		m.canceledRuns[sessionID] = m.activeRunKeys[sessionID]
		if cancel != nil {
			cancel()
		}
		if m.shell != nil {
			for i := range m.shell.tabs {
				if m.shell.tabs[i].ID == sessionID {
					m.shell.tabs[i].Transcript = append(m.shell.tabs[i].Transcript, TranscriptEvent{
						ID:        newEventID("cancel"),
						SessionID: sessionID,
						Time:      now,
						Kind:      EventRunCanceled,
						Summary:   "run canceled",
					})
				}
			}
		}
		delete(m.activeCancels, sessionID)
		delete(m.activeRuns, sessionID)
		delete(m.activeRunKinds, sessionID)
		delete(m.activeRunKeys, sessionID)
	}
	if len(m.activeRuns) == 0 {
		m.activeOps = map[string]string{}
	}
	return true
}

func (m *model) quitCmd() tea.Cmd {
	var shell *ShellObject
	if m != nil {
		shell = m.shell
		if m.cancel != nil {
			m.cancel()
		}
		for _, cancel := range m.activeCancels {
			if cancel != nil {
				cancel()
			}
		}
	}
	return tea.Batch(closeShellSessionsCmd(shell), tea.Quit)
}

func closeShellSessionsCmd(shell *ShellObject) tea.Cmd {
	return func() tea.Msg {
		if shell == nil || shell.client == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		seen := map[string]bool{}
		for _, tab := range shell.tabs {
			if tab.ID == "" || tab.ID == disconnectedSessionID || seen[tab.ID] {
				continue
			}
			seen[tab.ID] = true
			_ = shell.client.CloseSession(ctx, tab.ID)
		}
		return nil
	}
}

func (m *model) clearMention() {
	if m == nil {
		return
	}
	if m.mentionCancel != nil {
		m.mentionCancel()
		m.mentionCancel = nil
	}
	m.mentionSeq++
	m.mention = MentionState{}
}

func (m *model) queueMentionRefresh() tea.Cmd {
	if m == nil || m.shell == nil {
		return nil
	}
	if m.mentionCancel != nil {
		m.mentionCancel()
		m.mentionCancel = nil
	}
	tab := m.shell.ActiveTab()
	state, query, sessionID, input, ok := m.mentionSearch(tab)
	if !ok {
		m.clearMention()
		return nil
	}
	previous := MentionState{}
	if m.mention.Open && m.mention.Kind == state.Kind && m.mention.Query == state.Query && m.mention.CommandPath == state.CommandPath {
		previous = m.mention
	}
	state.Results = previous.Results
	state.Index = previous.Index
	state.Loading = true
	m.mention = state
	m.mentionSeq++
	seq := m.mentionSeq
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	searchCtx, cancel := context.WithCancel(ctx)
	m.mentionCancel = cancel
	client := m.shell.client
	return func() tea.Msg {
		timer := time.NewTimer(mentionDebounceDelay)
		defer timer.Stop()
		select {
		case <-searchCtx.Done():
			return mentionRefreshMsg{seq: seq, err: searchCtx.Err()}
		case <-timer.C:
		}
		if client == nil {
			return mentionRefreshMsg{seq: seq, err: fmt.Errorf("shell client is unavailable")}
		}
		results, err := client.ResourceSearch(searchCtx, sessionID, query)
		return mentionRefreshMsg{seq: seq, input: input, state: state, results: results, err: err}
	}
}

func (m *model) mentionSearch(tab *TabSession) (MentionState, ResourceSearchQuery, string, string, bool) {
	if m == nil || tab == nil {
		return MentionState{}, ResourceSearchQuery{}, "", "", false
	}
	input := tab.InputBuffer
	if state, ok := slashCompletionQuery(input); ok {
		query := ResourceSearchQuery{Text: state.Query, Limit: 6, Kinds: []ResourceKind{ResourceCommand}, PrefixMode: "slash", CWD: tab.CWD}
		if state.Kind == completionOption {
			query.PrefixMode = "slash-option"
			query.CommandPath = state.CommandPath
		}
		return state, query, tab.ID, input, true
	}
	queryText, ok := mentionQuery(input)
	if !ok {
		return MentionState{}, ResourceSearchQuery{}, "", "", false
	}
	return MentionState{Open: true, Kind: completionMention, Query: queryText}, ResourceSearchQuery{Text: queryText, Limit: 6, Mention: true, CWD: tab.CWD}, tab.ID, input, true
}

func slashCommandInputComplete(input string, query string, results []ResourceSearchResult) bool {
	if !strings.HasSuffix(input, " ") && !strings.HasSuffix(input, "\t") {
		return false
	}
	query = normalizeSlashCompletionText(query)
	for _, result := range results {
		if normalizeSlashCompletionText(result.Label) == query {
			return true
		}
	}
	return false
}

func normalizeSlashCompletionText(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "/"))
	value = strings.ReplaceAll(value, "/", " ")
	return strings.Join(strings.Fields(value), " ")
}
