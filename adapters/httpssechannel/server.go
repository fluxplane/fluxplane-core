package httpssechannel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/command"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/policy"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
)

// Server exposes a ChannelClient through JSON endpoints and SSE event streams.
type Server struct {
	client    clientapi.ChannelClient
	authority Authority
	mux       *http.ServeMux
}

// ServerConfig configures an HTTP/SSE channel server.
type ServerConfig struct {
	Client    clientapi.ChannelClient
	Authority Authority
}

// Authority describes the listener-derived authority for remote submissions.
type Authority struct {
	Caller              policy.Caller
	Trust               policy.Trust
	AllowTrustDowngrade bool
}

// NewServer returns an HTTP handler for a channel client.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("httpssechannel: client is nil")
	}
	server := &Server{client: cfg.Client, authority: normalizeAuthority(cfg.Authority), mux: http.NewServeMux()}
	server.routes()
	return server, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /sessions/open", s.handleOpen)
	s.mux.HandleFunc("POST /sessions/resume", s.handleResume)
	s.mux.HandleFunc("GET /sessions", s.handleListSessions)
	s.mux.HandleFunc("POST /sessions/{threadID}/submit", s.handleSubmit)
	s.mux.HandleFunc("GET /sessions/{threadID}/events", s.handleEvents)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req clientapi.OpenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	session, err := s.client.Open(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, session.Info())
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	var req clientapi.ResumeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	session, err := s.client.Resume(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, session.Info())
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	req := clientapi.ListSessionsRequest{
		IncludeArchived: parseBool(r.URL.Query().Get("include_archived")),
		Limit:           parseInt(r.URL.Query().Get("limit")),
	}
	summaries, err := s.client.ListSessions(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, summaries)
}

type submitRequest struct {
	Session    clientapi.SessionInfo `json:"session"`
	Submission remoteSubmission      `json:"submission"`
}

type remoteSubmission struct {
	ID             clientapi.RunID           `json:"id,omitempty"`
	Kind           clientapi.SubmissionKind  `json:"kind"`
	Input          *clientapi.Input          `json:"input,omitempty"`
	Command        *command.Invocation       `json:"command,omitempty"`
	TrustDowngrade *clientapi.TrustDowngrade `json:"trust_downgrade,omitempty"`
	Metadata       map[string]any            `json:"metadata,omitempty"`
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	threadID := corethread.ID(r.PathValue("threadID"))
	var req submitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Session.Thread.ID == "" {
		req.Session.Thread.ID = threadID
	}
	if req.Session.Thread.ID != threadID {
		writeError(w, http.StatusBadRequest, fmt.Errorf("httpssechannel: submit thread id mismatch"))
		return
	}
	session, err := s.client.Open(r.Context(), clientapi.OpenRequest{
		Session:      req.Session.Session,
		ThreadID:     req.Session.Thread.ID,
		Conversation: req.Session.Conversation,
		Metadata:     req.Session.Metadata,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	submission, err := s.normalizeRemoteSubmission(req.Submission)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	run, err := session.Submit(r.Context(), submission)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	drained := drainSubmittedRunEvents(r.Context(), run.Events())
	result, err := run.Wait(r.Context())
	waitDrain(drained)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func normalizeAuthority(value Authority) Authority {
	if value.Trust.Level != "" && value.Trust.Kind == "" {
		value.Trust.Kind = policy.TrustInvocation
	}
	return value
}

func (s *Server) normalizeRemoteSubmission(remote remoteSubmission) (clientapi.Submission, error) {
	submission := clientapi.Submission{
		ID:             remote.ID,
		Kind:           remote.Kind,
		Input:          remote.Input,
		Command:        remote.Command,
		TrustDowngrade: remote.TrustDowngrade,
		Metadata:       remote.Metadata,
	}
	if s.authority.Caller.Kind != "" {
		submission.Caller = s.authority.Caller
	}
	if s.authority.Trust.Kind != "" || s.authority.Trust.Level != "" {
		submission.Trust = s.authority.Trust
	}
	if remote.TrustDowngrade != nil {
		if !s.authority.AllowTrustDowngrade {
			return clientapi.Submission{}, fmt.Errorf("httpssechannel: trust downgrade is not allowed by this listener")
		}
		if s.authority.Trust.Level == "" {
			return clientapi.Submission{}, fmt.Errorf("httpssechannel: listener trust is not configured")
		}
		if remote.TrustDowngrade.Level == "" {
			return clientapi.Submission{}, fmt.Errorf("httpssechannel: trust downgrade level is empty")
		}
		if !policy.TrustSatisfies(s.authority.Trust.Level, remote.TrustDowngrade.Level) {
			return clientapi.Submission{}, fmt.Errorf("httpssechannel: authority_exceeds_transport")
		}
		if !scopesSubset(remote.TrustDowngrade.Scopes, s.authority.Trust.Scopes) {
			return clientapi.Submission{}, fmt.Errorf("httpssechannel: authority_exceeds_transport")
		}
		submission.Trust.Level = remote.TrustDowngrade.Level
		submission.Trust.Scopes = append([]policy.Scope(nil), remote.TrustDowngrade.Scopes...)
		submission.Trust.Reason = remote.TrustDowngrade.Reason
		submission.Trust.Downgraded = true
	}
	if err := submission.Validate(); err != nil {
		return clientapi.Submission{}, err
	}
	return submission, nil
}

func scopesSubset(requested, granted []policy.Scope) bool {
	if len(requested) == 0 {
		return true
	}
	grants := map[policy.Scope]struct{}{}
	for _, scope := range granted {
		grants[scope] = struct{}{}
	}
	for _, scope := range requested {
		if _, ok := grants[scope]; !ok {
			return false
		}
	}
	return true
}

func drainSubmittedRunEvents(ctx context.Context, events <-chan clientapi.Event) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-events:
				if !ok {
					return
				}
			}
		}
	}()
	return done
}

func waitDrain(done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	threadID := corethread.ID(r.PathValue("threadID"))
	session, err := s.client.Resume(r.Context(), clientapi.ResumeRequest{ThreadID: threadID})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	opts := clientapi.EventOptions{
		Buffer: parseInt(r.URL.Query().Get("buffer")),
		Replay: parseBool(r.URL.Query().Get("replay")),
		After: clientapi.EventCursor{
			Sequence: coreevent.Sequence(parseUint(r.URL.Query().Get("after"))),
		},
	}
	events, cancel, err := session.Events(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, event clientapi.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if event.Cursor.Sequence != 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", event.Cursor.Sequence); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event.Kind); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer func() { _ = r.Body.Close() }()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func parseBool(raw string) bool {
	switch strings.ToLower(raw) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func parseInt(raw string) int {
	value, _ := strconv.Atoi(raw)
	return value
}

func parseUint(raw string) uint64 {
	value, _ := strconv.ParseUint(raw, 10, 64)
	return value
}
