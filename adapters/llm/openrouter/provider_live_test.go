package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	corellmagent "github.com/fluxplane/agentruntime/core/agent/llmagent"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

const liveGPT55Model = "openai/gpt-5.5"

func TestLiveOpenRouterGPT55ResponsesMatrix(t *testing.T) {
	apiKey := openRouterLiveAPIKey(t)

	cases := []struct {
		name        string
		serviceTier string
		provider    *liveProviderRouting
	}{
		{name: "auto_tier_openrouter_default_routing"},
		{name: "default_tier_openrouter_default_routing", serviceTier: "default"},
		{name: "auto_tier_pinned_openai", provider: pinnedOpenAIProviderRouting()},
		{name: "default_tier_pinned_openai", serviceTier: "default", provider: pinnedOpenAIProviderRouting()},
	}
	attempts := liveAttempts(t)

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name+"/non_streaming", func(t *testing.T) {
			outcome := runLiveResponsesNonStreaming(t, apiKey, tc.serviceTier, tc.provider)
			t.Logf("%s", outcome)
		})
		t.Run(tc.name+"/streaming", func(t *testing.T) {
			for attempt := 1; attempt <= attempts; attempt++ {
				outcome := runLiveResponsesStreaming(t, apiKey, tc.serviceTier, tc.provider, attempt)
				t.Logf("attempt %02d: %s", attempt, outcome)
			}
		})
	}
}

func TestLiveOpenRouterGPT55AdapterStreamReliability(t *testing.T) {
	apiKey := openRouterLiveAPIKey(t)
	model, err := New(Config{
		Model:           liveGPT55Model,
		APIKey:          apiKey,
		ReasoningEffort: "minimal",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	attempts := liveAttempts(t)
	for attempt := 1; attempt <= attempts; attempt++ {
		t.Run(fmt.Sprintf("attempt_%02d", attempt), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			resp, err := model.Stream(ctx, llmagent.Request{
				Goal: "ok",
				Driver: corellmagent.Spec{
					Inference: corellmagent.InferencePolicy{MaxOutputTokens: 16},
				},
			}, nil)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if resp.Message == nil && len(resp.Operations) == 0 && len(resp.Transcript.Items) == 0 {
				t.Fatalf("empty stream response: %#v", resp)
			}
			t.Logf("message=%q operations=%d transcript_items=%d", messageContent(resp), len(resp.Operations), len(resp.Transcript.Items))
		})
	}
}

type liveResponsesRequest struct {
	Model           string               `json:"model"`
	Input           string               `json:"input"`
	MaxOutputTokens int                  `json:"max_output_tokens,omitempty"`
	Reasoning       liveReasoning        `json:"reasoning,omitempty"`
	ServiceTier     string               `json:"service_tier,omitempty"`
	Provider        *liveProviderRouting `json:"provider,omitempty"`
	Stream          bool                 `json:"stream"`
}

type liveReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type liveProviderRouting struct {
	Order             []string `json:"order,omitempty"`
	Ignore            []string `json:"ignore,omitempty"`
	AllowFallbacks    bool     `json:"allow_fallbacks"`
	RequireParameters bool     `json:"require_parameters"`
}

type liveResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Model       string `json:"model"`
	ServiceTier string `json:"service_tier"`
	Error       struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (r liveResponse) OutputText() string {
	var out strings.Builder
	for _, item := range r.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" {
				out.WriteString(part.Text)
			}
		}
	}
	return out.String()
}

type liveStreamResult struct {
	Completed  bool
	OutputText string
	Failed     *liveStreamFailure
	Created    liveStreamResponse
	Final      liveStreamResponse
	Recent     []string
}

type liveStreamResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Model       string `json:"model"`
	ServiceTier string `json:"service_tier"`
}

type liveStreamFailure struct {
	Code    string
	Message string
}

func openRouterLiveAPIKey(t *testing.T) string {
	t.Helper()
	if os.Getenv("TEST_OPENROUTER_LIVE") != "1" {
		t.Skip("set TEST_OPENROUTER_LIVE=1 to run live OpenRouter provider tests")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("set OPENROUTER_API_KEY to run live OpenRouter provider tests")
	}
	return apiKey
}

func liveAttempts(t *testing.T) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_OPENROUTER_LIVE_ATTEMPTS"))
	if raw == "" {
		return 3
	}
	attempts, err := strconv.Atoi(raw)
	if err != nil || attempts < 1 {
		t.Fatalf("TEST_OPENROUTER_LIVE_ATTEMPTS=%q, want positive integer", raw)
	}
	return attempts
}

func runLiveResponsesNonStreaming(t *testing.T, apiKey, serviceTier string, provider *liveProviderRouting) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	body := liveProbeRequest(false, serviceTier, provider, 0)
	resp, raw, err := postOpenRouterResponses(ctx, apiKey, body)
	if err != nil {
		t.Fatalf("non-streaming /responses request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read non-streaming response: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("HTTP %s body=%s request=%s", resp.Status, data, raw)
	}
	var decoded liveResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode non-streaming response: %v body=%s", err, data)
	}
	if decoded.Error.Message != "" {
		return fmt.Sprintf("response status=%s model=%s service_tier=%s error=%s: %s body=%s", decoded.Status, decoded.Model, decoded.ServiceTier, decoded.Error.Code, decoded.Error.Message, data)
	}
	return fmt.Sprintf("response status=%s model=%s service_tier=%s output=%q id=%s", decoded.Status, decoded.Model, decoded.ServiceTier, strings.TrimSpace(decoded.OutputText()), decoded.ID)
}

func runLiveResponsesStreaming(t *testing.T, apiKey, serviceTier string, provider *liveProviderRouting, attempt int) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	body := liveProbeRequest(true, serviceTier, provider, attempt)
	resp, raw, err := postOpenRouterResponses(ctx, apiKey, body)
	if err != nil {
		t.Fatalf("streaming /responses request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Sprintf("HTTP %s body=%s request=%s", resp.Status, data, raw)
	}
	result, err := readOpenRouterSSE(resp.Body)
	if err != nil {
		t.Fatalf("read streaming SSE: %v\nrecent events:\n%s", err, strings.Join(result.Recent, "\n"))
	}
	if result.Failed != nil {
		return fmt.Sprintf("response.failed created_model=%s final_model=%s service_tier=%s code=%s message=%s recent=%s", result.Created.Model, result.Final.Model, result.Final.ServiceTier, result.Failed.Code, result.Failed.Message, strings.Join(result.Recent, " | "))
	}
	return fmt.Sprintf("completed=%v created_model=%s final_model=%s service_tier=%s output=%q recent=%s", result.Completed, result.Created.Model, result.Final.Model, result.Final.ServiceTier, strings.TrimSpace(result.OutputText), strings.Join(result.Recent, " | "))
}

func liveProbeRequest(stream bool, serviceTier string, provider *liveProviderRouting, attempt int) liveResponsesRequest {
	input := "ok"
	if stream {
		input = fmt.Sprintf("ok %d", attempt)
	}
	return liveResponsesRequest{
		Model:           liveGPT55Model,
		Input:           input,
		MaxOutputTokens: 16,
		Reasoning:       liveReasoning{Effort: "minimal"},
		ServiceTier:     serviceTier,
		Provider:        provider,
		Stream:          stream,
	}
}

func pinnedOpenAIProviderRouting() *liveProviderRouting {
	return &liveProviderRouting{
		Order:             []string{"openai"},
		Ignore:            []string{"azure", "microsoft"},
		AllowFallbacks:    false,
		RequireParameters: true,
	}
}

func postOpenRouterResponses(ctx context.Context, apiKey string, body liveResponsesRequest) (*http.Response, []byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, DefaultBaseURL+"/responses", bytes.NewReader(raw))
	if err != nil {
		return nil, raw, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "coder-openrouter-live-test")
	resp, err := http.DefaultClient.Do(req)
	return resp, raw, err
}

func readOpenRouterSSE(body io.Reader) (liveStreamResult, error) {
	var result liveStreamResult
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var eventType string
	var data strings.Builder
	flush := func() error {
		rawData := strings.TrimSpace(data.String())
		data.Reset()
		if rawData == "" {
			eventType = ""
			return nil
		}
		appendRecent(&result, eventType, rawData)
		if rawData == "[DONE]" {
			eventType = ""
			return nil
		}
		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				ID          string `json:"id"`
				Status      string `json:"status"`
				Model       string `json:"model"`
				ServiceTier string `json:"service_tier"`
				Error       struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(rawData), &event); err != nil {
			return fmt.Errorf("decode SSE data %q: %w", rawData, err)
		}
		typ := firstNonEmptyString(event.Type, eventType)
		switch typ {
		case "response.created":
			result.Created = liveStreamResponse{
				ID:          event.Response.ID,
				Status:      event.Response.Status,
				Model:       event.Response.Model,
				ServiceTier: event.Response.ServiceTier,
			}
		case "response.completed":
			result.Completed = true
			result.Final = liveStreamResponse{
				ID:          event.Response.ID,
				Status:      event.Response.Status,
				Model:       event.Response.Model,
				ServiceTier: event.Response.ServiceTier,
			}
		case "response.failed":
			result.Final = liveStreamResponse{
				ID:          event.Response.ID,
				Status:      event.Response.Status,
				Model:       event.Response.Model,
				ServiceTier: event.Response.ServiceTier,
			}
			result.Failed = &liveStreamFailure{
				Code:    event.Response.Error.Code,
				Message: event.Response.Error.Message,
			}
		case "response.output_text.delta":
			result.OutputText += event.Delta
		}
		eventType = ""
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return result, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	if data.Len() > 0 {
		if err := flush(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func appendRecent(result *liveStreamResult, eventType, data string) {
	entry := strings.TrimSpace(eventType)
	if entry == "" {
		entry = "message"
	}
	entry += " " + data
	result.Recent = append(result.Recent, entry)
	if len(result.Recent) > 12 {
		result.Recent = result.Recent[len(result.Recent)-12:]
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func messageContent(resp llmagent.Response) string {
	if resp.Message == nil {
		return ""
	}
	return fmt.Sprint(resp.Message.Content)
}
