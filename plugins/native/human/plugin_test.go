package human

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-event"
	fpsystem "github.com/fluxplane/fluxplane-system"
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

func operationForTest(t *testing.T, name string, process fpsystem.ProcessManager) operation.Operation {
	t.Helper()
	return operationForTestWithSpeech(t, name, process, nil)
}

func operationForTestWithSpeech(t *testing.T, name string, process fpsystem.ProcessManager, speak func(string) error) operation.Operation {
	t.Helper()
	plugin := NewWithConfig(Config{Process: process, Speak: speak})
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

type recordingProcess struct {
	runs   []fpsystem.ProcessRequest
	starts []fpsystem.ProcessRequest
}

func (p *recordingProcess) Run(_ context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessResult, error) {
	p.runs = append(p.runs, req)
	return fpsystem.ProcessResult{Command: req.Command, Args: req.Args}, nil
}

func (p *recordingProcess) Start(_ context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessHandle, error) {
	p.starts = append(p.starts, req)
	return recordingHandle{info: fpsystem.ProcessInfo{ID: fmt.Sprintf("proc-%d", len(p.starts)), Command: req.Command, Args: req.Args}}, nil
}

func (p *recordingProcess) Ensure(ctx context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessHandle, bool, error) {
	handle, err := p.Start(ctx, req)
	return handle, false, err
}

func (p *recordingProcess) Group(string) fpsystem.ProcessGroup                   { return nil }
func (p *recordingProcess) List(context.Context) ([]fpsystem.ProcessInfo, error) { return nil, nil }
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
	info fpsystem.ProcessInfo
}

func (h recordingHandle) ID() string                 { return h.info.ID }
func (h recordingHandle) Info() fpsystem.ProcessInfo { return h.info }
func (h recordingHandle) Events() <-chan fpsystem.ProcessEvent {
	ch := make(chan fpsystem.ProcessEvent)
	close(ch)
	return ch
}
func (h recordingHandle) Subscribe(context.Context) <-chan fpsystem.ProcessEvent { return h.Events() }
func (h recordingHandle) Wait(context.Context) (fpsystem.ProcessResult, error) {
	return fpsystem.ProcessResult{Command: h.info.Command, Args: h.info.Args}, nil
}

func (h recordingHandle) Stop(context.Context) error                              { return nil }
func (h recordingHandle) Kill(context.Context) error                              { return nil }
func (h recordingHandle) Signal(context.Context, fpsystem.ProcessSignal) error    { return nil }
func (h recordingHandle) Interrupt(context.Context) error                         { return nil }
func (h recordingHandle) Reload(context.Context) error                            { return nil }
func (h recordingHandle) Pause(context.Context) error                             { return nil }
func (h recordingHandle) Resume(context.Context) error                            { return nil }
func (h recordingHandle) Write(context.Context, []byte) (int, error)              { return 0, nil }
func (h recordingHandle) CloseInput(context.Context) error                        { return nil }
func (h recordingHandle) Restart(context.Context) (fpsystem.ProcessHandle, error) { return h, nil }
func (h recordingHandle) Detach(context.Context) error                            { return nil }
