package httpcontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/daemon"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

func TestServerStatusAndSessions(t *testing.T) {
	host, err := daemon.New(daemon.Config{
		Client:    fakeClient{},
		StartedAt: time.Date(2026, 5, 12, 1, 2, 3, 0, time.UTC),
		SessionCatalog: session.SessionCatalog{
			"embedded:apps/demo:coder": {
				ID: resource.ResourceID{
					Kind:      "session",
					Origin:    "embedded",
					Namespace: resource.NewNamespace("apps/demo"),
					Name:      "coder",
				},
				Spec: coresession.Spec{Name: "coder"},
			},
		},
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	server, err := NewServer(ServerConfig{Host: host})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	statusResp := httptest.NewRecorder()
	server.ServeHTTP(statusResp, httptest.NewRequest(http.MethodGet, "/status", nil))
	if statusResp.Code != http.StatusOK {
		t.Fatalf("status code = %d", statusResp.Code)
	}

	sessionsResp := httptest.NewRecorder()
	server.ServeHTTP(sessionsResp, httptest.NewRequest(http.MethodGet, "/sessions", nil))
	if sessionsResp.Code != http.StatusOK {
		t.Fatalf("sessions code = %d", sessionsResp.Code)
	}
	var sessions []clientapi.SessionSummary
	if err := json.Unmarshal(sessionsResp.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("Unmarshal sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Info.Thread.ID != "thread-1" {
		t.Fatalf("sessions = %#v", sessions)
	}

	configuredResp := httptest.NewRecorder()
	server.ServeHTTP(configuredResp, httptest.NewRequest(http.MethodGet, "/configured-sessions", nil))
	if configuredResp.Code != http.StatusOK {
		t.Fatalf("configured sessions code = %d", configuredResp.Code)
	}
	var configured []daemon.ConfiguredSession
	if err := json.Unmarshal(configuredResp.Body.Bytes(), &configured); err != nil {
		t.Fatalf("Unmarshal configured sessions: %v", err)
	}
	if len(configured) != 1 || configured[0].Spec.Name != "coder" {
		t.Fatalf("configured sessions = %#v", configured)
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
