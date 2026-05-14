package shellplugin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestShellExecStreamsProcessEvents(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	op := findOp(t, ops, ExecOp)
	var events []event.Event
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		events = append(events, evt)
	}))
	result := op.Run(ctx, map[string]any{"command": "printf", "args": []any{"hello"}})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	var sawOutput bool
	for _, evt := range events {
		if proc, ok := evt.(system.ProcessEvent); ok && proc.Kind == "output" && strings.Contains(proc.Data, "hello") {
			sawOutput = true
		}
	}
	if !sawOutput {
		t.Fatalf("events = %#v, want process output", events)
	}
}

func TestShellExecIntentUsesNormalizedCommand(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	op := findOp(t, ops, ExecOp)
	provider, ok := op.(operation.IntentProvider)
	if !ok {
		t.Fatalf("%s does not implement IntentProvider", op.Spec().Ref.String())
	}

	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), execInput{Command: "printf hello", Workdir: "subdir"})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if len(intents.Operations) != 1 {
		t.Fatalf("intents = %#v, want one operation", intents)
	}
	target, ok := intents.Operations[0].Target.(operation.ProcessTarget)
	if !ok || target.Command != "printf" || len(target.Args) != 1 || target.Args[0] != "hello" || target.Workdir != "subdir" {
		t.Fatalf("target = %#v, want normalized printf command", intents.Operations[0].Target)
	}
}

func TestBackgroundProcessLifecycle(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	start := findOp(t, ops, ProcessStartOp)
	list := findOp(t, ops, ProcessListOp)
	status := findOp(t, ops, ProcessStatusOp)
	output := findOp(t, ops, ProcessOutputOp)
	result := start.Run(operation.NewContext(context.Background(), nil), map[string]any{
		"command": "printf",
		"args":    []any{"background"},
	})
	if result.IsError() {
		t.Fatalf("start error = %#v", result.Error)
	}
	rendered := result.Output.(operation.Rendered)
	data := rendered.Data.(map[string]any)
	processID, _ := data["id"].(string)
	if processID == "" {
		t.Fatalf("start data = %#v, want id", data)
	}
	if list.Run(operation.NewContext(context.Background(), nil), map[string]any{}).IsError() {
		t.Fatal("process_list failed")
	}
	if status.Run(operation.NewContext(context.Background(), nil), map[string]any{"process_id": processID}).IsError() {
		t.Fatal("process_status failed")
	}
	var out operation.Result
	for i := 0; i < 20; i++ {
		out = output.Run(operation.NewContext(context.Background(), nil), map[string]any{"process_id": processID})
		if out.IsError() {
			t.Fatalf("process_output error = %#v", out.Error)
		}
		if strings.Contains(out.Output.(operation.Rendered).Text, "background") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("output = %#v, want captured text", out.Output)
}

func findOp(t *testing.T, ops []operation.Operation, name string) operation.Operation {
	t.Helper()
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			return op
		}
	}
	t.Fatalf("operation %s not found", name)
	return nil
}
