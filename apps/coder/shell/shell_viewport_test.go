package codershell

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestShellViewKeepsChromeWithLongTimeline(t *testing.T) {
	m := viewportTestModel()
	appendViewportTestOutput(&m, 120)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 20})

	view := m.View()
	if lipgloss.Height(view) > 20 {
		t.Fatalf("view height = %d, want <= 20", lipgloss.Height(view))
	}
	if !strings.Contains(view, "coder shell") {
		t.Fatalf("view missing header:\n%s", view)
	}
	if !strings.Contains(view, "ctrl+t new tab") {
		t.Fatalf("view missing prompt/help:\n%s", view)
	}
}

func TestShellWindowSizeInitializesTimelineViewport(t *testing.T) {
	m := viewportTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 28})
	layout := m.layout()

	if m.timeline.Width != layout.timelineInnerWidth {
		t.Fatalf("timeline width = %d, want %d", m.timeline.Width, layout.timelineInnerWidth)
	}
	if m.timeline.Height != layout.timelineInnerHeight {
		t.Fatalf("timeline height = %d, want %d", m.timeline.Height, layout.timelineInnerHeight)
	}
}

func TestShellViewportScrollAndSubmitReturnsToBottom(t *testing.T) {
	m := viewportTestModel()
	appendViewportTestOutput(&m, 80)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 18})
	if !m.timeline.AtBottom() {
		t.Fatal("timeline is not pinned to bottom after content sync")
	}
	bottomOffset := m.timeline.YOffset

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.timeline.YOffset >= bottomOffset {
		t.Fatalf("YOffset after page up = %d, want less than %d", m.timeline.YOffset, bottomOffset)
	}
	if m.timelinePinned {
		t.Fatal("timelinePinned = true after user scroll, want false")
	}

	active := m.shell.ActiveTab()
	if active == nil {
		t.Fatal("active tab is nil")
	}
	active.InputBuffer = "echo hi"
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.timeline.AtBottom() {
		t.Fatalf("timeline YOffset = %d, want bottom", m.timeline.YOffset)
	}
	if !m.timelinePinned {
		t.Fatal("timelinePinned = false after explicit submit, want true")
	}
}

func TestShellViewportMouseWheelScrollsTimeline(t *testing.T) {
	m := viewportTestModel()
	appendViewportTestOutput(&m, 80)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 18})
	bottomOffset := m.timeline.YOffset

	m = updateModel(t, m, tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})
	if m.timeline.YOffset >= bottomOffset {
		t.Fatalf("YOffset after wheel up = %d, want less than %d", m.timeline.YOffset, bottomOffset)
	}
	if m.timelinePinned {
		t.Fatal("timelinePinned = true after wheel up, want false")
	}

	m = updateModel(t, m, tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	if m.timeline.YOffset <= 0 {
		t.Fatalf("YOffset after wheel down = %d, want positive", m.timeline.YOffset)
	}
}

func TestShellMentionNavigationDoesNotScrollViewport(t *testing.T) {
	m := viewportTestModel()
	appendViewportTestOutput(&m, 80)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 18})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	offset := m.timeline.YOffset
	m.mention = MentionState{
		Open:  true,
		Index: 1,
		Results: []ResourceSearchResult{
			{Kind: ResourceAgent, ID: "one", Label: "one"},
			{Kind: ResourceAgent, ID: "two", Label: "two"},
		},
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.mention.Index != 0 {
		t.Fatalf("mention index = %d, want 0", m.mention.Index)
	}
	if m.timeline.YOffset != offset {
		t.Fatalf("timeline YOffset = %d, want unchanged %d", m.timeline.YOffset, offset)
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
