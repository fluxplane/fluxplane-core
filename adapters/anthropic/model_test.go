package anthropic

import "testing"

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := New(Config{Model: "claude-test"})
	if err == nil {
		t.Fatal("New succeeded without API key")
	}
}

func TestNewUsesEnvAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	model, err := New(Config{Model: "claude-test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if model == nil {
		t.Fatal("model is nil")
	}
}
