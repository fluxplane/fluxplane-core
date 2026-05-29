package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	coreactivation "github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/orchestration/agentconfig"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-core/orchestration/identity"
	"github.com/fluxplane/fluxplane-core/orchestration/security"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionagent"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionrun"
	"github.com/fluxplane/fluxplane-core/orchestration/toolprojection"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	coreevent "github.com/fluxplane/fluxplane-event"
)

// Config contains the reusable runtime pieces a harness composes.
type Config struct {
	Agent                agent.Agent
	AgentProvider        AgentProvider
	Commands             *command.Registry
	Operations           *operation.Registry
	Resolver             *resource.Resolver
	CommandCatalog       session.CommandCatalog
	SessionCommands      session.SessionCommandCatalog
	OperationCatalog     session.OperationCatalog
	ActivationSets       []coreactivation.Set
	OperationSets        []operation.Set
	Datasources          []coredatasource.Spec
	PostEditChecks       []coresession.PostEditCheckSpec
	ContextProviders     []corecontext.Provider
	WorkflowCatalog      session.WorkflowCatalog
	SessionCatalog       session.SessionCatalog
	OperationExecutor    operationruntime.Executor
	Events               coreevent.Sink
	ThreadStore          corethread.Store
	SessionAgents        *sessionagent.Runner
	SessionRuns          *sessionrun.Runner
	EnvironmentObservers []runtimeevidence.Observer
	AssertionDerivers    []runtimeevidence.AssertionDeriver
	ReactionRules        []corereaction.Rule
	StopEvaluator        session.StopEvaluator
	IdentityResolver     identity.Resolver
	ExternalIdentity     identity.ExternalResolver
	ToolSetCatalog       session.ToolSetCatalog
	ToolProjection       toolprojection.Config

	// SessionToolProjection controls how activated operations are projected
	// to the LLM by each spawned session. Default ("") preserves legacy
	// behavior: activated ops land in the request tools list. Set to
	// session.ToolProjectionContextBlocksOnly to keep the tools list
	// stable across activations and rely on the surface schema context
	// provider + surface_call dispatch.
	SessionToolProjection session.ToolProjectionMode

	Security      policy.AuthorizationPolicy
	SecurityTrace bool
}

// AgentProvider resolves configured session profiles to runnable agents.
type AgentProvider interface {
	AgentForSession(context.Context, coresession.Spec) (agent.Agent, error)
}

// Service is the channel-facing use-case facade over sessions.
type Service struct {
	agent                 agent.Agent
	agentProvider         AgentProvider
	commands              *command.Registry
	operations            *operation.Registry
	resolver              *resource.Resolver
	commandCatalog        session.CommandCatalog
	sessionCommands       session.SessionCommandCatalog
	operationCatalog      session.OperationCatalog
	activationSets        []coreactivation.Set
	operationSets         []operation.Set
	datasources           []coredatasource.Spec
	postEditChecks        []coresession.PostEditCheckSpec
	contextProviders      []corecontext.Provider
	workflowCatalog       session.WorkflowCatalog
	sessionCatalog        session.SessionCatalog
	operationExecutor     operationruntime.Executor
	events                coreevent.Sink
	threadStore           corethread.Store
	sessionAgents         *sessionagent.Runner
	sessionRuns           *sessionrun.Runner
	startupObservations   []coreevidence.Observation
	startupAssertions     []coreevidence.Assertion
	environmentObservers  []runtimeevidence.Observer
	assertionDerivers     []runtimeevidence.AssertionDeriver
	reactionRules         []corereaction.Rule
	stopEvaluator         session.StopEvaluator
	identityResolver      identity.Resolver
	externalIdentity      identity.ExternalResolver
	toolSetCatalog        session.ToolSetCatalog
	toolProjection        toolprojection.Config
	sessionToolProjection session.ToolProjectionMode
	security              policy.AuthorizationPolicy
	securityTrace         bool

	bindMu      sync.Mutex
	mu          sync.Mutex
	bindings    map[bindingKey]corethread.Ref
	profiles    map[corethread.ID]coresession.Spec
	approvers   map[corethread.ID]operationruntime.ApprovalGate
	subscribers map[corethread.ID]map[int]*subscriber
	allSubs     map[int]*subscriber
	nextSub     int
}

// New returns a harness service.
func New(cfg Config) *Service {
	startupObservations, startupAssertions := startupEnvironment(cfg.EnvironmentObservers, cfg.AssertionDerivers)
	return &Service{
		agent:                 cfg.Agent,
		agentProvider:         cfg.AgentProvider,
		commands:              cfg.Commands,
		operations:            cfg.Operations,
		resolver:              cfg.Resolver,
		commandCatalog:        cfg.CommandCatalog,
		sessionCommands:       cfg.SessionCommands,
		operationCatalog:      cfg.OperationCatalog,
		activationSets:        append([]coreactivation.Set(nil), cfg.ActivationSets...),
		operationSets:         append([]operation.Set(nil), cfg.OperationSets...),
		datasources:           append([]coredatasource.Spec(nil), cfg.Datasources...),
		postEditChecks:        append([]coresession.PostEditCheckSpec(nil), cfg.PostEditChecks...),
		contextProviders:      append([]corecontext.Provider(nil), cfg.ContextProviders...),
		workflowCatalog:       cfg.WorkflowCatalog,
		sessionCatalog:        cfg.SessionCatalog,
		operationExecutor:     cfg.OperationExecutor,
		events:                cfg.Events,
		threadStore:           cfg.ThreadStore,
		sessionAgents:         cfg.SessionAgents,
		sessionRuns:           cfg.SessionRuns,
		startupObservations:   startupObservations,
		startupAssertions:     startupAssertions,
		environmentObservers:  append([]runtimeevidence.Observer(nil), cfg.EnvironmentObservers...),
		assertionDerivers:     append([]runtimeevidence.AssertionDeriver(nil), cfg.AssertionDerivers...),
		reactionRules:         append([]corereaction.Rule(nil), cfg.ReactionRules...),
		stopEvaluator:         cfg.StopEvaluator,
		identityResolver:      cfg.IdentityResolver,
		externalIdentity:      cfg.ExternalIdentity,
		toolSetCatalog:        cfg.ToolSetCatalog,
		toolProjection:        cfg.ToolProjection,
		sessionToolProjection: cfg.SessionToolProjection,
		security:              cfg.Security,
		securityTrace:         cfg.SecurityTrace,
		bindings:              map[bindingKey]corethread.Ref{},
		profiles:              map[corethread.ID]coresession.Spec{},
		subscribers:           map[corethread.ID]map[int]*subscriber{},
		allSubs:               map[int]*subscriber{},
	}
}

func startupEnvironment(observers []runtimeevidence.Observer, derivers []runtimeevidence.AssertionDeriver) ([]coreevidence.Observation, []coreevidence.Assertion) {
	observations, _ := runtimeevidence.RunObservers(context.Background(), observers, runtimeevidence.ObservationRequest{
		Phase: coreevidence.PhaseStartup,
	})
	assertions, _ := runtimeevidence.DeriveAssertions(context.Background(), derivers, runtimeevidence.AssertionDeriveRequest{
		Observations: append([]coreevidence.Observation(nil), observations...),
	})
	return observations, assertions
}

// SetSessionAgentRunner installs the command helper session runner used by
// session-targeted commands such as /task and /plan. It is set after
// channel-client construction so helper sessions enter through the same public
// session path as user sessions.
func (s *Service) SetSessionAgentRunner(runner *sessionagent.Runner) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sessionAgents = runner
	s.mu.Unlock()
}

// SetSessionRunRunner installs the generic helper session runner used by
// session commands that repeatedly or directly submit prompts to fresh sessions.
func (s *Service) SetSessionRunRunner(runner *sessionrun.Runner) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sessionRuns = runner
	s.mu.Unlock()
}

// OpenSessionRequest describes an explicit channel/session binding request.
type OpenSessionRequest struct {
	Session      coresession.Ref
	Profile      coresession.Spec
	Channel      channel.Ref
	Conversation channel.ConversationRef
	ThreadID     corethread.ID
	Metadata     map[string]string
	// Approver overrides the executor's approval gate for this session. It is
	// used by session-agent runners to propagate the parent's approval policy
	// (e.g. AutoApprover for --yolo) into helper sessions.
	Approver operationruntime.ApprovalGate
}

// ResumeSessionRequest describes a read-only thread resume lookup.
type ResumeSessionRequest struct {
	Channel  channel.Ref
	ThreadID corethread.ID
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
	Archived     bool                    `json:"archived,omitempty"`
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
	if req.Profile.Name != "" {
		s.bindProfile(ref.ID, req.Profile)
	}
	if req.Approver != nil {
		s.bindApprover(ref.ID, req.Approver)
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

// ResumeSession resolves an existing thread without creating a missing one.
func (s *Service) ResumeSession(ctx context.Context, req ResumeSessionRequest) (SessionInfo, error) {
	if s == nil {
		return SessionInfo{}, fmt.Errorf("harness: service is nil")
	}
	if req.ThreadID == "" {
		return SessionInfo{}, fmt.Errorf("harness: resume thread id is empty")
	}
	if info, ok := s.boundSessionInfo(req.ThreadID, req.Channel); ok {
		return info, nil
	}
	if s.threadStore == nil {
		return SessionInfo{}, fmt.Errorf("%w: thread %q", corethread.ErrNotFound, req.ThreadID)
	}
	snapshot, err := s.threadStore.Read(ctx, corethread.ReadParams{ID: req.ThreadID})
	if err != nil {
		return SessionInfo{}, err
	}
	id := snapshot.ID
	if id == "" {
		id = req.ThreadID
	}
	branchID := snapshot.BranchID
	if branchID == "" {
		branchID = corethread.MainBranch
	}
	return SessionInfo{
		Thread:   corethread.Ref{ID: id, BranchID: branchID},
		Channel:  req.Channel,
		Metadata: cloneStringMap(snapshot.Metadata),
		Resumed:  true,
	}, nil
}

// ListSessions returns currently known session bindings plus durable threads.
func (s *Service) ListSessions(ctx context.Context, req ListSessionsRequest) ([]SessionInfo, error) {
	if s == nil {
		return nil, fmt.Errorf("harness: service is nil")
	}
	seen := map[corethread.ID]bool{}
	s.mu.Lock()
	out := make([]SessionInfo, 0, len(s.bindings))
	limitReached := false
	for key, ref := range s.bindings {
		if req.Channel.Name != "" && key.channel != req.Channel.Name {
			continue
		}
		if ref.BranchID == "" {
			ref.BranchID = corethread.MainBranch
		}
		out = append(out, SessionInfo{
			Session:      coresession.Ref{Name: coresession.Name(key.session)},
			Thread:       ref,
			Channel:      channel.Ref{Name: key.channel},
			Conversation: channel.ConversationRef{ID: key.conversation},
		})
		seen[ref.ID] = true
		if req.Limit > 0 && len(out) >= req.Limit {
			limitReached = true
			break
		}
	}
	s.mu.Unlock()
	if limitReached {
		return out, nil
	}
	if s.threadStore == nil {
		return out, nil
	}
	page, err := s.threadStore.List(ctx, corethread.ListParams{IncludeArchived: req.IncludeArchived})
	if err != nil {
		return nil, err
	}
	for _, snapshot := range page.Threads {
		if snapshot.ID == "" || seen[snapshot.ID] {
			continue
		}
		branchID := snapshot.BranchID
		if branchID == "" {
			branchID = corethread.MainBranch
		}
		out = append(out, SessionInfo{
			Thread:   corethread.Ref{ID: snapshot.ID, BranchID: branchID},
			Channel:  req.Channel,
			Metadata: cloneStringMap(snapshot.Metadata),
			Archived: snapshot.Archived,
		})
		seen[snapshot.ID] = true
		if req.Limit > 0 && len(out) >= req.Limit {
			break
		}
	}
	return out, nil
}

// InboundResult is the result of handling one normalized channel input.
type InboundResult struct {
	Session   SessionInfo
	Input     session.InputResult
	Command   session.CommandResult
	Trigger   session.TriggerResult
	Operation session.OperationResult
	Outbound  *channel.Outbound
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
	normalized, err = s.resolveInboundIdentity(ctx, normalized)
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
	case channel.InboundOperation:
		return s.handleOperation(ctx, info, normalized)
	case channel.InboundTrigger:
		return s.handleTrigger(ctx, info, normalized)
	default:
		return InboundResult{Session: info}, fmt.Errorf("harness: inbound kind %q is not executable yet", normalized.Kind)
	}
}

func (s *Service) resolveInboundIdentity(ctx context.Context, inbound channel.Inbound) (channel.Inbound, error) {
	resolver := s.identityResolver
	if resolver == nil {
		resolver = identity.DefaultResolver{}
	}
	resolved, err := resolver.ResolveIdentity(ctx, identity.Request{Inbound: inbound})
	if err != nil {
		return channel.Inbound{}, err
	}
	inbound.Caller = resolved.Caller
	inbound.Trust = resolved.Trust
	actor := identity.EnrichActor(ctx, resolved.Actor, s.externalIdentity)
	inbound.Actor = &actor
	return inbound, nil
}

// Subscribe returns semantic events produced by a session thread. Active
// subscribers are lossless and apply backpressure instead of silently dropping
// events; durable replay belongs to event/thread stores.
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
	sub := newSubscriber(opts.Buffer+len(replayed), opts.Buffer)
	ch := sub.ch
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
	s.subscribers[threadID][id] = sub
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
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
		})
	}
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				cancel()
			case <-sub.done:
			}
		}()
	}
	return ch, cancel, nil
}

// SubscribeAll returns live semantic events produced by all session threads.
// It is intentionally live-only; durable replay remains scoped to a known
// session thread.
func (s *Service) SubscribeAll(ctx context.Context, opts clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	if opts.Buffer < 0 {
		opts.Buffer = 0
	}
	if s == nil {
		ch := make(chan clientapi.Event)
		close(ch)
		return ch, func() {}, nil
	}
	sub := newSubscriber(opts.Buffer, opts.Buffer)
	ch := sub.ch
	s.mu.Lock()
	if s.allSubs == nil {
		s.allSubs = map[int]*subscriber{}
	}
	id := s.nextSub
	s.nextSub++
	s.allSubs[id] = sub
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			var removed *subscriber
			s.mu.Lock()
			if existing, ok := s.allSubs[id]; ok {
				delete(s.allSubs, id)
				removed = existing
			}
			s.mu.Unlock()
			if removed != nil {
				removed.close()
			}
		})
	}
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				cancel()
			case <-sub.done:
			}
		}()
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
	agentInstance, err := s.agentForSession(ctx, info)
	if err != nil {
		return InboundResult{Session: info}, err
	}
	turnTools := s.projectToolsForInbound(agentInstance, inbound)
	profile, _, _ := s.profileForInfo(info)
	runtimeFailures := &runtimeEventPersistenceFailures{}
	exec := session.Session{
		Agent:                agentInstance,
		Profile:              profile,
		Commands:             s.commands,
		Operations:           s.operations,
		Resolver:             s.resolver,
		CommandCatalog:       s.commandCatalog,
		SessionCommands:      s.sessionCommands,
		OperationCatalog:     s.operationCatalog,
		ActivationSets:       append([]coreactivation.Set(nil), s.activationSets...),
		OperationSets:        append([]operation.Set(nil), s.operationSets...),
		Datasources:          append([]coredatasource.Spec(nil), s.datasources...),
		PostEditChecks:       append([]coresession.PostEditCheckSpec(nil), s.postEditChecks...),
		ContextProviders:     append([]corecontext.Provider(nil), s.contextProviders...),
		ToolSetCatalog:       s.toolSetCatalog,
		WorkflowCatalog:      s.workflowCatalog,
		OperationExecutor:    s.executorForInfo(info),
		Events:               s.runtimeEventSinkWithFailures(ctx, info, runID, runtimeFailures),
		ThreadStore:          s.threadStore,
		Thread:               info.Thread,
		SessionAgents:        s.currentSessionAgents(),
		SessionRuns:          s.currentSessionRuns(),
		Delegation:           s.delegationForInfo(info),
		StopEvaluator:        s.stopEvaluator,
		RunID:                string(runID),
		TurnTools:            turnTools,
		StartupObservations:  append([]coreevidence.Observation(nil), s.startupObservations...),
		StartupAssertions:    append([]coreevidence.Assertion(nil), s.startupAssertions...),
		EnvironmentObservers: append([]runtimeevidence.Observer(nil), s.environmentObservers...),
		AssertionDerivers:    append([]runtimeevidence.AssertionDeriver(nil), s.assertionDerivers...),
		ReactionRules:        append([]corereaction.Rule(nil), s.reactionRules...),
		Security:             s.security,
		SecurityTrace:        s.securityTrace,
		ToolProjection:       s.sessionToolProjection,
	}
	result := exec.ExecuteInboundInput(ctx, inbound)
	if err := runtimeFailures.Err(); err != nil {
		return InboundResult{Session: info, Input: result, Outbound: result.Outbound}, err
	}
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

func (s *Service) projectToolsForInbound(agentInstance agent.Agent, inbound channel.Inbound) []tool.Spec {
	if agentInstance == nil {
		return nil
	}
	cfg := s.toolProjection
	cfg.Commands = s.commandCatalog
	cfg.Operations = s.operationCatalog
	cfg.ToolSets = s.toolSetCatalog
	cfg.Caller = inbound.Caller
	cfg.Trust = inbound.Trust
	cfg.Authorization.Policy = s.security
	cfg.Authorization.Subjects = security.SubjectsForInbound(inbound, agentInstance.Spec())
	cfg.Authorization.Trust = inbound.Trust
	cfg.Authorization.TraceAllows = s.securityTrace
	projected := toolprojection.Project(cfg)
	filtered := agentconfig.FilterTools(agentInstance.Spec(), projected.Tools)
	if filtered == nil && (len(s.commandCatalog) > 0 || len(s.operationCatalog) > 0 || len(s.toolSetCatalog) > 0) {
		return []tool.Spec{}
	}
	return filtered
}

func (s *Service) handleCommand(ctx context.Context, info SessionInfo, inbound channel.Inbound) (InboundResult, error) {
	runID := clientapi.RunID(inbound.ID)
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:       clientapi.EventSubmissionReceived,
		RunID:      runID,
		Session:    toClientSessionInfo(info),
		Submission: submissionForInbound(normalizedSubmissionCommand, runID, inbound),
	})
	profile, _, _ := s.profileForInfo(info)
	agentInstance := s.agent
	var commandPath command.Path
	if inbound.Command != nil {
		commandPath = inbound.Command.Path
	} else if parsed, err := session.ParseCommandLine(inbound.CommandLine, s.commands, s.commandCatalog, s.sessionCommands); err == nil {
		inbound.Command = &parsed
		inbound.CommandLine = ""
		commandPath = parsed.Path
	}
	targetsSession := false
	if len(commandPath) > 0 {
		var err error
		targetsSession, err = session.CommandTargetsSession(commandPath, s.resolver, s.commandCatalog, s.sessionCommands, s.commands)
		if err != nil {
			targetsSession = false
		}
	}
	if targetsSession {
		var err error
		agentInstance, err = s.agentForSession(ctx, info)
		if err != nil {
			return InboundResult{Session: info}, err
		}
	}
	turnTools := s.projectToolsForInbound(agentInstance, inbound)
	runtimeFailures := &runtimeEventPersistenceFailures{}
	exec := session.Session{
		Agent:                agentInstance,
		Profile:              profile,
		Commands:             s.commands,
		Operations:           s.operations,
		Resolver:             s.resolver,
		CommandCatalog:       s.commandCatalog,
		SessionCommands:      s.sessionCommands,
		OperationCatalog:     s.operationCatalog,
		ActivationSets:       append([]coreactivation.Set(nil), s.activationSets...),
		OperationSets:        append([]operation.Set(nil), s.operationSets...),
		Datasources:          append([]coredatasource.Spec(nil), s.datasources...),
		PostEditChecks:       append([]coresession.PostEditCheckSpec(nil), s.postEditChecks...),
		ContextProviders:     append([]corecontext.Provider(nil), s.contextProviders...),
		WorkflowCatalog:      s.workflowCatalog,
		OperationExecutor:    s.executorForInfo(info),
		Events:               s.runtimeEventSinkWithFailures(ctx, info, runID, runtimeFailures),
		ThreadStore:          s.threadStore,
		Thread:               info.Thread,
		SessionAgents:        s.currentSessionAgents(),
		SessionRuns:          s.currentSessionRuns(),
		Delegation:           s.delegationForInfo(info),
		StopEvaluator:        s.stopEvaluator,
		RunID:                string(runID),
		TurnTools:            turnTools,
		StartupObservations:  append([]coreevidence.Observation(nil), s.startupObservations...),
		StartupAssertions:    append([]coreevidence.Assertion(nil), s.startupAssertions...),
		EnvironmentObservers: append([]runtimeevidence.Observer(nil), s.environmentObservers...),
		AssertionDerivers:    append([]runtimeevidence.AssertionDeriver(nil), s.assertionDerivers...),
		ReactionRules:        append([]corereaction.Rule(nil), s.reactionRules...),
		Security:             s.security,
		SecurityTrace:        s.securityTrace,
		ToolProjection:       s.sessionToolProjection,
	}
	result := exec.ExecuteInboundCommand(ctx, inbound)
	if err := runtimeFailures.Err(); err != nil {
		return InboundResult{Session: info, Command: result}, err
	}
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

func (s *Service) handleTrigger(ctx context.Context, info SessionInfo, inbound channel.Inbound) (InboundResult, error) {
	runID := clientapi.RunID(inbound.ID)
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:       clientapi.EventSubmissionReceived,
		RunID:      runID,
		Session:    toClientSessionInfo(info),
		Submission: submissionForInbound(normalizedSubmissionTrigger, runID, inbound),
	})
	profile, _, _ := s.profileForInfo(info)
	runtimeFailures := &runtimeEventPersistenceFailures{}
	exec := session.Session{
		Agent:                s.agent,
		Profile:              profile,
		Commands:             s.commands,
		Operations:           s.operations,
		Resolver:             s.resolver,
		CommandCatalog:       s.commandCatalog,
		SessionCommands:      s.sessionCommands,
		OperationCatalog:     s.operationCatalog,
		ActivationSets:       append([]coreactivation.Set(nil), s.activationSets...),
		OperationSets:        append([]operation.Set(nil), s.operationSets...),
		Datasources:          append([]coredatasource.Spec(nil), s.datasources...),
		PostEditChecks:       append([]coresession.PostEditCheckSpec(nil), s.postEditChecks...),
		ContextProviders:     append([]corecontext.Provider(nil), s.contextProviders...),
		ToolSetCatalog:       s.toolSetCatalog,
		WorkflowCatalog:      s.workflowCatalog,
		OperationExecutor:    s.executorForInfo(info),
		Events:               s.runtimeEventSinkWithFailures(ctx, info, runID, runtimeFailures),
		ThreadStore:          s.threadStore,
		Thread:               info.Thread,
		SessionAgents:        s.currentSessionAgents(),
		SessionRuns:          s.currentSessionRuns(),
		Delegation:           s.delegationForInfo(info),
		StopEvaluator:        s.stopEvaluator,
		RunID:                string(runID),
		StartupObservations:  append([]coreevidence.Observation(nil), s.startupObservations...),
		StartupAssertions:    append([]coreevidence.Assertion(nil), s.startupAssertions...),
		EnvironmentObservers: append([]runtimeevidence.Observer(nil), s.environmentObservers...),
		AssertionDerivers:    append([]runtimeevidence.AssertionDeriver(nil), s.assertionDerivers...),
		ReactionRules:        append([]corereaction.Rule(nil), s.reactionRules...),
		Security:             s.security,
		SecurityTrace:        s.securityTrace,
		ToolProjection:       s.sessionToolProjection,
	}
	result := exec.ExecuteInboundTrigger(ctx, inbound)
	if err := runtimeFailures.Err(); err != nil {
		return InboundResult{Session: info, Trigger: result}, err
	}
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:    clientapi.EventTriggerCompleted,
		RunID:   runID,
		Session: toClientSessionInfo(info),
		Trigger: &result,
	})
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:    clientapi.EventRunCompleted,
		RunID:   runID,
		Session: toClientSessionInfo(info),
	})
	return InboundResult{Session: info, Trigger: result}, nil
}

func (s *Service) handleOperation(ctx context.Context, info SessionInfo, inbound channel.Inbound) (InboundResult, error) {
	runID := clientapi.RunID(inbound.ID)
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:       clientapi.EventSubmissionReceived,
		RunID:      runID,
		Session:    toClientSessionInfo(info),
		Submission: submissionForInbound(normalizedSubmissionOperation, runID, inbound),
	})
	profile, _, _ := s.profileForInfo(info)
	runtimeFailures := &runtimeEventPersistenceFailures{}
	exec := session.Session{
		Agent:                s.agent,
		Profile:              profile,
		Commands:             s.commands,
		Operations:           s.operations,
		Resolver:             s.resolver,
		CommandCatalog:       s.commandCatalog,
		SessionCommands:      s.sessionCommands,
		OperationCatalog:     s.operationCatalog,
		ActivationSets:       append([]coreactivation.Set(nil), s.activationSets...),
		OperationSets:        append([]operation.Set(nil), s.operationSets...),
		Datasources:          append([]coredatasource.Spec(nil), s.datasources...),
		PostEditChecks:       append([]coresession.PostEditCheckSpec(nil), s.postEditChecks...),
		ContextProviders:     append([]corecontext.Provider(nil), s.contextProviders...),
		WorkflowCatalog:      s.workflowCatalog,
		OperationExecutor:    s.executorForInfo(info),
		Events:               s.runtimeEventSinkWithFailures(ctx, info, runID, runtimeFailures),
		ThreadStore:          s.threadStore,
		Thread:               info.Thread,
		SessionAgents:        s.currentSessionAgents(),
		SessionRuns:          s.currentSessionRuns(),
		Delegation:           s.delegationForInfo(info),
		StopEvaluator:        s.stopEvaluator,
		RunID:                string(runID),
		StartupObservations:  append([]coreevidence.Observation(nil), s.startupObservations...),
		StartupAssertions:    append([]coreevidence.Assertion(nil), s.startupAssertions...),
		EnvironmentObservers: append([]runtimeevidence.Observer(nil), s.environmentObservers...),
		AssertionDerivers:    append([]runtimeevidence.AssertionDeriver(nil), s.assertionDerivers...),
		ReactionRules:        append([]corereaction.Rule(nil), s.reactionRules...),
		Security:             s.security,
		SecurityTrace:        s.securityTrace,
		ToolProjection:       s.sessionToolProjection,
	}
	result := exec.ExecuteInboundOperation(ctx, inbound)
	if err := runtimeFailures.Err(); err != nil {
		return InboundResult{Session: info, Operation: result}, err
	}
	outbound := operationOutbound(inbound, result)
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
	return InboundResult{Session: info, Operation: result, Outbound: outbound}, nil
}

func (s *Service) agentForSession(ctx context.Context, info SessionInfo) (agent.Agent, error) {
	if s == nil || s.agentProvider == nil || info.Session.Name == "" {
		return s.agent, nil
	}
	spec, ok, err := s.profileForInfo(info)
	if err != nil {
		return nil, fmt.Errorf("harness: resolve session agent for %q: %w", info.Session.Name, err)
	}
	if !ok {
		return s.agent, nil
	}
	return s.agentProvider.AgentForSession(ctx, spec)
}

func (s *Service) currentSessionAgents() *sessionagent.Runner {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionAgents
}

func (s *Service) currentSessionRuns() *sessionrun.Runner {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionRuns
}

func (s *Service) delegationForInfo(info SessionInfo) coresession.DelegationPolicy {
	spec, ok, _ := s.profileForInfo(info)
	if !ok {
		return coresession.DelegationPolicy{}
	}
	return spec.Delegation
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
		out.Message = &channel.Message{Content: outboundContent(content)}
	case result.Output != nil:
		out.Message = &channel.Message{Content: outboundContent(result.Output)}
	case result.Error != nil:
		out.Message = &channel.Message{Content: result.Error.Message}
	default:
		out.Message = &channel.Message{Content: string(result.Status)}
	}
	return &out
}

func outboundContent(value any) any {
	if rendered, ok := value.(operation.ModelRenderable); ok {
		return rendered.ModelText()
	}
	return value
}

func operationOutbound(inbound channel.Inbound, result session.OperationResult) *channel.Outbound {
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
		out.Message = &channel.Message{Content: outboundContent(content)}
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
	normalizedSubmissionOperation
	normalizedSubmissionTrigger
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
		submission.CommandLine = inbound.CommandLine
	case normalizedSubmissionOperation:
		submission.Kind = clientapi.SubmissionOperation
		if inbound.Operation != nil {
			submission.Operation = &clientapi.OperationInvocation{
				Operation: inbound.Operation.Operation,
				Input:     inbound.Operation.Input,
			}
		}
	case normalizedSubmissionTrigger:
		submission.Kind = clientapi.SubmissionTrigger
		if inbound.Trigger != nil {
			submission.Trigger = &clientapi.Trigger{
				Name:     inbound.Trigger.Name,
				Source:   inbound.Trigger.Source,
				Payload:  inbound.Trigger.Payload,
				Actions:  append([]corereaction.Action(nil), inbound.Trigger.Actions...),
				Metadata: cloneAnyMap(inbound.Trigger.Metadata),
			}
		}
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
	if req.Profile.Name != "" {
		if req.Session.Name == "" {
			req.Session = coresession.Ref{Name: req.Profile.Name}
		}
		return applyProfileDefaults(req, req.Profile), nil
	}
	if req.Session.Name == "" {
		return req, nil
	}
	binding, err := s.sessionCatalog.Resolve(string(req.Session.Name))
	if err != nil {
		return OpenSessionRequest{}, fmt.Errorf("harness: configured session %q: %w", req.Session.Name, err)
	}
	spec := binding.Spec
	req.Session = coresession.Ref{Name: coresession.Name(binding.ID.Address())}
	req.Profile = spec
	return applyProfileDefaults(req, spec), nil
}

func applyProfileDefaults(req OpenSessionRequest, spec coresession.Spec) OpenSessionRequest {
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
	return req
}

func (s *Service) bindProfile(threadID corethread.ID, spec coresession.Spec) {
	if s == nil || threadID == "" || spec.Name == "" {
		return
	}
	s.mu.Lock()
	if s.profiles == nil {
		s.profiles = map[corethread.ID]coresession.Spec{}
	}
	s.profiles[threadID] = spec
	s.mu.Unlock()
}

// bindApprover stores a per-thread approval gate override. Session-agent
// helper sessions call this during Open so that every subsequent run on that thread
// uses the parent's approval policy (e.g. AutoApprover for --yolo).
func (s *Service) bindApprover(threadID corethread.ID, approver operationruntime.ApprovalGate) {
	if s == nil || threadID == "" || approver == nil {
		return
	}
	s.mu.Lock()
	if s.approvers == nil {
		s.approvers = map[corethread.ID]operationruntime.ApprovalGate{}
	}
	s.approvers[threadID] = approver
	s.mu.Unlock()
}

// executorForInfo returns the effective executor for a session. When the
// thread has a bound approval gate override it replaces the safety envelope's
// Approval field so that child sessions inherit the parent's approval policy.
func (s *Service) executorForInfo(info SessionInfo) operationruntime.Executor {
	if s == nil {
		return s.operationExecutor
	}
	s.mu.Lock()
	approver, hasOverride := s.approvers[info.Thread.ID]
	s.mu.Unlock()
	if !hasOverride {
		return s.operationExecutor
	}
	exec := s.operationExecutor
	if env, ok := exec.Safety.(operationruntime.SafetyEnvelope); ok {
		env.Approval = approver
		exec.Safety = env
	}
	return exec
}

func (s *Service) profileForInfo(info SessionInfo) (coresession.Spec, bool, error) {
	if s == nil {
		return coresession.Spec{}, false, nil
	}
	if info.Thread.ID != "" {
		s.mu.Lock()
		spec, ok := s.profiles[info.Thread.ID]
		s.mu.Unlock()
		if ok {
			return spec, true, nil
		}
	}
	if info.Session.Name == "" {
		return coresession.Spec{}, false, nil
	}
	binding, err := s.sessionCatalog.Resolve(string(info.Session.Name))
	if err != nil {
		return coresession.Spec{}, false, err
	}
	return binding.Spec, true, nil
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

func (s *Service) boundSessionInfo(threadID corethread.ID, ch channel.Ref) (SessionInfo, bool) {
	if s == nil || threadID == "" {
		return SessionInfo{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, ref := range s.bindings {
		if ref.ID != threadID {
			continue
		}
		if ch.Name != "" && key.channel != ch.Name {
			continue
		}
		if ref.BranchID == "" {
			ref.BranchID = corethread.MainBranch
		}
		return SessionInfo{
			Session:      coresession.Ref{Name: coresession.Name(key.session)},
			Thread:       ref,
			Channel:      channel.Ref{Name: key.channel},
			Conversation: channel.ConversationRef{ID: key.conversation},
			Resumed:      true,
		}, true
	}
	return SessionInfo{}, false
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
	if s == nil {
		return
	}
	s.mu.Lock()
	allSubs := make([]*subscriber, 0, len(s.allSubs))
	for _, sub := range s.allSubs {
		allSubs = append(allSubs, sub)
	}
	subs := make([]*subscriber, 0, len(s.subscribers[threadID]))
	if threadID != "" {
		for _, sub := range s.subscribers[threadID] {
			subs = append(subs, sub)
		}
	}
	s.mu.Unlock()
	for _, sub := range allSubs {
		sub.send(event)
	}
	for _, sub := range subs {
		sub.send(event)
	}
}

// PublishRuntimeEvent persists and publishes a runtime event for an existing
// session thread. Background orchestrators use this to surface progress after
// the originating turn has returned.
func (s *Service) PublishRuntimeEvent(ctx context.Context, thread corethread.Ref, runID clientapi.RunID, payload coreevent.Event) error {
	if s == nil || thread.ID == "" || payload == nil {
		return nil
	}
	info := s.harnessSessionInfoForThread(thread)
	if err := s.persistRuntimeEvent(ctx, info, runID, payload); err != nil {
		return runtimeEventPersistenceError(payload, err)
	}
	event := clientapi.Event{
		Kind:    clientapi.EventRuntimeEmitted,
		RunID:   runID,
		Session: toClientSessionInfo(info),
		Runtime: &clientapi.RuntimeEvent{
			Name:    payload.EventName(),
			Payload: payload,
		},
	}
	s.publish(thread.ID, event)
	if s.events != nil {
		s.events.Emit(payload)
	}
	return nil
}

func (s *Service) harnessSessionInfoForThread(thread corethread.Ref) SessionInfo {
	clientInfo := s.sessionInfoForThread(thread.ID)
	info := SessionInfo{
		Session:      clientInfo.Session,
		Thread:       thread,
		Channel:      clientInfo.Channel,
		Conversation: clientInfo.Conversation,
		Metadata:     clientInfo.Metadata,
		Resumed:      clientInfo.Resumed,
	}
	if info.Thread.BranchID == "" {
		if clientInfo.Thread.BranchID != "" {
			info.Thread.BranchID = clientInfo.Thread.BranchID
		} else {
			info.Thread.BranchID = corethread.MainBranch
		}
	}
	return info
}

func (s *Service) runtimeEventSink(ctx context.Context, info SessionInfo, runID clientapi.RunID) coreevent.Sink {
	return s.runtimeEventSinkWithFailures(ctx, info, runID, nil)
}

func (s *Service) runtimeEventSinkWithFailures(ctx context.Context, info SessionInfo, runID clientapi.RunID, failures *runtimeEventPersistenceFailures) coreevent.Sink {
	return coreevent.SinkFunc(func(payload coreevent.Event) {
		if payload == nil {
			return
		}
		if event, ok := liveSessionEvent(toClientSessionInfo(info), runID, payload); ok {
			s.publish(info.Thread.ID, event)
			if s.events != nil {
				s.events.Emit(payload)
			}
			return
		}
		if err := s.persistRuntimeEvent(ctx, info, runID, payload); err != nil {
			err = runtimeEventPersistenceError(payload, err)
			if failures != nil {
				failures.Record(err)
			}
			s.publishRuntimeEventPersistenceFailure(info, runID, err)
			return
		}
		s.publish(info.Thread.ID, clientapi.Event{
			Kind:    clientapi.EventRuntimeEmitted,
			RunID:   runID,
			Session: toClientSessionInfo(info),
			Runtime: &clientapi.RuntimeEvent{
				Name:    payload.EventName(),
				Payload: payload,
			},
		})
		if s.events != nil {
			s.events.Emit(payload)
		}
	})
}

type runtimeEventPersistenceFailures struct {
	mu  sync.Mutex
	err error
}

func (f *runtimeEventPersistenceFailures) Record(err error) {
	if f == nil || err == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == nil {
		f.err = err
	}
}

func (f *runtimeEventPersistenceFailures) Err() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (s *Service) persistRuntimeEvent(ctx context.Context, info SessionInfo, runID clientapi.RunID, payload coreevent.Event) error {
	if s == nil || s.threadStore == nil || info.Thread.ID == "" || payload == nil {
		return nil
	}
	name := payload.EventName()
	if !shouldPersistRuntimeEvent(name) {
		return nil
	}
	return retryRuntimeEventAppend(ctx, func(appendCtx context.Context) error {
		_, err := s.threadStore.Append(appendCtx, info.Thread, corethread.AppendRecord{
			Event: coreevent.Record{
				Name: coresession.EventRuntimeEmitted,
				Payload: coresession.RuntimeEmitted{
					RunID:   string(runID),
					Name:    name,
					Payload: payload,
				},
				Scope: coreevent.Scope{ThreadID: string(info.Thread.ID)},
			},
		})
		return err
	})
}

func runtimeEventPersistenceError(payload coreevent.Event, err error) error {
	name := coreevent.Name("")
	if payload != nil {
		name = payload.EventName()
	}
	return fmt.Errorf("harness: persist runtime event %q: %w", name, err)
}

func (s *Service) publishRuntimeEventPersistenceFailure(info SessionInfo, runID clientapi.RunID, err error) {
	if s == nil || err == nil {
		return
	}
	s.publish(info.Thread.ID, clientapi.Event{
		Kind:    clientapi.EventRunFailed,
		RunID:   runID,
		Session: toClientSessionInfo(info),
		Error:   err,
	})
}

func retryRuntimeEventAppend(ctx context.Context, append func(context.Context) error) error {
	if append == nil {
		return nil
	}
	var last error
	for attempt := 0; attempt < 8; attempt++ {
		if err := append(runtimeEventPersistenceContext(ctx)); err != nil {
			last = err
			if !errors.Is(err, coreevent.ErrAppendConflict) {
				return err
			}
			continue
		}
		return nil
	}
	return last
}

func runtimeEventPersistenceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func shouldPersistRuntimeEvent(name coreevent.Name) bool {
	value := string(name)
	return strings.HasPrefix(value, "plan.") ||
		strings.HasPrefix(value, "focus.") ||
		strings.HasPrefix(value, "surface.") ||
		strings.HasPrefix(value, "reaction.") ||
		strings.HasPrefix(value, "task.") ||
		strings.HasPrefix(value, "workflow.") ||
		strings.HasPrefix(value, "session_agent.") ||
		strings.HasPrefix(value, "session_run.") ||
		strings.HasPrefix(value, "skill.") ||
		value == "llmagent.model_requested" ||
		value == "llmagent.model_completed" ||
		value == "llmagent.model_failed" ||
		value == "usage.recorded"
}

func liveSessionEvent(info clientapi.SessionInfo, runID clientapi.RunID, payload coreevent.Event) (clientapi.Event, bool) {
	switch event := payload.(type) {
	case coresession.OperationRequested:
		if event.RunID != "" {
			runID = clientapi.RunID(event.RunID)
		}
		return clientapi.Event{
			Kind:    clientapi.EventOperationRequested,
			RunID:   runID,
			Session: info,
			Operation: &clientapi.OperationEvent{
				CallID:    event.CallID,
				Operation: event.Operation,
				Input:     event.Input,
			},
		}, true
	case coresession.OperationCompleted:
		if event.RunID != "" {
			runID = clientapi.RunID(event.RunID)
		}
		result := event.Result
		return clientapi.Event{
			Kind:    clientapi.EventOperationCompleted,
			RunID:   runID,
			Session: info,
			Operation: &clientapi.OperationEvent{
				CallID:    event.CallID,
				Operation: event.Operation,
				Result:    &result,
			},
		}, true
	default:
		return clientapi.Event{}, false
	}
}

type subscriber struct {
	ch   chan clientapi.Event
	in   chan clientapi.Event
	done chan struct{}
	once sync.Once
}

func newSubscriber(outBuffer, queueBuffer int) *subscriber {
	if outBuffer < 0 {
		outBuffer = 0
	}
	if queueBuffer < 0 {
		queueBuffer = 0
	}
	s := &subscriber{
		ch:   make(chan clientapi.Event, outBuffer),
		in:   make(chan clientapi.Event, queueBuffer),
		done: make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *subscriber) run() {
	defer close(s.ch)
	for {
		select {
		case <-s.done:
			return
		case event, ok := <-s.in:
			if !ok {
				return
			}
			select {
			case s.ch <- event:
			case <-s.done:
				return
			}
		}
	}
}

func (s *subscriber) send(event clientapi.Event) {
	if s == nil {
		return
	}
	select {
	case s.in <- event:
	case <-s.done:
	}
}

func (s *subscriber) close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.done)
	})
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
	case coresession.TriggerReceived:
		return []clientapi.Event{withSubmission(base, clientapi.Submission{
			ID:   clientapi.RunID(payload.RunID),
			Kind: clientapi.SubmissionTrigger,
			Trigger: &clientapi.Trigger{
				Name:     payload.Trigger.Name,
				Source:   payload.Trigger.Source,
				Payload:  payload.Trigger.Payload,
				Actions:  append([]corereaction.Action(nil), payload.Trigger.Actions...),
				Metadata: cloneAnyMap(payload.Trigger.Metadata),
			},
			Caller: payload.Caller,
			Trust:  payload.Trust,
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
	case coresession.OperationRequested:
		event := base
		event.Kind = clientapi.EventOperationRequested
		event.RunID = clientapi.RunID(payload.RunID)
		event.Operation = &clientapi.OperationEvent{CallID: payload.CallID, Operation: payload.Operation, Input: payload.Input}
		return []clientapi.Event{event}
	case coresession.OperationCompleted:
		event := base
		event.Kind = clientapi.EventOperationCompleted
		event.RunID = clientapi.RunID(payload.RunID)
		result := payload.Result
		event.Operation = &clientapi.OperationEvent{CallID: payload.CallID, Operation: payload.Operation, Result: &result}
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
	case coresession.RuntimeEmitted:
		event := base
		event.Kind = clientapi.EventRuntimeEmitted
		event.RunID = clientapi.RunID(payload.RunID)
		event.Runtime = &clientapi.RuntimeEvent{Name: payload.Name, Payload: payload.Payload}
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

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
