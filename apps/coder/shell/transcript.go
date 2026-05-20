package codershell

import (
	"strings"
	"time"
)

// TranscriptKind identifies shell transcript event types.
type TranscriptKind string

const (
	EventInputSubmitted    TranscriptKind = "input.submitted"
	EventCommandStarted    TranscriptKind = "command.started"
	EventCommandOutput     TranscriptKind = "command.output"
	EventCommandComplete   TranscriptKind = "command.completed"
	EventAskSubmitted      TranscriptKind = "ask.submitted"
	EventAskDelta          TranscriptKind = "ask.delta"
	EventAskOutput         TranscriptKind = "ask.output"
	EventThinking          TranscriptKind = "agent.thinking"
	EventSlashSubmitted    TranscriptKind = "slash.submitted"
	EventOperationStarted  TranscriptKind = "operation.started"
	EventOperationComplete TranscriptKind = "operation.completed"
	EventProcessStarted    TranscriptKind = "process.started"
	EventProcessOutput     TranscriptKind = "process.output"
	EventProcessExited     TranscriptKind = "process.exited"
	EventUsageRecorded     TranscriptKind = "usage.recorded"
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
	var agentDelta string
	var thinking strings.Builder
	flushAgentDelta := func() {
		if agentDelta == "" {
			return
		}
		lines = append(lines, "agent: "+agentDelta)
		agentDelta = ""
	}
	flushThinking := func() {
		text := strings.TrimSpace(thinking.String())
		if text == "" {
			return
		}
		lines = append(lines, "thinking: "+text)
		thinking.Reset()
	}
	for _, event := range events {
		summary := event.Summary
		switch event.Kind {
		case EventAskDelta:
			flushThinking()
			agentDelta += summary
			continue
		case EventThinking:
			flushAgentDelta()
			if strings.TrimSpace(summary) != "" && summary != "thinking..." {
				thinking.WriteString(summary)
			}
			continue
		case EventClientConnected:
			flushThinking()
			flushAgentDelta()
			if summary == "" {
				summary = "session connected"
			}
			lines = append(lines, summary)
		case EventInputSubmitted:
			flushThinking()
			flushAgentDelta()
			if summary == "" {
				lines = append(lines, "")
			} else {
				lines = append(lines, "> "+summary)
			}
		case EventCommandStarted:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "$ "+summary)
		case EventCommandOutput:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "out: "+summary)
		case EventCommandComplete:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "exit: "+summary)
		case EventAskSubmitted:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "? "+summary)
		case EventAskOutput:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "agent: "+summary)
		case EventOperationStarted:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "op: "+summary)
		case EventOperationComplete:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "op-done: "+summary)
		case EventProcessStarted:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "proc: "+summary)
		case EventProcessOutput:
			flushThinking()
			flushAgentDelta()
			if event.Data["raw"] == "true" {
				lines = append(lines, "raw: "+summary)
				continue
			}
			lines = append(lines, "proc-out: "+summary)
		case EventProcessExited:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "proc-exit: "+summary)
		case EventUsageRecorded:
			continue
		case EventResourceMentioned:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "mention: "+summary)
		case EventSlashSubmitted:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "slash: "+summary)
		case EventCWDChanged:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "cwd: "+summary)
		case EventError:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, "error: "+summary)
		default:
			flushThinking()
			flushAgentDelta()
			lines = append(lines, summary)
		}
	}
	flushThinking()
	flushAgentDelta()
	return lines
}
