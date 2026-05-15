// Package sessioncontrol centralizes session control-plane helpers that would
// otherwise couple the main session loop to resource, policy, invocation, and
// model-driver implementation packages.
package sessioncontrol

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	corellmagent "github.com/fluxplane/agentruntime/core/agent/llmagent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/tool"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

// Resolver aliases the core resource resolver used by session configuration.
type Resolver = resource.Resolver

// ResourceID aliases the canonical resource identity used by session catalogs.
type ResourceID = resource.ResourceID

// PolicyEvaluation aliases the policy evaluation returned by command dispatch.
type PolicyEvaluation = policy.Evaluation

// StopEvaluator evaluates continuation stop conditions that require model
// judgment.
type StopEvaluator interface {
	EvaluateStopCondition(context.Context, StopEvaluationInput) (StopEvaluation, error)
}

// StopEvaluationInput describes one outer-loop continuation decision.
type StopEvaluationInput struct {
	Agent            agent.Spec
	Condition        agent.StopConditionSpec
	Inbound          channel.Inbound
	AgentResult      agent.StepResult
	Effects          []environment.EffectResult
	Completed        int
	MaxContinuations int
}

type StopAction string

const (
	StopActionStop     StopAction = "stop"
	StopActionContinue StopAction = "continue"
)

// StopEvaluation is the normalized stop-condition decision.
type StopEvaluation struct {
	Action              StopAction `json:"action" jsonschema:"description=Whether the parent agent should stop or continue.,enum=stop,enum=continue,required"`
	Reason              string     `json:"reason,omitempty" jsonschema:"description=Brief reason for the decision."`
	ContinueInstruction string     `json:"continue_instruction,omitempty" jsonschema:"description=Instruction to send back to the parent agent when action is continue."`
}

// ModelStopEvaluator evaluates prompt stop conditions with a provider-neutral
// LLM model and a typed synthetic decision operation.
type ModelStopEvaluator struct {
	Model llmagent.Model
}

// EvaluateStopCondition implements StopEvaluator.
func (e ModelStopEvaluator) EvaluateStopCondition(ctx context.Context, input StopEvaluationInput) (StopEvaluation, error) {
	if e.Model == nil {
		return StopEvaluation{}, fmt.Errorf("stop evaluator model is nil")
	}
	req := llmagent.Request{
		Agent: evaluatorAgentSpec(input.Agent),
		Tools: []tool.Spec{continuationDecisionToolSpec()},
		Goal:  StopEvaluatorGoal(input),
	}
	var (
		response llmagent.Response
		err      error
	)
	if streaming, ok := e.Model.(llmagent.StreamingModel); ok {
		response, err = streaming.Stream(ctx, req, nil)
	} else {
		response, err = e.Model.Complete(ctx, req)
	}
	if err != nil {
		return StopEvaluation{}, err
	}
	if len(response.Operations) != 1 {
		return StopEvaluation{}, fmt.Errorf("stop evaluator must call continuation_decision exactly once")
	}
	request := response.Operations[0]
	if request.Operation.Name != continuationDecisionOperationName {
		return StopEvaluation{}, fmt.Errorf("stop evaluator called %q, want %q", request.Operation.String(), continuationDecisionOperationName)
	}
	return executeContinuationDecision(ctx, request.Input)
}

// ContextWithTranscript marks a session context as carrying a projected
// transcript and already-rendered provider context.
func ContextWithTranscript(ctx context.Context, transcript *coreconversation.Transcript) context.Context {
	return llmagent.ContextWithMaterializedContext(llmagent.ContextWithTranscript(ctx, transcript))
}

// IsLLMDriverKind reports whether kind is the built-in LLM driver.
func IsLLMDriverKind(kind agent.DriverKind) bool {
	return strings.TrimSpace(string(kind)) == string(llmagent.DriverKind)
}

type driverSpecAgent interface {
	DriverSpec() corellmagent.Spec
}

// OutputReserveTokens returns the output token reserve for an agent, respecting
// both authored inference settings and concrete LLM driver settings.
func OutputReserveTokens(agent agent.Agent, minimum int) int {
	reserve := minimum
	if agent == nil {
		return reserve
	}
	spec := agent.Spec()
	reserve = maxInt(reserve, spec.Inference.MaxOutputTokens)
	if value := intAnnotation(spec.Inference.Annotations, "llm.max_output_tokens"); value > 0 {
		reserve = maxInt(reserve, value)
	}
	if driver, ok := agent.(driverSpecAgent); ok {
		driverSpec := driver.DriverSpec()
		reserve = maxInt(reserve, driverSpec.Inference.MaxOutputTokens)
	}
	return reserve
}

// ContextCommandSpec is the built-in session command that previews model
// context.
var ContextCommandSpec = command.Spec{
	Path:        command.Path{"context"},
	Description: "Preview context that would be sent to the LLM.",
	Target:      invocation.Target{Kind: invocation.TargetSession},
	Policy: policy.InvocationPolicy{
		AllowedCallers: []policy.CallerKind{policy.CallerUser, policy.CallerSystem},
		RequiredTrust:  policy.TrustVerified,
	},
}

// CompactCommandSpec is the built-in session command that compacts transcript
// replay.
var CompactCommandSpec = command.Spec{
	Path:        command.Path{"compact"},
	Description: "Compact provider transcript replay for the current thread.",
	Target:      invocation.Target{Kind: invocation.TargetSession},
	Policy: policy.InvocationPolicy{
		AllowedCallers: []policy.CallerKind{policy.CallerUser, policy.CallerSystem},
		RequiredTrust:  policy.TrustVerified,
	},
}

// GoalCommandSpec is the built-in session command that runs goal continuations.
var GoalCommandSpec = command.Spec{
	Path:        command.Path{"goal"},
	Description: "Run a goal-driven task until the goal is complete or the continuation cap is reached.",
	Target:      invocation.Target{Kind: invocation.TargetSession},
	Policy: policy.InvocationPolicy{
		AllowedCallers: []policy.CallerKind{policy.CallerUser, policy.CallerSystem},
		RequiredTrust:  policy.TrustVerified,
	},
}

// EvaluateInvocation evaluates a command invocation policy.
func EvaluateInvocation(spec command.Spec, caller policy.Caller, trust policy.Trust) PolicyEvaluation {
	return policy.EvaluateInvocation(spec.Policy, caller, trust)
}

// PolicyDenied reports whether evaluation denied a command.
func PolicyDenied(evaluation PolicyEvaluation) bool {
	return evaluation.Decision == policy.DecisionDeny
}

// PolicyApprovalRequired reports whether evaluation requires explicit approval.
func PolicyApprovalRequired(evaluation PolicyEvaluation) bool {
	return evaluation.Decision == policy.DecisionApprovalRequired
}

// TargetsSession reports whether spec targets the session boundary.
func TargetsSession(spec command.Spec) bool {
	return spec.Target.Kind == invocation.TargetSession
}

// TargetsOperation reports whether spec targets an operation.
func TargetsOperation(spec command.Spec) bool {
	return spec.Target.Kind == invocation.TargetOperation
}

// NewResourceIndex returns an empty core resource index.
func NewResourceIndex() *resource.ResourceIndex {
	return resource.NewResourceIndex()
}

// NewResolver returns a resolver backed by index.
func NewResolver(index *resource.ResourceIndex) *resource.Resolver {
	return resource.NewResolver(resource.ResolverConfig{Index: index})
}

// ResolveResource resolves ref for kind.
func ResolveResource(resolver *resource.Resolver, kind, ref string) (resource.ResourceID, error) {
	return resolver.Resolve(kind, ref)
}

// ResolveResourceInScope resolves ref for kind within scope.
func ResolveResourceInScope(resolver *resource.Resolver, kind, ref string, scope resource.ResourceID) (resource.ResourceID, error) {
	return resolver.ResolveInScope(kind, ref, scope)
}

// EvaluateStopCondition evaluates a continuation stop condition tree.
func EvaluateStopCondition(ctx context.Context, condition agent.StopConditionSpec, input StopEvaluationInput, evaluator StopEvaluator) (StopEvaluation, error) {
	switch strings.TrimSpace(condition.Type) {
	case "":
		return StopEvaluation{Action: StopActionStop}, nil
	case "max-continuations":
		if condition.Max > 0 && input.Completed >= condition.Max {
			return StopEvaluation{Action: StopActionStop, Reason: "stop condition max reached"}, nil
		}
		return StopEvaluation{Action: StopActionContinue, ContinueInstruction: "Continue.", Reason: "max-continuations stop condition requested another continuation"}, nil
	case "prompt":
		if evaluator == nil {
			return StopEvaluation{}, fmt.Errorf("prompt stop condition requires a stop evaluator")
		}
		input.Condition = condition
		return evaluator.EvaluateStopCondition(ctx, input)
	case "agent":
		return StopEvaluation{}, fmt.Errorf("agent stop conditions require typed subagent decision tools and are not implemented")
	case "or":
		for _, child := range condition.Conditions {
			evaluation, err := EvaluateStopCondition(ctx, child, input, evaluator)
			if err != nil {
				return StopEvaluation{}, err
			}
			if evaluation.Action == StopActionContinue {
				return evaluation, nil
			}
		}
		return StopEvaluation{Action: StopActionStop}, nil
	case "and":
		var out StopEvaluation
		for _, child := range condition.Conditions {
			evaluation, err := EvaluateStopCondition(ctx, child, input, evaluator)
			if err != nil {
				return StopEvaluation{}, err
			}
			if evaluation.Action != StopActionContinue {
				return StopEvaluation{Action: StopActionStop, Reason: evaluation.Reason}, nil
			}
			out = evaluation
		}
		if out.Action == "" {
			out.Action = StopActionStop
		}
		return out, nil
	default:
		return StopEvaluation{}, fmt.Errorf("unsupported stop condition type %q", condition.Type)
	}
}

func evaluatorAgentSpec(parent agent.Spec) agent.Spec {
	return agent.Spec{
		Name:      agent.Name(string(parent.Name) + "-stop-evaluator"),
		System:    stopEvaluatorSystemPrompt(),
		Inference: parent.Inference,
	}
}

const continuationDecisionOperationName operation.Name = "continuation_decision"

func continuationDecisionToolSpec() tool.Spec {
	spec := continuationDecisionOperation().Spec()
	return tool.Spec{
		Name:        tool.Name(spec.Ref.Name),
		Description: spec.Description,
		Target:      invocation.Target{Kind: invocation.TargetOperation, Operation: spec.Ref},
		Input:       spec.Input,
		Output:      spec.Output,
	}
}

func continuationDecisionOperation() operation.Operation {
	return operationruntime.NewTyped[StopEvaluation, StopEvaluation](operation.Spec{
		Ref:         operation.Ref{Name: continuationDecisionOperationName},
		Description: "Return whether the parent agent should stop or continue.",
	}, func(_ operation.Context, input StopEvaluation) (StopEvaluation, error) {
		return normalizeStopEvaluation(input)
	})
}

func executeContinuationDecision(_ context.Context, input operation.Value) (StopEvaluation, error) {
	out, err := operationruntime.Bind[StopEvaluation](input)
	if err != nil {
		return StopEvaluation{}, fmt.Errorf("continuation decision failed: %w", err)
	}
	return normalizeStopEvaluation(out)
}

func stopEvaluatorSystemPrompt() string {
	return "You evaluate whether an agent should stop or continue. You must call the continuation_decision tool exactly once. Do not answer in text."
}

// StopEvaluatorGoal renders the model prompt for a continuation stop decision.
func StopEvaluatorGoal(input StopEvaluationInput) string {
	var b strings.Builder
	b.WriteString("Evaluate this continuation stop condition.\n\n")
	writeStopEvaluationContext(&b, input)
	fmt.Fprintf(&b, "Completed continuations: %d\n", input.Completed)
	fmt.Fprintf(&b, "Maximum continuations: %d\n\n", input.MaxContinuations)
	b.WriteString("Call continuation_decision with action stop or continue.")
	return b.String()
}

func writeStopEvaluationContext(b *strings.Builder, input StopEvaluationInput) {
	contextPolicy := strings.TrimSpace(input.Agent.Turns.Continuation.ContextPolicy)
	if contextPolicy == "" {
		contextPolicy = "inherit"
	}
	switch contextPolicy {
	case "summary", "new":
		writeStopEvaluationSummary(b, input)
	default:
		writeStopEvaluationInheritedContext(b, input)
	}
}

func writeStopEvaluationSummary(b *strings.Builder, input StopEvaluationInput) {
	if input.Condition.Prompt != "" {
		b.WriteString("Stop condition:\n")
		b.WriteString(input.Condition.Prompt)
		b.WriteString("\n\n")
	}
	if input.Inbound.Message != nil {
		b.WriteString("Original user input:\n")
		fmt.Fprint(b, input.Inbound.Message.Content)
		b.WriteString("\n\n")
	}
	if input.AgentResult.Decision.Message != nil {
		b.WriteString("Latest agent response:\n")
		fmt.Fprint(b, input.AgentResult.Decision.Message.Content)
		b.WriteString("\n\n")
	}
	if len(input.Effects) > 0 {
		fmt.Fprintf(b, "Operation effects observed: %d\n\n", len(input.Effects))
	}
}

func writeStopEvaluationInheritedContext(b *strings.Builder, input StopEvaluationInput) {
	writeStopEvaluationSummary(b, input)
	if len(input.Effects) == 0 {
		return
	}
	b.WriteString("Operation effect details:\n")
	for i, effect := range input.Effects {
		fmt.Fprintf(b, "- effect %d: %s\n", i+1, truncateText(fmt.Sprint(effect.Result.Output), 2000))
		if effect.Result.Error != nil {
			fmt.Fprintf(b, "  error: %s\n", truncateText(effect.Result.Error.Message, 1000))
		}
	}
	b.WriteString("\n")
}

func normalizeStopEvaluation(out StopEvaluation) (StopEvaluation, error) {
	action := StopAction(strings.ToLower(strings.TrimSpace(string(out.Action))))
	if action != StopActionStop && action != StopActionContinue {
		return StopEvaluation{}, fmt.Errorf("stop evaluation action must be stop or continue")
	}
	out.Action = action
	return out, nil
}

func intAnnotation(values map[string]string, key string) int {
	if len(values) == 0 {
		return 0
	}
	value := strings.TrimSpace(values[key])
	if value == "" {
		return 0
	}
	var out int
	if _, err := fmt.Sscan(value, &out); err != nil {
		return 0
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncateText(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max]
}
