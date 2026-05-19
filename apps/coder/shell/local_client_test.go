package codershell

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestLocalClientCreatesSeparateSessions(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	client := NewLocalClient(sys)
	first, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "."})
	if err != nil {
		t.Fatalf("CreateSession(first) error = %v", err)
	}
	second, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "."})
	if err != nil {
		t.Fatalf("CreateSession(second) error = %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("session IDs matched: %q", first.ID)
	}
}

func TestLocalClientSubmitCommandDoesNotExecuteProcesses(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	client := NewLocalClient(sys)
	session, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "."})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	events, err := client.SubmitCommand(context.Background(), session.ID, CommandRequest{Line: "go version"})
	if err != nil {
		t.Fatalf("SubmitCommand() error = %v", err)
	}
	var started, denied bool
	for _, event := range events {
		if event.SessionID != session.ID {
			t.Fatalf("event session = %q, want %q", event.SessionID, session.ID)
		}
		switch event.Kind {
		case EventCommandStarted:
			started = true
		case EventError:
			denied = true
		}
	}
	if !started || !denied {
		t.Fatalf("events started=%v denied=%v; events=%+v", started, denied, events)
	}
}

func TestLocalClientCommandErrorIsTranscriptEvent(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	client := NewLocalClient(sys)
	session, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "."})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	events, err := client.SubmitCommand(context.Background(), session.ID, CommandRequest{Line: "definitely-not-a-real-command"})
	if err != nil {
		t.Fatalf("SubmitCommand() returned error = %v", err)
	}
	for _, event := range events {
		if event.Kind == EventError {
			return
		}
	}
	t.Fatalf("error event not found: %+v", events)
}
