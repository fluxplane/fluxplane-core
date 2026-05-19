package codershell

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
