package directchannel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/policy"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/harness"
)

var _ clientapi.ChannelClient = (*Client)(nil)

// Client is a channel client that talks to a harness in-process.
type Client struct {
	service *harness.Service
	channel channel.Ref
	caller  policy.Caller
	trust   policy.Trust
}

// Config configures a direct channel client.
type Config struct {
	Service *harness.Service
	Channel channel.Ref
	Caller  policy.Caller
	Trust   policy.Trust
}

// New returns a direct channel client.
func New(cfg Config) (*Client, error) {
	if cfg.Service == nil {
		return nil, fmt.Errorf("directchannel: harness service is nil")
	}
	if cfg.Channel.Name == "" {
		cfg.Channel.Name = "direct"
	}
	if cfg.Caller.Kind == "" {
		cfg.Caller.Kind = policy.CallerUser
	}
	if cfg.Trust.Kind == "" {
		cfg.Trust.Kind = policy.TrustInvocation
	}
	if cfg.Trust.Level == "" {
		cfg.Trust.Level = policy.TrustUntrusted
	}
	return &Client{
		service: cfg.Service,
		channel: cfg.Channel,
		caller:  cfg.Caller,
		trust:   cfg.Trust,
	}, nil
}

// Open opens a channel-bound session and returns a session handle.
func (c *Client) Open(ctx context.Context, req clientapi.OpenRequest) (clientapi.SessionHandle, error) {
	if c == nil || c.service == nil {
		return nil, fmt.Errorf("directchannel: client is nil")
	}
	info, err := c.service.OpenSession(ctx, harness.OpenSessionRequest{
		Session:      req.Session,
		Profile:      req.Profile,
		Channel:      c.channel,
		Conversation: req.Conversation,
		ThreadID:     req.ThreadID,
		Metadata:     req.Metadata,
		Approver:     req.Approver,
	})
	if err != nil {
		return nil, err
	}
	return &Session{
		client: c,
		info:   toClientSessionInfo(info),
	}, nil
}

// Resume resumes a known thread ID through this channel.
func (c *Client) Resume(ctx context.Context, req clientapi.ResumeRequest) (clientapi.SessionHandle, error) {
	if req.ThreadID == "" {
		return nil, fmt.Errorf("directchannel: resume thread id is empty")
	}
	return c.Open(ctx, clientapi.OpenRequest{ThreadID: req.ThreadID})
}

// ListSessions lists sessions known for this direct channel.
func (c *Client) ListSessions(ctx context.Context, req clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	if c == nil || c.service == nil {
		return nil, fmt.Errorf("directchannel: client is nil")
	}
	infos, err := c.service.ListSessions(ctx, harness.ListSessionsRequest{
		Channel:         c.channel,
		IncludeArchived: req.IncludeArchived,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]clientapi.SessionSummary, len(infos))
	for i, info := range infos {
		out[i] = clientapi.SessionSummary{Info: toClientSessionInfo(info)}
	}
	return out, nil
}

// Session is a direct in-process session handle.
type Session struct {
	client *Client
	info   clientapi.SessionInfo
}

var _ clientapi.SessionHandle = (*Session)(nil)

// Info returns stable session identity.
func (s *Session) Info() clientapi.SessionInfo {
	if s == nil {
		return clientapi.SessionInfo{}
	}
	return s.info
}

// Submit sends one submission to the session and returns a run handle.
func (s *Session) Submit(ctx context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	if s == nil || s.client == nil || s.client.service == nil {
		return nil, fmt.Errorf("directchannel: session is nil")
	}
	if err := submission.Validate(); err != nil {
		return nil, err
	}
	if submission.ID == "" {
		submission.ID = clientapi.RunID(newID("run_"))
	}
	if submission.Caller.Kind == "" {
		submission.Caller = s.client.caller
	}
	if submission.Trust.Kind == "" {
		submission.Trust = s.client.trust
	}
	run := newRunHandle(s.info, submission)
	go run.execute(ctx, s.client.service, s.info)
	return run, nil
}

// Events subscribes to session-level outbound events.
func (s *Session) Events(ctx context.Context, opts clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	if s == nil || s.client == nil || s.client.service == nil {
		ch := make(chan clientapi.Event)
		close(ch)
		return ch, func() {}, fmt.Errorf("directchannel: session is nil")
	}
	events, cancel, err := s.client.service.Subscribe(ctx, s.info.Thread.ID, opts)
	return events, cancel, err
}

// OnEvent registers a callback for session-level events.
func (s *Session) OnEvent(ctx context.Context, fn func(clientapi.Event)) (func(), error) {
	if fn == nil {
		return func() {}, fmt.Errorf("directchannel: event callback is nil")
	}
	events, cancel, err := s.Events(ctx, clientapi.EventOptions{Buffer: 16})
	if err != nil {
		return cancel, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				fn(event)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}, nil
}

// Close closes the local handle. Session lifecycle persistence will be added
// once close semantics are modeled in harness.
func (s *Session) Close(context.Context) error { return nil }

type runHandle struct {
	id         clientapi.RunID
	session    clientapi.SessionInfo
	submission clientapi.Submission
	events     chan clientapi.Event
	done       chan struct{}

	mu     sync.Mutex
	result clientapi.Result
	err    error
}

var _ clientapi.RunHandle = (*runHandle)(nil)

func newRunHandle(session clientapi.SessionInfo, submission clientapi.Submission) *runHandle {
	return &runHandle{
		id:         submission.ID,
		session:    session,
		submission: submission,
		events:     make(chan clientapi.Event, clientapi.DefaultRunEventBuffer),
		done:       make(chan struct{}),
	}
}

func (r *runHandle) ID() clientapi.RunID { return r.id }

func (r *runHandle) Session() clientapi.SessionInfo { return r.session }

func (r *runHandle) Submission() clientapi.Submission { return r.submission }

func (r *runHandle) Events() <-chan clientapi.Event { return r.events }

func (r *runHandle) Done() <-chan struct{} { return r.done }

func (r *runHandle) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *runHandle) Wait(ctx context.Context) (clientapi.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return clientapi.Result{}, ctx.Err()
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.result, r.err
	}
}

func (r *runHandle) execute(ctx context.Context, service *harness.Service, info clientapi.SessionInfo) {
	defer close(r.events)
	defer close(r.done)

	events, cancel, err := service.Subscribe(ctx, info.Thread.ID, clientapi.EventOptions{Buffer: clientapi.DefaultRunEventBuffer})
	if err != nil {
		r.fail(info, err)
		return
	}
	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		r.forwardRunEvents(events)
	}()
	defer func() {
		select {
		case <-forwardDone:
		case <-time.After(time.Second):
		}
		cancel()
		select {
		case <-forwardDone:
		case <-time.After(time.Second):
		}
	}()

	switch r.submission.Kind {
	case clientapi.SubmissionInput:
		result, err := service.HandleSessionInbound(ctx, toHarnessSessionInfo(info), channel.Inbound{
			ID:           string(r.id),
			Channel:      info.Channel,
			Conversation: info.Conversation,
			Caller:       r.submission.Caller,
			Trust:        r.submission.Trust,
			Kind:         channel.InboundMessage,
			Message: &channel.Message{
				Content:  r.submission.Input.ContentOrText(),
				Metadata: r.submission.Input.Metadata,
			},
		})
		if err != nil {
			r.fail(info, err)
			return
		}
		r.setResult(clientapi.Result{
			RunID:      r.id,
			Session:    info,
			Submission: r.submission,
			Input:      &result.Input,
			Outbound:   result.Outbound,
		}, nil)
	case clientapi.SubmissionCommand:
		result, err := service.HandleSessionInbound(ctx, toHarnessSessionInfo(info), channel.Inbound{
			ID:           string(r.id),
			Channel:      info.Channel,
			Conversation: info.Conversation,
			Caller:       r.submission.Caller,
			Trust:        r.submission.Trust,
			Kind:         channel.InboundCommand,
			Command:      r.submission.Command,
		})
		if err != nil {
			r.fail(info, err)
			return
		}
		r.setResult(clientapi.Result{
			RunID:      r.id,
			Session:    info,
			Submission: r.submission,
			Command:    &result.Command,
			Outbound:   result.Outbound,
		}, nil)
	case clientapi.SubmissionEvent, clientapi.SubmissionSignal:
		r.fail(info, fmt.Errorf("directchannel: submission kind %q is not supported yet", r.submission.Kind))
	default:
		r.fail(info, fmt.Errorf("directchannel: submission kind %q is invalid", r.submission.Kind))
	}
}

func (r *runHandle) setResult(result clientapi.Result, err error) {
	r.mu.Lock()
	r.result = result
	r.err = err
	r.mu.Unlock()
}

func (r *runHandle) complete(result clientapi.Result, err error) {
	r.setResult(result, err)
	kind := clientapi.EventRunCompleted
	if err != nil {
		kind = clientapi.EventRunFailed
	}
	r.emit(clientapi.Event{
		Kind:    kind,
		RunID:   r.id,
		Session: r.session,
		Error:   err,
	})
}

func (r *runHandle) fail(info clientapi.SessionInfo, err error) {
	r.complete(clientapi.Result{
		RunID:      r.id,
		Session:    info,
		Submission: r.submission,
	}, err)
}

func (r *runHandle) emit(event clientapi.Event) {
	select {
	case r.events <- event:
	case <-time.After(time.Second):
	}
}

func (r *runHandle) forwardRunEvents(events <-chan clientapi.Event) {
	for {
		event, ok := <-events
		if !ok {
			return
		}
		if event.RunID != r.id {
			continue
		}
		r.emit(event)
		if event.Kind == clientapi.EventRunCompleted || event.Kind == clientapi.EventRunFailed {
			return
		}
	}
}

func toHarnessSessionInfo(info clientapi.SessionInfo) harness.SessionInfo {
	return harness.SessionInfo{
		Session:      info.Session,
		Thread:       info.Thread,
		Channel:      info.Channel,
		Conversation: info.Conversation,
		Metadata:     cloneStringMap(info.Metadata),
		Resumed:      info.Resumed,
	}
}

func toClientSessionInfo(info harness.SessionInfo) clientapi.SessionInfo {
	return clientapi.SessionInfo{
		Session:      info.Session,
		Thread:       info.Thread,
		Channel:      info.Channel,
		Conversation: info.Conversation,
		Metadata:     cloneStringMap(info.Metadata),
		Resumed:      info.Resumed,
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func newID(prefix string) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(raw[:])
}
