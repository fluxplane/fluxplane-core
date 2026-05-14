package llmagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	corellmagent "github.com/fluxplane/agentruntime/core/agent/llmagent"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/tool"
)

const (
	// DriverKind is the generic agent driver kind handled by this package.
	DriverKind agent.DriverKind = "llmagent"
)

var (
	// ErrModelMissing is returned when an LLM agent is created without a model
	// implementation.
	ErrModelMissing = errors.New("llmagent: model is nil")
)

// Agent implements agent.Agent using a provider-neutral model port.
type Agent struct {
	spec             agent.Spec
	driver           corellmagent.Spec
	model            Model
	tools            []tool.Spec
	contextProviders []corecontext.Provider
	streamPolicy     StreamPolicy
}

// Option configures an LLM agent.
type Option func(*Agent)

// WithDriverSpec sets the LLM-specific driver config. If omitted, the runtime
// derives a narrow driver spec from the generic agent spec.
func WithDriverSpec(spec corellmagent.Spec) Option {
	return func(a *Agent) { a.driver = spec }
}

// WithTools sets the model-visible tool projections for this runtime agent.
func WithTools(tools ...tool.Spec) Option {
	return func(a *Agent) { a.tools = append([]tool.Spec(nil), tools...) }
}

// WithContextProviders sets model-visible context providers for this runtime
// agent.
func WithContextProviders(providers ...corecontext.Provider) Option {
	return func(a *Agent) { a.contextProviders = append([]corecontext.Provider(nil), providers...) }
}

// StreamPolicy controls which transient model stream deltas are emitted
// through the agent event sink.
type StreamPolicy struct {
	EmitContent  bool
	EmitThinking bool
	EmitToolCall bool
}

// WithStreamPolicy configures model streaming event exposure. Raw thinking is
// deliberately opt-in.
func WithStreamPolicy(policy StreamPolicy) Option {
	return func(a *Agent) { a.streamPolicy = policy }
}

// New returns an LLM-backed agent runtime.
func New(spec agent.Spec, model Model, opts ...Option) (*Agent, error) {
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("llmagent: agent spec: %w", err)
	}
	if spec.Driver.Kind != "" && spec.Driver.Kind != DriverKind {
		return nil, fmt.Errorf("llmagent: unsupported driver kind %q", spec.Driver.Kind)
	}
	if model == nil {
		return nil, ErrModelMissing
	}
	if spec.Driver.Kind == "" {
		spec.Driver.Kind = DriverKind
	}
	a := &Agent{
		spec:   spec,
		driver: deriveDriverSpec(spec),
		model:  model,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	a.contextProviders = append([]corecontext.Provider{newSelfContextProvider(a.spec, a.driver, a.model)}, a.contextProviders...)
	return a, nil
}

// Spec returns the generic agent spec.
func (a *Agent) Spec() agent.Spec {
	if a == nil {
		return agent.Spec{}
	}
	return a.spec
}

// DriverSpec returns the LLM-specific runtime driver config.
func (a *Agent) DriverSpec() corellmagent.Spec {
	if a == nil {
		return corellmagent.Spec{}
	}
	return a.driver
}

// ContextProviders returns the providers configured for session-level context
// materialization.
func (a *Agent) ContextProviders() []corecontext.Provider {
	if a == nil {
		return nil
	}
	return append([]corecontext.Provider(nil), a.contextProviders...)
}

// ProviderIdentity reports the concrete provider identity selected by the
// model for this agent, when the model exposes it.
func (a *Agent) ProviderIdentity() coreconversation.ProviderIdentity {
	if a == nil {
		return coreconversation.ProviderIdentity{}
	}
	if identified, ok := a.model.(ProviderIdentityModel); ok {
		return identified.ProviderIdentity(Request{Agent: a.spec, Driver: a.driver})
	}
	return coreconversation.ProviderIdentity{}
}

// Step advances one LLM-backed agent turn.
func (a *Agent) Step(ctx agent.Context, input agent.StepInput) agent.StepResult {
	if a == nil {
		return failed("agent_missing", "llmagent: agent is nil", nil)
	}
	if a.model == nil {
		return failed("model_missing", ErrModelMissing.Error(), nil)
	}
	base := context.Background()
	if ctx != nil {
		base = ctx
	}
	base = coredatasource.ContextWithDetectionInput(base, detectionInput(input))
	if err := base.Err(); err != nil {
		return failed("context_canceled", err.Error(), nil)
	}

	if !contextMaterializedFromContext(base) {
		dynamicContext, err := a.buildContext(base)
		if err != nil {
			return failed("context_provider_failed", err.Error(), nil)
		}
		input.Context = append(dynamicContext, input.Context...)
	}
	req := a.request(ctx, input)
	provider := a.providerName(req)
	emit(ctx, ModelRequested{Agent: a.spec.Name, Provider: provider, Model: modelName(req)})
	resp, err := a.complete(base, ctx, req)
	if err != nil {
		emit(ctx, ModelFailed{Agent: a.spec.Name, Provider: provider, Model: modelName(req), Error: err.Error()})
		return failed("model_failed", err.Error(), nil)
	}
	for _, recorded := range resp.Usage {
		if !recorded.Empty() {
			emit(ctx, recorded)
		}
	}
	if !resp.Transcript.Empty() {
		if len(resp.Transcript.Items) > 0 {
			emit(ctx, coreconversation.ItemsAppended{
				Provider: resp.Transcript.Provider,
				Items:    resp.Transcript.Items,
			})
		}
		if resp.Transcript.Continuation != nil {
			emit(ctx, coreconversation.ContinuationStored{Handle: *resp.Transcript.Continuation})
		}
	}
	result := resultFromResponse(resp)
	if provider == "" && resp.Transcript.Provider.Provider != "" {
		provider = resp.Transcript.Provider.Provider
	}
	emit(ctx, ModelCompleted{Agent: a.spec.Name, Provider: provider, Model: modelName(req), Decision: result.Decision.Kind})
	return result
}

func (a *Agent) complete(ctx context.Context, agentCtx agent.Context, req Request) (Response, error) {
	if streaming, ok := a.model.(StreamingModel); ok {
		return streaming.Stream(ctx, req, func(evt StreamEvent) {
			a.emitStream(agentCtx, req, evt)
		})
	}
	return a.model.Complete(ctx, req)
}

func (a *Agent) emitStream(ctx agent.Context, req Request, evt StreamEvent) {
	switch evt.Kind {
	case StreamThinkingDelta:
		if !a.streamPolicy.EmitThinking {
			return
		}
	case StreamContentDelta:
		if !a.streamPolicy.EmitContent {
			return
		}
	case StreamToolCallDelta:
		if !a.streamPolicy.EmitToolCall {
			return
		}
	default:
		return
	}
	emit(ctx, ModelStreamed{Agent: a.spec.Name, Provider: a.providerName(req), Model: modelName(req), Event: evt})
}

func (a *Agent) request(ctx agent.Context, input agent.StepInput) Request {
	return Request{
		Agent:        a.spec,
		Driver:       a.driver,
		Tools:        append([]tool.Spec(nil), a.tools...),
		Goal:         input.Goal,
		Objective:    chooseObjective(input.Objective, a.spec.Objective),
		Observations: append([]environment.Observation(nil), input.Observations...),
		Context:      append([]corecontext.Block(nil), input.Context...),
		Transcript:   transcriptFromContext(ctx),
		State:        input.State,
	}
}

func (a *Agent) buildContext(ctx context.Context) ([]corecontext.Block, error) {
	if len(a.contextProviders) == 0 {
		return nil, nil
	}
	ctx = a.withDatasourceAccess(ctx)
	var out []corecontext.Block
	for _, provider := range a.contextProviders {
		if provider == nil {
			continue
		}
		blocks, err := provider.Build(ctx, corecontext.Request{})
		if err != nil {
			return nil, fmt.Errorf("context provider %q: %w", provider.Spec().Name, err)
		}
		out = append(out, blocks...)
	}
	return out, nil
}

func detectionInput(input agent.StepInput) coredatasource.DetectionInput {
	var sources []coredatasource.DetectionSource
	for i, observation := range input.Observations {
		if observation.Kind != "channel.message" {
			continue
		}
		text := detectionText(observation.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sources = append(sources, coredatasource.DetectionSource{
			ID:       fmt.Sprintf("observation-%d", i),
			Kind:     observation.Kind,
			Text:     text,
			Metadata: stringMetadata(observation.Metadata),
		})
	}
	return coredatasource.DetectionInput{Sources: sources}
}

func detectionText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
	case map[string]string:
		if text := typed["text"]; text != "" {
			return text
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func stringMetadata(values map[string]any) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range values {
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *Agent) withDatasourceAccess(ctx context.Context) context.Context {
	if a == nil {
		return ctx
	}
	names := make([]coredatasource.Name, 0, len(a.spec.Datasources))
	for _, ref := range a.spec.Datasources {
		if ref.Name != "" {
			names = append(names, ref.Name)
		}
	}
	return coredatasource.ContextWithAccessPolicy(ctx, coredatasource.AccessPolicy{Datasources: names})
}

func (a *Agent) providerName(req Request) string {
	if a == nil {
		return ""
	}
	if identified, ok := a.model.(ProviderIdentityModel); ok {
		return identified.ProviderIdentity(req).Provider
	}
	if req.Transcript != nil {
		return req.Transcript.Provider.Provider
	}
	return ""
}

func resultFromResponse(resp Response) agent.StepResult {
	result := agent.StepResult{
		Status: agent.StatusOK,
		State:  resp.State,
	}
	switch {
	case len(resp.Operations) > 0:
		result.Decision = agent.Decision{
			Kind:       agent.DecisionOperation,
			Operations: append([]agent.OperationRequest(nil), resp.Operations...),
		}
	case resp.Message != nil:
		result.Decision = agent.Decision{Kind: agent.DecisionMessage, Message: resp.Message}
	case resp.Completion != nil:
		result.Decision = agent.Decision{Kind: agent.DecisionComplete, Complete: resp.Completion}
	default:
		result.Decision = agent.Decision{Kind: agent.DecisionWait}
	}
	return result
}

func deriveDriverSpec(spec agent.Spec) corellmagent.Spec {
	return corellmagent.Spec{
		Instructions: spec.System,
		Model: corellmagent.ModelPolicy{
			Model: spec.Inference.Model,
		},
		Inference: corellmagent.InferencePolicy{
			MaxOutputTokens: spec.Inference.MaxOutputTokens,
			Temperature:     spec.Inference.Temperature,
			ReasoningEffort: spec.Inference.ReasoningEffort,
		},
	}
}

func chooseObjective(requested, fallback agent.Objective) agent.Objective {
	if requested.Role != "" || requested.Instructions != "" || requested.Success != "" {
		return requested
	}
	return fallback
}

func failed(code, message string, details map[string]any) agent.StepResult {
	return agent.StepResult{
		Status: agent.StatusFailed,
		Error:  &agent.Error{Code: code, Message: message, Details: details},
	}
}

func modelName(req Request) string {
	if req.Driver.Model.Model != "" {
		return req.Driver.Model.Model
	}
	return req.Agent.Inference.Model
}
