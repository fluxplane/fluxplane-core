package codershell

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSubmitDisconnectedSessionDoesNotCallClient(t *testing.T) {
	client := NewFakeClient()
	shell := &ShellObject{client: client, tabs: []TabSession{{
		ID:          disconnectedSessionID,
		CWD:         "/workspace",
		InputMode:   InputModeShell,
		InputBuffer: "ls",
		Transcript: []TranscriptEvent{{
			ID:      "err",
			Time:    time.Now(),
			Kind:    EventError,
			Summary: "create shell session: dial unix coder.sock: connect: no such file or directory",
		}},
	}}}

	err := shell.SubmitActiveInput(context.Background())
	if err == nil {
		t.Fatal("SubmitActiveInput() error is nil")
	}
	if !strings.Contains(err.Error(), "shell session is not connected") || !strings.Contains(err.Error(), "dial unix") {
		t.Fatalf("SubmitActiveInput() error = %v", err)
	}
	active := shell.ActiveTab()
	if active == nil {
		t.Fatal("ActiveTab() is nil")
	}
	if active.InputBuffer != "" {
		t.Fatalf("InputBuffer = %q, want empty", active.InputBuffer)
	}
	last := active.Transcript[len(active.Transcript)-1]
	if last.Kind != EventError || !strings.Contains(last.Summary, "shell session is not connected") {
		t.Fatalf("last event = %+v", last)
	}
}
