package codershell

import (
	"context"
	"fmt"
	"strings"
)

// RemoteClientOptions configures a client that connects to a remote shell/session endpoint.
type RemoteClientOptions struct {
	Endpoint   string
	ParseError error
}

// RemoteClient is a ShellClient placeholder for daemon/session transport. It
// preserves the client boundary while the concrete transport is added later.
type RemoteClient struct {
	endpoint   string
	parseError error
}

// NewRemoteClient returns a remote shell client.
func NewRemoteClient(opts RemoteClientOptions) *RemoteClient {
	return &RemoteClient{endpoint: strings.TrimSpace(opts.Endpoint), parseError: opts.ParseError}
}

func (c *RemoteClient) ConnectionDescription() string {
	if strings.TrimSpace(c.endpoint) == "" {
		return "remote:<unset>"
	}
	return c.endpoint
}

// Endpoint returns the configured remote endpoint.
func (c *RemoteClient) Endpoint() string { return c.endpoint }

func (c *RemoteClient) CreateSession(ctx context.Context, req CreateSessionRequest) (SessionInfo, error) {
	_ = ctx
	_ = req
	return SessionInfo{}, c.unavailable("create session")
}

func (c *RemoteClient) CloseSession(ctx context.Context, sessionID string) error {
	_ = ctx
	_ = sessionID
	return c.unavailable("close session")
}

func (c *RemoteClient) SubmitCommand(ctx context.Context, sessionID string, req CommandRequest) ([]TranscriptEvent, error) {
	_ = ctx
	_ = sessionID
	_ = req
	return nil, c.unavailable("submit command")
}

func (c *RemoteClient) SubmitAsk(ctx context.Context, sessionID string, req AskRequest) ([]TranscriptEvent, error) {
	_ = ctx
	_ = sessionID
	_ = req
	return nil, c.unavailable("submit ask")
}

func (c *RemoteClient) SubmitSlash(ctx context.Context, sessionID string, req SlashRequest) ([]TranscriptEvent, error) {
	_ = ctx
	_ = sessionID
	_ = req
	return nil, c.unavailable("submit slash")
}

func (c *RemoteClient) ChangeCWD(ctx context.Context, sessionID string, path string) (CWDResult, error) {
	_ = ctx
	_ = sessionID
	_ = path
	return CWDResult{}, c.unavailable("change cwd")
}

func (c *RemoteClient) ResourceSearch(ctx context.Context, sessionID string, query ResourceSearchQuery) ([]ResourceSearchResult, error) {
	_ = ctx
	_ = sessionID
	_ = query
	return nil, c.unavailable("resource search")
}

func (c *RemoteClient) unavailable(action string) error {
	if c.parseError != nil {
		return c.parseError
	}
	endpoint := c.endpoint
	if endpoint == "" {
		endpoint = "<unset>"
	}
	return fmt.Errorf("remote shell client %s unavailable: endpoint %s has no transport yet", action, endpoint)
}
