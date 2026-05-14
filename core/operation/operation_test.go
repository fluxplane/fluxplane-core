package operation

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
)

func TestNewContextCreatesWithDefaults(t *testing.T) {
	ctx := NewContext(nil, nil)
	if ctx == nil {
		t.Fatal("NewContext returned nil")
	}
	if ctx.Events() == nil {
		t.Fatal("Events sink is nil")
	}
}

func TestNewContextPreservesGivenContext(t *testing.T) {
	background := context.Background()
	sink := event.SinkFunc(func(event.Event) {})
	ctx := NewContext(background, sink)

	if ctx == nil {
		t.Fatal("NewContext returned nil")
	}
	// Events sink is preserved
	if ctx.Events() == nil {
		t.Fatal("Events sink is nil")
	}
}

func TestHandlerOperationSpec(t *testing.T) {
	spec := Spec{Ref: Ref{Name: "test-op"}}
	handler := func(Context, Value) Result { return OK(nil) }
	op := New(spec, handler)

	gotSpec := op.Spec()
	if gotSpec.Ref.Name != "test-op" {
		t.Fatalf("Spec.Ref.Name = %q, want test-op", gotSpec.Ref.Name)
	}
}

func TestHandlerOperationRunWithNilHandler(t *testing.T) {
	spec := Spec{Ref: Ref{Name: "test-op"}}
	op := New(spec, nil)
	ctx := NewContext(context.Background(), event.Discard())

	result := op.Run(ctx, nil)
	if result.Status != StatusOK {
		t.Fatalf("result.Status = %q, want ok", result.Status)
	}
}

func TestHandlerOperationRunCallsHandler(t *testing.T) {
	spec := Spec{Ref: Ref{Name: "test-op"}}
	called := false
	handler := func(Context, Value) Result {
		called = true
		return OK("handler-result")
	}
	op := New(spec, handler)
	ctx := NewContext(context.Background(), event.Discard())

	result := op.Run(ctx, nil)
	if !called {
		t.Fatal("handler was not called")
	}
	if result.IsError() {
		t.Fatalf("result.IsError = true, want false")
	}
}

func TestHandlerOperationRunPropagatesResult(t *testing.T) {
	spec := Spec{Ref: Ref{Name: "test-op"}}
	handler := func(Context, Value) Result {
		return Failed("test_code", "test error", nil)
	}
	op := New(spec, handler)
	ctx := NewContext(context.Background(), event.Discard())

	result := op.Run(ctx, nil)
	if !result.IsError() {
		t.Fatalf("result.IsError = false, want true")
	}
	if result.Error == nil || result.Error.Message != "test error" {
		t.Fatalf("result.Error = %v, want test error", result.Error)
	}
}

func TestWithCallIDNilContext(t *testing.T) {
	result := WithCallID(nil, "call-1")
	if result != nil {
		t.Fatal("WithCallID(nil, _) returned non-nil")
	}
}

func TestWithCallIDEmptyCallID(t *testing.T) {
	ctx := NewContext(context.Background(), event.Discard())
	result := WithCallID(ctx, "")
	if result != ctx {
		t.Fatal("WithCallID(_, \"\") should return same context")
	}
}

func TestCallIDFromContextEmpty(t *testing.T) {
	ctx := context.Background()
	callID := CallIDFromContext(ctx)
	if callID != "" {
		t.Fatalf("CallIDFromContext = %q, want empty", callID)
	}
}

func TestCallIDFromContextNil(t *testing.T) {
	callID := CallIDFromContext(nil)
	if callID != "" {
		t.Fatalf("CallIDFromContext(nil) = %q, want empty", callID)
	}
}

func TestResultOK(t *testing.T) {
	output := map[string]any{"key": "value"}
	result := OK(output)
	if result.Status != StatusOK {
		t.Fatalf("Status = %q, want %q", result.Status, StatusOK)
	}
	if result.IsError() {
		t.Fatalf("IsError = true, want false")
	}
	if result.Output != output {
		t.Fatalf("Output = %v, want %v", result.Output, output)
	}
	if result.Error != nil {
		t.Fatalf("Error = %v, want nil", result.Error)
	}
}

func TestResultFailed(t *testing.T) {
	details := map[string]any{"reason": "timeout"}
	result := Failed("timeout_error", "operation timed out", details)
	if result.Status != StatusFailed {
		t.Fatalf("Status = %q, want %q", result.Status, StatusFailed)
	}
	if !result.IsError() {
		t.Fatalf("IsError = false, want true")
	}
	if result.Error == nil {
		t.Fatalf("Error = nil, want error")
	}
	if result.Error.Code != "timeout_error" {
		t.Fatalf("Error.Code = %q, want timeout_error", result.Error.Code)
	}
	if result.Error.Message != "operation timed out" {
		t.Fatalf("Error.Message = %q, want 'operation timed out'", result.Error.Message)
	}
	if result.Error.Details != details {
		t.Fatalf("Error.Details = %v, want %v", result.Error.Details, details)
	}
}

func TestResultRejected(t *testing.T) {
	details := map[string]any{"reason": "insufficient permissions"}
	result := Rejected("permission_denied", "insufficient permissions", details)
	if result.Status != StatusRejected {
		t.Fatalf("Status = %q, want %q", result.Status, StatusRejected)
	}
	if !result.IsError() {
		t.Fatalf("IsError = false, want true")
	}
	if result.Error.Code != "permission_denied" {
		t.Fatalf("Error.Code = %q, want permission_denied", result.Error.Code)
	}
}

func TestResultCanceled(t *testing.T) {
	result := Canceled("user canceled the operation")
	if result.Status != StatusCanceled {
		t.Fatalf("Status = %q, want %q", result.Status, StatusCanceled)
	}
	if !result.IsError() {
		t.Fatalf("IsError = false, want true")
	}
	if result.Error.Code != "canceled" {
		t.Fatalf("Error.Code = %q, want canceled", result.Error.Code)
	}
	if result.Error.Message != "user canceled the operation" {
		t.Fatalf("Error.Message = %q, want 'user canceled the operation'", result.Error.Message)
	}
}

func TestResultIsErrorWithEmptyStatus(t *testing.T) {
	result := Result{Status: ""}
	if result.IsError() {
		t.Fatalf("IsError = true, want false for empty status")
	}
}

func TestRenderedModelText(t *testing.T) {
	tests := []struct {
		name     string
		rendered Rendered
		want     string
	}{
		{
			name: "returns model text when set",
			rendered: Rendered{
				Text:  "plain text",
				Model: "model text",
			},
			want: "model text",
		},
		{
			name: "returns text when model is empty",
			rendered: Rendered{
				Text: "plain text",
			},
			want: "plain text",
		},
		{
			name: "returns empty string when both are empty",
			rendered: Rendered{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rendered.ModelText()
			if got != tt.want {
				t.Fatalf("ModelText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRefString(t *testing.T) {
	tests := []struct {
		name string
		ref  Ref
		want string
	}{
		{
			name: "simple name",
			ref:  Ref{Name: "test-op"},
			want: "test-op",
		},
		{
			name: "name with version",
			ref:  Ref{Name: "test-op", Version: "v1"},
			want: "test-op@v1",
		},
		{
			name: "empty ref",
			ref:  Ref{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ref.String()
			if got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRefIsZero(t *testing.T) {
	tests := []struct {
		name string
		ref  Ref
		want bool
	}{
		{
			name: "zero ref",
			ref:  Ref{},
			want: true,
		},
		{
			name: "ref with name",
			ref:  Ref{Name: "test-op"},
			want: false,
		},
		{
			name: "ref with version",
			ref:  Ref{Version: "v1"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ref.IsZero()
			if got != tt.want {
				t.Fatalf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}
