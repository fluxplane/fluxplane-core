package operationruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
)

func TestReplaceLargeResultIncludesPreviewMetadata(t *testing.T) {
	payload := strings.Repeat("x", 12*1024)
	result := operation.OK(operation.Rendered{Text: payload, Model: payload, Data: map[string]string{"payload": payload}})

	replaced, replacement, err := ReplaceLargeResult(context.Background(), result, ReplacementOptions{
		ThresholdBytes: 1024,
		Operation:      operation.Ref{Name: "large_result"},
		CallID:         "call_1",
		TempDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ReplaceLargeResult: %v", err)
	}
	if replacement == nil || !replacement.Replaced {
		t.Fatalf("replacement = %#v, want replacement", replacement)
	}
	if replacement.Preview == "" || replacement.Tail == "" || replacement.OmittedBytes <= 0 {
		t.Fatalf("replacement preview metadata = %#v, want preview, tail, and omitted bytes", replacement)
	}
	rendered, ok := replaced.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want rendered replacement", replaced.Output)
	}
	if !strings.Contains(rendered.Text, "Preview:") || !strings.Contains(rendered.Text, "Full result:") {
		t.Fatalf("rendered text = %q, want preview and full result reference", rendered.Text)
	}
}

func TestReadReplacementFileReadsBoundedReplacementContent(t *testing.T) {
	payload := strings.Repeat("content ", 2048)
	result := operation.OK(operation.Rendered{Text: payload, Model: payload, Data: map[string]string{"payload": payload}})
	_, replacement, err := ReplaceLargeResult(context.Background(), result, ReplacementOptions{
		ThresholdBytes: 1024,
		TempDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ReplaceLargeResult: %v", err)
	}

	data, truncated, err := ReadReplacementFile(context.Background(), replacement.Path, 2048)
	if err != nil {
		t.Fatalf("ReadReplacementFile: %v", err)
	}
	if !truncated {
		t.Fatalf("truncated = false, want true")
	}
	if !strings.Contains(string(data), "content") {
		t.Fatalf("data = %q, want replacement content", string(data))
	}
}
