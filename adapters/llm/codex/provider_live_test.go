package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/adapters/llm/openai"
	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/channel"
	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/core/usage"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	conversationruntime "github.com/fluxplane/fluxplane-core/runtime/conversation"
	"github.com/fluxplane/fluxplane-core/runtime/eventstore"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimethread "github.com/fluxplane/fluxplane-core/runtime/thread"
	"github.com/fluxplane/fluxplane-operation"
)

const liveCodexSystem = "You are a concise live-test assistant. Follow the requested output exactly."

func TestLiveCodexWebSocketAndSSE(t *testing.T) {
	if os.Getenv("TEST_CODEX_LIVE") != "1" {
		t.Skip("set TEST_CODEX_LIVE=1 to run live Codex provider tests")
	}
	attempts := liveCodexAttempts(t)
	for attempt := 1; attempt <= attempts; attempt++ {
		t.Run("websocket", func(t *testing.T) {
			model := liveCodexModel(t, openai.ResponsesRuntimeConfig{
				Transport:         openai.ResponsesTransportWebSocket,
				Continuation:      openai.ResponsesContinuationProvider,
				WebSocketWarmup:   openai.ResponsesWebSocketWarmupOn,
				Output:            openai.ResponsesOutputStreamItems,
				StreamIdleTimeout: 90 * time.Second,
			})
			first := liveCodexStream(t, model, liveCodexRequest("live-codex-ws", "Reply with exactly: pong", nil))
			if first.Message == nil || !strings.Contains(strings.ToLower(fmt.Sprint(first.Message.Content)), "pong") {
				t.Fatalf("first websocket output = %#v, want pong", first.Message)
			}
			assertLiveUsageEmitted(t, "first websocket", first)
			secondTranscript := appendLiveCodexInput(first.Transcript, "Reply with exactly: done")
			second := liveCodexStream(t, model, liveCodexRequest("live-codex-ws", "", &secondTranscript))
			if second.Message == nil || !strings.Contains(strings.ToLower(fmt.Sprint(second.Message.Content)), "done") {
				t.Fatalf("second websocket output = %#v, want done", second.Message)
			}
			assertLiveUsageEmitted(t, "second websocket", second)
		})
		t.Run("sse", func(t *testing.T) {
			model := liveCodexModel(t, openai.ResponsesRuntimeConfig{
				Transport:         openai.ResponsesTransportSSE,
				Continuation:      openai.ResponsesContinuationReplay,
				Output:            openai.ResponsesOutputStreamItems,
				StreamIdleTimeout: 90 * time.Second,
			})
			resp := liveCodexStream(t, model, liveCodexRequest("live-codex-sse", "Reply with exactly: pong", nil))
			if resp.Message == nil || !strings.Contains(strings.ToLower(fmt.Sprint(resp.Message.Content)), "pong") {
				t.Fatalf("sse output = %#v, want pong", resp.Message)
			}
			assertLiveUsageEmitted(t, "sse", resp)
		})
	}
}

func TestLiveCodexWebSocketUsageIncludesPromptCache(t *testing.T) {
	if os.Getenv("TEST_CODEX_LIVE") != "1" {
		t.Skip("set TEST_CODEX_LIVE=1 to run live Codex provider tests")
	}
	runtime := openai.ResponsesRuntimeConfig{
		Transport:         openai.ResponsesTransportWebSocket,
		Continuation:      openai.ResponsesContinuationProvider,
		Cache:             openai.ResponsesCacheMax,
		WebSocketWarmup:   openai.ResponsesWebSocketWarmupOff,
		Output:            openai.ResponsesOutputStreamItems,
		StreamIdleTimeout: 90 * time.Second,
	}
	conversationKey := fmt.Sprintf("live-codex-cache-%d", time.Now().UnixNano())
	stablePrefix := liveCodexCacheContent()
	firstTranscript := liveCodexCacheTranscript(stablePrefix, "Reply with exactly: cache one")
	first := liveCodexStream(t, liveCodexModel(t, runtime), liveCodexRequestWithSystem(conversationKey, "", liveCodexSystem, &firstTranscript))
	if first.Message == nil || !strings.Contains(strings.ToLower(fmt.Sprint(first.Message.Content)), "cache one") {
		t.Fatalf("first cache output = %#v, want cache one", first.Message)
	}
	assertLiveUsageEmitted(t, "first cache websocket", first)

	secondTranscript := liveCodexCacheTranscript(stablePrefix, "Reply with exactly: cache two")
	second := liveCodexStream(t, liveCodexModel(t, runtime), liveCodexRequestWithSystem(conversationKey, "", liveCodexSystem, &secondTranscript))
	if second.Message == nil || !strings.Contains(strings.ToLower(fmt.Sprint(second.Message.Content)), "cache two") {
		t.Fatalf("second cache output = %#v, want cache two", second.Message)
	}
	assertLiveUsageEmitted(t, "second cache websocket", second)
	if cached := liveUsageQuantity(second.Usage, usage.MetricLLMCachedTokens); cached <= 0 {
		message := fmt.Sprintf("second cache websocket cached tokens = %.0f; Codex live backend did not report a prompt-cache hit for repeated stable input; usage: %s", cached, liveUsageSummary(second.Usage))
		if os.Getenv("TEST_CODEX_LIVE_REQUIRE_CACHE") == "1" {
			t.Fatal(message)
		}
		t.Skip(message + "; set TEST_CODEX_LIVE_REQUIRE_CACHE=1 to make this a hard assertion")
	}
}

func TestLiveCodexWebSocketToolCallContinuation(t *testing.T) {
	if os.Getenv("TEST_CODEX_LIVE") != "1" {
		t.Skip("set TEST_CODEX_LIVE=1 to run live Codex provider tests")
	}
	runtime := openai.ResponsesRuntimeConfig{
		Transport:         openai.ResponsesTransportWebSocket,
		Continuation:      openai.ResponsesContinuationProvider,
		Cache:             openai.ResponsesCacheMax,
		WebSocketWarmup:   openai.ResponsesWebSocketWarmupOff,
		Output:            openai.ResponsesOutputStreamItems,
		StreamIdleTimeout: 90 * time.Second,
	}
	model := liveCodexModel(t, runtime)
	conversationKey := fmt.Sprintf("live-codex-tool-%d", time.Now().UnixNano())
	prompt := "Call lookup_weather exactly once with city Berlin. After the tool result is available, reply with exactly: tool-ok"
	system := "You must use the provided tool for weather lookup requests. Do not answer from memory when a matching tool is available. After a tool result is present, answer from the tool result."
	first := liveCodexRawStream(t, model, liveCodexRequestWithSystem(
		conversationKey,
		"",
		system,
		&coreconversation.Transcript{
			Items: []coreconversation.Item{{
				Kind:    coreconversation.ItemInput,
				Role:    "user",
				Content: prompt,
			}},
			NewItems: []coreconversation.Item{{
				Kind:    coreconversation.ItemInput,
				Role:    "user",
				Content: prompt,
			}},
			Mode: coreconversation.ProjectionFullReplay,
		},
	), liveCodexWeatherTool())
	if len(first.Operations) != 1 {
		t.Fatalf("first websocket operations = %#v, want one tool call; message=%#v transcript=%#v", first.Operations, first.Message, first.Transcript.Items)
	}
	op := first.Operations[0]
	if op.Operation.Name != "lookup_weather" {
		t.Fatalf("operation = %s, want lookup_weather", op.Operation.String())
	}
	if strings.TrimSpace(op.ProviderCallID) == "" {
		t.Fatalf("provider call id is empty: %#v", op)
	}
	if strings.HasPrefix(op.ProviderCallID, "fc_") || strings.HasPrefix(op.ProviderCallID, "ctc_") {
		t.Fatalf("provider call id = %q, looks like a Responses item id", op.ProviderCallID)
	}
	assertLiveUsageEmitted(t, "tool call websocket", first)
	followTranscript := appendLiveCodexToolResult(first.Transcript, op, map[string]any{
		"city":          "Berlin",
		"temperature_c": 21,
		"condition":     "clear",
		"summary":       "tool-ok",
	})
	if err := conversationruntime.ValidateContinuity(followTranscript.Items, conversationruntime.ValidateOptions{Provider: first.Transcript.Provider}); err != nil {
		t.Fatalf("tool continuation transcript is invalid: %v", err)
	}
	second := liveCodexStream(t, model, liveCodexRequestWithSystem(
		conversationKey,
		"",
		system,
		&followTranscript,
	), liveCodexWeatherTool())
	if second.Message == nil || !strings.Contains(strings.ToLower(fmt.Sprint(second.Message.Content)), "tool-ok") {
		t.Fatalf("tool follow-up output = %#v, want tool-ok", second.Message)
	}
	assertLiveUsageEmitted(t, "tool result websocket", second)
}

func TestLiveCodexSessionWebSocketToolCallContinuation(t *testing.T) {
	if os.Getenv("TEST_CODEX_LIVE") != "1" {
		t.Skip("set TEST_CODEX_LIVE=1 to run live Codex provider tests")
	}
	runtime := openai.ResponsesRuntimeConfig{
		Transport:         openai.ResponsesTransportWebSocket,
		Continuation:      openai.ResponsesContinuationProvider,
		Cache:             openai.ResponsesCacheMax,
		WebSocketWarmup:   openai.ResponsesWebSocketWarmupOff,
		Output:            openai.ResponsesOutputStreamItems,
		StreamIdleTimeout: 90 * time.Second,
	}
	model := liveCodexModel(t, runtime)
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:      "live-codex",
		Driver:    agent.DriverSpec{Kind: llmagent.DriverKind},
		System:    "You must use lookup_weather for weather lookup requests. After the tool result is present, answer exactly: tool-ok",
		Inference: agent.InferenceSpec{Model: firstNonEmptyEnv("TEST_CODEX_LIVE_MODEL", "gpt-5.5")},
	}, model)
	if err != nil {
		t.Fatalf("new live llm agent: %v", err)
	}
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{
		Ref:         operation.Ref{Name: "lookup_weather"},
		Description: "Return deterministic weather for the requested city.",
		Input:       liveCodexWeatherInputType(),
	}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(map[string]any{
			"input":         input,
			"city":          "Berlin",
			"temperature_c": 21,
			"condition":     "clear",
			"summary":       "tool-ok",
		})
	})); err != nil {
		t.Fatalf("register lookup_weather: %v", err)
	}
	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	threadID := corethread.ID(fmt.Sprintf("live-codex-session-tool-%d", time.Now().UnixNano()))
	if _, err := threadStore.Create(context.Background(), corethread.CreateParams{ID: threadID}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	s := session.Session{
		Agent:             runtimeAgent,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
		Thread:            corethread.Ref{ID: threadID},
		TurnTools:         []tool.Spec{liveCodexWeatherTool()},
	}
	result := s.ExecuteInboundInput(context.Background(), channel.Inbound{
		ID:      "live-tool-run",
		Kind:    channel.InboundMessage,
		Message: &channel.Message{Content: "Call lookup_weather exactly once with city Berlin, then answer exactly tool-ok after the tool result."},
	})
	if result.Status != session.InputStatusOK {
		t.Fatalf("session result = %#v, want ok", result)
	}
	if result.Agent.Decision.Message == nil || !strings.Contains(strings.ToLower(fmt.Sprint(result.Agent.Decision.Message.Content)), "tool-ok") {
		t.Fatalf("session final message = %#v, want tool-ok", result.Agent.Decision.Message)
	}
	snapshot, err := threadStore.Read(context.Background(), corethread.ReadParams{ID: threadID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	provider := model.ProviderIdentity(liveCodexRequest("", "", nil))
	projected, err := conversationruntime.Project(conversationruntime.ProjectionInput{
		Thread:   snapshot,
		Provider: provider,
		Mode:     coreconversation.ProjectionFullReplay,
	})
	if err != nil {
		t.Fatalf("project live session transcript: %v", err)
	}
	if err := conversationruntime.ValidateContinuity(projected.Items, conversationruntime.ValidateOptions{Provider: provider}); err != nil {
		t.Fatalf("live session transcript continuity: %v", err)
	}
	if !liveTranscriptHasCallAndResult(projected.Items, "lookup_weather") {
		t.Fatalf("projected items missing lookup_weather call/result pair: %#v", projected.Items)
	}
}

func liveCodexModel(t *testing.T, runtime openai.ResponsesRuntimeConfig) *openai.Model {
	t.Helper()
	cfg := Config{
		Model:   firstNonEmptyEnv("TEST_CODEX_LIVE_MODEL", "gpt-5.5"),
		BaseURL: strings.TrimSpace(os.Getenv("TEST_CODEX_LIVE_BASE_URL")),
		Runtime: runtime,
	}
	model, err := New(cfg)
	if err != nil {
		t.Fatalf("new live codex model: %v", err)
	}
	return model
}

func liveCodexStream(t *testing.T, model *openai.Model, req llmagent.Request, tools ...tool.Spec) llmagent.Response {
	t.Helper()
	resp := liveCodexRawStream(t, model, req, tools...)
	if resp.Message == nil {
		t.Fatalf("live codex response has no message: %#v", resp)
	}
	return resp
}

func liveCodexRawStream(t *testing.T, model *openai.Model, req llmagent.Request, tools ...tool.Spec) llmagent.Response {
	t.Helper()
	req.Tools = append(req.Tools, tools...)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := model.Stream(ctx, req, nil)
	if err != nil {
		t.Fatalf("live codex stream: %v", err)
	}
	return resp
}

func liveCodexRequest(conversationKey, prompt string, transcript *coreconversation.Transcript) llmagent.Request {
	return liveCodexRequestWithSystem(conversationKey, prompt, liveCodexSystem, transcript)
}

func liveCodexRequestWithSystem(conversationKey, prompt, system string, transcript *coreconversation.Transcript) llmagent.Request {
	return llmagent.Request{
		ConversationKey: conversationKey,
		Agent: agent.Spec{
			Name:      "live-codex",
			System:    system,
			Inference: agent.InferenceSpec{Model: firstNonEmptyEnv("TEST_CODEX_LIVE_MODEL", "gpt-5.5")},
		},
		Goal:       prompt,
		Transcript: transcript,
	}
}

func liveCodexCacheContent() string {
	var b strings.Builder
	b.WriteString("Use the following stable context only as cacheable background. Do not quote it unless asked.\n")
	for i := 0; i < 180; i++ {
		fmt.Fprintf(&b, "cache line %03d: fluxplane websocket codex prompt-cache stability marker alpha beta gamma delta epsilon zeta eta theta.\n", i)
	}
	return b.String()
}

func liveCodexCacheTranscript(stablePrefix, prompt string) coreconversation.Transcript {
	items := []coreconversation.Item{
		{
			Kind:    coreconversation.ItemInput,
			Role:    "developer",
			Content: stablePrefix,
		},
		{
			Kind:    coreconversation.ItemInput,
			Role:    "user",
			Content: prompt,
		},
	}
	return coreconversation.Transcript{
		Items:    append([]coreconversation.Item(nil), items...),
		NewItems: append([]coreconversation.Item(nil), items...),
		Mode:     coreconversation.ProjectionFullReplay,
	}
}

func liveCodexWeatherTool() tool.Spec {
	return tool.Spec{
		Name:        "lookup_weather",
		Description: "Return deterministic weather for the requested city.",
		Target:      invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "lookup_weather"}},
		Input:       liveCodexWeatherInputType(),
	}
}

func liveCodexWeatherInputType() operation.Type {
	return operation.Type{Schema: operation.Schema{
		Format: "json-schema",
		Data: json.RawMessage(`{
			"type":"object",
			"properties":{"city":{"type":"string","description":"City to look up."}},
			"required":["city"],
			"additionalProperties":false
		}`),
	}}
}

func appendLiveCodexToolResult(transcript coreconversation.Transcript, op agent.OperationRequest, content any) coreconversation.Transcript {
	provider := transcript.Provider
	item := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemToolResult,
		CallID:   op.ProviderCallID,
		Name:     op.Operation.String(),
		Content:  content,
	}
	if op.ProviderCallType != "" {
		item.Metadata = map[string]string{"provider_call_type": op.ProviderCallType}
	}
	items := append([]coreconversation.Item(nil), transcript.Items...)
	items = append(items, item)
	return coreconversation.Transcript{
		Provider: provider,
		Items:    items,
		NewItems: []coreconversation.Item{item},
		Mode:     coreconversation.ProjectionFullReplay,
	}
}

func liveTranscriptHasCallAndResult(items []coreconversation.Item, name string) bool {
	open := map[string]bool{}
	for _, item := range items {
		if item.Kind == coreconversation.ItemOutput && item.Name == name {
			for _, call := range item.ToolCallRefs() {
				if strings.TrimSpace(call.CallID) != "" {
					open[call.CallID] = true
				}
			}
		}
		if item.Kind == coreconversation.ItemToolResult && item.Name == name && open[item.CallID] {
			return true
		}
	}
	return false
}

func assertLiveUsageEmitted(t *testing.T, label string, resp llmagent.Response) {
	t.Helper()
	if len(resp.Usage) == 0 {
		t.Fatalf("%s usage = none, want provider token usage", label)
	}
	t.Logf("%s usage: %s", label, liveUsageSummary(resp.Usage))
	input := liveUsageQuantity(resp.Usage, usage.MetricLLMInputTokens)
	cached := liveUsageQuantity(resp.Usage, usage.MetricLLMCachedTokens)
	output := liveUsageQuantity(resp.Usage, usage.MetricLLMOutputTokens)
	total := liveUsageQuantity(resp.Usage, usage.MetricLLMTotalTokens)
	if input+cached+output+total <= 0 {
		t.Fatalf("%s usage has no token measurements: %s", label, liveUsageSummary(resp.Usage))
	}
}

func liveUsageQuantity(records []usage.Recorded, metric usage.MetricName) float64 {
	var total float64
	for _, record := range records {
		for _, measurement := range record.Measurements {
			if measurement.Metric == metric {
				total += measurement.Quantity
			}
		}
	}
	return total
}

func liveUsageSummary(records []usage.Recorded) string {
	if len(records) == 0 {
		return "<none>"
	}
	var b strings.Builder
	for i, record := range records {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s/%s id=%s", record.Subject.Provider, record.Subject.Name, record.Subject.ID)
		for _, measurement := range record.Measurements {
			fmt.Fprintf(&b, " %s=%.0f", measurement.Metric, measurement.Quantity)
		}
	}
	return b.String()
}

func appendLiveCodexInput(transcript coreconversation.Transcript, prompt string) coreconversation.Transcript {
	provider := transcript.Provider
	item := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemInput,
		Role:     "user",
		Content:  prompt,
	}
	transcript.Items = append(append([]coreconversation.Item(nil), transcript.Items...), item)
	transcript.NewItems = []coreconversation.Item{item}
	transcript.Continuation = nil
	transcript.Mode = coreconversation.ProjectionFullReplay
	return transcript
}

func liveCodexAttempts(t *testing.T) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_CODEX_LIVE_ATTEMPTS"))
	if raw == "" {
		return 1
	}
	attempts, err := strconv.Atoi(raw)
	if err != nil || attempts < 1 {
		t.Fatalf("TEST_CODEX_LIVE_ATTEMPTS=%q, want positive integer", raw)
	}
	return attempts
}

func firstNonEmptyEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
