package codershell

import (
	"context"
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

func TestShellPromptShowsEmptyInputPlaceholder(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}

	tab.InputMode = InputModeAsk
	askPrompt := m.renderPrompt(100)
	if !strings.Contains(askPrompt, "Ask the agent...") {
		t.Fatalf("ask prompt missing placeholder:\n%s", askPrompt)
	}

	tab.InputMode = InputModeShell
	shellPrompt := m.renderPrompt(100)
	if !strings.Contains(shellPrompt, "Run a shell command...") {
		t.Fatalf("shell prompt missing placeholder:\n%s", shellPrompt)
	}
}

func TestShellPromptFooterShowsModeHints(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}

	tab.InputMode = InputModeAsk
	askPrompt := m.renderPrompt(100)
	for _, want := range []string{"ask mode", "! shell", "/ command", "@ mention", "↑ history", "pgup scroll", "usage --"} {
		if !strings.Contains(askPrompt, want) {
			t.Fatalf("ask prompt missing %q:\n%s", want, askPrompt)
		}
	}

	tab.InputMode = InputModeShell
	shellPrompt := m.renderPrompt(100)
	for _, want := range []string{"shell mode", "? ask", "/ command", "@ mention", "↑ history", "pgup scroll", "usage --"} {
		if !strings.Contains(shellPrompt, want) {
			t.Fatalf("shell prompt missing %q:\n%s", want, shellPrompt)
		}
	}
}

func TestShellPromptFooterShowsCancelHintWhileRunning(t *testing.T) {
	m := viewportTestModel()
	m.activeRuns["session-1"] = "ask.submitted"

	prompt := m.renderPrompt(120)
	for _, want := range []string{"esc cancel", "pgup scroll", "usage --"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("running prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestShellPromptFooterShowsEditingShortcuts(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "alpha beta"

	prompt := m.renderPrompt(120)
	for _, want := range []string{"ctrl+w word", "ctrl+k tail", "usage --"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("editing prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestShellAdvancedEditingKeys(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "alpha beta gamma"
	tab.InputCursor = len([]rune(tab.InputBuffer))

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModAlt}))
	if got, want := tab.InputCursor, len([]rune("alpha beta ")); got != want {
		t.Fatalf("cursor after alt-left = %d, want %d", got, want)
	}

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModCtrl}))
	if got := tab.InputBuffer; got != "alpha gamma" {
		t.Fatalf("input after ctrl+w = %q, want alpha gamma", got)
	}
	if got, want := tab.InputCursor, len([]rune("alpha ")); got != want {
		t.Fatalf("cursor after ctrl+w = %d, want %d", got, want)
	}

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: tea.ModAlt}))
	if got, want := tab.InputCursor, len([]rune("alpha gamma")); got != want {
		t.Fatalf("cursor after alt-right = %d, want %d", got, want)
	}
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}))
	if got := tab.InputBuffer; got != "apha gamma" {
		t.Fatalf("input after delete = %q, want apha gamma", got)
	}
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: 'k', Mod: tea.ModCtrl}))
	if got := tab.InputBuffer; got != "a" {
		t.Fatalf("input after ctrl+k = %q, want a", got)
	}
}

func TestShellPromptFooterShowsUsageStats(t *testing.T) {
	m := viewportTestModel()
	m.appendStreamTranscript("session-1", "", TranscriptEvent{
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

	refreshMentionSync(&m)
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

func TestShellCompletionPickerShowsControls(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "/co"
	refreshMentionSync(&m)

	picker := m.renderMentionPicker(100)
	for _, want := range []string{"1/", "results", "info", "tab accept", "↑↓ select", "esc close"} {
		if !strings.Contains(picker, want) {
			t.Fatalf("picker missing hint %q:\n%s", want, picker)
		}
	}
	if strings.Contains(picker, "enter submit") {
		t.Fatalf("picker contains misleading enter-submit hint:\n%s", picker)
	}
}

func TestShellEscapeClosesCompletionPickerBeforeQuit(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "/co"
	refreshMentionSync(&m)
	if !m.mention.Open {
		t.Fatal("completion did not open")
	}

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if m.mention.Open {
		t.Fatalf("completion still open after escape: %#v", m.mention)
	}
}

func TestShellSlashCommandPickerOmitsRedundantKindPrefix(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputBuffer = "/co"
	refreshMentionSync(&m)

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

	refreshMentionSync(&m)
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
	refreshMentionSync(&m)
	if m.mention.Open {
		t.Fatalf("completion open after complete command with trailing space: %#v", m.mention)
	}

	tab.InputBuffer = "/goal write tests"
	refreshMentionSync(&m)
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
	refreshMentionSync(&m)
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
	refreshMentionSync(&m)
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

func TestShellHomeAllowsModeSwitchForRecalledHistory(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.recordHistory("go test ./apps/coder/shell", InputModeShell)

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if tab.InputCursor != len([]rune(tab.InputBuffer)) {
		t.Fatalf("history cursor = %d, want end of %q", tab.InputCursor, tab.InputBuffer)
	}
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Text: "?", Code: '?'}))
	if tab.InputMode != InputModeAsk {
		t.Fatalf("mode after leading ? = %q, want ask", tab.InputMode)
	}
	if tab.InputBuffer != "go test ./apps/coder/shell" {
		t.Fatalf("input after leading ? = %q, want recalled text unchanged", tab.InputBuffer)
	}
	if tab.InputCursor != 0 {
		t.Fatalf("cursor after leading ? = %d, want 0", tab.InputCursor)
	}

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Text: "!", Code: '!'}))
	if tab.InputBuffer != "go test ./apps/coder/shell!" {
		t.Fatalf("input after end ! = %q, want literal suffix", tab.InputBuffer)
	}
	if tab.InputMode != InputModeAsk {
		t.Fatalf("mode after end ! = %q, want ask unchanged", tab.InputMode)
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
	for _, want := range []string{"tip", "ask a question", "/ for commands", "@ for resources", "! for shell mode"} {
		if !strings.Contains(content, want) {
			t.Fatalf("initial transcript missing onboarding tip %q:\n%s", want, content)
		}
	}
}

func TestTimelineCachePreservesMultilineStreamedAnswerWhenNextAskStarts(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeAsk
	tab.Transcript = append(tab.Transcript,
		TranscriptEvent{Kind: EventAskSubmitted, Summary: "list clusters"},
		TranscriptEvent{Kind: EventAskDelta, Summary: "Here are your available clusters:\n"},
		TranscriptEvent{Kind: EventAskDelta, Summary: "- prod\n- staging\n"},
	)
	before := m.timelineContent(100)
	for _, want := range []string{"Here are your available clusters:", "prod", "staging"} {
		if !strings.Contains(before, want) {
			t.Fatalf("before content missing %q:\n%s", want, before)
		}
	}

	tab.Transcript = append(tab.Transcript, TranscriptEvent{Kind: EventAskSubmitted, Summary: "next question"})
	after := m.timelineContent(100)
	for _, want := range []string{"Here are your available clusters:", "prod", "staging", "next question"} {
		if !strings.Contains(after, want) {
			t.Fatalf("after content missing %q:\n%s", want, after)
		}
	}
}

func TestShellModeTimelineRendersLikePlainShell(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeShell
	tab.Transcript = append(tab.Transcript,
		TranscriptEvent{Kind: EventCommandStarted, Summary: "printf hello"},
		TranscriptEvent{Kind: EventProcessOutput, Summary: "hello", Data: map[string]string{"raw": "true", "stream": "stdout"}},
	)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})

	view := m.View().Content
	for _, want := range []string{"printf hello", "hello"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	for _, forbidden := range []string{"stdout:", "● shell_exec", "shell_exec status=ok", "process proc-1", "exit proc-1", "╭", "╰"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("view contains shell chrome %q:\n%s", forbidden, view)
		}
	}
}

func TestSwitchingInputModeDoesNotReflowTimeline(t *testing.T) {
	m := viewportTestModel()
	tab := m.shell.ActiveTab()
	if tab == nil {
		t.Fatal("active tab is nil")
	}
	tab.InputMode = InputModeAsk
	tab.Transcript = append(tab.Transcript,
		TranscriptEvent{Kind: EventAskSubmitted, Summary: "list clusters"},
		TranscriptEvent{Kind: EventAskDelta, Summary: "Here are your available clusters:\n- prod\n- staging\n"},
		TranscriptEvent{Kind: EventCommandStarted, Summary: "printf hello"},
		TranscriptEvent{Kind: EventProcessOutput, Summary: "hello", Data: map[string]string{"raw": "true", "stream": "stdout"}},
	)
	appendViewportTestOutput(&m, 80)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 88, Height: 20})
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	before := m.layout()
	offset := m.timeline.YOffset()
	content := m.timeline.View()

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Text: "!", Code: '!'}))
	if got := m.shell.ActiveTab().InputMode; got != InputModeShell {
		t.Fatalf("mode after ! = %q, want shell", got)
	}
	afterShell := m.layout()
	if afterShell.timelineInnerWidth != before.timelineInnerWidth || afterShell.timelineInnerHeight != before.timelineInnerHeight {
		t.Fatalf("shell layout = %dx%d, want %dx%d", afterShell.timelineInnerWidth, afterShell.timelineInnerHeight, before.timelineInnerWidth, before.timelineInnerHeight)
	}
	if m.timeline.YOffset() != offset || m.timeline.View() != content {
		t.Fatalf("timeline changed after shell mode switch: offset %d->%d", offset, m.timeline.YOffset())
	}

	m = updateModel(t, m, tea.KeyPressMsg(tea.Key{Text: "?", Code: '?'}))
	if got := m.shell.ActiveTab().InputMode; got != InputModeAsk {
		t.Fatalf("mode after ? = %q, want ask", got)
	}
	afterAsk := m.layout()
	if afterAsk.timelineInnerWidth != before.timelineInnerWidth || afterAsk.timelineInnerHeight != before.timelineInnerHeight {
		t.Fatalf("ask layout = %dx%d, want %dx%d", afterAsk.timelineInnerWidth, afterAsk.timelineInnerHeight, before.timelineInnerWidth, before.timelineInnerHeight)
	}
	if m.timeline.YOffset() != offset || m.timeline.View() != content {
		t.Fatalf("timeline changed after ask mode switch: offset %d->%d", offset, m.timeline.YOffset())
	}
}

func TestTimelineContentBoundsRenderedHistory(t *testing.T) {
	m := viewportTestModel()
	appendViewportTestOutput(&m, maxRenderedTimelineEvents+20)

	content := m.timelineContent(100)
	if !strings.Contains(content, "older events hidden") {
		t.Fatalf("timeline missing hidden history notice:\n%s", content)
	}
	if strings.Contains(content, "line 000") {
		t.Fatalf("timeline rendered oldest hidden line:\n%s", content)
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

func refreshMentionSync(m *model) {
	if m == nil || m.shell == nil {
		return
	}
	tab := m.shell.ActiveTab()
	state, query, sessionID, input, ok := m.mentionSearch(tab)
	if !ok {
		m.mention = MentionState{}
		return
	}
	results, err := m.shell.client.ResourceSearch(context.Background(), sessionID, query)
	if err != nil {
		m.mention = MentionState{}
		return
	}
	if state.Kind == completionCommand {
		if len(results) == 0 || slashCommandInputComplete(input, state.Query, results) {
			m.mention = MentionState{}
			return
		}
	}
	if state.Kind == completionOption && len(results) == 0 {
		m.mention = MentionState{}
		return
	}
	state.Results = results
	m.mention = state
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
