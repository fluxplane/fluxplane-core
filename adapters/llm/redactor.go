package llm

import (
	"github.com/fluxplane/agentruntime/core/policy"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

// Redactor controls which provider-normalized stream data is exposed through
// runtime model streaming events.
type Redactor struct {
	ExposeThinking bool
	ExposeToolArgs bool
}

// ToRuntimeStream converts and redacts a provider-normalized stream event into
// an llmagent stream event. The bool reports whether the event should be
// emitted at all.
func (r Redactor) ToRuntimeStream(evt StreamEvent) (llmagent.StreamEvent, bool) {
	switch evt.Kind {
	case StreamContentDelta:
		return llmagent.StreamEvent{
			Kind:  llmagent.StreamContentDelta,
			Text:  evt.Text,
			Index: streamIndex(evt.Index),
			Final: evt.Final,
		}, true
	case StreamThinkingDelta:
		if !r.ExposeThinking {
			return llmagent.StreamEvent{}, false
		}
		text := evt.Text
		redaction := evt.Redaction
		if redaction == "" && policy.NormalizeSensitivity(evt.Sensitivity) != policy.SensitivityPublic {
			text = ""
			redaction = "thinking_redacted"
		}
		return llmagent.StreamEvent{
			Kind:      llmagent.StreamThinkingDelta,
			Text:      text,
			Index:     streamIndex(evt.Index),
			Final:     evt.Final,
			Redaction: redaction,
		}, true
	case StreamToolCallStart, StreamToolCallDelta, StreamToolCallDone:
		text := evt.Arguments
		redaction := evt.Redaction
		if !r.ExposeToolArgs || sensitivityRank(policy.NormalizeSensitivity(evt.Sensitivity)) >= sensitivityRank(policy.SensitivityConfidential) {
			text = ""
			if redaction == "" {
				redaction = "tool_arguments_redacted"
			}
		}
		return llmagent.StreamEvent{
			Kind:      llmagent.StreamToolCallDelta,
			Text:      text,
			Tool:      evt.Tool,
			Index:     streamIndex(evt.Index),
			Final:     evt.Kind == StreamToolCallDone || evt.Final,
			Redaction: redaction,
		}, true
	default:
		return llmagent.StreamEvent{}, false
	}
}

func streamIndex(index int) *int {
	return &index
}

func sensitivityRank(sensitivity policy.Sensitivity) int {
	switch sensitivity {
	case policy.SensitivitySecret:
		return 5
	case policy.SensitivityConfidential:
		return 4
	case policy.SensitivityRestricted:
		return 3
	case policy.SensitivityInternal:
		return 2
	case policy.SensitivityPublic:
		return 1
	default:
		return 3
	}
}
