package codex

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/adapters/llm/openai"
	"github.com/fluxplane/fluxplane-core/core/agent"
	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
)

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
			secondTranscript := appendLiveCodexInput(first.Transcript, "Reply with exactly: done")
			second := liveCodexStream(t, model, liveCodexRequest("live-codex-ws", "", &secondTranscript))
			if second.Message == nil || !strings.Contains(strings.ToLower(fmt.Sprint(second.Message.Content)), "done") {
				t.Fatalf("second websocket output = %#v, want done", second.Message)
			}
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
		})
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

func liveCodexStream(t *testing.T, model *openai.Model, req llmagent.Request) llmagent.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := model.Stream(ctx, req, nil)
	if err != nil {
		t.Fatalf("live codex stream: %v", err)
	}
	if resp.Message == nil {
		t.Fatalf("live codex response has no message: %#v", resp)
	}
	return resp
}

func liveCodexRequest(conversationKey, prompt string, transcript *coreconversation.Transcript) llmagent.Request {
	return llmagent.Request{
		ConversationKey: conversationKey,
		Agent: agent.Spec{
			Name:      "live-codex",
			System:    "You are a concise live-test assistant. Follow the requested output exactly.",
			Inference: agent.InferenceSpec{Model: firstNonEmptyEnv("TEST_CODEX_LIVE_MODEL", "gpt-5.5")},
		},
		Goal:       prompt,
		Transcript: transcript,
	}
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
