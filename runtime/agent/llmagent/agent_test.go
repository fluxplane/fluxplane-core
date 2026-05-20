package llmagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/agent"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/event"
	coreevidence "github.com/fluxplane/agentruntime/core/evidence"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/core/usage"
)

func TestAgentStepSendsStructuredRequestAndReturnsMessageDecision(t *testing.T) {
	var got Request
	model := ModelFunc(func(_ context.Context, req Request) (Response, error) {
		got = req
		return MessageResponse("hello"), nil
	})
	spec := agent.Spec{
		Name:   "main",
		System: "You are helpful.",
		Objective: agent.Objective{
			Role:         "engineer",
			Instructions: "Maintain the repo.",
		},
		Inference: agent.InferenceSpec{
			Model:           "test-model",
			MaxOutputTokens: 128,
			ReasoningEffort: "low",
		},
	}
	runtime, err := New(spec, model)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{
		Goal: "fix tests",
		Observations: []coreevidence.Observation{{
			Source:  "channel",
			Kind:    "message",
			Content: "please fix tests",
		}},
		Context: []corecontext.Block{{
			ID:      "repo",
			Kind:    corecontext.BlockText,
			Content: "repo context",
		}},
	})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok: %#v", result.Status, result.Error)
	}
	if result.Decision.Kind != agent.DecisionMessage {
		t.Fatalf("decision = %q, want message", result.Decision.Kind)
	}
	if result.Decision.Message.Content != "hello" {
		t.Fatalf("message = %#v, want hello", result.Decision.Message.Content)
	}
	if got.Agent.Name != spec.Name {
		t.Fatalf("request agent = %q, want %q", got.Agent.Name, spec.Name)
	}
	if got.Driver.Instructions != spec.System {
		t.Fatalf("driver instructions = %q, want system", got.Driver.Instructions)
	}
	if got.Driver.Model.Model != "test-model" {
		t.Fatalf("driver model = %q, want test-model", got.Driver.Model.Model)
	}
	if got.Objective.Role != "engineer" {
		t.Fatalf("objective role = %q, want engineer", got.Objective.Role)
	}
	if len(got.Observations) != 1 || got.Observations[0].Content != "please fix tests" {
		t.Fatalf("observations = %#v, want channel message", got.Observations)
	}
	if !hasContextBlock(got.Context, "", "repo context") {
		t.Fatalf("context = %#v, want repo context", got.Context)
	}
	if !hasContextBlock(got.Context, SelfContextProviderName, "model: test-model") {
		t.Fatalf("context = %#v, want self context with model", got.Context)
	}
}

func TestAgentStepIncludesSelfContextWithProviderIdentity(t *testing.T) {
	var got Request
	runtime, err := New(agent.Spec{Name: "main", Inference: agent.InferenceSpec{Model: "fallback-model"}}, capturingIdentifiedModel{
		identity: coreconversation.ProviderIdentity{Provider: "test-provider", Model: "resolved-model"},
		response: MessageResponse("ok"),
		got:      &got,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if !hasContextBlock(got.Context, SelfContextProviderName, "model: test-provider/resolved-model") {
		t.Fatalf("context = %#v, want self context with provider/model", got.Context)
	}
}

func TestAgentStepIncludesProjectedTools(t *testing.T) {
	var got Request
	runtime, err := New(agent.Spec{Name: "main"}, ModelFunc(func(_ context.Context, req Request) (Response, error) {
		got = req
		return MessageResponse("ok"), nil
	}), WithTools(tool.Spec{Name: "inspect"}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "inspect" {
		t.Fatalf("tools = %#v, want inspect", got.Tools)
	}
}

func TestAgentStepWithToolsOverridesConfiguredTools(t *testing.T) {
	var got Request
	runtime, err := New(agent.Spec{Name: "main"}, ModelFunc(func(_ context.Context, req Request) (Response, error) {
		got = req
		return MessageResponse("ok"), nil
	}), WithTools(tool.Spec{Name: "inspect"}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.StepWithTools(testAgentContext{}, agent.StepInput{}, []tool.Spec{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(got.Tools) != 0 {
		t.Fatalf("tools = %#v, want override to no tools", got.Tools)
	}
}

func TestAgentStepPassesObservationsToContextProviders(t *testing.T) {
	var gotObservations []coreevidence.Observation
	provider := contextProviderFunc{
		spec: corecontext.ProviderSpec{Name: "detect"},
		build: func(_ context.Context, req corecontext.Request) ([]corecontext.Block, error) {
			gotObservations = append([]coreevidence.Observation(nil), req.Observations...)
			return []corecontext.Block{{ID: "detect", Provider: "detect", Kind: corecontext.BlockText, Content: "detected"}}, nil
		},
	}
	var got Request
	runtime, err := New(agent.Spec{Name: "main"}, ModelFunc(func(_ context.Context, req Request) (Response, error) {
		got = req
		return MessageResponse("ok"), nil
	}), WithContextProviders(provider))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{Observations: []coreevidence.Observation{{
		Source:  "channel",
		Kind:    "channel.message",
		Content: map[string]any{"text": "see DEV-381"},
	}}})
	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(gotObservations) != 1 {
		t.Fatalf("observations = %#v, want one channel text observation", gotObservations)
	}
	content, ok := gotObservations[0].Content.(map[string]any)
	if !ok || content["text"] != "see DEV-381" {
		t.Fatalf("observations = %#v, want channel text observation", gotObservations)
	}
	if !hasContextBlock(got.Context, "detect", "detected") {
		t.Fatalf("context = %#v, want provider block", got.Context)
	}
}

func TestAgentStepMapsOperationResponse(t *testing.T) {
	runtime, err := New(agent.Spec{Name: "main"}, StaticModel{
		Response: OperationResponse(agent.OperationRequest{
			Operation: operation.Ref{Name: "echo"},
			Input:     "hello",
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if result.Decision.Kind != agent.DecisionOperation {
		t.Fatalf("decision = %q, want operation", result.Decision.Kind)
	}
	if len(result.Decision.Operations) != 1 {
		t.Fatalf("operations len = %d, want 1", len(result.Decision.Operations))
	}
	if result.Decision.Operations[0].Operation.Name != "echo" {
		t.Fatalf("operation = %q, want echo", result.Decision.Operations[0].Operation.Name)
	}
}

func TestAgentStepMapsMultipleOperationResponses(t *testing.T) {
	runtime, err := New(agent.Spec{Name: "main"}, StaticModel{
		Response: OperationResponse(
			agent.OperationRequest{Operation: operation.Ref{Name: "read"}, Input: "README.md"},
			agent.OperationRequest{Operation: operation.Ref{Name: "test"}, Input: "./..."},
		),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if result.Decision.Kind != agent.DecisionOperation {
		t.Fatalf("decision = %q, want operation", result.Decision.Kind)
	}
	if len(result.Decision.Operations) != 2 {
		t.Fatalf("operations len = %d, want 2", len(result.Decision.Operations))
	}
	if result.Decision.Operations[0].Operation.Name != "read" || result.Decision.Operations[1].Operation.Name != "test" {
		t.Fatalf("operations = %#v, want read/test", result.Decision.Operations)
	}
}

func TestAgentStepMapsCompletionResponse(t *testing.T) {
	runtime, err := New(agent.Spec{Name: "main"}, StaticModel{
		Response: CompleteResponse("done", "finished"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if result.Decision.Kind != agent.DecisionComplete {
		t.Fatalf("decision = %q, want complete", result.Decision.Kind)
	}
	if result.Decision.Complete.Output != "done" {
		t.Fatalf("completion output = %#v, want done", result.Decision.Complete.Output)
	}
}

func TestAgentStepReturnsWaitForEmptyResponse(t *testing.T) {
	runtime, err := New(agent.Spec{Name: "main"}, StaticModel{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if result.Decision.Kind != agent.DecisionWait {
		t.Fatalf("decision = %q, want wait", result.Decision.Kind)
	}
}

func TestAgentStepEmitsRedactedModelEvents(t *testing.T) {
	var events []event.Event
	ctx := testAgentContext{events: event.SinkFunc(func(evt event.Event) {
		events = append(events, evt)
	})}
	runtime, err := New(agent.Spec{Name: "main", Inference: agent.InferenceSpec{Model: "test-model"}}, StaticModel{
		Response: MessageResponse("secret response"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(ctx, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2: %#v", len(events), events)
	}
	requested, ok := events[0].(ModelRequested)
	if !ok {
		t.Fatalf("event[0] = %T, want ModelRequested", events[0])
	}
	if requested.Model != "test-model" {
		t.Fatalf("requested model = %q, want test-model", requested.Model)
	}
	completed, ok := events[1].(ModelCompleted)
	if !ok {
		t.Fatalf("event[1] = %T, want ModelCompleted", events[1])
	}
	if completed.Decision != agent.DecisionMessage {
		t.Fatalf("completed decision = %q, want message", completed.Decision)
	}
}

func TestAgentStepEmitsModelLifecycleProvider(t *testing.T) {
	var events []event.Event
	ctx := testAgentContext{events: event.SinkFunc(func(evt event.Event) {
		events = append(events, evt)
	})}
	runtime, err := New(agent.Spec{Name: "main", Inference: agent.InferenceSpec{Model: "test-model"}}, identifiedModel{
		identity: coreconversation.ProviderIdentity{Provider: "codex", Model: "test-model"},
		response: MessageResponse("ok"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(ctx, agent.StepInput{})
	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	requested, ok := events[0].(ModelRequested)
	if !ok {
		t.Fatalf("event[0] = %T, want ModelRequested", events[0])
	}
	if requested.Provider != "codex" {
		t.Fatalf("requested provider = %q, want codex", requested.Provider)
	}
	completed, ok := events[len(events)-1].(ModelCompleted)
	if !ok {
		t.Fatalf("last event = %T, want ModelCompleted", events[len(events)-1])
	}
	if completed.Provider != "codex" {
		t.Fatalf("completed provider = %q, want codex", completed.Provider)
	}
}

func TestAgentStepEmitsUsageEventsFromModelResponse(t *testing.T) {
	var events []event.Event
	ctx := testAgentContext{events: event.SinkFunc(func(evt event.Event) {
		events = append(events, evt)
	})}
	runtime, err := New(agent.Spec{Name: "main", Inference: agent.InferenceSpec{Model: "test-model"}}, StaticModel{
		Response: Response{
			Message: &agent.Message{Content: "ok"},
			Usage: []usage.Recorded{{
				Source: "test",
				Subject: usage.Subject{
					Kind:     usage.SubjectLLM,
					Provider: "test",
					Name:     "test-model",
				},
				Measurements: []usage.Measurement{{
					Metric:   usage.MetricLLMInputTokens,
					Quantity: 12,
					Unit:     usage.UnitToken,
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(ctx, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	var got usage.Recorded
	for _, evt := range events {
		if recorded, ok := evt.(usage.Recorded); ok {
			got = recorded
			break
		}
	}
	if got.Subject.Name != "test-model" || len(got.Measurements) != 1 {
		t.Fatalf("usage event = %#v, want test-model measurement", got)
	}
}

func TestAgentStepStreamingIsOptInAndRedactsThinkingByDefault(t *testing.T) {
	var events []event.Event
	ctx := testAgentContext{events: event.SinkFunc(func(evt event.Event) {
		events = append(events, evt)
	})}
	runtime, err := New(agent.Spec{Name: "main"}, streamingModel{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(ctx, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want requested/completed only: %#v", len(events), events)
	}
}

func TestAgentStepStreamingCanEmitContentAndToolCallDeltas(t *testing.T) {
	var streamed []ModelStreamed
	ctx := testAgentContext{events: event.SinkFunc(func(evt event.Event) {
		if payload, ok := evt.(ModelStreamed); ok {
			streamed = append(streamed, payload)
		}
	})}
	runtime, err := New(
		agent.Spec{Name: "main"},
		streamingModel{},
		WithStreamPolicy(StreamPolicy{EmitContent: true, EmitToolCall: true}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(ctx, agent.StepInput{})

	if result.Status != agent.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(streamed) != 2 {
		t.Fatalf("streamed len = %d, want content/tool deltas: %#v", len(streamed), streamed)
	}
	if streamed[0].Event.Kind != StreamContentDelta {
		t.Fatalf("event[0] = %#v, want content delta", streamed[0].Event)
	}
	if streamed[1].Event.Kind != StreamToolCallDelta {
		t.Fatalf("event[1] = %#v, want tool call delta", streamed[1].Event)
	}
}

func TestAgentStepReturnsFailureForModelError(t *testing.T) {
	runtime, err := New(agent.Spec{Name: "main"}, StaticModel{Err: errors.New("boom")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result := runtime.Step(testAgentContext{}, agent.StepInput{})

	if result.Status != agent.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "model_failed" {
		t.Fatalf("error = %#v, want model_failed", result.Error)
	}
}

func TestNewRejectsUnsupportedDriverKind(t *testing.T) {
	_, err := New(agent.Spec{
		Name:   "main",
		Driver: agent.DriverSpec{Kind: "rule"},
	}, StaticModel{})
	if err == nil {
		t.Fatal("New error is nil, want unsupported driver kind")
	}
}

type testAgentContext struct {
	events event.Sink
}

func (c testAgentContext) Deadline() (deadline time.Time, ok bool) {
	return time.Time{}, false
}

func (c testAgentContext) Done() <-chan struct{} { return nil }

func (c testAgentContext) Err() error { return nil }

func (c testAgentContext) Value(any) any { return nil }

func (c testAgentContext) Events() event.Sink {
	if c.events == nil {
		return event.Discard()
	}
	return c.events
}

type contextProviderFunc struct {
	spec  corecontext.ProviderSpec
	build func(context.Context, corecontext.Request) ([]corecontext.Block, error)
}

func (p contextProviderFunc) Spec() corecontext.ProviderSpec { return p.spec }

func (p contextProviderFunc) Build(ctx context.Context, req corecontext.Request) ([]corecontext.Block, error) {
	return p.build(ctx, req)
}

func hasContextBlock(blocks []corecontext.Block, provider corecontext.ProviderName, content string) bool {
	for _, block := range blocks {
		if provider != "" && block.Provider != provider {
			continue
		}
		if strings.Contains(block.Content, content) {
			return true
		}
	}
	return false
}

type streamingModel struct{}

func (streamingModel) Complete(context.Context, Request) (Response, error) {
	return MessageResponse("fallback"), nil
}

func (streamingModel) Stream(_ context.Context, _ Request, emit StreamFunc) (Response, error) {
	emit(StreamEvent{Kind: StreamThinkingDelta, Text: "hidden reasoning"})
	emit(StreamEvent{Kind: StreamContentDelta, Text: "hello"})
	emit(StreamEvent{Kind: StreamToolCallDelta, Tool: "inspect"})
	return MessageResponse("hello"), nil
}

type identifiedModel struct {
	identity coreconversation.ProviderIdentity
	response Response
}

func (m identifiedModel) Complete(context.Context, Request) (Response, error) {
	return m.response, nil
}

func (m identifiedModel) ProviderIdentity(Request) coreconversation.ProviderIdentity {
	return m.identity
}

type capturingIdentifiedModel struct {
	identity coreconversation.ProviderIdentity
	response Response
	got      *Request
}

func (m capturingIdentifiedModel) Complete(_ context.Context, req Request) (Response, error) {
	if m.got != nil {
		*m.got = req
	}
	return m.response, nil
}

func (m capturingIdentifiedModel) ProviderIdentity(Request) coreconversation.ProviderIdentity {
	return m.identity
}
