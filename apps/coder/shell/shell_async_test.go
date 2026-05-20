package codershell

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestShellAskEnterSubmitsImmediatelyAndClearsInput(t *testing.T) {
	client := &asyncAskTestClient{}
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project", goVersion: "go1.25"}, client, "test")
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeAsk
	tab.InputBuffer = "explain this code"

	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("Update returned nil command, want async ask command")
	}
	m, ok := updated.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", updated)
	}

	tab = m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil after submit")
	}
	if tab.InputBuffer != "" {
		t.Fatalf("InputBuffer = %q, want empty", tab.InputBuffer)
	}
	if countTranscriptKind(tab.Transcript, EventAskSubmitted) != 1 {
		t.Fatalf("ask submitted count = %d, want 1", countTranscriptKind(tab.Transcript, EventAskSubmitted))
	}
	if got := lastTranscriptKind(tab.Transcript); got != EventAskSubmitted {
		t.Fatalf("last transcript kind = %q, want %q", got, EventAskSubmitted)
	}
}

func TestShellAskAsyncResultAppendsWithoutDuplicateSubmit(t *testing.T) {
	client := &asyncAskTestClient{
		events: []TranscriptEvent{
			{ID: "client-submit", Time: time.Now(), Kind: EventAskSubmitted, Summary: "duplicate"},
			{ID: "client-output", Time: time.Now(), Kind: EventAskOutput, Summary: "**done**"},
			{ID: "client-complete", Time: time.Now(), Kind: EventCommandComplete, Summary: "completed"},
		},
	}
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project", goVersion: "go1.25"}, client, "test")
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeAsk
	tab.InputBuffer = "summarize"

	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("Update returned nil command, want async ask command")
	}
	m = updated.(model)
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(model)

	tab = m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil after result")
	}
	if countTranscriptKind(tab.Transcript, EventAskSubmitted) != 1 {
		t.Fatalf("ask submitted count = %d, want 1", countTranscriptKind(tab.Transcript, EventAskSubmitted))
	}
	if countTranscriptKind(tab.Transcript, EventAskOutput) != 1 {
		t.Fatalf("ask output count = %d, want 1", countTranscriptKind(tab.Transcript, EventAskOutput))
	}
	if client.lastRequest.Text != "summarize" {
		t.Fatalf("request text = %q, want summarize", client.lastRequest.Text)
	}
}

func TestShellAskAsyncErrorAppendsTranscriptError(t *testing.T) {
	client := &asyncAskTestClient{err: errors.New("ask failed")}
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project", goVersion: "go1.25"}, client, "test")
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeAsk
	tab.InputBuffer = "fail"

	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("Update returned nil command, want async ask command")
	}
	m = updated.(model)
	updated, _ = m.Update(cmd())
	m = updated.(model)

	tab = m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil after error")
	}
	if got := lastTranscriptKind(tab.Transcript); got != EventError {
		t.Fatalf("last transcript kind = %q, want %q", got, EventError)
	}
}

func TestShellStreamingAskUpdatesBeforeCompletion(t *testing.T) {
	events := make(chan TranscriptEvent, 4)
	done := make(chan ShellRunDone, 1)
	client := &streamingAskTestClient{stream: ShellRunStream{Events: events, Done: done}}
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project", goVersion: "go1.25"}, client, "test")
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeAsk
	tab.InputBuffer = "stream"

	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("Update returned nil command, want stream command")
	}
	m = updated.(model)
	tab = m.shell.ActiveTab()
	if countTranscriptKind(tab.Transcript, EventAskSubmitted) != 1 {
		t.Fatalf("ask submitted count = %d, want 1", countTranscriptKind(tab.Transcript, EventAskSubmitted))
	}

	events <- TranscriptEvent{ID: "delta-1", Kind: EventAskDelta, Summary: "hello"}
	updated, cmd = m.Update(cmd())
	if cmd == nil {
		t.Fatal("stream event update returned nil command, want next stream command")
	}
	m = updated.(model)
	tab = m.shell.ActiveTab()
	if countTranscriptKind(tab.Transcript, EventAskDelta) != 1 {
		t.Fatalf("ask delta count = %d, want 1", countTranscriptKind(tab.Transcript, EventAskDelta))
	}
	if got := m.observedFacts()[0].Value; got != "agent streaming" {
		t.Fatalf("active fact = %q, want agent streaming", got)
	}

	done <- ShellRunDone{Events: []TranscriptEvent{{ID: "done", Kind: EventCommandComplete, Summary: "ok"}}}
	close(events)
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if len(m.activeRuns) != 0 {
		t.Fatalf("active runs = %#v, want empty", m.activeRuns)
	}
	if countTranscriptKind(m.shell.ActiveTab().Transcript, EventCommandComplete) != 0 {
		t.Fatalf("command complete count = %d, want no visible ask success completion", countTranscriptKind(m.shell.ActiveTab().Transcript, EventCommandComplete))
	}
}

func TestShellStreamingDrainsBufferedEventsBeforeDone(t *testing.T) {
	events := make(chan TranscriptEvent, 4)
	done := make(chan ShellRunDone, 1)
	client := &streamingAskTestClient{stream: ShellRunStream{Events: events, Done: done}}
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project", goVersion: "go1.25"}, client, "test")
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeAsk
	tab.InputBuffer = "list clusters"

	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("Update returned nil command, want stream command")
	}
	m = updated.(model)

	events <- TranscriptEvent{ID: "delta-1", Kind: EventAskDelta, Summary: "Here are your available clusters:\n"}
	events <- TranscriptEvent{ID: "delta-2", Kind: EventAskDelta, Summary: "- prod\n- staging\n"}
	done <- ShellRunDone{Events: []TranscriptEvent{{ID: "done", Kind: EventCommandComplete, Summary: "ok"}}}
	close(events)

	updated, cmd = m.Update(cmd())
	if cmd == nil {
		t.Fatal("first stream event update returned nil command")
	}
	m = updated.(model)
	updated, _ = m.Update(cmd())
	m = updated.(model)

	transcript := m.shell.ActiveTab().Transcript
	if countTranscriptKind(transcript, EventAskDelta) != 2 {
		t.Fatalf("ask delta count = %d, want 2", countTranscriptKind(transcript, EventAskDelta))
	}
	content := m.timelineContent(100)
	for _, want := range []string{"Here are your available clusters:", "prod", "staging"} {
		if !strings.Contains(content, want) {
			t.Fatalf("timeline missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "ok") || countTranscriptKind(transcript, EventCommandComplete) != 0 {
		t.Fatalf("timeline/transcript includes visible ask success completion:\n%s\n%#v", content, transcript)
	}
}

func TestTimelineCoalescesThinkingChunks(t *testing.T) {
	lines := TimelineLines([]TranscriptEvent{
		{Kind: EventAskSubmitted, Summary: "work"},
		{Kind: EventThinking, Summary: "first token"},
		{Kind: EventThinking, Summary: "second token"},
		{Kind: EventAskDelta, Summary: "done"},
	})
	got := strings.Join(lines, "\n")
	if strings.Count(got, "thinking:") != 1 {
		t.Fatalf("lines = %#v, want one thinking row", lines)
	}
	if !strings.Contains(got, "first token") || !strings.Contains(got, "second token") {
		t.Fatalf("lines = %#v, want coalesced thinking content", lines)
	}
}

func TestRenderTimelineLineUsesMarkdownForAgentOutput(t *testing.T) {
	rendered := renderTimelineLine("agent: - **done**", 80)
	if !strings.Contains(rendered, "done") {
		t.Fatalf("rendered = %q, want markdown content", rendered)
	}
	if strings.Contains(rendered, "**done**") {
		t.Fatalf("rendered = %q, want markdown rendered not raw markers", rendered)
	}
}

func TestRenderTimelineLineUsesMarkdownForThinking(t *testing.T) {
	rendered := renderTimelineLine("thinking: - **checking**", 80)
	if !strings.Contains(rendered, "checking") {
		t.Fatalf("rendered = %q, want thinking markdown content", rendered)
	}
	if strings.Contains(rendered, "**checking**") {
		t.Fatalf("rendered = %q, want markdown rendered not raw markers", rendered)
	}
}

type asyncAskTestClient struct {
	events      []TranscriptEvent
	err         error
	lastRequest AskRequest
}

func (c *asyncAskTestClient) CreateSession(ctx context.Context, req CreateSessionRequest) (SessionInfo, error) {
	return SessionInfo{ID: "session-1", CWD: req.CWD}, nil
}

func (c *asyncAskTestClient) CloseSession(ctx context.Context, sessionID string) error {
	return nil
}

func (c *asyncAskTestClient) SubmitCommand(ctx context.Context, sessionID string, req CommandRequest) ([]TranscriptEvent, error) {
	return nil, nil
}

func (c *asyncAskTestClient) SubmitAsk(ctx context.Context, sessionID string, req AskRequest) ([]TranscriptEvent, error) {
	c.lastRequest = req
	return c.events, c.err
}

func (c *asyncAskTestClient) SubmitSlash(ctx context.Context, sessionID string, req SlashRequest) ([]TranscriptEvent, error) {
	return nil, nil
}

func (c *asyncAskTestClient) ChangeCWD(ctx context.Context, sessionID string, path string) (CWDResult, error) {
	return CWDResult{CWD: path}, nil
}

func (c *asyncAskTestClient) ResourceSearch(ctx context.Context, sessionID string, query ResourceSearchQuery) ([]ResourceSearchResult, error) {
	return nil, nil
}

type streamingAskTestClient struct {
	asyncAskTestClient
	stream ShellRunStream
}

func (c *streamingAskTestClient) SubmitAskStream(ctx context.Context, sessionID string, req AskRequest) (ShellRunStream, error) {
	c.lastRequest = req
	return c.stream, nil
}

func (c *streamingAskTestClient) SubmitCommandStream(context.Context, string, CommandRequest) (ShellRunStream, error) {
	return ShellRunStream{}, errors.New("unexpected command stream")
}

func (c *streamingAskTestClient) SubmitSlashStream(context.Context, string, SlashRequest) (ShellRunStream, error) {
	return ShellRunStream{}, errors.New("unexpected slash stream")
}

func countTranscriptKind(events []TranscriptEvent, kind TranscriptKind) int {
	count := 0
	for _, event := range events {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

func lastTranscriptKind(events []TranscriptEvent) TranscriptKind {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1].Kind
}
