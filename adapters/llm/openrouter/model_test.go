package openrouter

import (
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/adapters/llm/openai"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	_, err := New(Config{Model: "anthropic/claude-sonnet-4.6"})
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("error = %v, want missing OPENROUTER_API_KEY", err)
	}
}

func TestNewUsesOpenRouterIdentityAndDefaults(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	model, err := New(Config{Model: "anthropic/claude-sonnet-4.6"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	identity := model.ProviderIdentity(llmagent.Request{})
	if identity.Provider != "openrouter" || identity.API != "openrouter.responses" || identity.Model != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("identity = %#v, want openrouter responses", identity)
	}
}

func TestNewAllowsRuntimeOverride(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	model, err := New(Config{
		Model: "anthropic/claude-sonnet-4.6",
		Runtime: openai.ResponsesRuntimeConfig{
			Cache:        openai.ResponsesCacheMax,
			Continuation: openai.ResponsesContinuationProvider,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	identity := model.ProviderIdentity(llmagent.Request{})
	if identity.Provider != "openrouter" {
		t.Fatalf("identity = %#v, want openrouter", identity)
	}
}
