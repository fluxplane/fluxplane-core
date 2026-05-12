package modelcatalog

import "testing"

func TestBuiltInCatalogContainsOpenAIAndCodexGPT55(t *testing.T) {
	if _, model, ok := Find("openai", "gpt-5.5"); !ok {
		t.Fatalf("openai/gpt-5.5 not found")
	} else if len(model.Pricing) == 0 {
		t.Fatalf("openai/gpt-5.5 pricing is empty")
	}
	if _, model, ok := Find("codex", "gpt-5.5"); !ok {
		t.Fatalf("codex/gpt-5.5 not found")
	} else if !model.Capabilities.Has("prompt_caching") {
		t.Fatalf("codex/gpt-5.5 capabilities = %#v, want prompt caching", model.Capabilities)
	}
}
