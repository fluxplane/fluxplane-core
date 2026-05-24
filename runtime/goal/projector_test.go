package goal

import (
	"testing"

	coreevent "github.com/fluxplane/fluxplane-core/core/event"
	coregoal "github.com/fluxplane/fluxplane-core/core/goal"
	"github.com/fluxplane/fluxplane-core/core/review"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
)

func TestProjectThreadRecordsLifecycle(t *testing.T) {
	records := []corethread.Record{
		record(coregoal.Set{GoalID: "goal_1", ThreadID: "thread_1", Text: "Ship durable goals"}),
		record(coregoal.AcceptanceCriteriaGenerated{GoalID: "goal_1", Criteria: []review.Criterion{{Description: "Goal is complete", Required: true}}}),
		record(coregoal.Paused{GoalID: "goal_1"}),
		record(coregoal.Resumed{GoalID: "goal_1"}),
		record(coregoal.Rejected{GoalID: "goal_1", Review: coregoal.Review{Result: review.Result{
			Decision:    review.DecisionRejected,
			Summary:     "not done",
			Suggestions: []review.Suggestion{{Text: "finish tests"}},
		}}}),
		record(coregoal.Reached{GoalID: "goal_1", Review: coregoal.Review{Result: review.Result{
			Decision: review.DecisionAccepted,
			Summary:  "done",
			Evidence: []review.Evidence{{Summary: "tests pass"}},
		}}}),
	}

	state := ProjectThreadRecords("thread_1", records)
	if state.ID != "goal_1" || state.Status != coregoal.StatusReached || state.Text != "Ship durable goals" {
		t.Fatalf("state = %#v, want reached durable goal", state)
	}
	if len(state.AcceptanceCriteria) != 1 {
		t.Fatalf("criteria = %#v, want projected criterion", state.AcceptanceCriteria)
	}
	if state.LatestReview == nil || state.LatestReview.Result.Decision != review.DecisionAccepted {
		t.Fatalf("latest review = %#v, want accepted review", state.LatestReview)
	}
}

func TestProjectThreadRecordsClearHidesGoal(t *testing.T) {
	state := ProjectThreadRecords("thread_1", []corethread.Record{
		record(coregoal.Set{GoalID: "goal_1", ThreadID: "thread_1", Text: "Ship durable goals"}),
		record(coregoal.Cleared{GoalID: "goal_1"}),
	})
	if state.Visible() {
		t.Fatalf("state = %#v, want cleared goal hidden", state)
	}
}

func record(payload coreevent.Event) corethread.Record {
	return corethread.Record{
		ThreadID: "thread_1",
		BranchID: corethread.MainBranch,
		Event: coreevent.Record{
			Name:    payload.EventName(),
			Payload: payload,
		},
	}
}
