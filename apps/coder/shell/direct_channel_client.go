package codershell

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/httpssechannel"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
)

// DirectChannelClient adapts the agentruntime direct channel API to ShellClient.
type DirectChannelClient struct {
	client  agentruntime.ChannelClient
	session agentruntime.SessionRef
	prefix  string
	mu      sync.Mutex
	handles map[string]agentruntime.Session
}

// DirectChannelClientOptions configures a direct channel shell client.
type DirectChannelClientOptions struct {
	Client  agentruntime.ChannelClient
	Session agentruntime.SessionRef
	Prefix  string
}

// NewDirectChannelClient creates a ShellClient over an agentruntime direct channel client.
func NewDirectChannelClient(opts DirectChannelClientOptions) *DirectChannelClient {
	if opts.Prefix == "" {
		opts.Prefix = "direct"
	}
	return &DirectChannelClient{client: opts.Client, session: opts.Session, prefix: opts.Prefix, handles: map[string]agentruntime.Session{}}
}

func (c *DirectChannelClient) ConnectionDescription() string { return "direct-channel" }

func (c *DirectChannelClient) CreateSession(ctx context.Context, req CreateSessionRequest) (SessionInfo, error) {
	if c == nil || c.client == nil {
		return SessionInfo{}, fmt.Errorf("direct channel client unavailable")
	}
	handle, err := c.client.Open(ctx, agentruntime.OpenRequest{
		Session:      c.session,
		Conversation: channel.ConversationRef{ID: fmt.Sprintf("shell-%d", time.Now().UnixNano())},
		Metadata: map[string]string{
			"surface": "coder-shell",
			"cwd":     strings.TrimSpace(req.CWD),
		},
	})
	if err != nil {
		return SessionInfo{}, err
	}
	info := handle.Info()
	id := string(info.Thread.ID)
	if id == "" {
		id = fmt.Sprintf("%s-%d", c.prefix, time.Now().UnixNano())
	}
	c.mu.Lock()
	c.handles[id] = handle
	c.mu.Unlock()
	return SessionInfo{ID: id, CWD: strings.TrimSpace(req.CWD)}, nil
}

func (c *DirectChannelClient) CloseSession(ctx context.Context, sessionID string) error {
	c.mu.Lock()
	handle := c.handles[sessionID]
	delete(c.handles, sessionID)
	c.mu.Unlock()
	if handle == nil {
		return nil
	}
	return handle.Close(ctx)
}

func (c *DirectChannelClient) SubmitCommand(ctx context.Context, sessionID string, req CommandRequest) ([]TranscriptEvent, error) {
	line := strings.TrimSpace(req.Line)
	start := TranscriptEvent{ID: newEventID("cmd-start"), SessionID: sessionID, Time: time.Now(), Kind: EventCommandStarted, Summary: line, Data: map[string]string{"cwd": req.CWD}}
	invocation, err := shellCommandInvocation(line, req.CWD)
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	handle, err := c.sessionHandle(sessionID)
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	run, err := handle.Submit(ctx, agentruntime.NewSubmission().WithCommand(invocation))
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	result, err := run.Wait(ctx)
	events := []TranscriptEvent{start}
	if err != nil {
		return events, err
	}
	return append(events, transcriptEventsForResult(sessionID, result, EventCommandOutput, EventCommandComplete)...), nil
}

func (c *DirectChannelClient) SubmitAsk(ctx context.Context, sessionID string, req AskRequest) ([]TranscriptEvent, error) {
	text := strings.TrimSpace(req.Text)
	start := TranscriptEvent{ID: newEventID("ask"), SessionID: sessionID, Time: time.Now(), Kind: EventAskSubmitted, Summary: text, Data: map[string]string{"cwd": req.CWD, "context_items": fmt.Sprintf("%d", len(req.Context))}}
	handle, err := c.sessionHandle(sessionID)
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	run, err := handle.Submit(ctx, agentruntime.NewSubmission().WithText(text))
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	result, err := run.Wait(ctx)
	events := []TranscriptEvent{start}
	if err != nil {
		return events, err
	}
	return append(events, transcriptEventsForResult(sessionID, result, EventAskOutput, EventCommandComplete)...), nil
}

func (c *DirectChannelClient) SubmitSlash(ctx context.Context, sessionID string, req SlashRequest) ([]TranscriptEvent, error) {
	line := strings.TrimSpace(req.Line)
	start := TranscriptEvent{ID: newEventID("slash"), SessionID: sessionID, Time: time.Now(), Kind: EventSlashSubmitted, Summary: line, Data: map[string]string{"cwd": req.CWD}}
	invocation, err := parseSlashInvocation(line)
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	handle, err := c.sessionHandle(sessionID)
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	run, err := handle.Submit(ctx, agentruntime.NewSubmission().WithCommand(invocation))
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	result, err := run.Wait(ctx)
	events := []TranscriptEvent{start}
	if err != nil {
		return events, err
	}
	return append(events, transcriptEventsForResult(sessionID, result, EventCommandOutput, EventCommandComplete)...), nil
}

func (c *DirectChannelClient) sessionHandle(sessionID string) (agentruntime.Session, error) {
	if c == nil {
		return nil, fmt.Errorf("direct channel client unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	handle := c.handles[sessionID]
	if handle == nil {
		return nil, fmt.Errorf("unknown shell session %q", sessionID)
	}
	return handle, nil
}

func transcriptEventsForResult(sessionID string, result agentruntime.Result, outputKind TranscriptKind, completeKind TranscriptKind) []TranscriptEvent {
	now := time.Now()
	events := []TranscriptEvent{}
	if result.Outbound != nil && result.Outbound.Message != nil {
		events = append(events, TranscriptEvent{ID: newEventID("out"), SessionID: sessionID, Time: now, Kind: outputKind, Summary: fmt.Sprint(result.Outbound.Message.Content)})
	}
	if result.Command != nil {
		summary := string(result.Command.Status)
		if result.Command.Error != nil {
			return append(events, TranscriptEvent{ID: newEventID("cmd-error"), SessionID: sessionID, Time: now, Kind: EventError, Summary: result.Command.Error.Message})
		}
		events = append(events, TranscriptEvent{ID: newEventID("cmd-done"), SessionID: sessionID, Time: now, Kind: completeKind, Summary: summary})
		return events
	}
	if result.Input != nil {
		summary := string(result.Input.Status)
		if result.Input.Error != nil {
			return append(events, TranscriptEvent{ID: newEventID("input-error"), SessionID: sessionID, Time: now, Kind: EventError, Summary: result.Input.Error.Message})
		}
		events = append(events, TranscriptEvent{ID: newEventID("input-done"), SessionID: sessionID, Time: now, Kind: completeKind, Summary: summary})
	}
	return events
}

func parseSlashInvocation(line string) (command.Invocation, error) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "/"))
	if line == "" {
		return command.Invocation{}, fmt.Errorf("slash command is empty")
	}
	fields, err := splitShellFields(line)
	if err != nil {
		return command.Invocation{}, err
	}
	if len(fields) == 0 {
		return command.Invocation{}, fmt.Errorf("slash command is empty")
	}
	return command.Invocation{Path: command.Path{fields[0]}, Args: fields[1:], Input: fields[1:]}, nil
}

func splitShellFields(line string) ([]string, error) {
	fields := []string{}
	current := strings.Builder{}
	var quote rune
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if current.Len() > 0 {
				fields = append(fields, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted string")
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields, nil
}

func (c *DirectChannelClient) ChangeCWD(ctx context.Context, sessionID string, path string) (CWDResult, error) {
	_ = ctx
	_ = sessionID
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return CWDResult{}, fmt.Errorf("cd: missing path")
	}
	if cleaned == "-" {
		return CWDResult{}, fmt.Errorf("cd - is not supported yet")
	}
	return CWDResult{CWD: cleaned}, nil
}

func (c *DirectChannelClient) ResourceSearch(ctx context.Context, sessionID string, query ResourceSearchQuery) ([]ResourceSearchResult, error) {
	_ = ctx
	_ = sessionID
	return staticResourceSearch(query), nil
}

func newRemoteDirectChannelClient(endpoint string) (ShellClient, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = defaultDirectEndpoint
	}
	cfg := httpssechannel.ClientConfig{BaseURL: endpoint}
	if parsed, err := url.Parse(endpoint); err == nil && strings.EqualFold(parsed.Scheme, "unix") {
		cfg.BaseURL = "http://unix"
		cfg.UnixSocket = parsed.Path
	}
	client, err := httpssechannel.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return NewDirectChannelClient(DirectChannelClientOptions{
		Client:  client,
		Session: agentruntime.SessionRef{Name: defaultSessionName},
		Prefix:  "remote",
	}), nil
}

func shellCommandInvocation(line string, cwd string) (command.Invocation, error) {
	fields, err := splitShellFields(line)
	if err != nil {
		return command.Invocation{}, err
	}
	if len(fields) == 0 {
		return command.Invocation{}, fmt.Errorf("shell command is empty")
	}
	return command.Invocation{
		Path: command.Path{"shell", "exec"},
		Input: map[string]any{
			"command": fields[0],
			"args":    fields[1:],
			"workdir": strings.TrimSpace(cwd),
		},
	}, nil
}
