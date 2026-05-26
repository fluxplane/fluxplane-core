package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
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

func TestHostListConfiguredSessions(t *testing.T) {
	host, err := New(Config{
		Client: fakeClient{},
		SessionCatalog: session.SessionCatalog{
			"embedded:apps/demo:assistant": {
				ID: resource.ResourceID{
					Kind:      "session",
					Origin:    "embedded",
					Namespace: resource.NewNamespace("apps/demo"),
					Name:      "assistant",
				},
				Spec: coresession.Spec{Name: "assistant"},
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sessions, err := host.ListConfiguredSessions(context.Background())
	if err != nil {
		t.Fatalf("ListConfiguredSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("configured sessions len = %d, want 1", len(sessions))
	}
	if sessions[0].ID != "embedded:apps/demo:assistant" || sessions[0].Spec.Name != "assistant" {
		t.Fatalf("configured sessions = %#v", sessions)
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
