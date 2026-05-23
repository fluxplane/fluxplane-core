package human

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/runtime/system"
	"github.com/fluxplane/engine/runtime/systemtest"
)

func TestNotifySendRunsDesktopNotificationAndEmitsEvent(t *testing.T) {
	var emitted []event.Event
	process := &recordingProcess{}
	op := operationForTest(t, NotifySendOp, process)
	result := op.Run(operation.NewContext(context.Background(), event.SinkFunc(func(payload event.Event) {
		emitted = append(emitted, payload)
	})), map[string]any{
		"summary": "Health summary",
		"body":    "Load is normal.",
		"urgency": "normal",
	})

	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	if len(process.runs) != 1 || process.runs[0].Command != "notify-send" {
		t.Fatalf("runs = %#v, want notify-send", process.runs)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted len = %d, want 1", len(emitted))
	}
	notification, ok := emitted[0].(NotificationSent)
	if !ok {
		t.Fatalf("emitted = %#v, want NotificationSent", emitted)
	}
	if notification.Title != "Health summary" || notification.Message != "Load is normal." || notification.Level != "info" {
		t.Fatalf("notification = %#v", notification)
	}
}

func TestNotifySendIntentIncludesNotificationToneAndTTSCommands(t *testing.T) {
	op := operationForTest(t, NotifySendOp, &recordingProcess{})
	provider, ok := op.(operation.IntentProvider)
	if !ok {
		t.Fatal("notify_send does not expose operation intent")
	}
	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), map[string]any{
		"summary": "Health summary",
		"body":    "Load is normal.",
		"tone":    "info",
		"speak":   "Load is normal.",
	})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	for _, command := range []string{"notify-send", "paplay", "play", "piper_embedded", "aplay"} {
		if !hasProcessIntent(intents, command) {
			t.Fatalf("intents = %#v, missing process command %q", intents, command)
		}
	}
	if hasProcessIntent(intents, "espeak") || hasProcessIntent(intents, "spd-say") {
		t.Fatalf("intents = %#v, did not expect espeak/spd-say fallback", intents)
	}
}

func TestNotifySendSanitizesSpeechText(t *testing.T) {
	process := &recordingProcess{}
	var spoken string
	op := operationForTestWithSpeech(t, NotifySendOp, process, func(text string) error {
		spoken = text
		return nil
	})
	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"speak": "# System Health\n- **Disk** usage is high. See [runbook](https://example.invalid/runbook).\n- Free space now.",
	})

	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	for _, bad := range []string{"#", "**", "[", "]", "https://"} {
		if strings.Contains(spoken, bad) {
			t.Fatalf("speech = %q, contains markdown/url token %q", spoken, bad)
		}
	}
	if strings.Contains(spoken, "runbook") == false || strings.Contains(spoken, "Disk usage is high") == false {
		t.Fatalf("speech = %q, want readable link text and alert content", spoken)
	}
	if len([]rune(spoken)) > maxSpeechChars+3 {
		t.Fatalf("speech length = %d, want bounded", len([]rune(spoken)))
	}
}

func TestSpeechTextTruncatesLongMarkdown(t *testing.T) {
	got := speechText("## Alert\n" + strings.Repeat("- this is a long markdown bullet with details\n", 20))
	if len([]rune(got)) > maxSpeechChars+3 {
		t.Fatalf("speech length = %d, want bounded text: %q", len([]rune(got)), got)
	}
	if strings.Contains(got, "##") || strings.Contains(got, "- ") {
		t.Fatalf("speech = %q, want markdown stripped", got)
	}
}

func operationForTest(t *testing.T, name string, process system.ProcessManager) operation.Operation {
	t.Helper()
	return operationForTestWithSpeech(t, name, process, nil)
}

func operationForTestWithSpeech(t *testing.T, name string, process system.ProcessManager, speak func(string) error) operation.Operation {
	t.Helper()
	plugin := NewWithSystem(notifyTestSystem{MemorySystem: systemtest.NewMemory(), process: process})
	plugin.speak = speak
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			return op
		}
	}
	t.Fatalf("operation %q not found", name)
	return nil
}

type notifyTestSystem struct {
	*systemtest.MemorySystem
	process system.ProcessManager
}

func (s notifyTestSystem) Process() system.ProcessManager { return s.process }

type recordingProcess struct {
	runs   []system.ProcessRequest
	starts []system.ProcessRequest
}

func (p *recordingProcess) Run(_ context.Context, req system.ProcessRequest) (system.ProcessResult, error) {
	p.runs = append(p.runs, req)
	return system.ProcessResult{Command: req.Command, Args: req.Args}, nil
}

func (p *recordingProcess) Start(_ context.Context, req system.ProcessRequest) (system.ProcessHandle, error) {
	p.starts = append(p.starts, req)
	return recordingHandle{info: system.ProcessInfo{ID: fmt.Sprintf("proc-%d", len(p.starts)), Command: req.Command, Args: req.Args}}, nil
}

func (p *recordingProcess) Ensure(ctx context.Context, req system.ProcessRequest) (system.ProcessHandle, bool, error) {
	handle, err := p.Start(ctx, req)
	return handle, false, err
}

func (p *recordingProcess) List(context.Context) ([]system.ProcessInfo, error) { return nil, nil }
func (p *recordingProcess) Status(context.Context, string) (system.ProcessInfo, error) {
	return system.ProcessInfo{}, fmt.Errorf("not found")
}
func (p *recordingProcess) Output(context.Context, string) (system.ProcessOutput, error) {
	return system.ProcessOutput{}, fmt.Errorf("not found")
}
func (p *recordingProcess) Wait(context.Context, string, time.Duration) (system.ProcessResult, error) {
	return system.ProcessResult{}, fmt.Errorf("not found")
}
func (p *recordingProcess) Stop(context.Context, string) error { return nil }
func (p *recordingProcess) Kill(context.Context, string) error { return nil }

func hasProcessIntent(intents operation.IntentSet, command string) bool {
	for _, intent := range intents.Operations {
		target, ok := intent.Target.(operation.ProcessTarget)
		if ok && string(target.Command) == command {
			return true
		}
	}
	return false
}

type recordingHandle struct {
	info system.ProcessInfo
}

func (h recordingHandle) ID() string               { return h.info.ID }
func (h recordingHandle) Info() system.ProcessInfo { return h.info }
func (h recordingHandle) Events() <-chan system.ProcessEvent {
	ch := make(chan system.ProcessEvent)
	close(ch)
	return ch
}
func (h recordingHandle) Wait(context.Context) (system.ProcessResult, error) {
	return system.ProcessResult{Command: h.info.Command, Args: h.info.Args}, nil
}
