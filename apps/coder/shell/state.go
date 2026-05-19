package codershell

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// InputMode identifies how the active input buffer will be submitted.
type InputMode string

const (
	InputModeShell InputMode = "shell"
	InputModeAsk   InputMode = "ask"
)
const disconnectedSessionID = "session-error"

// ShellObject owns shell state that must stay independent from the TUI layer.
type ShellObject struct {
	client        ShellClient
	connection    string
	contextPolicy ContextPolicy
	tabs          []TabSession
	active        int
}

// ShellObjectOptions configures a new ShellObject.
type ShellObjectOptions struct {
	Client        ShellClient
	CWD           string
	Connection    string
	ContextPolicy ContextPolicy
}

// NewShellObject creates shell state with one client-backed session tab.
func NewShellObject(ctx context.Context, opts ShellObjectOptions) (*ShellObject, error) {
	client := opts.Client
	if client == nil {
		client = NewFakeClient()
	}
	shell := &ShellObject{client: client, connection: strings.TrimSpace(opts.Connection), contextPolicy: defaultContextPolicy(opts.ContextPolicy)}
	if connectionAware, ok := client.(ConnectionDescriber); ok && shell.connection == "" {
		shell.connection = connectionAware.ConnectionDescription()
	}
	if _, err := shell.NewTab(ctx, opts.CWD); err != nil {
		return nil, err
	}
	return shell, nil
}

func defaultContextPolicy(policy ContextPolicy) ContextPolicy {
	if policy.MaxEvents <= 0 {
		policy.MaxEvents = 40
	}
	if policy.MaxBytes <= 0 {
		policy.MaxBytes = 12 * 1024
	}
	return policy
}

func connectionSummary(connection string) string {
	connection = strings.TrimSpace(connection)
	if connection == "" {
		return "session connected"
	}
	return "connected: " + connection
}

func transcriptConnectionEvent(sessionID, connection string) TranscriptEvent {
	return TranscriptEvent{
		ID:        newEventID("client"),
		SessionID: sessionID,
		Time:      time.Now(),
		Kind:      EventClientConnected,
		Summary:   connectionSummary(connection),
		Data:      map[string]string{"connection": strings.TrimSpace(connection)},
	}
}

// Client returns the shell client.
func (s *ShellObject) Client() ShellClient { return s.client }

// Tabs returns a copy of tab state summaries.
func (s *ShellObject) Tabs() []TabSession {
	out := make([]TabSession, len(s.tabs))
	copy(out, s.tabs)
	return out
}

// ActiveIndex returns the active tab index.
func (s *ShellObject) ActiveIndex() int { return s.active }

// ActiveTab returns the active tab.
func (s *ShellObject) ActiveTab() *TabSession {
	if s == nil || s.active < 0 || s.active >= len(s.tabs) {
		return nil
	}
	return &s.tabs[s.active]
}

// NewTab creates a new client session and selects it.
func (s *ShellObject) NewTab(ctx context.Context, cwd string) (*TabSession, error) {
	if s.client == nil {
		s.client = NewFakeClient()
	}
	info, err := s.client.CreateSession(ctx, CreateSessionRequest{CWD: cwd})
	if err != nil {
		return nil, fmt.Errorf("create shell session: %w", err)
	}
	tab := TabSession{
		ID:        info.ID,
		Label:     fmt.Sprintf("%d", len(s.tabs)+1),
		CWD:       strings.TrimSpace(info.CWD),
		InputMode: InputModeShell,
		Transcript: []TranscriptEvent{
			transcriptConnectionEvent(info.ID, s.connection),
		},
	}
	if tab.CWD == "" {
		tab.CWD = strings.TrimSpace(cwd)
	}
	s.tabs = append(s.tabs, tab)
	s.active = len(s.tabs) - 1
	return &s.tabs[s.active], nil
}

// SelectTab selects a tab by zero-based index.
func (s *ShellObject) SelectTab(index int) bool {
	if index < 0 || index >= len(s.tabs) {
		return false
	}
	s.active = index
	return true
}

func (s *ShellObject) AppendInput(text string) {
	if tab := s.ActiveTab(); tab != nil {
		tab.InputBuffer += sanitizeInputText(text)
	}
}

func sanitizeInputText(text string) string {
	if text == "" {
		return ""
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if next, ok := consumeLeakedMouseSequence(text, i); ok {
			i = next
			continue
		}
		if text[i] == 0x1b {
			i++
			if i < len(text) && text[i] == '[' {
				i++
				if i < len(text) && text[i] == 'M' {
					i++
					if i+3 <= len(text) {
						i += 3
					}
					continue
				}
				for i < len(text) {
					b := text[i]
					i++
					if b >= '@' && b <= '~' {
						break
					}
				}
				continue
			}
			continue
		}
		r, size := rune(text[i]), 1
		if text[i] >= 0x80 {
			r, size = utf8.DecodeRuneInString(text[i:])
		}
		i += size
		if r == utf8.RuneError && size == 1 {
			continue
		}
		if unicode.IsControl(r) && r != '\t' {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func consumeLeakedMouseSequence(text string, start int) (int, bool) {
	if start < 0 || start >= len(text) || text[start] != '[' {
		return start, false
	}
	if start+2 < len(text) && text[start+1] == 'M' {
		end := start + 5
		if end <= len(text) {
			return end, true
		}
		return len(text), true
	}
	if start+2 >= len(text) || text[start+1] != '<' {
		return start, false
	}
	i := start + 2
	fields := 0
	digits := 0
	for i < len(text) {
		b := text[i]
		switch {
		case b >= '0' && b <= '9':
			digits++
			i++
		case b == ';' && digits > 0:
			fields++
			digits = 0
			i++
		case (b == 'M' || b == 'm') && digits > 0 && fields == 2:
			return i + 1, true
		default:
			return start, false
		}
	}
	return start, false
}

// BackspaceInput removes one rune from the active tab input buffer.
func (s *ShellObject) BackspaceInput() {
	tab := s.ActiveTab()
	if tab == nil || tab.InputBuffer == "" {
		return
	}
	runes := []rune(tab.InputBuffer)
	tab.InputBuffer = string(runes[:len(runes)-1])
}

// ClearInput clears the active input buffer.
func (s *ShellObject) ClearInput() {
	if tab := s.ActiveTab(); tab != nil {
		tab.InputBuffer = ""
	}
}

// SubmitActiveInput submits the active tab input through the shell client and
// records returned events in that tab transcript.
func (s *ShellObject) SubmitActiveInput(ctx context.Context) error {
	tab := s.ActiveTab()
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
		return err
	}

	intent := classifyInput(line, tab.InputMode)
	if intent.Kind == IntentCD {
		result, err := s.client.ChangeCWD(ctx, tab.ID, intent.Arg)
		if err != nil {
			tab.Transcript = append(tab.Transcript, errorEvent(tab.ID, err))
			return err
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

	var events []TranscriptEvent
	var err error
	switch intent.Kind {
	case IntentSlash:
		events, err = s.client.SubmitSlash(ctx, tab.ID, SlashRequest{Line: intent.Text, CWD: tab.CWD})
	case IntentAsk:
		projection := ProjectTranscript(tab.Transcript, s.contextPolicy)
		events, err = s.client.SubmitAsk(ctx, tab.ID, AskRequest{Text: line, CWD: tab.CWD, Context: projection})
	default:
		events, err = s.client.SubmitCommand(ctx, tab.ID, CommandRequest{Line: line, CWD: tab.CWD})
	}
	if err != nil {
		tab.Transcript = append(tab.Transcript, errorEvent(tab.ID, err))
		return err
	}
	tab.Transcript = append(tab.Transcript, events...)
	tab.InputBuffer = ""
	return nil
}
func lastSessionError(events []TranscriptEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == EventError && strings.TrimSpace(events[i].Summary) != "" {
			return strings.TrimSpace(events[i].Summary)
		}
	}
	return "create shell session failed"
}

// ToggleInputMode switches active tab between shell and ask mode.
func (s *ShellObject) ToggleInputMode() {
	tab := s.ActiveTab()
	if tab == nil {
		return
	}
	if tab.InputMode == InputModeAsk {
		tab.InputMode = InputModeShell
		return
	}
	tab.InputMode = InputModeAsk
}

// SearchResources asks the client for resources using the active session and cwd.
func (s *ShellObject) SearchResources(ctx context.Context, query ResourceSearchQuery) ([]ResourceSearchResult, error) {
	tab := s.ActiveTab()
	if tab == nil {
		return nil, nil
	}
	query.CWD = tab.CWD
	return s.client.ResourceSearch(ctx, tab.ID, query)
}

func parseCD(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != "cd" {
		return "", false
	}
	if len(fields) == 1 {
		return ".", true
	}
	return fields[1], true
}

func errorEvent(sessionID string, err error) TranscriptEvent {
	return TranscriptEvent{
		ID:        newEventID("error"),
		SessionID: sessionID,
		Time:      time.Now(),
		Kind:      EventError,
		Summary:   err.Error(),
	}
}

// TabSession is the state owned by one session-backed tab.
type TabSession struct {
	ID    string
	Label string

	CWD         string
	InputMode   InputMode
	InputBuffer string
	Transcript  []TranscriptEvent
	Mentions    []ResourceMention
	Processes   []ProcessSummary
	Approvals   []ApprovalSummary
}

// ProcessSummary is a session-scoped process row for rendering.
type ProcessSummary struct {
	ID     string
	Line   string
	Status string
}

// ApprovalSummary is a session-scoped approval row for rendering.
type ApprovalSummary struct {
	ID      string
	Reason  string
	Pending bool
}
