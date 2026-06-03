package sessionagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/channel"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionrun"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-operation"
)

// Config wires a Runner.
type Config struct {
	Client         Client
	Runner         *sessionrun.Runner
	MaxParallel    int
	ResolveProfile ProfileResolver
}

// Client is the small child-session port a command session-agent needs.
type Client interface {
	Open(context.Context, OpenRequest) (Session, error)
}

// ProfileResolver resolves a session profile before it is opened.
type ProfileResolver interface {
	ResolveProfile(context.Context, coresession.Ref) (coresession.Spec, error)
}

// ProfileResolverFunc adapts a function into ProfileResolver.
type ProfileResolverFunc func(context.Context, coresession.Ref) (coresession.Spec, error)

func (f ProfileResolverFunc) ResolveProfile(ctx context.Context, ref coresession.Ref) (coresession.Spec, error) {
	if f == nil {
		return coresession.Spec{}, fmt.Errorf("sessionagent: profile resolver is nil")
	}
	return f(ctx, ref)
}

type OpenRequest struct {
	Session      coresession.Ref
	Profile      coresession.Spec
	Conversation channel.ConversationRef
	Metadata     map[string]string
	Approver     operationruntime.ApprovalGate
}

type Session interface {
	Info() SessionInfo
	SendInput(context.Context, Input) (Run, error)
}

type SessionInfo struct {
	Thread corethread.Ref
}

type Input struct {
	Text     string
	Metadata map[string]any
}

type Run interface {
	ID() string
	Events() <-chan RunEvent
	Wait(context.Context) (RunResult, error)
}

type RunEvent struct {
	Kind      string
	Operation string
	Runtime   string
}

type RunResult struct {
	Text string
}

// Request describes one command helper session invocation.
type Request struct {
	ID             ID
	Profile        coresession.Ref
	Agent          agent.Ref
	Task           string
	TaskID         string
	Timeout        time.Duration
	Policy         coresession.DelegationPolicy
	ParentThreadID corethread.ID
	ParentRunID    string
	ParentCallID   operation.CallID
	Metadata       map[string]string
	Events         event.Sink
	Approver       operationruntime.ApprovalGate
}

// Result is the terminal output from a command helper session.
type Result struct {
	ID            ID                `json:"id"`
	Profile       coresession.Ref   `json:"profile,omitempty"`
	Agent         agent.Ref         `json:"agent,omitempty"`
	ChildThreadID corethread.ID     `json:"child_thread_id,omitempty"`
	ChildRunID    string            `json:"child_run_id,omitempty"`
	Output        string            `json:"output,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Runner runs command helper sessions with delegation-policy narrowing.
type Runner struct {
	runner *sessionrun.Runner
}

// New returns a command session-agent runner.
func New(cfg Config) *Runner {
	runner := cfg.Runner
	if runner == nil {
		runner = sessionrun.New(sessionrun.Config{
			Client:         clientAdapter{client: cfg.Client},
			MaxParallel:    cfg.MaxParallel,
			ResolveProfile: cfg.ResolveProfile,
		})
	}
	return &Runner{runner: runner}
}

// Run opens the requested profile, submits the task text, and waits for the
// helper session's final response.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	if r == nil || r.runner == nil {
		return Result{}, fmt.Errorf("sessionagent: runner is not configured")
	}
	if req.ID == "" {
		req.ID = ID(newID("session_agent_"))
	}
	result, err := r.runner.Run(ctx, sessionrun.Request{
		ID:             sessionrun.ID(req.ID),
		Session:        req.Profile,
		Agent:          req.Agent,
		Input:          req.Task,
		InputMetadata:  map[string]any{"session_agent_id": string(req.ID), "task_id": req.TaskID},
		Timeout:        req.Timeout,
		Policy:         req.Policy,
		EnforcePolicy:  true,
		ParentThreadID: req.ParentThreadID,
		ParentRunID:    req.ParentRunID,
		ParentCallID:   req.ParentCallID,
		TaskID:         req.TaskID,
		Metadata:       req.Metadata,
		Events:         sessionAgentEvents(req.Events),
		Approver:       req.Approver,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{
		ID:            ID(result.ID),
		Profile:       result.Profile,
		Agent:         result.Agent,
		ChildThreadID: result.ChildThreadID,
		ChildRunID:    result.ChildRunID,
		Output:        result.Output,
		Metadata:      cloneStringMap(result.Metadata),
	}, nil
}

type clientAdapter struct {
	client Client
}

func (c clientAdapter) Open(ctx context.Context, req sessionrun.OpenRequest) (sessionrun.Session, error) {
	if c.client == nil {
		return nil, fmt.Errorf("sessionagent: client is nil")
	}
	session, err := c.client.Open(ctx, OpenRequest{
		Session:      req.Session,
		Profile:      req.Profile,
		Conversation: req.Conversation,
		Metadata:     req.Metadata,
		Approver:     req.Approver,
	})
	if err != nil {
		return nil, err
	}
	return sessionAdapter{session: session}, nil
}

type sessionAdapter struct {
	session Session
}

func (s sessionAdapter) Info() sessionrun.SessionInfo {
	info := s.session.Info()
	return sessionrun.SessionInfo{Thread: info.Thread}
}

func (s sessionAdapter) SendInput(ctx context.Context, input sessionrun.Input) (sessionrun.Run, error) {
	run, err := s.session.SendInput(ctx, Input{Text: input.Text, Metadata: input.Metadata})
	if err != nil {
		return nil, err
	}
	return runAdapter{run: run}, nil
}

type runAdapter struct {
	run Run
}

func (r runAdapter) ID() string { return r.run.ID() }

func (r runAdapter) Events() <-chan sessionrun.RunEvent {
	out := make(chan sessionrun.RunEvent, 16)
	go func() {
		defer close(out)
		for event := range r.run.Events() {
			out <- sessionrun.RunEvent{
				Kind:      event.Kind,
				Operation: event.Operation,
				Runtime:   event.Runtime,
			}
		}
	}()
	return out
}

func (r runAdapter) Wait(ctx context.Context) (sessionrun.RunResult, error) {
	result, err := r.run.Wait(ctx)
	if err != nil {
		return sessionrun.RunResult{}, err
	}
	return sessionrun.RunResult{Text: result.Text}, nil
}

type sessionAgentEventSink struct {
	inner event.Sink
}

func sessionAgentEvents(inner event.Sink) event.Sink {
	if inner == nil {
		return nil
	}
	return sessionAgentEventSink{inner: inner}
}

func (s sessionAgentEventSink) Emit(payload event.Event) {
	if s.inner == nil || payload == nil {
		return
	}
	switch ev := payload.(type) {
	case sessionrun.Requested:
		s.inner.Emit(Requested{Causation: sessionAgentCausation(ev.Causation), Task: ev.Input})
	case sessionrun.Started:
		s.inner.Emit(Started{Causation: sessionAgentCausation(ev.Causation), Task: ev.Input})
	case sessionrun.Progressed:
		s.inner.Emit(Progressed{Causation: sessionAgentCausation(ev.Causation), Message: ev.Message, Percent: ev.Percent})
	case sessionrun.Completed:
		s.inner.Emit(Completed{Causation: sessionAgentCausation(ev.Causation), Output: ev.Output})
	case sessionrun.Failed:
		s.inner.Emit(Failed{Causation: sessionAgentCausation(ev.Causation), Error: ev.Error})
	case sessionrun.Cancelled:
		s.inner.Emit(Cancelled{Causation: sessionAgentCausation(ev.Causation), Reason: ev.Reason})
	default:
		s.inner.Emit(payload)
	}
}

func sessionAgentCausation(c sessionrun.Causation) Causation {
	return Causation{
		ID:             ID(c.ID),
		ParentThreadID: c.ParentThreadID,
		ParentRunID:    c.ParentRunID,
		ParentCallID:   c.ParentCallID,
		ChildThreadID:  c.ChildThreadID,
		ChildRunID:     c.ChildRunID,
		Profile:        c.Profile,
		Agent:          c.Agent,
		TaskID:         c.TaskID,
		Metadata:       cloneStringMap(c.Metadata),
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
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b[:])
}
