package codershell

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/agentruntime/runtime/system"
)

// LocalClient provides local shell session bookkeeping for tests and embedders.
// It intentionally does not execute commands; process execution must use a
// channel-backed client so shell_exec goes through the operation safety envelope.
type LocalClient struct {
	sys system.System

	mu       sync.Mutex
	nextID   int
	sessions map[string]localSession
}

type localSession struct {
	ID  string
	CWD string
}

func (c *LocalClient) ConnectionDescription() string { return "direct" }

// NewLocalClient returns a ShellClient backed by the provided runtime system.
func NewLocalClient(sys system.System) *LocalClient {
	return &LocalClient{sys: sys, sessions: map[string]localSession{}}
}

func (c *LocalClient) CreateSession(ctx context.Context, req CreateSessionRequest) (SessionInfo, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = "."
	}
	info := localSession{ID: fmt.Sprintf("local-%d", c.nextID), CWD: cwd}
	c.sessions[info.ID] = info
	return SessionInfo(info), nil
}

func (c *LocalClient) CloseSession(ctx context.Context, sessionID string) error {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, sessionID)
	return nil
}

func (c *LocalClient) SubmitCommand(ctx context.Context, sessionID string, req CommandRequest) ([]TranscriptEvent, error) {
	session, err := c.session(sessionID)
	if err != nil {
		return nil, err
	}
	_ = ctx
	line := strings.TrimSpace(req.Line)
	if line == "" {
		return nil, nil
	}
	now := time.Now()
	return []TranscriptEvent{
		{ID: newEventID("cmd-start"), SessionID: sessionID, Time: now, Kind: EventCommandStarted, Summary: line, Data: map[string]string{"cwd": session.CWD}},
		{ID: newEventID("cmd-error"), SessionID: sessionID, Time: now, Kind: EventError, Summary: "local shell command execution requires a channel client", Data: map[string]string{"cwd": session.CWD}},
	}, nil
}

func (c *LocalClient) SubmitAsk(ctx context.Context, sessionID string, req AskRequest) ([]TranscriptEvent, error) {
	_ = ctx
	if _, err := c.session(sessionID); err != nil {
		return nil, err
	}
	now := time.Now()
	return []TranscriptEvent{
		{ID: newEventID("ask"), SessionID: sessionID, Time: now, Kind: EventAskSubmitted, Summary: strings.TrimSpace(req.Text), Data: map[string]string{"cwd": req.CWD, "context_items": fmt.Sprintf("%d", len(req.Context))}},
		{ID: newEventID("ask-out"), SessionID: sessionID, Time: now, Kind: EventAskOutput, Summary: "local client ask transport is not connected yet"},
	}, nil
}

func (c *LocalClient) SubmitSlash(ctx context.Context, sessionID string, req SlashRequest) ([]TranscriptEvent, error) {
	_ = ctx
	if _, err := c.session(sessionID); err != nil {
		return nil, err
	}
	now := time.Now()
	return []TranscriptEvent{
		{ID: newEventID("slash"), SessionID: sessionID, Time: now, Kind: EventSlashSubmitted, Summary: strings.TrimSpace(req.Line), Data: map[string]string{"cwd": req.CWD}},
		{ID: newEventID("slash-out"), SessionID: sessionID, Time: now, Kind: EventCommandOutput, Summary: "local client slash transport is not connected yet"},
	}, nil
}

func (c *LocalClient) ChangeCWD(ctx context.Context, sessionID string, path string) (CWDResult, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	session, ok := c.sessions[sessionID]
	if !ok {
		return CWDResult{}, fmt.Errorf("unknown shell session %q", sessionID)
	}
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return CWDResult{}, fmt.Errorf("cd: missing path")
	}
	if cleaned == "-" {
		return CWDResult{}, fmt.Errorf("cd - is not supported yet")
	}
	if !strings.HasPrefix(cleaned, "/") && session.CWD != "" && session.CWD != "." {
		cleaned = strings.TrimRight(session.CWD, "/") + "/" + cleaned
	}
	session.CWD = cleaned
	c.sessions[sessionID] = session
	return CWDResult{CWD: cleaned}, nil
}

func (c *LocalClient) ResourceSearch(ctx context.Context, sessionID string, query ResourceSearchQuery) ([]ResourceSearchResult, error) {
	if _, err := c.session(sessionID); err != nil {
		return nil, err
	}
	return staticResourceSearch(query, nil), nil
}

func (c *LocalClient) session(sessionID string) (localSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, ok := c.sessions[sessionID]
	if !ok {
		return localSession{}, fmt.Errorf("unknown shell session %q", sessionID)
	}
	return session, nil
}
