package openaiadapter

import "strings"

// ResponsesTransportMode selects the transport used for OpenAI Responses-like
// providers. The current native adapter uses SSE and records this setting for
// provider selection; websocket support is owned by the shared Responses
// transport layer as it lands.
type ResponsesTransportMode string

const (
	ResponsesTransportAuto      ResponsesTransportMode = "auto"
	ResponsesTransportWebSocket ResponsesTransportMode = "websocket"
	ResponsesTransportSSE       ResponsesTransportMode = "sse"
)

// ResponsesCacheMode controls prompt-cache request fields.
type ResponsesCacheMode string

const (
	ResponsesCacheMax  ResponsesCacheMode = "max"
	ResponsesCacheAuto ResponsesCacheMode = "auto"
	ResponsesCacheOff  ResponsesCacheMode = "off"
)

// ResponsesContinuationMode controls provider-side conversation continuation.
type ResponsesContinuationMode string

const (
	ResponsesContinuationAuto     ResponsesContinuationMode = "auto"
	ResponsesContinuationReplay   ResponsesContinuationMode = "replay"
	ResponsesContinuationProvider ResponsesContinuationMode = "provider"
)

// ResponsesOutputMode controls which response payload supplies completed
// output items for streaming requests.
type ResponsesOutputMode string

const (
	ResponsesOutputFinalResponse ResponsesOutputMode = "final_response"
	ResponsesOutputStreamItems   ResponsesOutputMode = "stream_items"
)

// ResponsesRuntimeConfig is the provider-neutral runtime tuning shared by
// OpenAI Responses-compatible adapters.
type ResponsesRuntimeConfig struct {
	Transport    ResponsesTransportMode
	Cache        ResponsesCacheMode
	Continuation ResponsesContinuationMode
	Output       ResponsesOutputMode
	ToolSearch   bool
}

// DefaultResponsesRuntimeConfig returns conservative max-cache defaults used by
// first-party OpenAI-compatible providers.
func DefaultResponsesRuntimeConfig() ResponsesRuntimeConfig {
	return ResponsesRuntimeConfig{
		Transport:    ResponsesTransportAuto,
		Cache:        ResponsesCacheMax,
		Continuation: ResponsesContinuationAuto,
		Output:       ResponsesOutputFinalResponse,
		ToolSearch:   true,
	}
}

func (c ResponsesRuntimeConfig) withDefaults() ResponsesRuntimeConfig {
	if c.Transport == "" {
		c.Transport = ResponsesTransportAuto
	}
	if c.Cache == "" {
		c.Cache = ResponsesCacheMax
	}
	if c.Continuation == "" {
		c.Continuation = ResponsesContinuationAuto
	}
	if c.Output == "" {
		c.Output = ResponsesOutputFinalResponse
	}
	return c
}

func (m ResponsesTransportMode) Valid() bool {
	switch m {
	case "", ResponsesTransportAuto, ResponsesTransportWebSocket, ResponsesTransportSSE:
		return true
	default:
		return false
	}
}

func normalizeProvider(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "openai"
	}
	return name
}
