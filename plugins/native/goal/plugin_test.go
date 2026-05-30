package goal

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	coregoal "github.com/fluxplane/fluxplane-core/core/goal"
	"github.com/fluxplane/fluxplane-core/core/operation"
	corereview "github.com/fluxplane/fluxplane-core/core/review"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-core/orchestration/sessioncontrol"
	"github.com/fluxplane/fluxplane-core/runtime/eventstore"
	runtimegoal "github.com/fluxplane/fluxplane-core/runtime/goal"
	runtimethread "github.com/fluxplane/fluxplane-core/runtime/thread"
	"github.com/fluxplane/fluxplane-policy"
)

func TestExecuteCommandSetsDurableThreadGoal(t *testing.T) {
	ctx := context.Background()
	threadStore := testThreadStore(t, "thread-goal")
	s := session.Session{ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-goal"}}

	result := ExecuteCommand(s, ctx, inbound("run-goal", "Test coverage has increased to 90%"), CommandSpec(), sessioncontrol.PolicyEvaluation{Decision: policy.DecisionAllow})
	if result.Status != session.CommandStatusOK {
		t.Fatalf("status = %q error = %#v, want ok", result.Status, result.Error)
	}
	output, ok := result.Output.(string)
	if !ok || !strings.Contains(output, "Goal set.") || !strings.Contains(output, "Test coverage has increased to 90%") {
		t.Fatalf("output = %#v, want goal status", result.Output)
	}
	state := projectedState(t, threadStore, "thread-goal")
	if state.Status != coregoal.StatusActive || state.Text != "Test coverage has increased to 90%" {
		t.Fatalf("goal state = %#v, want active goal", state)
	}
	if len(state.AcceptanceCriteria) != 1 {
		t.Fatalf("criteria = %#v, want generated acceptance criterion", state.AcceptanceCriteria)
	}
}

func TestExecuteCommandLifecycle(t *testing.T) {
	ctx := context.Background()
	threadStore := testThreadStore(t, "thread-goal-life")
	s := session.Session{ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-goal-life"}}

	if result := ExecuteCommand(s, ctx, inbound("set", "Ship durable goals"), CommandSpec(), sessioncontrol.PolicyEvaluation{Decision: policy.DecisionAllow}); result.Status != session.CommandStatusOK {
		t.Fatalf("set result = %#v, want ok", result)
	}
	if result := ExecuteCommand(s, ctx, inbound("pause", "pause"), CommandSpec(), sessioncontrol.PolicyEvaluation{Decision: policy.DecisionAllow}); result.Status != session.CommandStatusOK {
		t.Fatalf("pause result = %#v, want ok", result)
	}
	if state := projectedState(t, threadStore, "thread-goal-life"); state.Status != coregoal.StatusPaused {
		t.Fatalf("paused state = %#v, want paused", state)
	}
	if result := ExecuteCommand(s, ctx, inbound("resume", "resume"), CommandSpec(), sessioncontrol.PolicyEvaluation{Decision: policy.DecisionAllow}); result.Status != session.CommandStatusOK {
		t.Fatalf("resume result = %#v, want ok", result)
	}
	if state := projectedState(t, threadStore, "thread-goal-life"); state.Status != coregoal.StatusActive {
		t.Fatalf("resumed state = %#v, want active", state)
	}
	status := ExecuteCommand(s, ctx, inbound("status"), CommandSpec(), sessioncontrol.PolicyEvaluation{Decision: policy.DecisionAllow})
	if status.Status != session.CommandStatusOK || !strings.Contains(fmt.Sprint(status.Output), "Ship durable goals") {
		t.Fatalf("status result = %#v, want visible goal", status)
	}
	if result := ExecuteCommand(s, ctx, inbound("clear", "clear"), CommandSpec(), sessioncontrol.PolicyEvaluation{Decision: policy.DecisionAllow}); result.Status != session.CommandStatusOK {
		t.Fatalf("clear result = %#v, want ok", result)
	}
	if state := projectedState(t, threadStore, "thread-goal-life"); state.Visible() {
		t.Fatalf("cleared state = %#v, want hidden", state)
	}
}

func TestParseCommandInputRejectsLegacyMax(t *testing.T) {
	_, err := parseCommandInput(command.Invocation{
		Path: command.Path{"goal"},
		Args: []string{"Test coverage has increased to 90%"},
		Input: map[string]any{
			"max": 0,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "no longer accepts") {
		t.Fatalf("parseCommandInput error = %v, want legacy max rejected", err)
	}
}

func TestSessionCommandsContributesGoalHandler(t *testing.T) {
	bindings, err := (Plugin{}).SessionCommands(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("SessionCommands: %v", err)
	}
	if len(bindings) != 1 || bindings[0].Spec.Path.String() != "/goal" || bindings[0].Handler == nil {
		t.Fatalf("bindings = %#v, want goal handler", bindings)
	}
}

func TestReviewDecisionHandlerRecordsGoalReviewDecision(t *testing.T) {
	ctx := context.Background()
	threadStore := testThreadStore(t, "thread-goal-review")
	session := session.Session{ThreadStore: threadStore, Thread: corethread.Ref{ID: "thread-goal-review"}}
	if err := session.AppendThreadEvents(ctx, coregoal.Set{GoalID: "goal_review_target", ThreadID: "thread-goal-review", Text: "Ship the goal verifier"}); err != nil {
		t.Fatalf("append goal: %v", err)
	}
	handler := reviewDecisionHandler(threadStore)
	rejected := handler(operation.NewContext(ctx, nil), ReviewDecisionInput{
		ThreadID: "thread-goal-review",
		GoalID:   "goal_review_target",
		ReviewID: "review_rejected",
		RunID:    "run-review",
		Decision: "rejected",
		Summary:  "tests are missing",
		Suggestions: []corereview.Suggestion{{
			Text: "add smoke tests",
		}},
	})
	if rejected.Status != operation.StatusOK {
		t.Fatalf("rejected result = %#v, want ok", rejected)
	}
	if state := projectedState(t, threadStore, "thread-goal-review"); state.Status != coregoal.StatusRejected || state.LatestReview == nil || state.LatestReview.ReviewID != "review_rejected" {
		t.Fatalf("rejected state = %#v, want latest rejected review", state)
	}

	reached := handler(operation.NewContext(ctx, nil), ReviewDecisionInput{
		ThreadID: "thread-goal-review",
		GoalID:   "goal_review_target",
		ReviewID: "review_reached",
		RunID:    "run-review",
		Decision: "reached",
		Summary:  "goal satisfied",
		Evidence: []corereview.Evidence{{
			Kind:    "test",
			Summary: "smoke tests passed",
		}},
	})
	if reached.Status != operation.StatusOK {
		t.Fatalf("reached result = %#v, want ok", reached)
	}
	if state := projectedState(t, threadStore, "thread-goal-review"); state.Status != coregoal.StatusReached || state.LatestReview == nil || state.LatestReview.ReviewID != "review_reached" {
		t.Fatalf("reached state = %#v, want latest reached review", state)
	}
}

func inbound(id string, args ...string) channel.Inbound {
	return channel.Inbound{
		ID:     id,
		Kind:   channel.InboundCommand,
		Caller: policy.Caller{Kind: policy.CallerUser},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Command: &command.Invocation{
			Path: command.Path{"goal"},
			Args: args,
		},
	}
}

func testThreadStore(t *testing.T, id corethread.ID) *runtimethread.Store {
	t.Helper()
	store, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("thread store: %v", err)
	}
	if _, err := store.Create(context.Background(), corethread.CreateParams{ID: id}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	return store
}

func projectedState(t *testing.T, store corethread.Store, id corethread.ID) coregoal.State {
	t.Helper()
	snapshot, err := store.Read(context.Background(), corethread.ReadParams{ID: id})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	state, err := runtimegoal.ProjectThread(snapshot)
	if err != nil {
		t.Fatalf("project goal: %v", err)
	}
	return state
}
