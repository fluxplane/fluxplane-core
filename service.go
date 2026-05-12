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
	corethread "github.com/fluxplane/agentruntime/core/thread"
	appcomposition "github.com/fluxplane/agentruntime/orchestration/app"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/harness"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

type (
	ChannelClient       = clientapi.ChannelClient
	Session             = clientapi.SessionHandle
	Run                 = clientapi.RunHandle
	OpenRequest         = clientapi.OpenRequest
	ResumeRequest       = clientapi.ResumeRequest
	ListSessionsRequest = clientapi.ListSessionsRequest
	SessionInfo         = clientapi.SessionInfo
	SessionSummary      = clientapi.SessionSummary
	RunID               = clientapi.RunID
	SubmissionKind      = clientapi.SubmissionKind
	Submission          = clientapi.Submission
	Input               = clientapi.Input
	Signal              = clientapi.Signal
	EventKind           = clientapi.EventKind
	EventCursor         = clientapi.EventCursor
	OperationEvent      = clientapi.OperationEvent
	Event               = clientapi.Event
	EventOptions        = clientapi.EventOptions
	Result              = clientapi.Result
	Composition         = appcomposition.Composition
	ResourceBundle      = resource.ContributionBundle
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
	EventOperationCompleted = clientapi.EventOperationCompleted
	EventOutboundProduced   = clientapi.EventOutboundProduced
	EventRunCompleted       = clientapi.EventRunCompleted
	EventRunFailed          = clientapi.EventRunFailed
)

// Config assembles the default in-process runtime. Nil registries and stores
// are replaced with empty in-memory defaults; nil Agent is preserved so command
// only runtimes do not need to provide one. OperationExecutor is safe to leave
// as its zero value.
type Config struct {
	Agent             agent.Agent
	Commands          *command.Registry
	Operations        *operation.Registry
	Resolver          *resource.Resolver
	CommandCatalog    session.CommandCatalog
	OperationCatalog  session.OperationCatalog
	OperationExecutor operationruntime.Executor
	Events            coreevent.Sink
	ThreadStore       corethread.Store
	Channel           channel.Ref
	Caller            policy.Caller
	Trust             policy.Trust
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

	service := harness.New(harness.Config{
		Agent:             cfg.Agent,
		Commands:          commands,
		Operations:        operations,
		Resolver:          cfg.Resolver,
		CommandCatalog:    cfg.CommandCatalog,
		OperationCatalog:  cfg.OperationCatalog,
		OperationExecutor: executor,
		Events:            cfg.Events,
		ThreadStore:       threadStore,
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
	return &Service{harness: service, client: client}, nil
}

// NewFromComposition assembles an in-process runtime service from composed app
// resources. Explicit cfg fields override matching composition fields.
func NewFromComposition(composition appcomposition.Composition, cfg Config) (*Service, error) {
	if cfg.Agent == nil {
		cfg.Agent = composition.Agent
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
	if cfg.OperationExecutor.Validator == nil && len(cfg.OperationExecutor.Middleware) == 0 && cfg.OperationExecutor.EventSink == nil && cfg.OperationExecutor.Safety == nil {
		cfg.OperationExecutor = composition.OperationExecutor
	}
	if cfg.Events == nil {
		cfg.Events = composition.Events
	}
	if cfg.ThreadStore == nil {
		cfg.ThreadStore = composition.ThreadStore
	}
	return New(cfg)
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
