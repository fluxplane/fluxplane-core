package llm

import (
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/tool"
)

// StreamKind classifies provider-normalized stream deltas.
type StreamKind string

const (
	StreamContentDelta  StreamKind = "content_delta"
	StreamThinkingDelta StreamKind = "thinking_delta"
	StreamToolCallStart StreamKind = "tool_call_start"
	StreamToolCallDelta StreamKind = "tool_call_delta"
	StreamToolCallDone  StreamKind = "tool_call_done"
	StreamRefusal       StreamKind = "refusal"
	StreamError         StreamKind = "error"
)

// StreamEvent is the provider-normalized stream shape. It can be redacted into
// runtime llmagent stream events and assembled into final operation requests.
type StreamEvent struct {
	Kind        StreamKind         `json:"kind"`
	Text        string             `json:"text,omitempty"`
	Tool        tool.Name          `json:"tool,omitempty"`
	ToolCallID  string             `json:"tool_call_id,omitempty"`
	CallType    string             `json:"call_type,omitempty"`
	Index       int                `json:"index,omitempty"`
	Arguments   string             `json:"arguments,omitempty"`
	Final       bool               `json:"final,omitempty"`
	Visibility  Visibility         `json:"visibility,omitempty"`
	Sensitivity policy.Sensitivity `json:"sensitivity,omitempty"`
	Error       string             `json:"error,omitempty"`
	Redaction   string             `json:"redaction,omitempty"`
}
