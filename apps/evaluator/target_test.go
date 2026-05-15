package evaluator

import (
	"strings"
	"testing"
)

func TestTargetInputBuildsTargetDescription(t *testing.T) {
	input, err := targetInput(targetOptions{
		baseURL:      "http://unix",
		socket:       "/tmp/coder.sock",
		session:      "coder",
		conversation: "eval-thread",
		targetKind:   "coder",
		timeout:      "45s",
		probe:        "Reply with ok",
	})
	if err != nil {
		t.Fatalf("targetInput: %v", err)
	}
	for _, want := range []string{
		"- base_url: http://unix",
		"- unix_socket: /tmp/coder.sock",
		"- session: coder",
		"- conversation: eval-thread",
		"- target_kind: coder",
		"- timeout: 45s",
		"Reply with ok",
	} {
		if !strings.Contains(input, want) {
			t.Fatalf("target input = %q, want %q", input, want)
		}
	}
}

func TestTargetInputRequiresBaseURLAndSession(t *testing.T) {
	if _, err := targetInput(targetOptions{session: "coder"}); err == nil {
		t.Fatalf("targetInput missing baseURL error = nil")
	}
	if _, err := targetInput(targetOptions{baseURL: "http://unix"}); err == nil {
		t.Fatalf("targetInput missing session error = nil")
	}
}

func TestTargetCommandDeclaresStructuredFlags(t *testing.T) {
	cmd := newTargetCommand(Distribution())
	for _, name := range []string{"base-url", "socket", "session", "conversation", "target-kind", "timeout", "probe", "model", "yolo"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("target command missing --%s", name)
		}
	}
}
