package invocation

import "testing"

func TestTargetCanRepresentPromptInvocation(t *testing.T) {
	target := Target{
		Kind:   TargetPrompt,
		Prompt: "Review the following code changes: {{.Query}}",
		Input:  map[string]any{"Query": "diff"},
	}

	if target.Kind != TargetPrompt {
		t.Fatalf("kind = %q, want prompt", target.Kind)
	}
	if target.Prompt == "" {
		t.Fatal("prompt is empty")
	}
}
