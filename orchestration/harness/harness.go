package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/session"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

// Config contains the reusable runtime pieces a harness composes.
type Config struct {
	Agent             agent.Agent
	Commands          *command.Registry
	Operations        *operation.Registry
	Resolver          *resource.Resolver
	CommandCatalog    session.CommandCatalog
	OperationCatalog  session.OperationCatalog
	SessionCatalog    session.SessionCatalog
	OperationExecutor operationruntime.Executor
	Events            coreevent.Sink
	ThreadStore       corethread.Store
}

// Service is the channel-facing use-case facade over sessions.
type Service struct {
	agent             agent.Agent
	commands          *command.Registry
	operations        *operation.Registry
	resolver          *resource.Resolver
	commandCatalog    session.CommandCatalog
	operationCatalog  session.OperationCatalog
	sessionCatalog    session.SessionCatalog
	operationExecutor operationruntime.Executor
	events            coreevent.Sink
	threadStore       corethread.Store

	bindMu      sync.Mutex
	mu          sync.Mutex
	bindings    map[bindingKey]corethread.Ref
	subscribers map[corethread.ID]map[int]*subscriber
	nextSub     int
}

// New returns a harness service.
func New(cfg Config) *Service {
	return &Service{
		agent:             cfg.Agent,
		commands:          cfg.Commands,
		operations:        cfg.Operations,
		resolver:          cfg.Resolver,
		commandCatalog:    cfg.CommandCatalog,
		operationCatalog:  cfg.OperationCatalog,
		sessionCatalog:    cfg.SessionCatalog,
		operationExecutor: cfg.OperationExecutor,
		events:            cfg.Events,
		threadStore:       cfg.ThreadStore,
		bindings:          map[bindingKey]corethread.Ref{},
		subscribers:       map[corethread.ID]map[int]*subscriber{},
	}
}

// OpenSessionRequest describes an explicit channel/session binding request.
type OpenSessionRequest struct {
	Session      coresession.Ref
	Channel      channel.Ref
	Conversation channel.ConversationRef
	ThreadID     corethread.ID
	Metadata     map[string]string
}

// ListSessionsRequest filters harness session bindings.
type ListSessionsRequest struct {
	Channel         channel.Ref
	IncludeArchived bool
	Limit           int
}

// SessionInfo is the stable identity returned after opening or resolving a
// channel-bound session.
type SessionInfo struct {
	Session      coresession.Ref         `json:"session,omitempty"`
	Thread       corethread.Ref          `json:"thread"`
	Channel      channel.Ref             `json:"channel,omitempty"`
	Conversation channel.ConversationRef `json:"conversation,omitempty"`
	Metadata     map[string]string       `json:"metadata,omitempty"`
	Resumed      bool                    `json:"resumed,omitempty"`
}

// OpenSession resolves or creates the thread/session for a channel
// conversation.
func (s *Service) OpenSession(ctx context.Context, req OpenSessionRequest) (SessionInfo, error) {
	if s == nil {
		return SessionInfo{}, fmt.Errorf("harness: service is nil")
	}
	req, err := s.applySessionSpec(req)
	if err != nil {
		return SessionInfo{}, err
	}
	ref, resumed, err := s.resolveThread(ctx, req)
	if err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{
		Session:      req.Session,
		Thread:       ref,
		Channel:      req.Channel,
		Conversation: req.Conversation,
		Metadata:     cloneStringMap(req.Metadata),
		Resumed:      resumed,
	}, nil
}

// ListSessions returns currently known session bindings for a channel.
func (s *Service) ListSessions(_ context.Context, req ListSessionsRequest) ([]SessionInfo, error) {
	if s == nil {
		return nil, fmt.Errorf("harness: service is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionInfo, 0, len(s.bindings))
	for key, ref := range s.bindings {
		if req.Channel.Name != "" && key.channel != req.Channel.Name {
			continue
		}
		out = append(out, SessionInfo{
			Session:      coresession.Ref{Name: coresession.Name(key.session)},
			Thread:       ref,
			Channel:      channel.Ref{Name: key.channel},
			Conversation: channel.ConversationRef{ID: key.conversation},
		})
		if req.Limit > 0 && len(out) >= req.Limit {
			break
		}
	}
	return out, nil
}

// InboundResult is the result of handling one normalized channel input.
type InboundResult struct {
	Session  SessionInfo
	Input    session.InputResult
	Command  session.CommandResult
	Outbound *channel.Outbound
}

// HandleInbound resolves the target session for an inbound channel envelope and
// delegates execution to the session orchestrator.
func (s *Service) HandleInbound(ctx context.Context, inbound channel.Inbound) (InboundResult, error) {
	if s == nil {
		return InboundResult{}, fmt.Errorf("harness: service is nil")
	}
	if err := inbound.Validate(); err != nil {
		return InboundResult{}, err
	}
	info, err := s.OpenSession(ctx, OpenSessionRequest{
		Channel:      inbound.Channel,
		Conversation: inbound.Conversation,
	})
	if err != nil {
		return InboundResult{}, err
	}
	return s.HandleSessionInbound(ctx, info, inbound)
}

// HandleSessionInbound dispatches one normalized inbound envelope against an
// already resolved session/thread. This is the execution boundary channel
// clients should use after Open/Resume.
func (s *Service) HandleSessionInbound(ctx context.Context, info SessionInfo, inbound channel.Inbound) (InboundResult, error) {
	if s == nil {
		return InboundResult{}, fmt.Errorf("harness: service is nil")
	}
	if info.Thread.ID == "" {
		return InboundResult{}, fmt.Errorf("harness: session thread id is empty")
	}
	if info.Thread.BranchID == "" {
		info.Thread.BranchID = corethread.MainBranch
	}
	normalized, err := normalizeSessionInbound(info, inbound)
	if err != nil {
		return InboundResult{}, err
	}
	info = normalizeSessionInfo(info, normalized)
	if err := normalized.Validate(); err != nil {
		return InboundResult{}, err
	}
	if _, err := s.ensureThread(ctx, info.Thread.ID, info.Metadata); err != nil {
		return InboundResult{}, err
	}

	switch normalized.Kind {
	case channel.InboundMessage:
		return s.handleInput(ctx, info, normalized)
	case channel.InboundCommand:
		return s.handleCommand(ctx, info, normalized)
	default:
		return InboundResult{Session: info}, fmt.Errorf("harness: inbound kind %q is not executable yet", normalized.Kind)
	}
}

// Subscribe returns semantic events produced by a session thread. Slow
// subscribers may drop events; durable replay belongs to event/thread stores.
func (s *Service) Subscribe(ctx context.Context, threadID corethread.ID, opts clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	if opts.Buffer < 0 {
		opts.Buffer = 0
	}
	if s == nil || threadID == "" {
		ch := make(chan clientapi.Event)
		close(ch)
		return ch, func() {}, nil
	}
	replayed, err := s.replayEvents(ctx, threadID, opts)
	if err != nil {
		return nil, nil, err
	}
	ch := make(chan clientapi.Event, opts.Buffer+len(replayed))
	for _, event := range replayed {
		ch <- event
	}
	s.mu.Lock()
	if s.subscribers == nil {
		s.subscribers = map[corethread.ID]map[int]*subscriber{}
	}
	if s.subscribers[threadID] == nil {
		s.subscribers[threadID] = map[int]*subscriber{}
	}
	id := s.nextSub
	s.nextSub++
	sub := &subscriber{ch: ch}
	s.subscribers[threadID][id] = sub
	s.mu.Unlock()

	cancel := func() {
		var removed *subscriber
		s.mu.Lock()
		if subs := s.subscribers[threadID]; subs != nil {
			if existing, ok := subs[id]; ok {
				delete(subs, id)
				removed = existing
			}
			if len(subs) == 0 {
				delete(s.subscribers, threadID)
			}
		}
		s.mu.Unlock()
		if removed != nil {
			removed.close()
		}
	}
	return ch, cancel, nil
}

func (s *Service) handleInput(ctx context.Context, info SessionInfo, inbound channel.Inbound) (InboundResult, error) {
	runID := clientapi.RunID(inbound.ID)
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:       clientapi.EventSubmissionReceived,
		RunID:      runID,
		Session:    toClientSessionInfo(info),
		Submission: submissionForInbound(normalizedSubmissionInput, runID, inbound),
	})
	exec := session.Session{
		Agent:             s.agent,
		Commands:          s.commands,
		Operations:        s.operations,
		Resolver:          s.resolver,
		CommandCatalog:    s.commandCatalog,
		OperationCatalog:  s.operationCatalog,
		OperationExecutor: s.operationExecutor,
		Events:            s.events,
		ThreadStore:       s.threadStore,
		Thread:            info.Thread,
	}
	result := exec.ExecuteInboundInput(ctx, inbound)
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:    clientapi.EventInputCompleted,
		RunID:   runID,
		Session: toClientSessionInfo(info),
		Input:   &result,
	})
	if result.Outbound != nil {
		s.publish(info.Thread.ID, clientapi.Event{
			Kind:     clientapi.EventOutboundProduced,
			RunID:    runID,
			Session:  toClientSessionInfo(info),
			Outbound: result.Outbound,
		})
	}
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:    clientapi.EventRunCompleted,
		RunID:   runID,
		Session: toClientSessionInfo(info),
	})
	return InboundResult{Session: info, Input: result, Outbound: result.Outbound}, nil
}

func (s *Service) handleCommand(ctx context.Context, info SessionInfo, inbound channel.Inbound) (InboundResult, error) {
	runID := clientapi.RunID(inbound.ID)
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:       clientapi.EventSubmissionReceived,
		RunID:      runID,
		Session:    toClientSessionInfo(info),
		Submission: submissionForInbound(normalizedSubmissionCommand, runID, inbound),
	})
	exec := session.Session{
		Agent:             s.agent,
		Commands:          s.commands,
		Operations:        s.operations,
		Resolver:          s.resolver,
		CommandCatalog:    s.commandCatalog,
		OperationCatalog:  s.operationCatalog,
		OperationExecutor: s.operationExecutor,
		Events:            s.events,
		ThreadStore:       s.threadStore,
		Thread:            info.Thread,
	}
	result := exec.ExecuteInboundCommand(ctx, inbound)
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:    clientapi.EventCommandCompleted,
		RunID:   runID,
		Session: toClientSessionInfo(info),
		Command: &result,
	})
	outbound := commandOutbound(inbound, result)
	if outbound != nil {
		s.publish(info.Thread.ID, clientapi.Event{
			Kind:     clientapi.EventOutboundProduced,
			RunID:    runID,
			Session:  toClientSessionInfo(info),
			Outbound: outbound,
		})
	}
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:    clientapi.EventRunCompleted,
		RunID:   runID,
		Session: toClientSessionInfo(info),
	})
	return InboundResult{Session: info, Command: result, Outbound: outbound}, nil
}

func commandOutbound(inbound channel.Inbound, result session.CommandResult) *channel.Outbound {
	out := channel.Outbound{
		Channel:       inbound.Channel,
		Conversation:  inbound.Conversation,
		CorrelationID: inbound.CorrelationID,
		CausationID:   inbound.ID,
		Kind:          channel.OutboundMessage,
	}
	switch {
	case result.Effect != nil:
		content := result.Effect.Result.Output
		if result.Effect.Result.IsError() && result.Effect.Result.Error != nil {
			content = result.Effect.Result.Error.Message
		}
		out.Message = &channel.Message{Content: content}
	case result.Error != nil:
		out.Message = &channel.Message{Content: result.Error.Message}
	default:
		out.Message = &channel.Message{Content: string(result.Status)}
	}
	return &out
}

type normalizedSubmissionKind int

const (
	normalizedSubmissionInput normalizedSubmissionKind = iota
	normalizedSubmissionCommand
)

func submissionForInbound(kind normalizedSubmissionKind, runID clientapi.RunID, inbound channel.Inbound) *clientapi.Submission {
	submission := clientapi.Submission{
		ID:     runID,
		Caller: inbound.Caller,
		Trust:  inbound.Trust,
	}
	switch kind {
	case normalizedSubmissionInput:
		submission.Kind = clientapi.SubmissionInput
		if inbound.Message != nil {
			submission.Input = &clientapi.Input{
				Content:  inbound.Message.Content,
				Metadata: inbound.Message.Metadata,
			}
		}
	case normalizedSubmissionCommand:
		submission.Kind = clientapi.SubmissionCommand
		submission.Command = inbound.Command
	}
	return &submission
}

func normalizeSessionInbound(info SessionInfo, inbound channel.Inbound) (channel.Inbound, error) {
	if inbound.Channel.Name == "" {
		inbound.Channel = info.Channel
	} else if info.Channel.Name != "" && inbound.Channel.Name != info.Channel.Name {
		return channel.Inbound{}, fmt.Errorf("harness: inbound channel %q does not match session channel %q", inbound.Channel.Name, info.Channel.Name)
	}
	if inbound.Conversation.ID == "" {
		inbound.Conversation = info.Conversation
	} else if info.Conversation.ID != "" && inbound.Conversation.ID != info.Conversation.ID {
		return channel.Inbound{}, fmt.Errorf("harness: inbound conversation %q does not match session conversation %q", inbound.Conversation.ID, info.Conversation.ID)
	}
	return inbound, nil
}

func normalizeSessionInfo(info SessionInfo, inbound channel.Inbound) SessionInfo {
	if info.Channel.Name == "" {
		info.Channel = inbound.Channel
	}
	if info.Conversation.ID == "" {
		info.Conversation = inbound.Conversation
	}
	return info
}

func (s *Service) applySessionSpec(req OpenSessionRequest) (OpenSessionRequest, error) {
	if req.Session.Name == "" {
		return req, nil
	}
	binding, err := s.sessionCatalog.Resolve(string(req.Session.Name))
	if err != nil {
		return OpenSessionRequest{}, fmt.Errorf("harness: configured session %q: %w", req.Session.Name, err)
	}
	spec := binding.Spec
	req.Session = coresession.Ref{Name: coresession.Name(binding.ID.Address())}
	if req.Channel.Name == "" {
		req.Channel = spec.Channel
	}
	if req.Conversation.ID == "" {
		req.Conversation = spec.Conversation
	}
	if len(spec.Metadata) > 0 {
		merged := cloneStringMap(spec.Metadata)
		for k, v := range req.Metadata {
			merged[k] = v
		}
		req.Metadata = merged
	}
	return req, nil
}

func (s *Service) resolveThread(ctx context.Context, req OpenSessionRequest) (corethread.Ref, bool, error) {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()

	ref := corethread.Ref{ID: req.ThreadID, BranchID: corethread.MainBranch}
	if ref.ID == "" {
		ref.ID = s.boundThread(req.Session, req.Channel, req.Conversation)
	}
	if ref.ID == "" {
		ref.ID = corethread.ID(newID("thread_"))
	}

	key := makeBindingKey(req.Session, req.Channel, req.Conversation)
	if existing := s.boundThread(req.Session, req.Channel, req.Conversation); existing != "" && existing == ref.ID {
		return corethread.Ref{ID: existing, BranchID: corethread.MainBranch}, true, nil
	}

	resumed, err := s.ensureThread(ctx, ref.ID, req.Metadata)
	if err != nil {
		return corethread.Ref{}, false, err
	}

	if key.valid() {
		s.mu.Lock()
		s.bindings[key] = ref
		s.mu.Unlock()
	}
	return ref, resumed, nil
}

func (s *Service) boundThread(sess coresession.Ref, ch channel.Ref, conv channel.ConversationRef) corethread.ID {
	key := makeBindingKey(sess, ch, conv)
	if !key.valid() || s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindings[key].ID
}

func (s *Service) ensureThread(ctx context.Context, id corethread.ID, metadata map[string]string) (bool, error) {
	if s.threadStore == nil {
		return false, nil
	}
	if _, err := s.threadStore.Read(ctx, corethread.ReadParams{ID: id}); err == nil {
		return true, nil
	} else if !errors.Is(err, corethread.ErrNotFound) {
		return false, err
	}
	if _, err := s.threadStore.Create(ctx, corethread.CreateParams{ID: id, Metadata: metadata}); err != nil {
		if errors.Is(err, corethread.ErrAlreadyExists) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func (s *Service) publish(threadID corethread.ID, event clientapi.Event) {
	if s == nil || threadID == "" {
		return
	}
	s.mu.Lock()
	subs := make([]*subscriber, 0, len(s.subscribers[threadID]))
	for _, sub := range s.subscribers[threadID] {
		subs = append(subs, sub)
	}
	s.mu.Unlock()
	for _, sub := range subs {
		sub.send(event)
	}
}

type subscriber struct {
	mu     sync.Mutex
	ch     chan clientapi.Event
	closed bool
}

func (s *subscriber) send(event clientapi.Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- event:
	default:
	}
}

func (s *subscriber) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

func (s *Service) replayEvents(ctx context.Context, threadID corethread.ID, opts clientapi.EventOptions) ([]clientapi.Event, error) {
	if !opts.Replay && opts.After.Sequence == 0 {
		return nil, nil
	}
	if s.threadStore == nil {
		return nil, nil
	}
	snapshot, err := s.threadStore.Read(ctx, corethread.ReadParams{ID: threadID})
	if err != nil {
		return nil, err
	}
	records, err := snapshot.EventsForBranch(snapshot.BranchID)
	if err != nil {
		return nil, err
	}
	info := s.sessionInfoForThread(threadID)
	if info.Thread.ID == "" {
		info.Thread = corethread.Ref{ID: threadID, BranchID: snapshot.BranchID}
	}
	var out []clientapi.Event
	for _, record := range records {
		if record.Sequence <= opts.After.Sequence {
			continue
		}
		events := recordToClientEvents(info, record)
		out = append(out, events...)
	}
	return out, nil
}

func (s *Service) sessionInfoForThread(threadID corethread.ID) clientapi.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, ref := range s.bindings {
		if ref.ID == threadID {
			return clientapi.SessionInfo{
				Session:      coresession.Ref{Name: coresession.Name(key.session)},
				Thread:       ref,
				Channel:      channel.Ref{Name: key.channel},
				Conversation: channel.ConversationRef{ID: key.conversation},
			}
		}
	}
	return clientapi.SessionInfo{}
}

func recordToClientEvents(info clientapi.SessionInfo, record corethread.Record) []clientapi.Event {
	base := clientapi.Event{
		Cursor:   clientapi.EventCursor{Sequence: record.Sequence},
		Replayed: true,
		Session:  info,
	}
	switch payload := record.Event.Payload.(type) {
	case coresession.InputReceived:
		return []clientapi.Event{withSubmission(base, clientapi.Submission{
			ID:     clientapi.RunID(payload.RunID),
			Kind:   clientapi.SubmissionInput,
			Input:  &clientapi.Input{Content: payload.Message.Content, Metadata: payload.Message.Metadata},
			Caller: payload.Caller,
			Trust:  payload.Trust,
		})}
	case coresession.CommandReceived:
		return []clientapi.Event{withSubmission(base, clientapi.Submission{
			ID:      clientapi.RunID(payload.RunID),
			Kind:    clientapi.SubmissionCommand,
			Command: &payload.Command,
			Caller:  payload.Caller,
			Trust:   payload.Trust,
		})}
	case coresession.CommandRejected:
		event := base
		event.Kind = clientapi.EventCommandCompleted
		event.RunID = clientapi.RunID(payload.RunID)
		event.Command = &session.CommandResult{
			Status: session.CommandStatusRejected,
			Error:  &session.CommandError{Code: "command_rejected", Message: payload.Reason},
		}
		return []clientapi.Event{event}
	case coresession.AgentStepCompleted:
		event := base
		event.Kind = clientapi.EventAgentStepCompleted
		event.RunID = clientapi.RunID(payload.RunID)
		event.Agent = &payload.Result
		return []clientapi.Event{event}
	case coresession.OperationCompleted:
		event := base
		event.Kind = clientapi.EventOperationCompleted
		event.RunID = clientapi.RunID(payload.RunID)
		event.Operation = &clientapi.OperationEvent{Operation: payload.Operation, Result: payload.Result}
		return []clientapi.Event{event}
	case coresession.OutboundProduced:
		event := base
		event.Kind = clientapi.EventOutboundProduced
		event.RunID = clientapi.RunID(payload.RunID)
		event.Outbound = &channel.Outbound{
			Channel:      info.Channel,
			Conversation: info.Conversation,
			Kind:         channel.OutboundMessage,
			Message:      &payload.Message,
		}
		return []clientapi.Event{event}
	default:
		return nil
	}
}

func withSubmission(base clientapi.Event, submission clientapi.Submission) clientapi.Event {
	base.Kind = clientapi.EventSubmissionReceived
	base.RunID = submission.ID
	base.Submission = &submission
	return base
}

func toClientSessionInfo(info SessionInfo) clientapi.SessionInfo {
	return clientapi.SessionInfo{
		Session:      info.Session,
		Thread:       info.Thread,
		Channel:      info.Channel,
		Conversation: info.Conversation,
		Metadata:     cloneStringMap(info.Metadata),
		Resumed:      info.Resumed,
	}
}

type bindingKey struct {
	session      coresession.Name
	channel      channel.Name
	conversation string
}

func makeBindingKey(sess coresession.Ref, ch channel.Ref, conv channel.ConversationRef) bindingKey {
	return bindingKey{session: sess.Name, channel: ch.Name, conversation: conv.ID}
}

func (k bindingKey) valid() bool {
	return k.channel != "" && k.conversation != ""
}

func newID(prefix string) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return prefix + "unknown"
	}
	return prefix + hex.EncodeToString(raw[:])
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
