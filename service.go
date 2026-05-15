// Package agentruntime provides the public in-process runtime facade.
package agentruntime

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/adapters/directchannel"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/agentfactory"
	appcomposition "github.com/fluxplane/agentruntime/orchestration/app"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/harness"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
	"github.com/fluxplane/agentruntime/orchestration/toolprojection"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

type (
	ChannelClient        = clientapi.ChannelClient
	Session              = clientapi.SessionHandle
	Run                  = clientapi.RunHandle
	OpenRequest          = clientapi.OpenRequest
	ResumeRequest        = clientapi.ResumeRequest
	ListSessionsRequest  = clientapi.ListSessionsRequest
	SessionInfo          = clientapi.SessionInfo
	SessionSummary       = clientapi.SessionSummary
	RunID                = clientapi.RunID
	SubmissionKind       = clientapi.SubmissionKind
	Submission           = clientapi.Submission
	Input                = clientapi.Input
	Signal               = clientapi.Signal
	EventKind            = clientapi.EventKind
	EventCursor          = clientapi.EventCursor
	OperationEvent       = clientapi.OperationEvent
	Event                = clientapi.Event
	EventOptions         = clientapi.EventOptions
	Result               = clientapi.Result
	Composition          = appcomposition.Composition
	ResourceBundle       = resource.ContributionBundle
	AgentProvider        = harness.AgentProvider
	LLMModel             = llmagent.Model
	LLMModelResolver     = agentfactory.ModelResolver
	LLMStreamPolicy      = llmagent.StreamPolicy
	ToolProjectionConfig = toolprojection.Config
	SessionName          = coresession.Name
	SessionRef           = coresession.Ref
	SessionSpec          = coresession.Spec
	DelegationPolicy     = coresession.DelegationPolicy
)

const (
	SubmissionInput   = clientapi.SubmissionInput
	SubmissionCommand = clientapi.SubmissionCommand
	SubmissionEvent   = clientapi.SubmissionEvent
	SubmissionSignal  = clientapi.SubmissionSignal

	EventSubmissionReceived = clientapi.EventSubmissionReceived
	EventInputCompleted     = clientapi.EventInputCompleted
	EventCommandCompleted   = clientapi.EventCommandCompleted
	EventAgentStepCompleted = clientapi.EventAgentStepCompleted
	EventOperationRequested = clientapi.EventOperationRequested
	EventOperationCompleted = clientapi.EventOperationCompleted
	EventOutboundProduced   = clientapi.EventOutboundProduced
	EventRuntimeEmitted     = clientapi.EventRuntimeEmitted
	EventRunCompleted       = clientapi.EventRunCompleted
	EventRunFailed          = clientapi.EventRunFailed
)

// NewSubmission returns an empty fluent submission value.
func NewSubmission() Submission {
	return clientapi.NewSubmission()
}

// Config assembles the default in-process runtime. Nil registries and stores
// are replaced with empty in-memory defaults; nil Agent is preserved so command
// only runtimes do not need to provide one. OperationExecutor is safe to leave
// as its zero value.
type Config struct {
	Agent             agent.Agent
	AgentProvider     AgentProvider
	Commands          *command.Registry
	Operations        *operation.Registry
	Resolver          *resource.Resolver
	CommandCatalog    session.CommandCatalog
	OperationCatalog  session.OperationCatalog
	SessionCatalog    session.SessionCatalog
	OperationExecutor operationruntime.Executor
	Events            coreevent.Sink
	ThreadStore       corethread.Store
	StopEvaluator     session.StopEvaluator
	Channel           channel.Ref
	Caller            policy.Caller
	Trust             policy.Trust
	LLMModel          LLMModel
	LLMModelResolver  LLMModelResolver
	LLMStreamPolicy   LLMStreamPolicy
	ToolProjection    ToolProjectionConfig
}

// Service is the public library facade over the default in-process runtime.
type Service struct {
	harness *harness.Service
	client  ChannelClient
}

var _ ChannelClient = (*Service)(nil)

// New assembles an in-process runtime service with a direct channel client.
func New(cfg Config) (*Service, error) {
	commands := cfg.Commands
	if commands == nil {
		commands = command.NewRegistry()
	}
	operations := cfg.Operations
	if operations == nil {
		operations = operation.NewRegistry()
	}
	threadStore := cfg.ThreadStore
	if threadStore == nil {
		store, err := runtimethread.NewStore(eventstore.NewMemoryStore())
		if err != nil {
			return nil, fmt.Errorf("agentruntime: create thread store: %w", err)
		}
		threadStore = store
	}
	executor := cfg.OperationExecutor
	stopEvaluator := cfg.StopEvaluator
	if stopEvaluator == nil {
		switch {
		case cfg.LLMModel != nil:
			stopEvaluator = session.ModelStopEvaluator{Model: cfg.LLMModel}
		case cfg.LLMModelResolver != nil:
			stopEvaluator = resolverStopEvaluator{resolver: cfg.LLMModelResolver}
		}
	}

	service := harness.New(harness.Config{
		Agent:             cfg.Agent,
		AgentProvider:     cfg.AgentProvider,
		Commands:          commands,
		Operations:        operations,
		Resolver:          cfg.Resolver,
		CommandCatalog:    cfg.CommandCatalog,
		OperationCatalog:  cfg.OperationCatalog,
		SessionCatalog:    cfg.SessionCatalog,
		OperationExecutor: executor,
		Events:            cfg.Events,
		ThreadStore:       threadStore,
		StopEvaluator:     stopEvaluator,
	})
	client, err := directchannel.New(directchannel.Config{
		Service: service,
		Channel: cfg.Channel,
		Caller:  cfg.Caller,
		Trust:   cfg.Trust,
	})
	if err != nil {
		return nil, err
	}
	service.SetSubagentSupervisor(subagent.New(subagent.Config{
		Client: subagentClient{client: client},
		ResolveProfile: subagent.ProfileResolverFunc(func(_ context.Context, ref coresession.Ref) (coresession.Spec, error) {
			binding, err := cfg.SessionCatalog.Resolve(string(ref.Name))
			if err != nil {
				return coresession.Spec{}, err
			}
			return binding.Spec, nil
		}),
	}))
	return &Service{harness: service, client: client}, nil
}

type subagentClient struct {
	client ChannelClient
}

func (c subagentClient) Open(ctx context.Context, req subagent.OpenRequest) (subagent.Session, error) {
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
	return subagentSession{session: session}, nil
}

type subagentSession struct {
	session Session
}

func (s subagentSession) Info() subagent.SessionInfo {
	info := s.session.Info()
	return subagent.SessionInfo{Thread: info.Thread}
}

func (s subagentSession) SendInput(ctx context.Context, input subagent.Input) (subagent.Run, error) {
	run, err := s.session.Submit(ctx, NewSubmission().WithInput(Input{Text: input.Text, Metadata: input.Metadata}))
	if err != nil {
		return nil, err
	}
	return subagentRun{run: run}, nil
}

type subagentRun struct {
	run Run
}

func (r subagentRun) ID() string { return string(r.run.ID()) }

func (r subagentRun) Events() <-chan subagent.RunEvent {
	out := make(chan subagent.RunEvent, 16)
	go func() {
		defer close(out)
		for event := range r.run.Events() {
			converted := subagent.RunEvent{Kind: string(event.Kind)}
			if event.Operation != nil {
				converted.Operation = event.Operation.Operation.String()
			}
			if event.Runtime != nil {
				converted.Runtime = string(event.Runtime.Name)
			}
			out <- converted
		}
	}()
	return out
}

func (r subagentRun) Wait(ctx context.Context) (subagent.RunResult, error) {
	result, err := r.run.Wait(ctx)
	if err != nil {
		return subagent.RunResult{}, err
	}
	if result.Outbound != nil && result.Outbound.Message != nil {
		return subagent.RunResult{Text: fmt.Sprint(result.Outbound.Message.Content)}, nil
	}
	return subagent.RunResult{}, nil
}

// NewFromComposition assembles an in-process runtime service from composed app
// resources. Explicit cfg fields override matching composition fields.
func NewFromComposition(composition appcomposition.Composition, cfg Config) (*Service, error) {
	if cfg.Agent == nil {
		cfg.Agent = composition.Agent
	}
	if cfg.AgentProvider == nil && cfg.Agent == nil && (cfg.LLMModel != nil || cfg.LLMModelResolver != nil) {
		cfg.AgentProvider = agentfactory.New(agentfactory.Config{
			Agents:           composition.AgentCatalog,
			Skills:           composition.SkillCatalog,
			Resolver:         composition.Resolver,
			CommandCatalog:   composition.CommandCatalog,
			OperationCatalog: composition.OperationCatalog,
			ToolSetCatalog:   composition.ToolSetCatalog,
			ContextProviders: composition.ContextProviderImpls,
			Model:            cfg.LLMModel,
			ModelResolver:    cfg.LLMModelResolver,
			StreamPolicy:     cfg.LLMStreamPolicy,
			Projection:       cfg.ToolProjection,
		})
	}
	if cfg.Commands == nil {
		cfg.Commands = composition.Commands
	}
	if cfg.Operations == nil {
		cfg.Operations = composition.Operations
	}
	if cfg.Resolver == nil {
		cfg.Resolver = composition.Resolver
	}
	if cfg.CommandCatalog == nil {
		cfg.CommandCatalog = composition.CommandCatalog
	}
	if cfg.OperationCatalog == nil {
		cfg.OperationCatalog = composition.OperationCatalog
	}
	if cfg.SessionCatalog == nil {
		cfg.SessionCatalog = composition.SessionCatalog
	}
	if cfg.OperationExecutor.Validator == nil && len(cfg.OperationExecutor.Middleware) == 0 && cfg.OperationExecutor.EventSink == nil && cfg.OperationExecutor.Safety == nil {
		cfg.OperationExecutor = composition.OperationExecutor
	}
	return New(cfg)
}

type resolverStopEvaluator struct {
	resolver LLMModelResolver
}

func (e resolverStopEvaluator) EvaluateStopCondition(ctx context.Context, input session.StopEvaluationInput) (session.StopEvaluation, error) {
	if e.resolver == nil {
		return session.StopEvaluation{}, fmt.Errorf("agentruntime: stop evaluator model resolver is nil")
	}
	model, err := e.resolver.ResolveModel(ctx, input.Agent)
	if err != nil {
		return session.StopEvaluation{}, err
	}
	return session.ModelStopEvaluator{Model: model}.EvaluateStopCondition(ctx, input)
}

// Client returns the default in-process channel client.
func (s *Service) Client() ChannelClient {
	if s == nil {
		return nil
	}
	return s.client
}

// Open opens or creates a session through the default in-process channel.
func (s *Service) Open(ctx context.Context, req OpenRequest) (Session, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("agentruntime: service is nil")
	}
	return s.client.Open(ctx, req)
}

// Resume resumes a known session/thread through the default in-process channel.
func (s *Service) Resume(ctx context.Context, req ResumeRequest) (Session, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("agentruntime: service is nil")
	}
	return s.client.Resume(ctx, req)
}

// ListSessions lists sessions visible to the default in-process channel.
func (s *Service) ListSessions(ctx context.Context, req ListSessionsRequest) ([]SessionSummary, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("agentruntime: service is nil")
	}
	return s.client.ListSessions(ctx, req)
}
