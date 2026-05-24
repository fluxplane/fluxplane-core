package llmagent

import (
	"context"
	"errors"

	"github.com/fluxplane/fluxplane-core/core/agent"
	corellmagent "github.com/fluxplane/fluxplane-core/core/agent/llmagent"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/core/usage"
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

// ProviderIdentityModel exposes the provider identity a model will use for a
// request. Adapters implement this when lifecycle events should identify the
// concrete provider before the response is available.
type ProviderIdentityModel interface {
	ProviderIdentity(Request) coreconversation.ProviderIdentity
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
	Agent        agent.Spec                   `json:"agent"`
	Driver       corellmagent.Spec            `json:"driver,omitempty"`
	Tools        []tool.Spec                  `json:"tools,omitempty"`
	Goal         string                       `json:"goal,omitempty"`
	Objective    agent.Objective              `json:"objective,omitempty"`
	Observations []coreevidence.Observation   `json:"observations,omitempty"`
	Context      []corecontext.Block          `json:"context,omitempty"`
	Transcript   *coreconversation.Transcript `json:"transcript,omitempty"`
	State        agent.StateRef               `json:"state,omitempty"`
}

// Response is the provider-neutral structured output of one model turn. The
// adapter/model implementation is responsible for parsing provider-native text
// or tool calls into this shape.
type Response struct {
	Message    *agent.Message              `json:"message,omitempty"`
	Operations []agent.OperationRequest    `json:"operations,omitempty"`
	Completion *agent.Completion           `json:"completion,omitempty"`
	State      agent.StateUpdate           `json:"state,omitempty"`
	Usage      []usage.Recorded            `json:"usage,omitempty"`
	Transcript coreconversation.Transcript `json:"transcript,omitempty"`
}

// PartialResponseError reports a model failure that still produced transcript
// or usage data that must be preserved for conversation continuity.
type PartialResponseError struct {
	Response Response
	Err      error
}

func (e PartialResponseError) Error() string {
	if e.Err == nil {
		return "model failed with partial response"
	}
	return e.Err.Error()
}

func (e PartialResponseError) Unwrap() error { return e.Err }

// PartialError returns an error carrying the provider response observed before
// failure.
func PartialError(resp Response, err error) error {
	if err == nil {
		return nil
	}
	return PartialResponseError{Response: resp, Err: err}
}

// PartialResponse extracts response data from a partial model error.
func PartialResponse(err error) (Response, bool) {
	var partial PartialResponseError
	if errors.As(err, &partial) {
		return partial.Response, true
	}
	var partialPtr *PartialResponseError
	if errors.As(err, &partialPtr) && partialPtr != nil {
		return partialPtr.Response, true
	}
	return Response{}, false
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
