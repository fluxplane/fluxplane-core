package codershell

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestShellViewKeepsChromeWithLongTimeline(t *testing.T) {
	m := viewportTestModel()
	appendViewportTestOutput(&m, 120)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 20})

	view := m.View().Content
	if lipgloss.Height(view) > 20 {
		t.Fatalf("view height = %d, want <= 20", lipgloss.Height(view))
	}
	if !strings.Contains(view, "coder shell") {
		t.Fatalf("view missing header:\n%s", view)
	}
	if !strings.Contains(view, "usage --") {
		t.Fatalf("view missing usage footer:\n%s", view)
	}
}

func TestShellHeaderRendersUnifiedObservedFacts(t *testing.T) {
	m := newModel(shellStatus{
		cwd:          "/workspace/agentruntime",
		projectName:  "agentruntime",
		projectKind:  "go",
		goModule:     "github.com/fluxplane/agentruntime",
		goVersion:    "go1.25.0",
		locale:       "en_US.UTF-8",
		user:         "dev/timo",
		provider:     "codex",
		model:        "gpt-5.5",
		facets:       []string{"git", "go.mod", "task"},
		taskCount:    4,
		projectCount: 1,
	}, NewFakeClient(), "direct")
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})

	view := m.View().Content
	for _, want := range []string{
		"coder shell",
		"agentruntime",
		"model",
		"codex/gpt-5.5",
		"tab",
		"1/1",
		"session",
		"workspace",
		"go.mod",
		"inventory ok",
		"go",
		"1.25.0",
		"locale",
		"en_US",
		"user",
		"timo",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestShellHeaderOmitsUnknownGoVersion(t *testing.T) {
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project", goVersion: "go n/a"}, NewFakeClient(), "fake")
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 20})

	view := m.View().Content
	if strings.Contains(view, "go n/a") || strings.Contains(view, "go n/") {
		t.Fatalf("view renders noisy unavailable Go version:\n%s", view)
	}
}

func TestShellHeaderKeepsTitleReadableInNarrowGhosttyLayout(t *testing.T) {
	m := newModel(shellStatus{
		cwd:         "/home/timo/projects/fluxplane/agentruntime",
		projectName: "fluxplane/agentruntime",
		projectKind: "multi",
		facets:      []string{"agents", "ai_config"},
	}, NewFakeClient(), "direct-channel")
	header := m.renderHeader(68)

	if !strings.Contains(header, "coder shell") {
		t.Fatalf("header lost readable product title:\n%s", header)
	}
	if strings.Contains(header, "co…") {
		t.Fatalf("header collapsed product title:\n%s", header)
	}
	if !strings.Contains(header, "fluxplane/agentruntime") {
		t.Fatalf("header missing project line:\n%s", header)
	}
}

func TestShellPromptSeparatesInputFromFooter(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "status"

	prompt := m.renderPrompt(68)
	if !strings.Contains(prompt, "status") {
		t.Fatalf("prompt missing input:\n%s", prompt)
	}
	if !strings.Contains(prompt, "usage --") {
		t.Fatalf("prompt missing usage footer:\n%s", prompt)
	}
	for _, forbidden := range []string{"ctrl+t", "alt+tab", "quit"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt contains footer hint %q:\n%s", forbidden, prompt)
		}
	}
	if lipgloss.Height(prompt) < 3 {
		t.Fatalf("prompt height = %d, want separated input and footer:\n%s", lipgloss.Height(prompt), prompt)
	}
}

func TestShellPromptFooterShowsUsageStats(t *testing.T) {
	m := viewportTestModel()
	m.appendStreamTranscript("session-1", TranscriptEvent{
		Kind: EventUsageRecorded,
		Data: map[string]string{
			"input_tokens":     "1200",
			"cached_tokens":    "500",
			"output_tokens":    "34",
			"reasoning_tokens": "8",
			"cost":             "0.0012",
			"currency":         "USD",
		},
	})

	prompt := m.renderPrompt(80)
	for _, want := range []string{"usage", "in 1.2k", "cached 500", "out 34", "reason 8", "$0.0012"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing usage part %q:\n%s", want, prompt)
		}
	}
}

func TestShellSlashCommandCompletionUsesCommandCatalog(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "/co"

	m.refreshMention()
	if !m.mention.Open || m.mention.Kind != completionCommand {
		t.Fatalf("completion = %#v, want command completion", m.mention)
	}
	if len(m.mention.Results) == 0 {
		t.Fatal("command completion returned no results")
	}
	for _, result := range m.mention.Results {
		if result.Kind != ResourceCommand {
			t.Fatalf("result kind = %q, want command", result.Kind)
		}
	}
}

func TestShellSlashCommandPickerOmitsRedundantKindPrefix(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "/co"
	m.refreshMention()

	picker := m.renderMentionPicker(100)
	if strings.Contains(picker, "[command]") {
		t.Fatalf("slash picker contains redundant command kind:\n%s", picker)
	}
	if !strings.Contains(picker, "/ commands") {
		t.Fatalf("slash picker missing command header:\n%s", picker)
	}
}

func TestShellSlashCommandCompletionIgnoresNonLeadingSlash(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "hello /co"

	m.refreshMention()
	if m.mention.Open {
		t.Fatalf("completion open for non-leading slash: %#v", m.mention)
	}
}

func TestShellSlashCommandCompletionStopsForFreeformArgs(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}

	tab.InputBuffer = "/goal "
	m.refreshMention()
	if m.mention.Open {
		t.Fatalf("completion open after complete command with trailing space: %#v", m.mention)
	}

	tab.InputBuffer = "/goal write tests"
	m.refreshMention()
	if m.mention.Open {
		t.Fatalf("completion open while typing freeform command args: %#v", m.mention)
	}
}

func TestShellSlashCommandCompletionInsertsMultiSegmentCommand(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "/env"
	m.refreshMention()
	found := false
	for i, result := range m.mention.Results {
		if result.Label == "/env explain" {
			m.mention.Index = i
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("results = %#v, missing /env explain", m.mention.Results)
	}

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if tab.InputBuffer != "/env explain " {
		t.Fatalf("input = %q, want /env explain ", tab.InputBuffer)
	}
}

func TestShellSlashOptionCompletionInsertsFlag(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "/goal --m"
	m.refreshMention()
	if !m.mention.Open || m.mention.Kind != completionOption {
		t.Fatalf("completion = %#v, want option completion", m.mention)
	}
	found := false
	for i, result := range m.mention.Results {
		if result.Label == "--max" {
			m.mention.Index = i
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("results = %#v, missing --max", m.mention.Results)
	}

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if tab.InputBuffer != "/goal --max " {
		t.Fatalf("input = %q, want /goal --max ", tab.InputBuffer)
	}
}

func TestShellLayoutUsesFullTerminalWidth(t *testing.T) {
	m := viewportTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 88, Height: 24})

	layout := m.layout()
	if layout.contentWidth != 88 {
		t.Fatalf("content width = %d, want terminal width", layout.contentWidth)
	}
}

func TestShellModeSwitchMarkersDoNotEnterInput(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}

	tab.InputMode = InputModeAsk
	tab.InputBuffer = ""
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Text: "!", Code: '!'}))
	if tab.InputMode != InputModeShell {
		t.Fatalf("mode after ! = %q, want shell", tab.InputMode)
	}
	if tab.InputBuffer != "" {
		t.Fatalf("input after ! = %q, want unchanged", tab.InputBuffer)
	}

	tab.InputMode = InputModeShell
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Text: "?", Code: '?'}))
	if tab.InputMode != InputModeAsk {
		t.Fatalf("mode after ? = %q, want ask", tab.InputMode)
	}
	if tab.InputBuffer != "" {
		t.Fatalf("input after ? = %q, want unchanged", tab.InputBuffer)
	}
}

func TestShellModeSwitchMarkersOnlyApplyAtPromptStart(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}

	tab.InputMode = InputModeAsk
	tab.InputBuffer = "before"
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Text: "!", Code: '!'}))
	if tab.InputMode != InputModeAsk {
		t.Fatalf("mode after non-leading ! = %q, want ask", tab.InputMode)
	}
	if tab.InputBuffer != "before!" {
		t.Fatalf("input after non-leading ! = %q, want before!", tab.InputBuffer)
	}

	tab.InputMode = InputModeShell
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Text: "?", Code: '?'}))
	if tab.InputMode != InputModeShell {
		t.Fatalf("mode after non-leading ? = %q, want shell", tab.InputMode)
	}
	if tab.InputBuffer != "before!?" {
		t.Fatalf("input after non-leading ? = %q, want before!?", tab.InputBuffer)
	}
}

func TestShellHistoryNavigationRestoresInputMode(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeShell
	tab.recordHistory("go test ./apps/coder/shell", InputModeShell)
	tab.recordHistory("explain the failure", InputModeAsk)

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if tab.InputBuffer != "explain the failure" || tab.InputMode != InputModeAsk {
		t.Fatalf("first history = (%q, %q), want ask entry", tab.InputBuffer, tab.InputMode)
	}
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if tab.InputBuffer != "go test ./apps/coder/shell" || tab.InputMode != InputModeShell {
		t.Fatalf("second history = (%q, %q), want shell entry", tab.InputBuffer, tab.InputMode)
	}
}

func TestShellIgnoresUnhandledAltModifiedRunes(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "echo"

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Text: "+", Code: '+', Mod: tea.ModAlt}))
	if tab.InputBuffer != "echo" {
		t.Fatalf("input after alt rune = %q, want unchanged", tab.InputBuffer)
	}
}

func TestShellTabDoesNotToggleInputMode(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeShell

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if tab.InputMode != InputModeShell {
		t.Fatalf("mode after tab = %q, want shell", tab.InputMode)
	}
}

func TestShellInitialTranscriptDoesNotSeedPlaceholderCopy(t *testing.T) {
	m := viewportTestModel()
	content := m.timelineContent(100)
	for _, forbidden := range []string{
		"coder shell experimental TUI",
		"project:",
		"type text and press enter",
		"press ctrl+c",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("initial transcript contains placeholder %q:\n%s", forbidden, content)
		}
	}
	if !strings.Contains(content, "connected: fake") {
		t.Fatalf("initial transcript missing connection event:\n%s", content)
	}
}

func TestShellWindowSizeInitializesTimelineViewport(t *testing.T) {
	m := viewportTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 28})
	layout := m.layout()

	if m.timeline.Width() != layout.timelineInnerWidth {
		t.Fatalf("timeline width = %d, want %d", m.timeline.Width(), layout.timelineInnerWidth)
	}
	if m.timeline.Height() != layout.timelineInnerHeight {
		t.Fatalf("timeline height = %d, want %d", m.timeline.Height(), layout.timelineInnerHeight)
	}
}

func TestShellViewportScrollAndSubmitReturnsToBottom(t *testing.T) {
	m := viewportTestModel()
	appendViewportTestOutput(&m, 80)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 18})
	if !m.timeline.AtBottom() {
		t.Fatal("timeline is not pinned to bottom after content sync")
	}
	bottomOffset := m.timeline.YOffset()

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	if m.timeline.YOffset() >= bottomOffset {
		t.Fatalf("YOffset after page up = %d, want less than %d", m.timeline.YOffset(), bottomOffset)
	}
	if m.timelinePinned {
		t.Fatal("timelinePinned = true after user scroll, want false")
	}

	active := m.shell.ActiveTab()
	if active == nil {
		t.Fatal("active tab is nil")
	}
	active.InputBuffer = "echo hi"
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !m.timeline.AtBottom() {
		t.Fatalf("timeline YOffset = %d, want bottom", m.timeline.YOffset())
	}
	if !m.timelinePinned {
		t.Fatal("timelinePinned = false after explicit submit, want true")
	}
}

func TestShellViewportMouseWheelScrollsTimeline(t *testing.T) {
	m := viewportTestModel()
	appendViewportTestOutput(&m, 80)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 18})
	bottomOffset := m.timeline.YOffset()

	m = updateModel(t, m, tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	if m.timeline.YOffset() >= bottomOffset {
		t.Fatalf("YOffset after wheel up = %d, want less than %d", m.timeline.YOffset(), bottomOffset)
	}
	if m.timelinePinned {
		t.Fatal("timelinePinned = true after wheel up, want false")
	}

	m = updateModel(t, m, tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	if m.timeline.YOffset() <= 0 {
		t.Fatalf("YOffset after wheel down = %d, want positive", m.timeline.YOffset())
	}
}

func TestShellMentionNavigationDoesNotScrollViewport(t *testing.T) {
	m := viewportTestModel()
	appendViewportTestOutput(&m, 80)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 18})
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	offset := m.timeline.YOffset()
	m.mention = MentionState{
		Open:  true,
		Index: 1,
		Results: []ResourceSearchResult{
			{Kind: ResourceAgent, ID: "one", Label: "one"},
			{Kind: ResourceAgent, ID: "two", Label: "two"},
		},
	}

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if m.mention.Index != 0 {
		t.Fatalf("mention index = %d, want 0", m.mention.Index)
	}
	if m.timeline.YOffset() != offset {
		t.Fatalf("timeline YOffset = %d, want unchanged %d", m.timeline.YOffset(), offset)
	}
}

func viewportTestModel() model {
	return newModel(shellStatus{cwd: "/workspace", projectName: "project", goVersion: "go1.25"}, NewFakeClient(), "fake")
}

func appendViewportTestOutput(m *model, count int) {
	tab := m.shell.ActiveTab()
	if tab == nil {
		return
	}
	for i := 0; i < count; i++ {
		tab.Transcript = append(tab.Transcript, TranscriptEvent{
			ID:        newEventID("out"),
			SessionID: tab.ID,
			Time:      time.Now(),
			Kind:      EventCommandOutput,
			Summary:   fmt.Sprintf("line %03d %s", i, strings.Repeat("output ", 6)),
		})
	}
	m.syncTimelineViewport(true)
}

func updateModel(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	updated, _ := m.Update(msg)
	result, ok := updated.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", updated)
	}
	return result
}
