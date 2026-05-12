package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/channel"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
)

func TestHostStatusAndListSessions(t *testing.T) {
	started := time.Date(2026, 5, 12, 1, 2, 3, 0, time.UTC)
	host, err := New(Config{Client: fakeClient{}, StartedAt: started})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	status, err := host.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.StartedAt.Equal(started) {
		t.Fatalf("started = %s, want %s", status.StartedAt, started)
	}
	sessions, err := host.ListSessions(context.Background(), clientapi.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Info.Thread.ID != "thread-1" {
		t.Fatalf("sessions = %#v", sessions)
	}
}

type fakeClient struct{}

func (fakeClient) Open(context.Context, clientapi.OpenRequest) (clientapi.SessionHandle, error) {
	return nil, nil
}

func (fakeClient) Resume(context.Context, clientapi.ResumeRequest) (clientapi.SessionHandle, error) {
	return nil, nil
}

func (fakeClient) ListSessions(context.Context, clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	return []clientapi.SessionSummary{{
		Info: clientapi.SessionInfo{
			Thread:       corethread.Ref{ID: "thread-1", BranchID: corethread.MainBranch},
			Conversation: channel.ConversationRef{ID: "conv-1"},
		},
	}}, nil
}
