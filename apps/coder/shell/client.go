package codershell

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ShellClient is the session-scoped boundary used by the shell controller. Real
// implementations can be local unix socket, HTTP/channel, or in-process test
// clients. The TUI must not bypass this interface for executable actions.
type ShellClient interface {
	CreateSession(ctx context.Context, req CreateSessionRequest) (SessionInfo, error)
	CloseSession(ctx context.Context, sessionID string) error
	SubmitCommand(ctx context.Context, sessionID string, req CommandRequest) ([]TranscriptEvent, error)
	SubmitAsk(ctx context.Context, sessionID string, req AskRequest) ([]TranscriptEvent, error)
	SubmitSlash(ctx context.Context, sessionID string, req SlashRequest) ([]TranscriptEvent, error)
	ChangeCWD(ctx context.Context, sessionID string, path string) (CWDResult, error)
	ResourceSearch(ctx context.Context, sessionID string, query ResourceSearchQuery) ([]ResourceSearchResult, error)
}

// ConnectionDescriber optionally describes where a shell client is connected.
type ConnectionDescriber interface {
	ConnectionDescription() string
}

// CreateSessionRequest requests a new shell session.
type CreateSessionRequest struct {
	CWD string
}

// SessionInfo describes a client session.
type SessionInfo struct {
	ID  string
	CWD string
}

// CommandRequest submits a shell command line to a session.
type CommandRequest struct {
	Line string
	CWD  string
}

// AskRequest submits an agent ask to a session.
type AskRequest struct {
	Text    string
	CWD     string
	Context []ContextItem
}

// SlashRequest submits a slash command to a session.
type SlashRequest struct {
	Line string
	CWD  string
}

// CWDResult describes a validated cwd change.
type CWDResult struct {
	CWD string
}

// ResourceKind identifies resource search result kinds.
type ResourceKind string

const (
	ResourceCommand   ResourceKind = "command"
	ResourceOperation ResourceKind = "operation"
	ResourceWorkflow  ResourceKind = "workflow"
	ResourceTask      ResourceKind = "task"
	ResourceSkill     ResourceKind = "skill"
	ResourceAgent     ResourceKind = "agent"
	ResourceAction    ResourceKind = "action"
	ResourcePath      ResourceKind = "path"
	ResourceFile      ResourceKind = "file"
	ResourceURL       ResourceKind = "url"
	ResourceHistory   ResourceKind = "history"
)

// ResourceSearchQuery asks the client for completion/mention resources.
type ResourceSearchQuery struct {
	Text       string
	Kinds      []ResourceKind
	Limit      int
	Workspace  string
	CWD        string
	PrefixMode string
	Mention    bool
}

// ResourceSearchResult is one completion/mention result.
type ResourceSearchResult struct {
	Kind        ResourceKind
	ID          string
	Label       string
	Detail      string
	InsertText  string
	Description string
	URI         string
	Icon        string
	Score       float64
	Metadata    map[string]string
}

// ResourceMention is a selected structured resource reference.
type ResourceMention struct {
	Kind       ResourceKind
	ID         string
	Label      string
	URI        string
	InsertText string
	Metadata   map[string]string
}

// FakeClient is a deterministic local client for the initial shell UI. It keeps
// session ownership explicit without executing real commands.
type FakeClient struct {
	mu       sync.Mutex
	nextID   int
	sessions map[string]SessionInfo
}

func (c *FakeClient) ConnectionDescription() string { return "fake" }

// NewFakeClient returns a deterministic shell client implementation.
func NewFakeClient() *FakeClient {
	return &FakeClient{sessions: map[string]SessionInfo{}}
}

func (c *FakeClient) CreateSession(ctx context.Context, req CreateSessionRequest) (SessionInfo, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = "."
	}
	info := SessionInfo{ID: fmt.Sprintf("session-%d", c.nextID), CWD: cwd}
	c.sessions[info.ID] = info
	return info, nil
}

func (c *FakeClient) CloseSession(ctx context.Context, sessionID string) error {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, sessionID)
	return nil
}

func (c *FakeClient) SubmitCommand(ctx context.Context, sessionID string, req CommandRequest) ([]TranscriptEvent, error) {
	_ = ctx
	if err := c.requireSession(sessionID); err != nil {
		return nil, err
	}
	now := time.Now()
	line := strings.TrimSpace(req.Line)
	return []TranscriptEvent{
		{ID: newEventID("cmd-start"), SessionID: sessionID, Time: now, Kind: EventCommandStarted, Summary: line, Data: map[string]string{"cwd": req.CWD}},
		{ID: newEventID("cmd-out"), SessionID: sessionID, Time: now, Kind: EventCommandOutput, Summary: "fake client accepted command: " + line},
		{ID: newEventID("cmd-done"), SessionID: sessionID, Time: now, Kind: EventCommandComplete, Summary: "0"},
	}, nil
}

func (c *FakeClient) SubmitAsk(ctx context.Context, sessionID string, req AskRequest) ([]TranscriptEvent, error) {
	_ = ctx
	if err := c.requireSession(sessionID); err != nil {
		return nil, err
	}
	now := time.Now()
	text := strings.TrimSpace(req.Text)
	return []TranscriptEvent{
		{ID: newEventID("ask"), SessionID: sessionID, Time: now, Kind: EventAskSubmitted, Summary: text, Data: map[string]string{"cwd": req.CWD, "context_items": fmt.Sprintf("%d", len(req.Context))}},
		{ID: newEventID("ask-out"), SessionID: sessionID, Time: now, Kind: EventAskOutput, Summary: "fake client would ask agent with session transcript context"},
	}, nil
}
func (c *FakeClient) SubmitSlash(ctx context.Context, sessionID string, req SlashRequest) ([]TranscriptEvent, error) {
	_ = ctx
	if err := c.requireSession(sessionID); err != nil {
		return nil, err
	}
	now := time.Now()
	line := strings.TrimSpace(req.Line)
	return []TranscriptEvent{
		{ID: newEventID("slash"), SessionID: sessionID, Time: now, Kind: EventSlashSubmitted, Summary: line, Data: map[string]string{"cwd": req.CWD}},
		{ID: newEventID("slash-out"), SessionID: sessionID, Time: now, Kind: EventCommandOutput, Summary: "fake client would dispatch slash command: " + line},
	}, nil
}

func (c *FakeClient) ChangeCWD(ctx context.Context, sessionID string, path string) (CWDResult, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	info, ok := c.sessions[sessionID]
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
	if !strings.HasPrefix(cleaned, "/") && info.CWD != "" && info.CWD != "." {
		cleaned = strings.TrimRight(info.CWD, "/") + "/" + cleaned
	}
	info.CWD = cleaned
	c.sessions[sessionID] = info
	return CWDResult{CWD: cleaned}, nil
}

func (c *FakeClient) ResourceSearch(ctx context.Context, sessionID string, query ResourceSearchQuery) ([]ResourceSearchResult, error) {
	_ = ctx
	if err := c.requireSession(sessionID); err != nil {
		return nil, err
	}
	return staticResourceSearch(query), nil
}

func staticResourceSearch(query ResourceSearchQuery) []ResourceSearchResult {
	all := []ResourceSearchResult{
		{Kind: ResourceAgent, ID: "coder", Label: "coder", InsertText: "@coder", Icon: "🤖"},
		{Kind: ResourceSkill, ID: "code-review", Label: "code-review", InsertText: "@code-review"},
		{Kind: ResourceFile, ID: "apps/coder/shell/shell.go", Label: "apps/coder/shell/shell.go", InsertText: "@apps/coder/shell/shell.go"},
		{Kind: ResourceURL, ID: "https://example.test/spec", Label: "https://example.test/spec", InsertText: "@https://example.test/spec"},
		{Kind: ResourceWorkflow, ID: "release", Label: "release", InsertText: "@release"},
		{Kind: ResourceOperation, ID: "filesystem.read", Label: "filesystem.read", InsertText: "@filesystem.read"},
	}
	text := strings.ToLower(strings.TrimSpace(query.Text))
	limit := query.Limit
	if limit <= 0 {
		limit = len(all)
	}
	out := make([]ResourceSearchResult, 0, len(all))
	for _, result := range all {
		if text == "" || strings.Contains(strings.ToLower(result.Label), text) || strings.Contains(strings.ToLower(string(result.Kind)), text) {
			out = append(out, result)
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (c *FakeClient) requireSession(sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.sessions[sessionID]; !ok {
		return fmt.Errorf("unknown shell session %q", sessionID)
	}
	return nil
}

func newEventID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
