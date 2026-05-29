package httpsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	coreevent "github.com/fluxplane/fluxplane-event"
)

func TestServerEventsUsesLastEventIDAsReplayCursor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		target    string
		header    string
		wantAfter coreevent.Sequence
	}{
		{
			name:      "header",
			target:    "/sessions/thread-1/events?buffer=3",
			header:    "42",
			wantAfter: 42,
		},
		{
			name:      "query overrides header",
			target:    "/sessions/thread-1/events?after=9",
			header:    "42",
			wantAfter: 9,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &captureEventsSession{}
			client := &captureEventsClient{session: session}
			server, err := NewServer(ServerConfig{Client: client})
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			req.Header.Set("Last-Event-ID", tc.header)
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if client.resumed.ThreadID != "thread-1" {
				t.Fatalf("resumed thread = %q, want thread-1", client.resumed.ThreadID)
			}
			if session.events.After.Sequence != tc.wantAfter {
				t.Fatalf("after sequence = %d, want %d", session.events.After.Sequence, tc.wantAfter)
			}
		})
	}
}

type captureEventsClient struct {
	session *captureEventsSession
	resumed clientapi.ResumeRequest
}

func (c *captureEventsClient) Open(context.Context, clientapi.OpenRequest) (clientapi.SessionHandle, error) {
	return c.session, nil
}

func (c *captureEventsClient) Resume(_ context.Context, req clientapi.ResumeRequest) (clientapi.SessionHandle, error) {
	c.resumed = req
	return c.session, nil
}

func (c *captureEventsClient) ListSessions(context.Context, clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	return nil, nil
}

type captureEventsSession struct {
	events clientapi.EventOptions
}

func (s *captureEventsSession) Info() clientapi.SessionInfo {
	return clientapi.SessionInfo{Thread: corethread.Ref{ID: "thread-1", BranchID: corethread.MainBranch}}
}

func (s *captureEventsSession) Submit(context.Context, clientapi.Submission) (clientapi.RunHandle, error) {
	return nil, nil
}

func (s *captureEventsSession) Events(_ context.Context, opts clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	s.events = opts
	ch := make(chan clientapi.Event)
	close(ch)
	return ch, func() {}, nil
}

func (s *captureEventsSession) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	return func() {}, nil
}

func (s *captureEventsSession) Close(context.Context) error {
	return nil
}
