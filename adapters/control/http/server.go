package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	clientapi "github.com/fluxplane/engine/orchestration/client"
	"github.com/fluxplane/engine/orchestration/daemon"
)

// Server exposes a daemon host control API.
type Server struct {
	host *daemon.Host
	mux  *http.ServeMux
}

// ServerConfig configures a control server.
type ServerConfig struct {
	Host *daemon.Host
}

// NewServer returns an HTTP control server.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Host == nil {
		return nil, fmt.Errorf("httpcontrol: host is nil")
	}
	server := &Server{host: cfg.Host, mux: http.NewServeMux()}
	server.routes()
	return server, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /status", s.handleStatus)
	s.mux.HandleFunc("GET /sessions", s.handleSessions)
	s.mux.HandleFunc("GET /configured-sessions", s.handleConfiguredSessions)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.host.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.host.ListSessions(r.Context(), clientapi.ListSessionsRequest{
		IncludeArchived: parseBool(r.URL.Query().Get("include_archived")),
		Limit:           parseInt(r.URL.Query().Get("limit")),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleConfiguredSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.host.ListConfiguredSessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
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
