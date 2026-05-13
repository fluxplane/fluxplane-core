package minimax

import "testing"

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "")
	t.Setenv("MINIMAX_KEY", "")
	_, err := New(Config{Model: "MiniMax-M2.7"})
	if err == nil {
		t.Fatal("New succeeded without API key")
	}
}

func TestNewUsesEnvAPIKey(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "test-key")
	model, err := New(Config{Model: "MiniMax-M2.7"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if model == nil {
		t.Fatal("model is nil")
	}
}
