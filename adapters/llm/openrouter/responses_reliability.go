package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/option"
)

const (
	openRouterResponsesAttempts = 3
	openRouterRetryBaseWait     = 200 * time.Millisecond
	openRouterRetryMaxWait      = 2 * time.Second
	// maxNonStreamingResponseBytes bounds the non-streaming fallback body so a
	// misbehaving or hostile upstream cannot exhaust process memory.
	maxNonStreamingResponseBytes = 100 * 1024 * 1024
)

func responsesReliabilityMiddleware() option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if req == nil || !isResponsesRequest(req) || req.Body == nil {
			return next(req)
		}
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("openrouter: read responses request body: %w", err)
		}
		body = shapeResponsesRequest(body)
		streaming := requestStreams(body)
		if !streaming {
			attemptReq := requestWithBody(req, body)
			return next(attemptReq)
		}
		var lastFailure *streamFailure
		streamAttempts := openRouterResponsesAttempts - 1
		for attempt := 0; attempt < streamAttempts; attempt++ {
			resp, err := next(requestWithBody(req, body))
			if err != nil {
				return resp, err
			}
			decision, err := inspectResponsesStream(resp)
			if err != nil {
				return nil, err
			}
			switch decision.kind {
			case streamDecisionReturn:
				return decision.response, nil
			case streamDecisionRetry:
				lastFailure = decision.failure
				if attempt < streamAttempts-1 {
					wait := openRouterRetryWait(attempt)
					if decision.wait > wait {
						wait = decision.wait
					}
					if err := sleepOpenRouterRetry(req.Context(), wait); err != nil {
						return nil, err
					}
				}
			}
		}
		fallbackBody := nonStreamingRequestBody(body)
		resp, err := next(requestWithBody(req, fallbackBody))
		if err != nil {
			if lastFailure != nil {
				return nil, streamFailedAfterAttempts(lastFailure)
			}
			return resp, err
		}
		return synthesizeStreamFromResponse(resp, lastFailure)
	}
}

func isResponsesRequest(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	return req.Method == http.MethodPost && strings.HasSuffix(strings.TrimRight(req.URL.Path, "/"), "/responses")
}

func requestWithBody(req *http.Request, body []byte) *http.Request {
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return clone
}

func shapeResponsesRequest(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	model, _ := payload["model"].(string)
	if strings.TrimSpace(model) == "openai/gpt-5.5" {
		if _, ok := payload["service_tier"]; !ok {
			payload["service_tier"] = "default"
		}
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return out
}

func requestStreams(body []byte) bool {
	var payload struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Stream
}

func nonStreamingRequestBody(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	payload["stream"] = false
	out, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return out
}

type streamDecisionKind int

const (
	streamDecisionReturn streamDecisionKind = iota
	streamDecisionRetry
)

type streamDecision struct {
	kind     streamDecisionKind
	response *http.Response
	failure  *streamFailure
	wait     time.Duration
}

type streamFailure struct {
	Code    string
	Message string
}

func inspectResponsesStream(resp *http.Response) (streamDecision, error) {
	if failure := transientHTTPFailure(resp); failure != nil {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
		}
		return streamDecision{
			kind:    streamDecisionRetry,
			failure: failure,
			wait:    retryAfterWait(resp.Header.Get("Retry-After")),
		}, nil
	}
	if resp == nil || resp.Body == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || !isEventStream(resp.Header.Get("Content-Type")) {
		return streamDecision{kind: streamDecisionReturn, response: resp}, nil
	}
	originalBody := resp.Body
	reader := bufio.NewReader(originalBody)
	var prefix bytes.Buffer
	var eventType string
	var data strings.Builder
	outputStarted := false
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			prefix.WriteString(line)
			trimmed := strings.TrimRight(line, "\r\n")
			switch {
			case trimmed == "":
				failure, started, parseErr := inspectSSEFrame(eventType, data.String())
				eventType = ""
				data.Reset()
				if parseErr != nil {
					restored := cloneResponseWithBody(resp, &readCloser{
						Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), reader),
						Closer: originalBody,
					})
					return streamDecision{kind: streamDecisionReturn, response: restored}, nil
				}
				if started {
					restored := cloneResponseWithBody(resp, &readCloser{
						Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), reader),
						Closer: originalBody,
					})
					return streamDecision{kind: streamDecisionReturn, response: restored}, nil
				}
				if failure != nil {
					if outputStarted || !failure.retryable() {
						restored := cloneResponseWithBody(resp, io.NopCloser(bytes.NewReader(prefix.Bytes())))
						_ = originalBody.Close()
						return streamDecision{kind: streamDecisionReturn, response: restored}, nil
					}
					_ = originalBody.Close()
					return streamDecision{kind: streamDecisionRetry, failure: failure}, nil
				}
			case strings.HasPrefix(trimmed, "event:"):
				eventType = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			case strings.HasPrefix(trimmed, "data:"):
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				restored := cloneResponseWithBody(resp, io.NopCloser(bytes.NewReader(prefix.Bytes())))
				_ = originalBody.Close()
				return streamDecision{kind: streamDecisionReturn, response: restored}, nil
			}
			return streamDecision{}, err
		}
	}
}

func inspectSSEFrame(eventType, rawData string) (*streamFailure, bool, error) {
	rawData = strings.TrimSpace(rawData)
	if rawData == "" || rawData == "[DONE]" {
		return nil, false, nil
	}
	var event struct {
		Type     string `json:"type"`
		Response struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(rawData), &event); err != nil {
		return nil, false, err
	}
	typ := firstNonEmptyValue(strings.TrimSpace(event.Type), strings.TrimSpace(eventType))
	if responseStreamOutputStarted(typ, rawData) {
		return nil, true, nil
	}
	if typ == "response.failed" {
		return &streamFailure{
			Code:    strings.TrimSpace(event.Response.Error.Code),
			Message: strings.TrimSpace(event.Response.Error.Message),
		}, false, nil
	}
	return nil, false, nil
}

func responseStreamOutputStarted(typ, rawData string) bool {
	switch typ {
	case "response.output_text.delta", "response.output_text.done", "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		return true
	case "response.output_item.done":
		var event struct {
			Item struct {
				Type string `json:"type"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(rawData), &event); err != nil {
			return false
		}
		switch event.Item.Type {
		case "message", "function_call", "custom_tool_call":
			return true
		}
	}
	return false
}

func (f *streamFailure) retryable() bool {
	if f == nil {
		return false
	}
	switch f.Code {
	case "server_error", "rate_limit_exceeded":
		return true
	default:
		return false
	}
}

func transientHTTPFailure(resp *http.Response) *streamFailure {
	if resp == nil {
		return nil
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return &streamFailure{Code: "rate_limit_exceeded", Message: resp.Status}
	case resp.StatusCode >= 500 && resp.StatusCode <= 599:
		return &streamFailure{Code: "server_error", Message: resp.Status}
	default:
		return nil
	}
}

func synthesizeStreamFromResponse(resp *http.Response, lastFailure *streamFailure) (*http.Response, error) {
	if resp == nil {
		return nil, streamFailedAfterAttempts(lastFailure)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, streamFailedAfterAttempts(lastFailure)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxNonStreamingResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("openrouter: read non-streaming fallback: %w", err)
	}
	status, failure := responseStatus(data)
	if status != "completed" && failure != nil {
		return nil, streamFailedAfterAttempts(failure)
	}
	if status == "" && failure != nil {
		return nil, streamFailedAfterAttempts(failure)
	}
	if status != "completed" {
		return nil, streamFailedAfterAttempts(&streamFailure{
			Code:    firstNonEmptyValue(status, "unexpected_status"),
			Message: "non-streaming fallback did not complete",
		})
	}
	stream := bytes.NewBuffer(nil)
	_, _ = stream.WriteString("data: ")
	stream.Write(completedStreamEvent(data))
	_, _ = stream.WriteString("\n\ndata: [DONE]\n\n")
	out := cloneResponseWithBody(resp, io.NopCloser(bytes.NewReader(stream.Bytes())))
	out.Header = resp.Header.Clone()
	out.Header.Set("Content-Type", "text/event-stream")
	out.ContentLength = int64(stream.Len())
	return out, nil
}

func responseStatus(data []byte) (string, *streamFailure) {
	var response struct {
		Status string `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", nil
	}
	var failure *streamFailure
	if response.Error.Code != "" || response.Error.Message != "" {
		failure = &streamFailure{Code: strings.TrimSpace(response.Error.Code), Message: strings.TrimSpace(response.Error.Message)}
	}
	return strings.TrimSpace(response.Status), failure
}

func completedStreamEvent(response []byte) []byte {
	return []byte(`{"type":"response.completed","response":` + string(response) + `}`)
}

func streamFailedAfterAttempts(failure *streamFailure) error {
	if failure == nil {
		return fmt.Errorf("openrouter: responses stream failed after %d attempts", openRouterResponsesAttempts)
	}
	detail := strings.TrimSpace(failure.Code)
	if failure.Message != "" {
		if detail != "" {
			detail += ": "
		}
		detail += failure.Message
	}
	if detail == "" {
		detail = "unknown error"
	}
	return fmt.Errorf("openrouter: responses stream failed after %d attempts: %s", openRouterResponsesAttempts, detail)
}

func cloneResponseWithBody(resp *http.Response, body io.ReadCloser) *http.Response {
	clone := new(http.Response)
	*clone = *resp
	clone.Body = body
	return clone
}

func isEventStream(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "text/event-stream")
}

func openRouterRetryWait(attempt int) time.Duration {
	multiplier := math.Pow(2, float64(attempt))
	wait := time.Duration(float64(openRouterRetryBaseWait) * multiplier)
	if wait > 0 {
		wait += time.Duration(time.Now().UnixNano() % int64(wait/4+1))
	}
	if wait > openRouterRetryMaxWait {
		return openRouterRetryMaxWait
	}
	return wait
}

func sleepOpenRouterRetry(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retryAfterWait(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if wait := time.Until(retryAt); wait > 0 {
			return wait
		}
	}
	return 0
}

type readCloser struct {
	io.Reader
	io.Closer
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
