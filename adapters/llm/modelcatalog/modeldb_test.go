package modelcatalog

import (
	"testing"

	"github.com/codewandler/modeldb"
	corellm "github.com/fluxplane/agentruntime/core/llm"
)

func TestSupportsAPI(t *testing.T) {
	model := corellm.ModelSpec{Annotations: map[string]string{"modeldb.api_types": "openai-chat, openai-responses"}}
	if !SupportsAPI(model, "openai-responses") {
		t.Fatalf("SupportsAPI returned false, want true")
	}
	if SupportsAPI(model, "anthropic-messages") {
		t.Fatalf("SupportsAPI returned true, want false")
	}
}

func TestProjectProviderFiltersByAPIAndAllowedModelIDs(t *testing.T) {
	responsesKey := modeldb.NormalizeKey(modeldb.ModelKey{Creator: "openai", Family: "gpt", Version: "responses"})
	chatKey := modeldb.NormalizeKey(modeldb.ModelKey{Creator: "openai", Family: "gpt", Version: "chat"})
	catalog := modeldb.Catalog{
		Models: map[modeldb.ModelKey]modeldb.ModelRecord{
			responsesKey: {Key: responsesKey, Name: "Responses"},
			chatKey:      {Key: chatKey, Name: "Chat"},
		},
		Services: map[string]modeldb.Service{"openai": {ID: "openai", Name: "OpenAI"}},
		Offerings: map[modeldb.OfferingRef]modeldb.Offering{
			{ServiceID: "openai", WireModelID: "gpt-responses"}: {
				ServiceID:   "openai",
				WireModelID: "gpt-responses",
				ModelKey:    responsesKey,
				Exposures:   []modeldb.OfferingExposure{{APIType: modeldb.APITypeOpenAIResponses}},
			},
			{ServiceID: "openai", WireModelID: "gpt-chat"}: {
				ServiceID:   "openai",
				WireModelID: "gpt-chat",
				ModelKey:    chatKey,
				Exposures:   []modeldb.OfferingExposure{{APIType: modeldb.APITypeOpenAIChat}},
			},
		},
	}
	spec, ok := ProjectProvider(catalog, ProviderProjection{
		ServiceID: "openai",
		APIType:   modeldb.APITypeOpenAIResponses,
		ModelIDs:  []string{"gpt-responses", "missing"},
	})
	if !ok {
		t.Fatalf("ProjectProvider returned false")
	}
	if spec.Name != "openai" || spec.DisplayName != "OpenAI" {
		t.Fatalf("provider = %#v", spec)
	}
	if len(spec.Models) != 1 || spec.Models[0].Ref.Name != "gpt-responses" {
		t.Fatalf("models = %#v, want only gpt-responses", spec.Models)
	}
}

func TestProjectProviderPreservesModelAliases(t *testing.T) {
	key := modeldb.NormalizeKey(modeldb.ModelKey{Creator: "anthropic", Family: "claude", Version: "sonnet"})
	catalog := modeldb.Catalog{
		Models: map[modeldb.ModelKey]modeldb.ModelRecord{
			key: {Key: key, Name: "Claude Sonnet", Aliases: []string{"claude-sonnet"}},
		},
		Services: map[string]modeldb.Service{"anthropic": {ID: "anthropic", Name: "Anthropic"}},
		Offerings: map[modeldb.OfferingRef]modeldb.Offering{
			{ServiceID: "anthropic", WireModelID: "claude-sonnet-4-6"}: {
				ServiceID:   "anthropic",
				WireModelID: "claude-sonnet-4-6",
				ModelKey:    key,
				Aliases:     []string{"sonnet"},
				Exposures:   []modeldb.OfferingExposure{{APIType: modeldb.APITypeAnthropicMessages}},
			},
		},
	}
	spec, ok := ProjectProvider(catalog, ProviderProjection{
		ServiceID: "anthropic",
		APIType:   modeldb.APITypeAnthropicMessages,
	})
	if !ok {
		t.Fatalf("ProjectProvider returned false")
	}
	if got := spec.Models[0].Aliases; len(got) != 2 || got[0] != "claude-sonnet" || got[1] != "sonnet" {
		t.Fatalf("aliases = %#v, want model and offering aliases", got)
	}
}

func TestFromModelDBAnnotatesOpenAIResponsesReasoningValues(t *testing.T) {
	key := modeldb.NormalizeKey(modeldb.ModelKey{Creator: "moonshot", Family: "kimi", Version: "2"})
	catalog := modeldb.Catalog{
		Models: map[modeldb.ModelKey]modeldb.ModelRecord{
			key: {Key: key, Name: "Kimi"},
		},
		Services: map[string]modeldb.Service{"openrouter": {ID: "openrouter"}},
		Offerings: map[modeldb.OfferingRef]modeldb.Offering{
			{ServiceID: "openrouter", WireModelID: "moonshotai/kimi-k2"}: {
				ServiceID:   "openrouter",
				WireModelID: "moonshotai/kimi-k2",
				ModelKey:    key,
				Exposures: []modeldb.OfferingExposure{{
					APIType: modeldb.APITypeOpenAIResponses,
					ParameterValues: map[string][]string{
						string(modeldb.ParamReasoningEffort):  {"medium", "minimal"},
						string(modeldb.ParamReasoningSummary): {"concise", "auto"},
					},
				}},
			},
		},
	}
	specs := FromModelDB(catalog)
	if len(specs) != 1 || len(specs[0].Models) != 1 {
		t.Fatalf("specs = %#v, want one model", specs)
	}
	annotations := specs[0].Models[0].Annotations
	if annotations["modeldb.openai_responses.reasoning_efforts"] != "medium,minimal" {
		t.Fatalf("reasoning efforts = %q", annotations["modeldb.openai_responses.reasoning_efforts"])
	}
	if annotations["modeldb.openai_responses.reasoning_summaries"] != "auto,concise" {
		t.Fatalf("reasoning summaries = %q", annotations["modeldb.openai_responses.reasoning_summaries"])
	}
}

func TestFromModelDBAnnotatesAnthropicMessagesValues(t *testing.T) {
	key := modeldb.NormalizeKey(modeldb.ModelKey{Creator: "anthropic", Family: "claude", Version: "test"})
	catalog := modeldb.Catalog{
		Models: map[modeldb.ModelKey]modeldb.ModelRecord{
			key: {Key: key, Name: "Claude"},
		},
		Services: map[string]modeldb.Service{"anthropic": {ID: "anthropic"}},
		Offerings: map[modeldb.OfferingRef]modeldb.Offering{
			{ServiceID: "anthropic", WireModelID: "claude-test"}: {
				ServiceID:   "anthropic",
				WireModelID: "claude-test",
				ModelKey:    key,
				Exposures: []modeldb.OfferingExposure{{
					APIType: modeldb.APITypeAnthropicMessages,
					SupportedParameters: []modeldb.NormalizedParameter{
						modeldb.ParamBlockCacheControl,
					},
					ParameterValues: map[string][]string{
						string(modeldb.ParamReasoningEffort): {"high", "low"},
						string(modeldb.ParamThinkingMode):    {"adaptive", "enabled"},
					},
				}},
			},
		},
	}
	specs := FromModelDB(catalog)
	annotations := specs[0].Models[0].Annotations
	if annotations["modeldb.anthropic_messages.reasoning_efforts"] != "high,low" {
		t.Fatalf("reasoning efforts = %q", annotations["modeldb.anthropic_messages.reasoning_efforts"])
	}
	if annotations["modeldb.anthropic_messages.thinking_modes"] != "adaptive,enabled" {
		t.Fatalf("thinking modes = %q", annotations["modeldb.anthropic_messages.thinking_modes"])
	}
	if annotations["modeldb.anthropic_messages.cache_control"] != "true" {
		t.Fatalf("cache control = %q", annotations["modeldb.anthropic_messages.cache_control"])
	}
}
