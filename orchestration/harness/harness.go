package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/session"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

// Config contains the reusable runtime pieces a harness composes.
type Config struct {
	Commands          *command.Registry
	Operations        *operation.Registry
	OperationExecutor operationruntime.Executor
	Events            coreevent.Sink
	ThreadStore       corethread.Store
}

// Service is the channel-facing use-case facade over sessions.
type Service struct {
	commands          *command.Registry
	operations        *operation.Registry
	operationExecutor operationruntime.Executor
	events            coreevent.Sink
	threadStore       corethread.Store

	bindMu      sync.Mutex
	mu          sync.Mutex
	bindings    map[bindingKey]corethread.Ref
	subscribers map[corethread.ID]map[int]chan channel.Outbound
	nextSub     int
}

// New returns a harness service.
func New(cfg Config) *Service {
	return &Service{
		commands:          cfg.Commands,
		operations:        cfg.Operations,
		operationExecutor: cfg.OperationExecutor,
		events:            cfg.Events,
		threadStore:       cfg.ThreadStore,
		bindings:          map[bindingKey]corethread.Ref{},
		subscribers:       map[corethread.ID]map[int]chan channel.Outbound{},
	}
}

// OpenSessionRequest describes an explicit channel/session binding request.
type OpenSessionRequest struct {
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
	ref, resumed, err := s.resolveThread(ctx, req)
	if err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{
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

	switch inbound.Kind {
	case channel.InboundCommand:
		return s.handleCommand(ctx, info, inbound)
	default:
		return InboundResult{Session: info}, fmt.Errorf("harness: inbound kind %q is not executable yet", inbound.Kind)
	}
}

// Subscribe returns outbound events produced by a session thread. Slow
// subscribers may drop events; durable replay belongs to event/thread stores.
func (s *Service) Subscribe(threadID corethread.ID, buffer int) (<-chan channel.Outbound, func()) {
	if buffer < 0 {
		buffer = 0
	}
	ch := make(chan channel.Outbound, buffer)
	if s == nil || threadID == "" {
		close(ch)
		return ch, func() {}
	}
	s.mu.Lock()
	if s.subscribers == nil {
		s.subscribers = map[corethread.ID]map[int]chan channel.Outbound{}
	}
	if s.subscribers[threadID] == nil {
		s.subscribers[threadID] = map[int]chan channel.Outbound{}
	}
	id := s.nextSub
	s.nextSub++
	s.subscribers[threadID][id] = ch
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		if subs := s.subscribers[threadID]; subs != nil {
			if sub, ok := subs[id]; ok {
				delete(subs, id)
				close(sub)
			}
			if len(subs) == 0 {
				delete(s.subscribers, threadID)
			}
		}
		s.mu.Unlock()
	}
	return ch, cancel
}

func (s *Service) handleCommand(ctx context.Context, info SessionInfo, inbound channel.Inbound) (InboundResult, error) {
	exec := session.Session{
		Commands:          s.commands,
		Operations:        s.operations,
		OperationExecutor: s.operationExecutor,
		Events:            s.events,
		ThreadStore:       s.threadStore,
		Thread:            info.Thread,
	}
	result := exec.ExecuteInboundCommand(ctx, inbound)
	outbound := commandOutbound(inbound, result)
	if outbound != nil {
		s.publish(info.Thread.ID, *outbound)
	}
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

func (s *Service) resolveThread(ctx context.Context, req OpenSessionRequest) (corethread.Ref, bool, error) {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()

	ref := corethread.Ref{ID: req.ThreadID, BranchID: corethread.MainBranch}
	if ref.ID == "" {
		ref.ID = s.boundThread(req.Channel, req.Conversation)
	}
	if ref.ID == "" {
		ref.ID = corethread.ID(newID("thread_"))
	}

	key := makeBindingKey(req.Channel, req.Conversation)
	if existing := s.boundThread(req.Channel, req.Conversation); existing != "" && existing == ref.ID {
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

func (s *Service) boundThread(ch channel.Ref, conv channel.ConversationRef) corethread.ID {
	key := makeBindingKey(ch, conv)
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

func (s *Service) publish(threadID corethread.ID, outbound channel.Outbound) {
	if s == nil || threadID == "" {
		return
	}
	s.mu.Lock()
	subs := make([]chan channel.Outbound, 0, len(s.subscribers[threadID]))
	for _, ch := range s.subscribers[threadID] {
		subs = append(subs, ch)
	}
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- outbound:
		default:
		}
	}
}

type bindingKey struct {
	channel      channel.Name
	conversation string
}

func makeBindingKey(ch channel.Ref, conv channel.ConversationRef) bindingKey {
	return bindingKey{channel: ch.Name, conversation: conv.ID}
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
