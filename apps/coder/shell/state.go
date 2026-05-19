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
		ID:           info.ID,
		Label:        fmt.Sprintf("%d", len(s.tabs)+1),
		CWD:          strings.TrimSpace(info.CWD),
		InputMode:    InputModeShell,
		historyIndex: -1,
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
		cleaned, pending := sanitizeInputText(tab.inputEscapeRemainder + text)
		tab.inputEscapeRemainder = pending
		tab.InputBuffer += cleaned
		if cleaned != "" {
			tab.resetHistoryNavigation()
		}
	}
}

func sanitizeInputText(text string) (string, string) {
	if text == "" {
		return "", ""
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if next, ok := consumeLeakedModifierArtifact(text, i); ok {
			i = next
			continue
		}
		if next, ok := consumeLeakedMouseSequence(text, i); ok {
			i = next
			continue
		} else if isPotentialLeakedMousePrefix(text[i:]) {
			return out.String(), text[i:]
		}
		if text[i] == 0x1b {
			next, complete := consumeANSISequence(text, i)
			if !complete {
				return out.String(), text[i:]
			}
			i = next
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
	return out.String(), ""
}

func consumeLeakedModifierArtifact(text string, start int) (int, bool) {
	for _, artifact := range []string{"+alt", "+ctrl", "+shift", "+meta"} {
		if strings.HasPrefix(text[start:], artifact) {
			return start + len(artifact), true
		}
	}
	for _, artifact := range []string{"alt+", "ctrl+", "shift+", "meta+"} {
		if !strings.HasPrefix(text[start:], artifact) {
			continue
		}
		if start > 0 {
			prev := rune(text[start-1])
			if unicode.IsLetter(prev) || unicode.IsDigit(prev) {
				continue
			}
		}
		return start + len(artifact), true
	}
	return start, false
}

func consumeANSISequence(text string, start int) (int, bool) {
	if start < 0 || start >= len(text) || text[start] != 0x1b {
		return start, false
	}
	i := start + 1
	if i >= len(text) {
		return len(text), false
	}
	if text[i] != '[' {
		return i + 1, true
	}
	i++
	if i >= len(text) {
		return len(text), false
	}
	if text[i] == 'M' {
		i++
		if i+3 <= len(text) {
			return i + 3, true
		}
		return len(text), false
	}
	for i < len(text) {
		b := text[i]
		i++
		if b >= '@' && b <= '~' {
			return i, true
		}
	}
	return len(text), false
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
		return start, false
	}
	if start+2 >= len(text) || (text[start+1] != '<' && text[start+1] != '>') {
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

func isPotentialLeakedMousePrefix(text string) bool {
	if text == "" || text[0] != '[' {
		return false
	}
	if len(text) == 1 {
		return false
	}
	switch text[1] {
	case 'M':
		return len(text) < 5
	case '<', '>':
		fields := 0
		digits := 0
		for i := 2; i < len(text); i++ {
			b := text[i]
			switch {
			case b >= '0' && b <= '9':
				digits++
			case b == ';' && digits > 0:
				fields++
				digits = 0
			case (b == 'M' || b == 'm') && digits > 0 && fields == 2:
				return false
			default:
				return false
			}
		}
		return true
	default:
		return false
	}
}

// BackspaceInput removes one rune from the active tab input buffer.
func (s *ShellObject) BackspaceInput() {
	tab := s.ActiveTab()
	if tab == nil || tab.InputBuffer == "" {
		return
	}
	runes := []rune(tab.InputBuffer)
	tab.InputBuffer = string(runes[:len(runes)-1])
	tab.resetHistoryNavigation()
}

// ClearInput clears the active input buffer.
func (s *ShellObject) ClearInput() {
	if tab := s.ActiveTab(); tab != nil {
		tab.InputBuffer = ""
		tab.resetHistoryNavigation()
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
		tab.resetHistoryNavigation()
		return err
	}

	submittedMode := tab.InputMode
	tab.recordHistory(line, submittedMode)
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

// PreviousInputHistory recalls the previous submitted input for the active tab.
func (s *ShellObject) PreviousInputHistory() bool {
	tab := s.ActiveTab()
	if tab == nil || len(tab.InputHistory) == 0 {
		return false
	}
	if tab.historyIndex < 0 {
		tab.historyDraft = tab.InputBuffer
		tab.historyDraftMode = tab.InputMode
		tab.historyIndex = len(tab.InputHistory) - 1
	} else if tab.historyIndex > 0 {
		tab.historyIndex--
	}
	tab.applyHistoryEntry()
	return true
}

// NextInputHistory recalls the next submitted input for the active tab.
func (s *ShellObject) NextInputHistory() bool {
	tab := s.ActiveTab()
	if tab == nil || tab.historyIndex < 0 {
		return false
	}
	if tab.historyIndex+1 < len(tab.InputHistory) {
		tab.historyIndex++
		tab.applyHistoryEntry()
		return true
	}
	tab.InputBuffer = tab.historyDraft
	tab.InputMode = tab.historyDraftMode
	tab.resetHistoryNavigation()
	return true
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

	CWD                  string
	InputMode            InputMode
	InputBuffer          string
	inputEscapeRemainder string
	InputHistory         []InputHistoryEntry
	historyIndex         int
	historyDraft         string
	historyDraftMode     InputMode
	Transcript           []TranscriptEvent
	Mentions             []ResourceMention
	Processes            []ProcessSummary
	Approvals            []ApprovalSummary
}

// InputHistoryEntry is one submitted prompt line plus the input mode it used.
type InputHistoryEntry struct {
	Text string
	Mode InputMode
}

const maxInputHistoryEntries = 200

func (t *TabSession) recordHistory(text string, mode InputMode) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if mode == "" {
		mode = InputModeShell
	}
	entry := InputHistoryEntry{Text: text, Mode: mode}
	if len(t.InputHistory) > 0 {
		last := t.InputHistory[len(t.InputHistory)-1]
		if last == entry {
			t.resetHistoryNavigation()
			return
		}
	}
	t.InputHistory = append(t.InputHistory, entry)
	if len(t.InputHistory) > maxInputHistoryEntries {
		copy(t.InputHistory, t.InputHistory[len(t.InputHistory)-maxInputHistoryEntries:])
		t.InputHistory = t.InputHistory[:maxInputHistoryEntries]
	}
	t.resetHistoryNavigation()
}

func (t *TabSession) applyHistoryEntry() {
	if t == nil || t.historyIndex < 0 || t.historyIndex >= len(t.InputHistory) {
		return
	}
	entry := t.InputHistory[t.historyIndex]
	t.InputBuffer = entry.Text
	if entry.Mode != "" {
		t.InputMode = entry.Mode
	}
}

func (t *TabSession) resetHistoryNavigation() {
	if t == nil {
		return
	}
	t.historyIndex = -1
	t.historyDraft = ""
	t.historyDraftMode = ""
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
