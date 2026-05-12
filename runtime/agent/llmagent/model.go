package llmagent

import (
	"context"

	"github.com/fluxplane/agentruntime/core/agent"
	corellmagent "github.com/fluxplane/agentruntime/core/agent/llmagent"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/core/usage"
)

// Model is the provider-neutral inference port used by the LLM agent runtime.
// Provider SDKs, HTTP clients, credentials, retries, and wire formats belong in
// adapters that implement this interface.
type Model interface {
	Complete(context.Context, Request) (Response, error)
}

// StreamingModel is the optional provider-neutral streaming port. Streaming
// emits transient deltas while still returning a final structured Response.
type StreamingModel interface {
	Stream(context.Context, Request, StreamFunc) (Response, error)
}

// ModelFunc adapts a function into a model implementation.
type ModelFunc func(context.Context, Request) (Response, error)

// Complete calls f.
func (f ModelFunc) Complete(ctx context.Context, req Request) (Response, error) {
	if f == nil {
		return Response{}, ErrModelMissing
	}
	return f(ctx, req)
}

// Request is the structured model input assembled from an agent step. It is
// intentionally not a provider prompt format.
type Request struct {
	Agent        agent.Spec                `json:"agent"`
	Driver       corellmagent.Spec         `json:"driver,omitempty"`
	Tools        []tool.Spec               `json:"tools,omitempty"`
	Goal         string                    `json:"goal,omitempty"`
	Objective    agent.Objective           `json:"objective,omitempty"`
	Observations []environment.Observation `json:"observations,omitempty"`
	Context      []corecontext.Block       `json:"context,omitempty"`
	State        agent.StateRef            `json:"state,omitempty"`
}

// Response is the provider-neutral structured output of one model turn. The
// adapter/model implementation is responsible for parsing provider-native text
// or tool calls into this shape.
type Response struct {
	Message    *agent.Message           `json:"message,omitempty"`
	Operations []agent.OperationRequest `json:"operations,omitempty"`
	Completion *agent.Completion        `json:"completion,omitempty"`
	State      agent.StateUpdate        `json:"state,omitempty"`
	Usage      []usage.Recorded         `json:"usage,omitempty"`
}

// StreamKind classifies one provider-neutral streaming delta.
type StreamKind string

const (
	StreamThinkingDelta StreamKind = "thinking_delta"
	StreamContentDelta  StreamKind = "content_delta"
	StreamToolCallDelta StreamKind = "tool_call_delta"
)

// StreamEvent is one transient model-streaming event.
type StreamEvent struct {
	Kind      StreamKind `json:"kind"`
	Text      string     `json:"text,omitempty"`
	Tool      tool.Name  `json:"tool,omitempty"`
	Index     *int       `json:"index,omitempty"`
	Final     bool       `json:"final,omitempty"`
	Redaction string     `json:"redaction,omitempty"`
}

// StreamFunc receives streaming deltas.
type StreamFunc func(StreamEvent)

// StaticModel always returns the configured response.
type StaticModel struct {
	Response Response
	Err      error
}

// Complete returns m.Response or m.Err.
func (m StaticModel) Complete(context.Context, Request) (Response, error) {
	if m.Err != nil {
		return Response{}, m.Err
	}
	return m.Response, nil
}

// MessageResponse returns a model response that emits a message decision.
func MessageResponse(content any) Response {
	return Response{Message: &agent.Message{Content: content}}
}

// CompleteResponse returns a model response that completes the current step.
func CompleteResponse(output any, reason string) Response {
	return Response{Completion: &agent.Completion{Output: output, Reason: reason}}
}

// OperationResponse returns a model response that requests one or more
// operations.
func OperationResponse(reqs ...agent.OperationRequest) Response {
	return Response{Operations: append([]agent.OperationRequest(nil), reqs...)}
}
