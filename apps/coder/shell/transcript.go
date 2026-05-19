package codershell

import "time"

// TranscriptKind identifies shell transcript event types.
type TranscriptKind string

const (
	EventInputSubmitted    TranscriptKind = "input.submitted"
	EventCommandStarted    TranscriptKind = "command.started"
	EventCommandOutput     TranscriptKind = "command.output"
	EventCommandComplete   TranscriptKind = "command.completed"
	EventAskSubmitted      TranscriptKind = "ask.submitted"
	EventAskOutput         TranscriptKind = "ask.output"
	EventSlashSubmitted    TranscriptKind = "slash.submitted"
	EventResourceMentioned TranscriptKind = "resource.mentioned"
	EventCWDChanged        TranscriptKind = "cwd.changed"
	EventClientConnected   TranscriptKind = "client.connected"
	EventError             TranscriptKind = "error"
)

// TranscriptEvent records one session-scoped shell interaction or result.
type TranscriptEvent struct {
	ID        string
	SessionID string
	Time      time.Time
	Kind      TranscriptKind
	Summary   string
	Data      map[string]string
}

// TimelineLines returns compact display lines for a transcript.
func TimelineLines(events []TranscriptEvent) []string {
	lines := make([]string, 0, len(events))
	for _, event := range events {
		summary := event.Summary
		switch event.Kind {
		case EventClientConnected:
			if summary == "" {
				summary = "session connected"
			}
			lines = append(lines, summary)
		case EventInputSubmitted:
			if summary == "" {
				lines = append(lines, "")
			} else {
				lines = append(lines, "> "+summary)
			}
		case EventCommandStarted:
			lines = append(lines, "$ "+summary)
		case EventCommandOutput:
			lines = append(lines, "out: "+summary)
		case EventCommandComplete:
			lines = append(lines, "exit: "+summary)
		case EventAskSubmitted:
			lines = append(lines, "? "+summary)
		case EventAskOutput:
			lines = append(lines, "agent: "+summary)
		case EventResourceMentioned:
			lines = append(lines, "mention: "+summary)
		case EventSlashSubmitted:
			lines = append(lines, "slash: "+summary)
		case EventCWDChanged:
			lines = append(lines, "cwd: "+summary)
		case EventError:
			lines = append(lines, "error: "+summary)
		default:
			lines = append(lines, summary)
		}
	}
	return lines
}
