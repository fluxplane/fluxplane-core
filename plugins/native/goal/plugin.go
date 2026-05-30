package goal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coregoal "github.com/fluxplane/fluxplane-core/core/goal"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	corereview "github.com/fluxplane/fluxplane-core/core/review"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-core/orchestration/sessioncontrol"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
	runtimegoal "github.com/fluxplane/fluxplane-core/runtime/goal"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimethread "github.com/fluxplane/fluxplane-core/runtime/thread"
	"github.com/fluxplane/fluxplane-policy"
)

const (
	Name                = "goal"
	Command             = "goal"
	ContextProviderName = "session_goal"
	ReviewDecisionOp    = "goal_review_decision"
	ReviewerAgent       = "goal-reviewer"
	ReviewerSession     = "goal-reviewer"
)

type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.SessionCommandContributor = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

func New() Plugin { return Plugin{} }

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Durable session goal lifecycle and context."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Operations: []operation.Spec{reviewDecisionSpec()},
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Goal lifecycle and verifier operations.",
			Operations:  []operation.Ref{{Name: ReviewDecisionOp}},
		}},
		Agents: []agent.Spec{reviewerAgentSpec()},
		Sessions: []coresession.Spec{{
			Name:        ReviewerSession,
			Description: "Isolated goal verifier session.",
			Agent:       agent.Ref{Name: ReviewerAgent},
			Operations:  []operation.Ref{{Name: ReviewDecisionOp}},
			Metadata:    map[string]string{"role": "goal_reviewer"},
		}},
		ContextProviders: []corecontext.ProviderSpec{{
			Name:             ContextProviderName,
			Description:      "Current durable thread goal.",
			Kinds:            []corecontext.BlockKind{corecontext.BlockData},
			DefaultPlacement: corecontext.PlacementSystem,
			Annotations: map[string]string{
				corecontext.AnnotationAutoContext: "true",
			},
		}},
	}, nil
}

func (Plugin) SessionCommands(context.Context, pluginhost.Context) ([]session.SessionCommandBinding, error) {
	return []session.SessionCommandBinding{{
		Spec:    CommandSpec(),
		Handler: ExecuteCommand,
	}}, nil
}

func (Plugin) ContextProviders(_ context.Context, ctx pluginhost.Context) ([]corecontext.Provider, error) {
	if ctx.EventStore == nil {
		return nil, nil
	}
	store, err := runtimethread.NewStore(ctx.EventStore)
	if err != nil {
		return nil, err
	}
	return []corecontext.Provider{ContextProvider{Store: store}}, nil
}

func (Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	if ctx.EventStore == nil {
		return nil, nil
	}
	store, err := runtimethread.NewStore(ctx.EventStore)
	if err != nil {
		return nil, err
	}
	return []operation.Operation{operationruntime.NewTypedResult[ReviewDecisionInput, ReviewDecisionOutput](reviewDecisionSpec(), reviewDecisionHandler(store))}, nil
}

func CommandSpec() command.Spec {
	return command.Spec{
		Path:        command.Path{Command},
		Description: "Show, set, pause, resume, or clear the durable thread goal.",
		Target:      invocation.Target{Kind: invocation.TargetSession},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser, policy.CallerSystem},
			RequiredTrust:  policy.TrustVerified,
		},
	}
}

func reviewDecisionSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: ReviewDecisionOp},
		Description: "Submit the bound decision for one goal verification review.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectUpdate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	}
}

func reviewerAgentSpec() agent.Spec {
	return agent.Spec{
		Name:        ReviewerAgent,
		Description: "Narrow verifier that decides whether the bound durable goal has been reached.",
		System: strings.Join([]string{
			"You are a goal verifier, not an implementation agent.",
			"Review only the provided goal, acceptance criteria, and evidence.",
			"Do not continue the implementation work.",
			"You must call goal_review_decision exactly once.",
			"Use decision=reached only when the evidence satisfies the goal and required acceptance criteria.",
			"Use decision=rejected when work remains, evidence is insufficient, or the goal is blocked; include concrete suggestions for the parent agent.",
		}, " "),
		Driver:     agent.DriverSpec{Kind: "llmagent"},
		Turns:      agent.TurnPolicy{MaxSteps: 3},
		Operations: []operation.Ref{{Name: ReviewDecisionOp}},
	}
}

type ReviewDecisionInput struct {
	ThreadID    string                  `json:"thread_id" jsonschema:"description=Parent thread id that owns the goal.,required"`
	GoalID      string                  `json:"goal_id" jsonschema:"description=Bound goal id to review.,required"`
	ReviewID    string                  `json:"review_id" jsonschema:"description=Bound review id from the review request.,required"`
	RunID       string                  `json:"run_id,omitempty" jsonschema:"description=Parent run id being reviewed."`
	Decision    string                  `json:"decision" jsonschema:"description=Review decision.,enum=reached,enum=rejected,required"`
	Summary     string                  `json:"summary,omitempty" jsonschema:"description=Short reason for the decision."`
	Evidence    []corereview.Evidence   `json:"evidence,omitempty" jsonschema:"description=Evidence supporting reached decisions."`
	Suggestions []corereview.Suggestion `json:"suggestions,omitempty" jsonschema:"description=Concrete next actions for rejected decisions."`
	Findings    []corereview.Finding    `json:"findings,omitempty" jsonschema:"description=Optional findings that explain the decision."`
}

type ReviewDecisionOutput struct {
	GoalID   string `json:"goal_id"`
	ReviewID string `json:"review_id"`
	Decision string `json:"decision"`
	Status   string `json:"status"`
}

func reviewDecisionHandler(store corethread.Store) operationruntime.TypedResultHandler[ReviewDecisionInput, ReviewDecisionOutput] {
	return func(ctx operation.Context, input ReviewDecisionInput) operation.Result {
		input.ThreadID = strings.TrimSpace(input.ThreadID)
		input.GoalID = strings.TrimSpace(input.GoalID)
		input.ReviewID = strings.TrimSpace(input.ReviewID)
		input.RunID = strings.TrimSpace(input.RunID)
		input.Decision = strings.TrimSpace(strings.ToLower(input.Decision))
		if store == nil {
			return operation.Failed("goal_review_unavailable", "goal thread store is unavailable", nil)
		}
		if input.ThreadID == "" || input.GoalID == "" || input.ReviewID == "" {
			return operation.Failed("invalid_goal_review_decision", "thread_id, goal_id, and review_id are required", nil)
		}
		state, err := currentState(ctx, store, corethread.Ref{ID: corethread.ID(input.ThreadID)})
		if err != nil {
			return operation.Failed("goal_review_state_failed", err.Error(), nil)
		}
		if state.ID != coregoal.ID(input.GoalID) || !state.ActiveForContinuation() {
			return operation.Failed("goal_review_stale", "goal is not the active review target", map[string]any{
				"goal_id":        input.GoalID,
				"current_goal":   string(state.ID),
				"current_status": string(state.Status),
			})
		}
		result := corereview.Result{
			ID:          corereview.ID(input.ReviewID),
			Decision:    corereview.DecisionRejected,
			Summary:     strings.TrimSpace(input.Summary),
			Findings:    input.Findings,
			Evidence:    input.Evidence,
			Suggestions: input.Suggestions,
		}
		payload := sessionenv.Event(nil)
		switch input.Decision {
		case "reached":
			result.Decision = corereview.DecisionAccepted
			if len(result.Evidence) == 0 {
				return operation.Failed("invalid_goal_review_decision", "reached decisions require evidence", nil)
			}
			payload = coregoal.Reached{GoalID: state.ID, Review: reviewFromDecision(ctx, state.ID, input, result), RunID: input.RunID}
		case "rejected":
			if len(result.Suggestions) == 0 {
				return operation.Failed("invalid_goal_review_decision", "rejected decisions require suggestions", nil)
			}
			payload = coregoal.Rejected{GoalID: state.ID, Review: reviewFromDecision(ctx, state.ID, input, result), RunID: input.RunID}
		default:
			return operation.Failed("invalid_goal_review_decision", "decision must be reached or rejected", nil)
		}
		if err := appendParentThreadEvent(ctx, store, corethread.ID(input.ThreadID), payload); err != nil {
			return operation.Failed("goal_review_append_failed", err.Error(), nil)
		}
		return operation.OK(ReviewDecisionOutput{GoalID: input.GoalID, ReviewID: input.ReviewID, Decision: input.Decision, Status: "recorded"})
	}
}

func reviewFromDecision(ctx context.Context, goalID coregoal.ID, input ReviewDecisionInput, result corereview.Result) coregoal.Review {
	scope, _ := sessionenv.ScopeFromContext(ctx)
	return coregoal.Review{
		ReviewID:         coregoal.ReviewID(input.ReviewID),
		GoalID:           goalID,
		RunID:            input.RunID,
		ReviewerThreadID: scope.Thread.ID,
		ReviewerRunID:    scope.RunID,
		Result:           result,
	}
}

func appendParentThreadEvent(ctx context.Context, store corethread.Store, threadID corethread.ID, payload sessionenv.Event) error {
	records := sessionenv.ThreadAppendRecords(corethread.Ref{ID: threadID}, payload)
	if len(records) == 0 {
		return nil
	}
	_, err := store.Append(ctx, corethread.Ref{ID: threadID}, records...)
	return err
}

type commandInput struct {
	Goal []string `json:"goal,omitempty" command:"arg"`
}

type commandMode string

const (
	commandStatus commandMode = "status"
	commandSet    commandMode = "set"
	commandPause  commandMode = "pause"
	commandResume commandMode = "resume"
	commandClear  commandMode = "clear"
)

type parsedCommand struct {
	Mode commandMode
	Text string
}

func ExecuteCommand(s session.Session, ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) session.CommandResult {
	action, err := parseCommandInput(*inbound.Command)
	if err != nil {
		return session.CommandResult{
			Status: session.CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &session.CommandError{Code: "invalid_goal_command_input", Message: err.Error()},
		}
	}
	output, err := applyCommand(s, ctx, inbound, action)
	if err != nil {
		return session.CommandResult{
			Status: session.CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &session.CommandError{Code: "goal_command_failed", Message: err.Error()},
		}
	}
	return session.CommandResult{Status: session.CommandStatusOK, Spec: spec, Policy: evaluation, Output: output}
}

func parseCommandInput(inv command.Invocation) (parsedCommand, error) {
	if err := rejectLegacyFlags(inv.Input); err != nil {
		return parsedCommand{}, err
	}
	input, err := command.Bind[commandInput](inv)
	if err != nil {
		return parsedCommand{}, err
	}
	if len(inv.Args) == 0 && inv.Input != nil {
		input = structuredCommandInput(inv.Input)
	}
	return validateCommandInput(input)
}

func structuredCommandInput(value any) commandInput {
	values, ok := value.(map[string]any)
	if !ok {
		return commandInput{}
	}
	var input commandInput
	switch goal := values["goal"].(type) {
	case string:
		input.Goal = []string{goal}
	case []string:
		input.Goal = append([]string(nil), goal...)
	case []any:
		for _, item := range goal {
			input.Goal = append(input.Goal, fmt.Sprint(item))
		}
	}
	return input
}

func rejectLegacyFlags(value any) error {
	values, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"max", "max_continuations", "max-continuations"} {
		if _, ok := values[key]; ok {
			return fmt.Errorf("/goal no longer accepts %q; use /goal <goal>, /goal pause, /goal resume, or /goal clear", key)
		}
	}
	return nil
}

func validateCommandInput(input commandInput) (parsedCommand, error) {
	text := strings.TrimSpace(strings.Join(input.Goal, " "))
	if text == "" {
		return parsedCommand{Mode: commandStatus}, nil
	}
	if len(input.Goal) == 1 {
		switch strings.ToLower(strings.TrimSpace(input.Goal[0])) {
		case "status":
			return parsedCommand{Mode: commandStatus}, nil
		case "pause":
			return parsedCommand{Mode: commandPause}, nil
		case "resume":
			return parsedCommand{Mode: commandResume}, nil
		case "clear":
			return parsedCommand{Mode: commandClear}, nil
		}
	}
	if err := coregoal.ValidateText(text); err != nil {
		return parsedCommand{}, err
	}
	return parsedCommand{Mode: commandSet, Text: text}, nil
}

func applyCommand(s session.Session, ctx context.Context, inbound channel.Inbound, action parsedCommand) (any, error) {
	switch action.Mode {
	case commandStatus:
		state, err := currentState(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		return RenderStatus(state), nil
	case commandSet:
		state, err := currentState(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		goalID := newGoalID(s.Thread.ID, inbound.ID, action.Text)
		events := []sessionenv.Event{}
		if state.Visible() {
			events = append(events, coregoal.Archived{GoalID: state.ID, Reason: "superseded", SupersededBy: goalID, RunID: inbound.ID})
		}
		events = append(events,
			coregoal.Set{GoalID: goalID, ThreadID: s.Thread.ID, Text: action.Text, RunID: inbound.ID},
			coregoal.AcceptanceCriteriaGenerated{
				GoalID: goalID,
				Criteria: []coregoal.Criterion{{
					Description: "The requested goal has been completed: " + action.Text,
					Required:    true,
				}},
				RunID: inbound.ID,
			},
		)
		if err := s.AppendThreadEvents(ctx, events...); err != nil {
			return nil, err
		}
		return "Goal set.\n" + RenderStatus(coregoal.State{ID: goalID, ThreadID: s.Thread.ID, Status: coregoal.StatusActive, Text: action.Text, Revision: state.Revision + len(events)}), nil
	case commandPause:
		state, err := requireCurrent(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		if state.Status == coregoal.StatusPaused {
			return RenderStatus(state), nil
		}
		if err := s.AppendThreadEvents(ctx, coregoal.Paused{GoalID: state.ID, RunID: inbound.ID}); err != nil {
			return nil, err
		}
		state.Status = coregoal.StatusPaused
		return "Goal paused.\n" + RenderStatus(state), nil
	case commandResume:
		state, err := requireCurrent(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		if err := s.AppendThreadEvents(ctx, coregoal.Resumed{GoalID: state.ID, RunID: inbound.ID}); err != nil {
			return nil, err
		}
		state.Status = coregoal.StatusActive
		return "Goal resumed.\n" + RenderStatus(state), nil
	case commandClear:
		state, err := currentState(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		if !state.Visible() {
			return "No current goal.", nil
		}
		if err := s.AppendThreadEvents(ctx, coregoal.Cleared{GoalID: state.ID, RunID: inbound.ID}); err != nil {
			return nil, err
		}
		return "Goal cleared.", nil
	default:
		return nil, fmt.Errorf("unknown goal command mode %q", action.Mode)
	}
}

func currentState(ctx context.Context, store corethread.Store, thread corethread.Ref) (coregoal.State, error) {
	if store == nil || thread.ID == "" {
		return coregoal.State{}, fmt.Errorf("goal requires an active thread")
	}
	snapshot, err := store.Read(ctx, corethread.ReadParams{ID: thread.ID})
	if err != nil {
		return coregoal.State{}, err
	}
	if thread.BranchID != "" {
		snapshot.BranchID = thread.BranchID
	}
	return runtimegoal.ProjectThread(snapshot)
}

func requireCurrent(ctx context.Context, store corethread.Store, thread corethread.Ref) (coregoal.State, error) {
	state, err := currentState(ctx, store, thread)
	if err != nil {
		return coregoal.State{}, err
	}
	if !state.Visible() {
		return coregoal.State{}, fmt.Errorf("no current goal")
	}
	return state, nil
}

func RenderStatus(state coregoal.State) string {
	if !state.Visible() {
		return "No current goal."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", state.Text)
	fmt.Fprintf(&b, "Status: %s", state.Status)
	if len(state.AcceptanceCriteria) > 0 {
		b.WriteString("\nAcceptance criteria:")
		for _, criterion := range state.AcceptanceCriteria {
			description := strings.TrimSpace(criterion.Description)
			if description == "" {
				continue
			}
			b.WriteString("\n- ")
			b.WriteString(description)
		}
	}
	if state.LatestReview != nil {
		result := state.LatestReview.Result
		fmt.Fprintf(&b, "\nLatest review: %s", result.Decision)
		if summary := strings.TrimSpace(result.Summary); summary != "" {
			fmt.Fprintf(&b, " - %s", summary)
		}
	}
	return b.String()
}

func newGoalID(threadID corethread.ID, runID, text string) coregoal.ID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%s\n%d", threadID, runID, text, time.Now().UTC().UnixNano())))
	return coregoal.ID("goal_" + hex.EncodeToString(sum[:])[:16])
}
