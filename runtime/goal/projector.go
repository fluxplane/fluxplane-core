// Package goal projects thread-scoped goal state from event streams.
package goal

import (
	"fmt"

	coregoal "github.com/fluxplane/fluxplane-core/core/goal"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
)

type State = coregoal.State

func ProjectThread(snapshot corethread.Snapshot) (coregoal.State, error) {
	records, err := snapshot.EventsForBranch(snapshot.BranchID)
	if err != nil {
		return coregoal.State{}, err
	}
	return ProjectThreadRecords(snapshot.ID, records), nil
}

func ProjectThreadRecords(threadID corethread.ID, records []corethread.Record) coregoal.State {
	var state coregoal.State
	for _, record := range records {
		state.Revision++
		switch evt := record.Event.Payload.(type) {
		case coregoal.Set:
			state = coregoal.State{ID: evt.GoalID, ThreadID: firstThreadID(evt.ThreadID, threadID), Status: coregoal.StatusActive, Text: evt.Text, Revision: state.Revision}
		case *coregoal.Set:
			if evt != nil {
				state = coregoal.State{ID: evt.GoalID, ThreadID: firstThreadID(evt.ThreadID, threadID), Status: coregoal.StatusActive, Text: evt.Text, Revision: state.Revision}
			}
		case coregoal.Archived:
			if state.ID == evt.GoalID {
				state.Status = coregoal.StatusArchived
				state.ArchivedReason = evt.Reason
				state.SupersededBy = evt.SupersededBy
			}
		case *coregoal.Archived:
			if evt != nil && state.ID == evt.GoalID {
				state.Status = coregoal.StatusArchived
				state.ArchivedReason = evt.Reason
				state.SupersededBy = evt.SupersededBy
			}
		case coregoal.AcceptanceCriteriaGenerated:
			if state.ID == evt.GoalID {
				state.AcceptanceCriteria = append([]coregoal.Criterion(nil), evt.Criteria...)
			}
		case *coregoal.AcceptanceCriteriaGenerated:
			if evt != nil && state.ID == evt.GoalID {
				state.AcceptanceCriteria = append([]coregoal.Criterion(nil), evt.Criteria...)
			}
		case coregoal.Paused:
			if state.ID == evt.GoalID {
				state.Status = coregoal.StatusPaused
			}
		case *coregoal.Paused:
			if evt != nil && state.ID == evt.GoalID {
				state.Status = coregoal.StatusPaused
			}
		case coregoal.Resumed:
			if state.ID == evt.GoalID {
				state.Status = coregoal.StatusActive
			}
		case *coregoal.Resumed:
			if evt != nil && state.ID == evt.GoalID {
				state.Status = coregoal.StatusActive
			}
		case coregoal.Cleared:
			if evt.GoalID == "" || state.ID == evt.GoalID {
				state.Status = coregoal.StatusCleared
			}
		case *coregoal.Cleared:
			if evt != nil && (evt.GoalID == "" || state.ID == evt.GoalID) {
				state.Status = coregoal.StatusCleared
			}
		case coregoal.Reached:
			if state.ID == evt.GoalID {
				state.Status = coregoal.StatusReached
				review := evt.Review
				state.LatestReview = &review
			}
		case *coregoal.Reached:
			if evt != nil && state.ID == evt.GoalID {
				state.Status = coregoal.StatusReached
				review := evt.Review
				state.LatestReview = &review
			}
		case coregoal.Rejected:
			if state.ID == evt.GoalID {
				state.Status = coregoal.StatusRejected
				review := evt.Review
				state.LatestReview = &review
			}
		case *coregoal.Rejected:
			if evt != nil && state.ID == evt.GoalID {
				state.Status = coregoal.StatusRejected
				review := evt.Review
				state.LatestReview = &review
			}
		}
	}
	return state
}

func firstThreadID(value, fallback corethread.ID) corethread.ID {
	if value != "" {
		return value
	}
	return fallback
}

func RequireVisible(state coregoal.State) error {
	if !state.Visible() {
		return fmt.Errorf("goal: no current goal")
	}
	return nil
}
