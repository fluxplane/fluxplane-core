package codershell

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fluxplane/engine/runtime/system"
	"github.com/fluxplane/engine/runtime/systemtest"
)

func TestInitialStatusDoesNotProbeWorkspaceBeforeFirstPaint(t *testing.T) {
	sys := &statusProbeSystem{MemorySystem: systemtest.NewMemory()}
	start := time.Now()
	status := initialStatus(sys, "/workspace", Options{Provider: "codex", Model: "gpt-5.5"})
	m := newModel(status, NewFakeClient(), "fake")

	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("initial status/model took %s, want no blocking probe", elapsed)
	}
	if got := atomic.LoadInt32(&sys.processCalls); got != 0 {
		t.Fatalf("Process() calls before first paint = %d, want 0", got)
	}
	if !m.status.loading {
		t.Fatal("initial model status is not loading")
	}
	if view := m.renderStatus(100); !strings.Contains(view, "loading workspace...") {
		t.Fatalf("status view missing loading copy:\n%s", view)
	}
}

func TestInitialStatusUsesWorkspaceRootWhenPathOmitted(t *testing.T) {
	sys := systemtest.NewMemory()
	status := initialStatus(sys, "", Options{})
	if status.cwd != sys.Workspace().Root() {
		t.Fatalf("cwd = %q, want workspace root %q", status.cwd, sys.Workspace().Root())
	}
	m := newModel(status, NewFakeClient(), "fake")
	if got := m.shell.ActiveTab().CWD; got != sys.Workspace().Root() {
		t.Fatalf("active tab cwd = %q, want workspace root %q", got, sys.Workspace().Root())
	}
}

func TestAsyncCompletionCancelsStaleSearchAndDoesNotBlockTyping(t *testing.T) {
	client := newControlledCompletionClient()
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project"}, client, "test")
	tab := m.shell.ActiveTab()
	tab.InputBuffer = "/c"

	start := time.Now()
	updated, cmd1 := m.Update(tea.KeyPressMsg(tea.Key{Text: "o", Code: 'o'}))
	if cmd1 == nil {
		t.Fatal("first completion command is nil")
	}
	if elapsed := time.Since(start); elapsed > 40*time.Millisecond {
		t.Fatalf("keypress update took %s, want debounced async completion", elapsed)
	}
	if got := client.callCount(); got != 0 {
		t.Fatalf("ResourceSearch calls during keypress = %d, want 0", got)
	}
	m = updated.(model)

	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- cmd1() }()
	select {
	case got := <-client.calls:
		if got != "co" {
			t.Fatalf("first query = %q, want co", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first completion query did not start")
	}

	updated, cmd2 := m.Update(tea.KeyPressMsg(tea.Key{Text: "d", Code: 'd'}))
	if cmd2 == nil {
		t.Fatal("second completion command is nil")
	}
	m = updated.(model)

	select {
	case msg := <-msgCh:
		updated, _ = m.Update(msg)
		m = updated.(model)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stale completion command did not return after cancellation")
	}
	if !m.mention.Open || !m.mention.Loading || m.mention.Query != "cod" {
		t.Fatalf("stale result changed fresh loading picker: %#v", m.mention)
	}

	updated, _ = m.Update(cmd2())
	m = updated.(model)
	if !m.mention.Open || len(m.mention.Results) != 1 || m.mention.Results[0].Label != "/codex" {
		t.Fatalf("mention = %#v, want fresh /codex result", m.mention)
	}
}

func TestCanceledRunDoesNotDropNewRunCompletionForSameSession(t *testing.T) {
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project"}, NewFakeClient(), "test")
	sessionID := m.shell.ActiveTab().ID
	m.activeRuns[sessionID] = string(EventAskSubmitted)
	m.activeRunKinds[sessionID] = EventAskSubmitted
	m.activeRunKeys[sessionID] = "run-1"
	m.activeCancels[sessionID] = func() {}

	if !m.cancelActiveRuns() {
		t.Fatal("cancelActiveRuns() = false, want true")
	}
	m.activeRuns[sessionID] = string(EventAskSubmitted)
	m.activeRunKinds[sessionID] = EventAskSubmitted
	m.activeRunKeys[sessionID] = "run-2"
	m.appendAsyncTranscript(sessionID, "run-1", []TranscriptEvent{{ID: "late", Kind: EventAskOutput, Summary: "late canceled"}}, nil)
	m.appendAsyncTranscript(sessionID, "run-2", []TranscriptEvent{{ID: "fresh", Kind: EventAskOutput, Summary: "fresh result"}}, nil)

	text := transcriptText(m.shell.ActiveTab().Transcript)
	if strings.Contains(text, "late canceled") {
		t.Fatalf("transcript includes late canceled result:\n%s", text)
	}
	if !strings.Contains(text, "fresh result") {
		t.Fatalf("transcript missing fresh result:\n%s", text)
	}
}

func TestActiveRunCancelPropagatesAndLateResultIsIgnored(t *testing.T) {
	client := &cancellableAskClient{canceled: make(chan struct{}, 1)}
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project"}, client, "test")
	tab := m.shell.ActiveTab()
	tab.InputMode = InputModeAsk
	tab.InputBuffer = "keep working"

	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("submit command is nil")
	}
	m = updated.(model)
	if len(m.activeRuns) != 1 {
		t.Fatalf("active runs = %#v, want one", m.activeRuns)
	}

	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updated.(model)
	if len(m.activeRuns) != 0 {
		t.Fatalf("active runs after cancel = %#v, want empty", m.activeRuns)
	}

	msg := cmd()
	select {
	case <-client.canceled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("SubmitAsk context was not canceled")
	}
	updated, _ = m.Update(msg)
	m = updated.(model)

	if got := countTranscriptKind(m.shell.ActiveTab().Transcript, EventRunCanceled); got != 1 {
		t.Fatalf("cancel events after late canceled result = %d, want only local cancellation", got)
	}
	if got := countTranscriptKind(m.shell.ActiveTab().Transcript, EventError); got != 0 {
		t.Fatalf("error events after late canceled result = %d, want none", got)
	}
}

func TestCloseShellSessionsCmdClosesAllOpenSessions(t *testing.T) {
	client := &closeTrackingClient{}
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project"}, client, "test")
	if _, err := m.shell.NewTab(context.Background(), "/workspace/next"); err != nil {
		t.Fatalf("NewTab() error = %v", err)
	}

	closeShellSessionsCmd(m.shell)()

	if got := client.closedIDs(); len(got) != 2 {
		t.Fatalf("closed sessions = %#v, want two", got)
	}
}

func TestShellObjectAndModelShareSubmissionStartBehavior(t *testing.T) {
	syncClient := &recordingSubmitClient{}
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: syncClient, CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	shell.AppendInput("explain")
	if err := shell.SubmitActiveInput(context.Background()); err != nil {
		t.Fatalf("SubmitActiveInput() error = %v", err)
	}

	asyncClient := &recordingSubmitClient{}
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project"}, asyncClient, "test")
	tab := m.shell.ActiveTab()
	tab.InputMode = InputModeAsk
	tab.InputBuffer = "explain"
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("model submit command is nil")
	}
	m = updated.(model)
	updated, _ = m.Update(cmd())
	m = updated.(model)

	if syncClient.askText != asyncClient.askText {
		t.Fatalf("ask text sync=%q async=%q", syncClient.askText, asyncClient.askText)
	}
	if syncClient.contextItems != asyncClient.contextItems {
		t.Fatalf("context items sync=%d async=%d", syncClient.contextItems, asyncClient.contextItems)
	}
	if countTranscriptKind(shell.ActiveTab().Transcript, EventAskSubmitted) != 1 {
		t.Fatalf("sync ask submitted count = %d, want 1", countTranscriptKind(shell.ActiveTab().Transcript, EventAskSubmitted))
	}
	if countTranscriptKind(m.shell.ActiveTab().Transcript, EventAskSubmitted) != 1 {
		t.Fatalf("model ask submitted count = %d, want 1", countTranscriptKind(m.shell.ActiveTab().Transcript, EventAskSubmitted))
	}
}

type statusProbeSystem struct {
	*systemtest.MemorySystem
	processCalls int32
}

func (s *statusProbeSystem) Process() system.ProcessManager {
	atomic.AddInt32(&s.processCalls, 1)
	time.Sleep(200 * time.Millisecond)
	return nil
}

type controlledCompletionClient struct {
	asyncAskTestClient
	calls chan string
}

func newControlledCompletionClient() *controlledCompletionClient {
	return &controlledCompletionClient{calls: make(chan string, 4)}
}

func (c *controlledCompletionClient) ResourceSearch(ctx context.Context, sessionID string, query ResourceSearchQuery) ([]ResourceSearchResult, error) {
	c.calls <- query.Text
	if query.Text == "co" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []ResourceSearchResult{{Kind: ResourceCommand, ID: "codex", Label: "/codex", InsertText: "/codex"}}, nil
}

func (c *controlledCompletionClient) callCount() int {
	return len(c.calls)
}

func transcriptText(events []TranscriptEvent) string {
	var b strings.Builder
	for _, event := range events {
		b.WriteString(event.Summary)
		b.WriteByte('\n')
	}
	return b.String()
}

type cancellableAskClient struct {
	asyncAskTestClient
	canceled chan struct{}
}

func (c *cancellableAskClient) SubmitAsk(ctx context.Context, sessionID string, req AskRequest) ([]TranscriptEvent, error) {
	<-ctx.Done()
	select {
	case c.canceled <- struct{}{}:
	default:
	}
	return []TranscriptEvent{{ID: "late", Kind: EventAskOutput, Summary: "late"}}, ctx.Err()
}

type closeTrackingClient struct {
	asyncAskTestClient
	mu     sync.Mutex
	next   int
	closed []string
}

func (c *closeTrackingClient) CreateSession(ctx context.Context, req CreateSessionRequest) (SessionInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	return SessionInfo{ID: fmtSessionID(c.next), CWD: req.CWD}, nil
}

func (c *closeTrackingClient) CloseSession(ctx context.Context, sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = append(c.closed, sessionID)
	return nil
}

func (c *closeTrackingClient) closedIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.closed...)
}

type recordingSubmitClient struct {
	asyncAskTestClient
	askText      string
	contextItems int
}

func (c *recordingSubmitClient) SubmitAsk(ctx context.Context, sessionID string, req AskRequest) ([]TranscriptEvent, error) {
	c.askText = req.Text
	c.contextItems = len(req.Context)
	return []TranscriptEvent{
		{ID: "ask", Kind: EventAskSubmitted, Summary: req.Text},
		{ID: "out", Kind: EventAskOutput, Summary: "ok"},
	}, nil
}

func (c *recordingSubmitClient) SubmitCommand(ctx context.Context, sessionID string, req CommandRequest) ([]TranscriptEvent, error) {
	return nil, errors.New("unexpected command")
}

func fmtSessionID(index int) string {
	return "session-" + strconv.Itoa(index)
}
