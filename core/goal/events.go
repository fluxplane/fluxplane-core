package goal

import (
	"fmt"

	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/review"
	corethread "github.com/fluxplane/engine/core/thread"
)

const (
	EventSetName                         event.Name = "goal.set"
	EventArchivedName                    event.Name = "goal.archived"
	EventAcceptanceCriteriaGeneratedName event.Name = "goal.acceptance_criteria_generated"
	EventPausedName                      event.Name = "goal.paused"
	EventResumedName                     event.Name = "goal.resumed"
	EventClearedName                     event.Name = "goal.cleared"
	EventReviewRequestedName             event.Name = "goal.review_requested"
	EventReachedName                     event.Name = "goal.reached"
	EventRejectedName                    event.Name = "goal.rejected"
	EventReviewFailedName                event.Name = "goal.review_failed"
)

type Set struct {
	GoalID   ID            `json:"goal_id"`
	ThreadID corethread.ID `json:"thread_id,omitempty"`
	Text     string        `json:"text"`
	RunID    string        `json:"run_id,omitempty"`
}

func (Set) EventName() event.Name { return EventSetName }

type Archived struct {
	GoalID       ID     `json:"goal_id"`
	Reason       string `json:"reason,omitempty"`
	SupersededBy ID     `json:"superseded_by,omitempty"`
	RunID        string `json:"run_id,omitempty"`
}

func (Archived) EventName() event.Name { return EventArchivedName }

type AcceptanceCriteriaGenerated struct {
	GoalID   ID                 `json:"goal_id"`
	Criteria []review.Criterion `json:"criteria,omitempty"`
	Warning  string             `json:"warning,omitempty"`
	RunID    string             `json:"run_id,omitempty"`
}

func (AcceptanceCriteriaGenerated) EventName() event.Name {
	return EventAcceptanceCriteriaGeneratedName
}

type Paused struct {
	GoalID ID     `json:"goal_id"`
	Reason string `json:"reason,omitempty"`
	RunID  string `json:"run_id,omitempty"`
}

func (Paused) EventName() event.Name { return EventPausedName }

type Resumed struct {
	GoalID ID     `json:"goal_id"`
	RunID  string `json:"run_id,omitempty"`
}

func (Resumed) EventName() event.Name { return EventResumedName }

type Cleared struct {
	GoalID ID     `json:"goal_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
}

func (Cleared) EventName() event.Name { return EventClearedName }

type ReviewRequested struct {
	GoalID   ID       `json:"goal_id"`
	ReviewID ReviewID `json:"review_id,omitempty"`
	RunID    string   `json:"run_id,omitempty"`
}

func (ReviewRequested) EventName() event.Name { return EventReviewRequestedName }

type Reached struct {
	GoalID ID     `json:"goal_id"`
	Review Review `json:"review"`
	RunID  string `json:"run_id,omitempty"`
}

func (Reached) EventName() event.Name { return EventReachedName }

type Rejected struct {
	GoalID ID     `json:"goal_id"`
	Review Review `json:"review"`
	RunID  string `json:"run_id,omitempty"`
}

func (Rejected) EventName() event.Name { return EventRejectedName }

type ReviewFailed struct {
	GoalID ID     `json:"goal_id,omitempty"`
	Reason string `json:"reason,omitempty"`
	RunID  string `json:"run_id,omitempty"`
}

func (ReviewFailed) EventName() event.Name { return EventReviewFailedName }

func RegisterEvents(registry *event.Registry) error {
	if registry == nil {
		return fmt.Errorf("goal: event registry is nil")
	}
	for _, sample := range []event.Event{
		Set{},
		Archived{},
		AcceptanceCriteriaGenerated{},
		Paused{},
		Resumed{},
		Cleared{},
		ReviewRequested{},
		Reached{},
		Rejected{},
		ReviewFailed{},
	} {
		if err := registry.Register(sample); err != nil {
			return err
		}
	}
	return nil
}
