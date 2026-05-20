package codershell

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkLargeTranscriptTimelineRender(b *testing.B) {
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project"}, NewFakeClient(), "bench")
	tab := m.shell.ActiveTab()
	for i := 0; i < 2500; i++ {
		tab.Transcript = append(tab.Transcript, TranscriptEvent{
			ID:        fmt.Sprintf("out-%d", i),
			SessionID: tab.ID,
			Time:      time.Unix(int64(i), 0),
			Kind:      EventCommandOutput,
			Summary:   fmt.Sprintf("line %04d %s", i, strings.Repeat("output ", 8)),
		})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.timelineContent(100)
	}
}

func BenchmarkStreamingDeltaTimelineRender(b *testing.B) {
	m := newModel(shellStatus{cwd: "/workspace", projectName: "project"}, NewFakeClient(), "bench")
	tab := m.shell.ActiveTab()
	tab.Transcript = append(tab.Transcript, TranscriptEvent{ID: "ask", SessionID: tab.ID, Kind: EventAskSubmitted, Summary: "stream"})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tab.Transcript = append(tab.Transcript, TranscriptEvent{
			ID:        fmt.Sprintf("delta-%d", i),
			SessionID: tab.ID,
			Kind:      EventAskDelta,
			Summary:   " token",
		})
		_ = m.timelineContent(100)
	}
}
