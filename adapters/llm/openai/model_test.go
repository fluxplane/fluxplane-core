package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	adapterllm "github.com/fluxplane/fluxplane-core/adapters/llm"
	"github.com/fluxplane/fluxplane-core/core/agent"
	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/core/usage"
	"github.com/openai/openai-go/v3/responses"

	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
)

func TestResponseParamsUsesRequestModelAndTools(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, tools, _, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:  "Say hello.",
		Tools: []tool.Spec{{
			Name:        "inspect",
			Description: "Inspect a path.",
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "inspect"},
			},
			Input: operation.Type{Schema: operation.Schema{Data: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
		}},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "inspect" {
		t.Fatalf("tools = %#v, want inspect", tools)
	}
}

func TestResponseParamsUsesAgentSystemAsInstructionFallback(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, _, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{
			System:    "You are the stop evaluator.",
			Inference: agent.InferenceSpec{Model: "gpt-test"},
		},
		Goal: "Decide whether to continue.",
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(raw), `"instructions":"You are the stop evaluator."`) {
		t.Fatalf("params JSON = %s, want agent system instructions", raw)
	}
	if strings.Contains(string(raw), `"input":"`) || !strings.Contains(string(raw), `"type":"input_text"`) {
		t.Fatalf("params JSON = %s, want input item list", raw)
	}
}

func TestTranscriptSystemContextItemUsesInputRoleSystem(t *testing.T) {
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses"}
	item := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemInput,
		Role:     "system",
		Content:  "<system-context>rules</system-context>",
	}
	param, recorded, err := inputItemFromTranscriptItem(provider, item)
	if err != nil {
		t.Fatalf("inputItemFromTranscriptItem: %v", err)
	}
	if recorded.Role != "system" {
		t.Fatalf("recorded role = %q, want system", recorded.Role)
	}
	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal param: %v", err)
	}
	if !strings.Contains(string(data), `"role":"system"`) || strings.Contains(string(data), `"instructions"`) {
		t.Fatalf("param JSON = %s, want transcript input role system", data)
	}
}

func TestResponseParamsDefaultsToMaxCaching(t *testing.T) {
	model, err := New(Config{Model: "gpt-5.5"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, _, err := model.responseParams(llmagent.Request{Agent: agent.Spec{Name: "coder"}, Goal: "hello"})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	json := string(raw)
	for _, want := range []string{`"store":true`, `"prompt_cache_key":"fluxplane:openai:gpt-5.5:coder"`, `"prompt_cache_retention":"24h"`, `"reasoning.encrypted_content"`, `"summary":"auto"`} {
		if !strings.Contains(json, want) {
			t.Fatalf("params json = %s, want %s", json, want)
		}
	}
}

func TestResponseParamsCacheAutoIsProviderNeutral(t *testing.T) {
	model, err := New(Config{
		Model: "gpt-5.5",
		Runtime: ResponsesRuntimeConfig{
			Cache:        ResponsesCacheAuto,
			Continuation: ResponsesContinuationReplay,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, _, err := model.responseParams(llmagent.Request{Agent: agent.Spec{Name: "coder"}, Goal: "hello"})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	json := string(raw)
	for _, notWant := range []string{`"prompt_cache_key"`, `"prompt_cache_retention"`, `"reasoning.encrypted_content"`, `"store":true`} {
		if strings.Contains(json, notWant) {
			t.Fatalf("params json = %s, did not want %s", json, notWant)
		}
	}
	if !strings.Contains(json, `"store":false`) {
		t.Fatalf("params json = %s, want explicit store false", json)
	}
}

func TestResponseParamsAppliesReasoningEffort(t *testing.T) {
	model, err := New(Config{Model: "gpt-5.5", ReasoningEffort: "minimal", ReasoningSummary: "concise"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, _, err := model.responseParams(llmagent.Request{Agent: agent.Spec{Name: "coder"}, Goal: "hello"})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	json := string(raw)
	for _, want := range []string{`"effort":"minimal"`, `"summary":"concise"`} {
		if !strings.Contains(json, want) {
			t.Fatalf("params json = %s, want %s", json, want)
		}
	}
}

func TestResponseParamsRejectsMissingModel(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, _, err = model.responseParams(llmagent.Request{Agent: agent.Spec{Name: "coder"}})
	if !errors.Is(err, ErrModelMissing) {
		t.Fatalf("error = %v, want ErrModelMissing", err)
	}
}

func TestResponseParamsUsesTranscriptItemsAndPreviousResponseID(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"},
			Mode:     coreconversation.ProjectionNativeContinuation,
			Continuation: &coreconversation.ContinuationHandle{
				Provider:   coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"},
				Mode:       coreconversation.ContinuationPreviousResponseID,
				Transport:  coreconversation.TransportHTTPSSE,
				ResponseID: "resp_prev",
			},
			Items: []coreconversation.Item{{
				Kind:    coreconversation.ItemToolResult,
				CallID:  "call_1",
				Content: map[string]any{"ok": true},
			}},
			NewItems: []coreconversation.Item{{
				Kind:    coreconversation.ItemToolResult,
				CallID:  "call_1",
				Content: map[string]any{"ok": true},
			}},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(raw), `"previous_response_id":"resp_prev"`) || !strings.Contains(string(raw), `"type":"function_call_output"`) {
		t.Fatalf("params json = %s, want previous_response_id and function_call_output", raw)
	}
	if len(sent) != 1 || sent[0].CallID != "call_1" || len(sent[0].Native) == 0 {
		t.Fatalf("sent = %#v, want native tool output", sent)
	}
}

func TestResponseParamsUsesCustomToolCallOutputForCustomCalls(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Provider: "codex", API: "codex.responses", Family: "responses", Model: "gpt-test"},
			Mode:     coreconversation.ProjectionNativeContinuation,
			Continuation: &coreconversation.ContinuationHandle{
				Provider:   coreconversation.ProviderIdentity{Provider: "codex", API: "codex.responses", Family: "responses", Model: "gpt-test"},
				Mode:       coreconversation.ContinuationPreviousResponseID,
				Transport:  coreconversation.TransportHTTPSSE,
				ResponseID: "resp_prev",
			},
			Items: []coreconversation.Item{{
				Kind:     coreconversation.ItemToolResult,
				CallID:   "call_1",
				Content:  map[string]any{"ok": true},
				Metadata: map[string]string{"provider_call_type": "custom_tool_call"},
			}},
			NewItems: []coreconversation.Item{{
				Kind:     coreconversation.ItemToolResult,
				CallID:   "call_1",
				Content:  map[string]any{"ok": true},
				Metadata: map[string]string{"provider_call_type": "custom_tool_call"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(raw), `"previous_response_id":"resp_prev"`) || !strings.Contains(string(raw), `"type":"custom_tool_call_output"`) {
		t.Fatalf("params json = %s, want previous_response_id and custom_tool_call_output", raw)
	}
	if len(sent) != 1 || sent[0].Metadata["provider_call_type"] != "custom_tool_call" || len(sent[0].Native) == 0 {
		t.Fatalf("sent = %#v, want native custom tool output", sent)
	}
}

func TestResponseParamsRendersCanonicalFunctionToolCallReplay(t *testing.T) {
	model, err := New(Config{Runtime: ResponsesRuntimeConfig{Continuation: ResponsesContinuationReplay}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	call := coreconversation.Item{
		Kind:   coreconversation.ItemOutput,
		CallID: "call_1",
		Name:   "task_create",
		ToolCalls: []coreconversation.ToolCallRef{{
			CallID: "call_1",
			Name:   "task_create",
			Type:   "function_call",
			Input:  map[string]string{"title": "Fix"},
		}},
		Metadata: map[string]string{"provider_call_type": "function_call"},
	}
	result := coreconversation.Item{
		Kind:    coreconversation.ItemToolResult,
		CallID:  "call_1",
		Name:    "task_create",
		Content: map[string]any{"ok": true},
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Provider: "openrouter", API: "openrouter.responses", Family: "responses", Model: "anthropic/claude-sonnet-4.6"},
			Mode:     coreconversation.ProjectionFullReplay,
			Items: []coreconversation.Item{
				{Kind: coreconversation.ItemInput, Role: "user", Content: "make a plan"},
				call,
				result,
			},
			NewItems: []coreconversation.Item{call, result},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	json := string(raw)
	if strings.Contains(json, `"role":"developer"`) {
		t.Fatalf("params json = %s, did not want developer message", raw)
	}
	if !strings.Contains(json, `"type":"function_call"`) || !strings.Contains(json, `"type":"function_call_output"`) {
		t.Fatalf("params json = %s, want tool call and tool output", raw)
	}
	if !strings.Contains(json, "task_create") || !strings.Contains(json, "Fix") {
		t.Fatalf("params json = %s, want original tool call and result", raw)
	}
	if len(sent) != 2 || sent[0].Kind != coreconversation.ItemOutput || sent[0].Metadata["repair"] != "" || sent[1].Kind != coreconversation.ItemToolResult || sent[1].CallID != "call_1" {
		t.Fatalf("sent = %#v, want tool call plus original tool result", sent)
	}
}

func TestResponseParamsRendersCanonicalCustomToolReplay(t *testing.T) {
	model, err := New(Config{Runtime: ResponsesRuntimeConfig{Continuation: ResponsesContinuationReplay}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	call := coreconversation.Item{
		Kind:   coreconversation.ItemOutput,
		CallID: "call_custom",
		Name:   "inspect",
		ToolCalls: []coreconversation.ToolCallRef{{
			CallID: "call_custom",
			Name:   "inspect",
			Type:   "custom_tool_call",
			Input:  map[string]string{"path": "README.md"},
		}},
		Metadata: map[string]string{"provider_call_type": "custom_tool_call"},
	}
	result := coreconversation.Item{
		Kind:     coreconversation.ItemToolResult,
		CallID:   "call_custom",
		Name:     "inspect",
		Content:  "custom result",
		Metadata: map[string]string{"provider_call_type": "custom_tool_call"},
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Provider: "codex", API: "codex.responses", Family: "responses", Model: "gpt-test"},
			Mode:     coreconversation.ProjectionFullReplay,
			Items:    []coreconversation.Item{call, result},
			NewItems: []coreconversation.Item{call, result},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	json := string(raw)
	if strings.Contains(json, `"role":"developer"`) {
		t.Fatalf("params json = %s, did not want developer message", raw)
	}
	if !strings.Contains(json, `"type":"custom_tool_call"`) || !strings.Contains(json, `"type":"custom_tool_call_output"`) {
		t.Fatalf("params json = %s, want custom tool call and output", raw)
	}
	if len(sent) != 2 || sent[0].Metadata["provider_call_type"] != "custom_tool_call" || sent[0].Metadata["repair"] != "" || sent[1].Metadata["provider_call_type"] != "custom_tool_call" {
		t.Fatalf("sent = %#v, want custom call plus original custom result", sent)
	}
}

func TestResponseParamsPreservesMatchedReplayToolResult(t *testing.T) {
	model, err := New(Config{Runtime: ResponsesRuntimeConfig{Continuation: ResponsesContinuationReplay}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Provider: "openrouter", API: "openrouter.responses", Family: "responses", Model: "anthropic/claude-sonnet-4.6"},
			Mode:     coreconversation.ProjectionFullReplay,
			Items: []coreconversation.Item{
				{Kind: coreconversation.ItemInput, Role: "user", Content: "make a plan"},
				{
					Kind:   coreconversation.ItemOutput,
					CallID: "call_1",
					Name:   "task_create",
					Native: []byte(`{"type":"function_call","call_id":"call_1","name":"task_create","arguments":"{\"title\":\"Fix\"}"}`),
				},
				{Kind: coreconversation.ItemToolResult, CallID: "call_1", Name: "task_create", Content: map[string]any{"ok": true}},
			},
			NewItems: []coreconversation.Item{{Kind: coreconversation.ItemToolResult, CallID: "call_1", Name: "task_create", Content: map[string]any{"ok": true}}},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	json := string(raw)
	if !strings.Contains(json, `"type":"function_call"`) || !strings.Contains(json, `"type":"function_call_output"`) {
		t.Fatalf("params json = %s, want matched tool call and output", raw)
	}
	if len(sent) != 1 || sent[0].Kind != coreconversation.ItemToolResult || sent[0].CallID != "call_1" || len(sent[0].Native) == 0 {
		t.Fatalf("sent = %#v, want native tool result", sent)
	}
}

func TestResponseParamsPreservesMatchedMultiToolReplayResults(t *testing.T) {
	model, err := New(Config{Runtime: ResponsesRuntimeConfig{Continuation: ResponsesContinuationReplay}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Provider: "openrouter", API: "openrouter.responses", Family: "responses", Model: "anthropic/claude-sonnet-4.6"},
			Mode:     coreconversation.ProjectionFullReplay,
			Items: []coreconversation.Item{
				{Kind: coreconversation.ItemInput, Role: "user", Content: "inspect"},
				{Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "read", Native: []byte(`{"type":"function_call","call_id":"call_1","name":"read","arguments":"{}"}`)},
				{Kind: coreconversation.ItemOutput, CallID: "call_2", Name: "diff", Native: []byte(`{"type":"function_call","call_id":"call_2","name":"diff","arguments":"{}"}`)},
				{Kind: coreconversation.ItemToolResult, CallID: "call_1", Name: "read", Content: "content"},
				{Kind: coreconversation.ItemToolResult, CallID: "call_2", Name: "diff", Content: "diff"},
			},
			NewItems: []coreconversation.Item{
				{Kind: coreconversation.ItemToolResult, CallID: "call_1", Name: "read", Content: "content"},
				{Kind: coreconversation.ItemToolResult, CallID: "call_2", Name: "diff", Content: "diff"},
			},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	json := string(raw)
	if strings.Count(json, `"type":"function_call"`) != 2 || strings.Count(json, `"type":"function_call_output"`) != 2 {
		t.Fatalf("params json = %s, want two tool calls and two outputs", raw)
	}
	if len(sent) != 2 || sent[0].CallID != "call_1" || sent[1].CallID != "call_2" {
		t.Fatalf("sent = %#v, want both tool results in order", sent)
	}
}

func TestResponseParamsLeavesLargeReplayToolResultUncompacted(t *testing.T) {
	model, err := New(Config{
		Model:        "gpt-test",
		ProviderName: "codex",
		Runtime:      ResponsesRuntimeConfig{Continuation: ResponsesContinuationReplay},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	large := strings.Repeat("large diff line ", 2000)
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder"},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Provider: "codex", API: "codex.responses", Family: "responses", Model: "gpt-test"},
			Mode:     coreconversation.ProjectionFullReplay,
			Items: []coreconversation.Item{
				{Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "file_edit", Native: []byte(`{"type":"function_call","call_id":"call_1","name":"file_edit","arguments":"{}"}`)},
				{
					Kind:    coreconversation.ItemToolResult,
					CallID:  "call_1",
					Name:    "file_edit",
					Content: large,
					Native:  []byte(`{"type":"function_call_output","call_id":"call_1","output":"` + large + `"}`),
				},
			},
			NewItems: []coreconversation.Item{{
				Kind:    coreconversation.ItemToolResult,
				CallID:  "call_1",
				Name:    "file_edit",
				Content: large,
				Native:  []byte(`{"type":"function_call_output","call_id":"call_1","output":"` + large + `"}`),
			}},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(raw), "large diff line") {
		t.Fatalf("params json = %s, want original large tool output", raw)
	}
	if strings.Contains(string(raw), "file_edit result omitted") {
		t.Fatalf("params json = %s, want no provider-local compaction", raw)
	}
	if len(sent) != 1 || len(sent[0].Native) == 0 || !strings.Contains(string(sent[0].Native), "large diff line") {
		t.Fatalf("sent = %#v, want original native tool result", sent)
	}
}

func TestResponseParamsReplaysPlainAssistantOutput(t *testing.T) {
	model, err := New(Config{Runtime: ResponsesRuntimeConfig{Continuation: ResponsesContinuationReplay}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"},
			Mode:     coreconversation.ProjectionFullReplay,
			Items: []coreconversation.Item{
				{Kind: coreconversation.ItemInput, Role: "user", Content: "hello"},
				{Kind: coreconversation.ItemOutput, Role: "assistant", Content: "hi"},
				{Kind: coreconversation.ItemInput, Role: "user", Content: "again"},
			},
			NewItems: []coreconversation.Item{{Kind: coreconversation.ItemInput, Role: "user", Content: "again"}},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(raw), `"role":"assistant"`) || !strings.Contains(string(raw), `"hi"`) {
		t.Fatalf("params json = %s, want assistant output replay", raw)
	}
	if len(sent) != 1 || sent[0].Content != "again" {
		t.Fatalf("sent = %#v, want only pending item recorded", sent)
	}
}

func TestResponseParamsRecordsOnlyTranscriptNewItems(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Model: "gpt-test"},
			Mode:     coreconversation.ProjectionFullReplay,
			Items: []coreconversation.Item{
				{Kind: coreconversation.ItemInput, Role: "user", Content: "old"},
				{Kind: coreconversation.ItemInput, Role: "user", Content: "current"},
			},
			NewItems: []coreconversation.Item{
				{Kind: coreconversation.ItemInput, Role: "user", Content: "current"},
			},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(raw), `"text":"old"`) || !strings.Contains(string(raw), `"text":"current"`) {
		t.Fatalf("params json = %s, want full replay input", raw)
	}
	if len(sent) != 1 || sent[0].Content != "current" {
		t.Fatalf("sent = %#v, want only current new item", sent)
	}
	if sent[0].Provider.Provider != "openai" || sent[0].Provider.API != "openai.responses" {
		t.Fatalf("sent provider = %#v, want openai responses", sent[0].Provider)
	}
}

func TestStreamReturnsIdleTimeoutForSilentStream(t *testing.T) {
	model, err := New(Config{
		Model:  "gpt-test",
		APIKey: "test",
		Runtime: ResponsesRuntimeConfig{
			Cache:             ResponsesCacheOff,
			Continuation:      ResponsesContinuationReplay,
			StreamIdleTimeout: 10 * time.Millisecond,
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			_, _ = io.Copy(io.Discard, req.Body)
			return &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:          contextBlockingBody{ctx: req.Context()},
				ContentLength: -1,
				Request:       req,
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = model.Stream(context.Background(), llmagent.Request{
		Agent: agent.Spec{Name: "coder"},
		Goal:  "hello",
	}, nil)
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Fatalf("Stream error = %v, want ErrStreamIdleTimeout", err)
	}
}

func TestStreamReturnsCompletedResponseWhenConnectionStaysOpen(t *testing.T) {
	completed := `data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}` + "\n\n"
	model, err := New(Config{
		Model:  "gpt-test",
		APIKey: "test",
		Runtime: ResponsesRuntimeConfig{
			Cache:             ResponsesCacheOff,
			Continuation:      ResponsesContinuationReplay,
			StreamIdleTimeout: 10 * time.Millisecond,
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			_, _ = io.Copy(io.Discard, req.Body)
			return &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:          io.NopCloser(io.MultiReader(strings.NewReader(completed), contextBlockingBody{ctx: req.Context()})),
				ContentLength: -1,
				Request:       req,
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := model.Stream(context.Background(), llmagent.Request{
		Agent: agent.Spec{Name: "coder"},
		Goal:  "hello",
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got.Message == nil || got.Message.Content != "done" {
		t.Fatalf("message = %#v, want completed response text", got.Message)
	}
}

func TestStreamReturnsTerminalErrorWhenConnectionStaysOpen(t *testing.T) {
	failed := `data: {"type":"response.failed","response":{"id":"resp_1","object":"response","status":"failed","error":{"code":"server_error","message":"boom"},"output":[]}}` + "\n\n"
	model, err := New(Config{
		Model:  "gpt-test",
		APIKey: "test",
		Runtime: ResponsesRuntimeConfig{
			Cache:             ResponsesCacheOff,
			Continuation:      ResponsesContinuationReplay,
			StreamIdleTimeout: 10 * time.Millisecond,
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			_, _ = io.Copy(io.Discard, req.Body)
			return &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:          io.NopCloser(io.MultiReader(strings.NewReader(failed), contextBlockingBody{ctx: req.Context()})),
				ContentLength: -1,
				Request:       req,
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = model.Stream(context.Background(), llmagent.Request{
		Agent: agent.Spec{Name: "coder"},
		Goal:  "hello",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "server_error: boom") {
		t.Fatalf("Stream error = %v, want terminal response failure", err)
	}
}

func TestResponseFromOpenAIConvertsText(t *testing.T) {
	resp := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"output": [{
			"id": "msg_1",
			"type": "message",
			"status": "completed",
			"role": "assistant",
			"content": [{"type": "output_text", "text": "hello"}]
		}]
	}`)
	got, err := responseFromOpenAI(resp, nil, openAIProviderIdentity("gpt-test"), false, nil)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if got.Message == nil || got.Message.Content != "hello" {
		t.Fatalf("message = %#v, want hello", got.Message)
	}
}

func TestResponseForOutputModeUsesStreamItemsAsPrimary(t *testing.T) {
	final := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"output": []
	}`)
	done := mustStreamEvent(t, `{
		"type":"response.output_item.done",
		"output_index":0,
		"item":{
			"id":"msg_1",
			"type":"message",
			"status":"completed",
			"role":"assistant",
			"content":[{"type":"output_text","text":"from stream item"}]
		}
	}`)
	resp := responseForOutputMode(final, []responses.ResponseOutputItemUnion{done.AsResponseOutputItemDone().Item}, ResponsesOutputStreamItems)
	got, err := responseFromOpenAI(resp, nil, openAIProviderIdentity("gpt-test"), false, nil)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if got.Message == nil || got.Message.Content != "from stream item" {
		t.Fatalf("message = %#v, want stream item text", got.Message)
	}
	if len(got.Transcript.Items) != 1 || len(got.Transcript.Items[0].Native) == 0 {
		t.Fatalf("transcript items = %#v, want native stream item", got.Transcript.Items)
	}
}

func TestResponseForOutputModeDefaultsToFinalResponse(t *testing.T) {
	final := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"output": [{
			"id": "msg_1",
			"type": "message",
			"status": "completed",
			"role": "assistant",
			"content": [{"type": "output_text", "text": "from final"}]
		}]
	}`)
	done := mustStreamEvent(t, `{
		"type":"response.output_item.done",
		"output_index":0,
		"item":{
			"id":"msg_2",
			"type":"message",
			"status":"completed",
			"role":"assistant",
			"content":[{"type":"output_text","text":"from stream item"}]
		}
	}`)
	resp := responseForOutputMode(final, []responses.ResponseOutputItemUnion{done.AsResponseOutputItemDone().Item}, ResponsesOutputFinalResponse)
	got, err := responseFromOpenAI(resp, nil, openAIProviderIdentity("gpt-test"), false, nil)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if got.Message == nil || got.Message.Content != "from final" {
		t.Fatalf("message = %#v, want final response text", got.Message)
	}
}

func TestResponseFromOpenAIConvertsUsage(t *testing.T) {
	resp := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"model": "gpt-test",
		"status": "completed",
		"usage": {
			"input_tokens": 10,
			"input_tokens_details": {"cached_tokens": 3},
			"output_tokens": 5,
			"output_tokens_details": {"reasoning_tokens": 2},
			"total_tokens": 15
		},
		"output": [{
			"id": "msg_1",
			"type": "message",
			"status": "completed",
			"role": "assistant",
			"content": [{"type": "output_text", "text": "hello"}]
		}]
	}`)
	got, err := responseFromOpenAI(resp, nil, openAIProviderIdentity("gpt-test"), false, nil)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if len(got.Usage) != 1 {
		t.Fatalf("usage len = %d, want 1", len(got.Usage))
	}
	if got.Usage[0].Subject.Provider != "openai" || got.Usage[0].Subject.Name != "gpt-test" {
		t.Fatalf("usage subject = %#v, want openai/gpt-test", got.Usage[0].Subject)
	}
	if len(got.Usage[0].Measurements) != 5 {
		t.Fatalf("usage measurements = %#v, want 5", got.Usage[0].Measurements)
	}
}

func TestResponseFromOpenAIEnrichesUsageCost(t *testing.T) {
	resp := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"model": "gpt-test",
		"status": "completed",
		"usage": {"input_tokens": 1000},
		"output": [{
			"id": "msg_1",
			"type": "message",
			"status": "completed",
			"role": "assistant",
			"content": [{"type": "output_text", "text": "hello"}]
		}]
	}`)
	got, err := responseFromOpenAI(resp, nil, openAIProviderIdentity("gpt-test"), false, []corellm.PricingSpec{{
		Metric:    usage.MetricLLMInputTokens,
		Unit:      usage.UnitToken,
		Direction: usage.DirectionInput,
		Currency:  "USD",
		Price:     2,
		Per:       1000000,
	}})
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	measurements := got.Usage[0].Measurements
	cost := measurements[len(measurements)-1]
	if cost.Metric != usage.MetricCost || cost.Quantity != 0.002 || cost.Dimensions["estimated"] != "true" {
		t.Fatalf("cost measurement = %#v, want estimated cost", cost)
	}
}

func TestResponseFromOpenAIConvertsMultipleFunctionCalls(t *testing.T) {
	resp := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"output": [
			{"type": "function_call", "call_id": "call_1", "name": "inspect", "arguments": "{\"path\":\"README.md\"}"},
			{"type": "function_call", "call_id": "call_2", "name": "test", "arguments": "{\"package\":\"./...\"}"}
		]
	}`)
	got, err := responseFromOpenAI(resp, []adapterllm.ToolSpec{
		{
			Name: "inspect",
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "inspect"},
			},
		},
		{
			Name: "test",
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "test"},
			},
		},
	}, openAIProviderIdentity("gpt-test"), true, nil)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if len(got.Operations) != 2 {
		t.Fatalf("operations = %#v, want two", got.Operations)
	}
	if got.Operations[0].Operation.Name != "inspect" || got.Operations[1].Operation.Name != "test" {
		t.Fatalf("operations = %#v, want inspect then test", got.Operations)
	}
	if got.Operations[0].ProviderCallID != "call_1" || got.Operations[1].ProviderCallID != "call_2" {
		t.Fatalf("provider call IDs = %#v, want OpenAI call IDs", got.Operations)
	}
	if got.Transcript.Continuation == nil || got.Transcript.Continuation.ResponseID != "resp_1" {
		t.Fatalf("continuation = %#v, want resp_1", got.Transcript.Continuation)
	}
}

func TestResponseFromOpenAIConvertsCustomToolCall(t *testing.T) {
	resp := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"output": [
			{"type": "custom_tool_call", "call_id": "call_1", "name": "inspect", "input": "{\"path\":\"README.md\"}"}
		]
	}`)
	got, err := responseFromOpenAI(resp, []adapterllm.ToolSpec{{
		Name: "inspect",
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "inspect"},
		},
	}}, openAIProviderIdentity("gpt-test"), true, nil)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if len(got.Operations) != 1 {
		t.Fatalf("operations = %#v, want one", got.Operations)
	}
	if got.Operations[0].Operation.Name != "inspect" || got.Operations[0].ProviderCallID != "call_1" {
		t.Fatalf("operation = %#v, want inspect call_1", got.Operations[0])
	}
	if got.Operations[0].ProviderCallType != "custom_tool_call" {
		t.Fatalf("provider call type = %q, want custom_tool_call", got.Operations[0].ProviderCallType)
	}
	if len(got.Transcript.Items) != 1 || got.Transcript.Items[0].Metadata["provider_call_type"] != "custom_tool_call" {
		t.Fatalf("transcript items = %#v, want custom tool call metadata", got.Transcript.Items)
	}
}

func TestStreamEventsNormalizeThinkingAndToolCalls(t *testing.T) {
	model := &Model{}
	state := &openAIStreamState{toolNames: map[int]tool.Name{}, outputPhases: map[int]string{}}

	added := mustStreamEvent(t, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"inspect","arguments":""}}`)
	events := model.streamEvents(added, state)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamToolCallStart || events[0].Tool != "inspect" {
		t.Fatalf("added events = %#v, want inspect start", events)
	}

	delta := mustStreamEvent(t, `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"call_1","delta":"{\"path\""}`)
	events = model.streamEvents(delta, state)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamToolCallDelta || events[0].Tool != "inspect" {
		t.Fatalf("delta events = %#v, want inspect delta", events)
	}

	thinking := mustStreamEvent(t, `{"type":"response.reasoning_summary_text.delta","output_index":1,"item_id":"rs_1","delta":"checking"}`)
	events = model.streamEvents(thinking, state)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamThinkingDelta || events[0].Text != "checking" {
		t.Fatalf("thinking events = %#v, want thinking delta", events)
	}
}

func TestStreamEventsNormalizeCustomToolCalls(t *testing.T) {
	model := &Model{}
	state := &openAIStreamState{toolNames: map[int]tool.Name{}, outputPhases: map[int]string{}}

	added := mustStreamEvent(t, `{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","call_id":"call_1","name":"inspect","input":""}}`)
	events := model.streamEvents(added, state)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamToolCallStart || events[0].Tool != "inspect" || events[0].CallType != "custom_tool_call" {
		t.Fatalf("added events = %#v, want custom inspect start", events)
	}

	delta := mustStreamEvent(t, `{"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"call_1","delta":"{\"path\""}`)
	events = model.streamEvents(delta, state)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamToolCallDelta || events[0].Tool != "inspect" || events[0].CallType != "custom_tool_call" {
		t.Fatalf("delta events = %#v, want custom inspect delta", events)
	}

	done := mustStreamEvent(t, `{"type":"response.custom_tool_call_input.done","output_index":0,"item_id":"call_1","input":"{\"path\":\"README.md\"}"}`)
	events = model.streamEvents(done, state)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamToolCallDone || !events[0].Final || events[0].Arguments != `{"path":"README.md"}` {
		t.Fatalf("done events = %#v, want custom inspect done", events)
	}
}

func TestStreamEventsTreatCommentaryPhaseTextAsThinking(t *testing.T) {
	model := &Model{}
	state := &openAIStreamState{toolNames: map[int]tool.Name{}, outputPhases: map[int]string{}}

	added := mustStreamEvent(t, `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","phase":"commentary"}}`)
	if events := model.streamEvents(added, state); len(events) != 0 {
		t.Fatalf("added events = %#v, want none", events)
	}
	delta := mustStreamEvent(t, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"**checking**"}`)
	events := model.streamEvents(delta, state)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamThinkingDelta || events[0].Text != "**checking**" || events[0].Sensitivity != "internal" {
		t.Fatalf("delta events = %#v, want internal thinking", events)
	}
	if got := state.finalContent(); got != "" {
		t.Fatalf("finalContent = %q, want commentary excluded", got)
	}
}

func TestStreamEventsBufferUnphasedTextUntilOutcome(t *testing.T) {
	model := &Model{}
	state := &openAIStreamState{}
	delta := mustStreamEvent(t, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Inspecting repo"}`)
	if events := model.streamEvents(delta, state); len(events) != 0 {
		t.Fatalf("delta events = %#v, want buffered", events)
	}
	flushed := state.flushAllUnphased(adapterllm.StreamContentDelta)
	if len(flushed) != 1 || flushed[0].Kind != adapterllm.StreamContentDelta || flushed[0].Text != "Inspecting repo" {
		t.Fatalf("flushed = %#v, want content", flushed)
	}
}

func TestStreamEventsFlushUnphasedTextAsThinkingBeforeTool(t *testing.T) {
	model := &Model{}
	state := &openAIStreamState{}
	delta := mustStreamEvent(t, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Inspecting repo"}`)
	if events := model.streamEvents(delta, state); len(events) != 0 {
		t.Fatalf("delta events = %#v, want buffered", events)
	}
	done := mustStreamEvent(t, `{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_1","name":"inspect","arguments":"{\"path\":\"README.md\"}"}}`)
	events := model.streamEvents(done, state)
	if len(events) != 2 {
		t.Fatalf("events = %#v, want thinking plus tool", events)
	}
	if events[0].Kind != adapterllm.StreamThinkingDelta || events[0].Text != "Inspecting repo" {
		t.Fatalf("events[0] = %#v, want thinking", events[0])
	}
	if events[1].Kind != adapterllm.StreamToolCallDone || events[1].Tool != "inspect" {
		t.Fatalf("events[1] = %#v, want tool done", events[1])
	}
}

func TestStreamEventsFlushUnphasedTextWhenMessagePhaseArrivesLate(t *testing.T) {
	model := &Model{}
	state := &openAIStreamState{}
	delta := mustStreamEvent(t, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"done"}`)
	if events := model.streamEvents(delta, state); len(events) != 0 {
		t.Fatalf("delta events = %#v, want buffered", events)
	}
	done := mustStreamEvent(t, `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}}`)
	events := model.streamEvents(done, state)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamContentDelta || events[0].Text != "done" {
		t.Fatalf("events = %#v, want content", events)
	}
}

func TestStreamedContentFallbackCreatesMessage(t *testing.T) {
	provider := openAIProviderIdentity("gpt-test")
	state := &openAIStreamState{}
	state.appendContent(0, "The answer is ")
	state.appendContent(0, "2.")
	out := applyStreamedContentFallback(llmagent.Response{
		Transcript: coreconversation.Transcript{Provider: provider},
	}, provider, state)
	if out.Message == nil || out.Message.Content != "The answer is 2." {
		t.Fatalf("message = %#v, want streamed fallback", out.Message)
	}
	if len(out.Transcript.Items) != 1 || out.Transcript.Items[0].Content != "The answer is 2." {
		t.Fatalf("transcript items = %#v, want assistant fallback item", out.Transcript.Items)
	}
	if len(out.Transcript.Items[0].Native) == 0 || !strings.Contains(string(out.Transcript.Items[0].Native), `"role":"assistant"`) {
		t.Fatalf("native = %s, want assistant message", out.Transcript.Items[0].Native)
	}
}

func TestStreamedOperationsFallbackUsesStreamedToolCalls(t *testing.T) {
	streamed := []agent.OperationRequest{{
		Operation:      operation.Ref{Name: "datasource_search"},
		Input:          map[string]any{"query": "go.mod module path"},
		ProviderCallID: "call_1",
	}}
	out := applyStreamedOperationsFallback(llmagent.Response{}, streamed)
	if len(out.Operations) != 1 {
		t.Fatalf("operations = %#v, want streamed operation", out.Operations)
	}
	if out.Operations[0].Operation.Name != "datasource_search" || out.Operations[0].ProviderCallID != "call_1" {
		t.Fatalf("operation = %#v, want datasource_search call_1", out.Operations[0])
	}
}

func TestStreamedOperationsFallbackKeepsFinalResponseOperations(t *testing.T) {
	final := llmagent.OperationResponse(agent.OperationRequest{
		Operation:      operation.Ref{Name: "final_tool"},
		ProviderCallID: "final_call",
	})
	out := applyStreamedOperationsFallback(final, []agent.OperationRequest{{
		Operation:      operation.Ref{Name: "streamed_tool"},
		ProviderCallID: "streamed_call",
	}})
	if len(out.Operations) != 1 || out.Operations[0].Operation.Name != "final_tool" {
		t.Fatalf("operations = %#v, want final response operation preserved", out.Operations)
	}
}

func TestStreamedOperationTranscriptItemsPreserveProviderCall(t *testing.T) {
	items := streamedOperationTranscriptItems(openAIProviderIdentity("gpt-test"), []agent.OperationRequest{{
		Operation:      operation.Ref{Name: "datasource_search"},
		Input:          map[string]any{"query": "go.mod module path"},
		ProviderCallID: "call_1",
	}})
	if len(items) != 1 {
		t.Fatalf("items = %#v, want one", items)
	}
	item := items[0]
	if item.Kind != coreconversation.ItemOutput || item.CallID != "call_1" || item.Name != "datasource_search" {
		t.Fatalf("item = %#v, want output datasource_search call_1", item)
	}
	if item.Metadata["provider_call_type"] != "function_call" {
		t.Fatalf("metadata = %#v, want function_call", item.Metadata)
	}
	if !strings.Contains(string(item.Native), `"type":"function_call"`) || !strings.Contains(string(item.Native), `"arguments":"{\"query\":\"go.mod module path\"}"`) {
		t.Fatalf("native = %s, want function_call with arguments", item.Native)
	}
}

func TestResponseFinalTextExcludesCommentaryPhase(t *testing.T) {
	resp := mustResponse(t, `{
		"id": "resp_1",
		"model": "gpt-test",
		"status": "completed",
		"output": [
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"working"}]},
			{"id":"msg_2","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}
		]
	}`)
	if got := responseFinalText(resp); got != "done" {
		t.Fatalf("responseFinalText = %q, want done", got)
	}
	out, err := responseFromOpenAI(resp, nil, openAIProviderIdentity("gpt-test"), false, nil)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if out.Message == nil || out.Message.Content != "done" {
		t.Fatalf("message = %#v, want final answer", out.Message)
	}
	if len(out.Transcript.Items) != 2 || out.Transcript.Items[0].Phase != "commentary" || out.Transcript.Items[1].Phase != "final_answer" {
		t.Fatalf("transcript items = %#v, want preserved phases", out.Transcript.Items)
	}
}

func TestProviderSpecValidates(t *testing.T) {
	if err := ProviderSpec().Validate(); err != nil {
		t.Fatalf("ProviderSpec Validate: %v", err)
	}
}

func mustResponse(t *testing.T, raw string) responses.Response {
	t.Helper()
	var resp responses.Response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func mustStreamEvent(t *testing.T, raw string) responses.ResponseStreamEventUnion {
	t.Helper()
	var event responses.ResponseStreamEventUnion
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal stream event: %v", err)
	}
	return event
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type contextBlockingBody struct {
	ctx context.Context
}

func (b contextBlockingBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b contextBlockingBody) Close() error {
	return nil
}
