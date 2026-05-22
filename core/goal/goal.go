// Package goal defines inert thread-goal contracts and events.
package goal

import (
	"fmt"
	"strings"

	"github.com/fluxplane/engine/core/review"
	corethread "github.com/fluxplane/engine/core/thread"
)

type ID string
type ReviewID string
type Status string
type Criterion = review.Criterion

const (
	StatusActive   Status = "active"
	StatusPaused   Status = "paused"
	StatusRejected Status = "rejected"
	StatusReached  Status = "reached"
	StatusCleared  Status = "cleared"
	StatusArchived Status = "archived"
)

type State struct {
	ID                 ID                 `json:"id,omitempty"`
	ThreadID           corethread.ID      `json:"thread_id,omitempty"`
	Status             Status             `json:"status,omitempty"`
	Text               string             `json:"text,omitempty"`
	AcceptanceCriteria []review.Criterion `json:"acceptance_criteria,omitempty"`
	Revision           int                `json:"revision,omitempty"`
	LatestReview       *Review            `json:"latest_review,omitempty"`
	ArchivedReason     string             `json:"archived_reason,omitempty"`
	SupersededBy       ID                 `json:"superseded_by,omitempty"`
}

func (s State) ActiveForContinuation() bool {
	return s.ID != "" && (s.Status == StatusActive || s.Status == StatusRejected)
}

func (s State) Visible() bool {
	return s.ID != "" && s.Status != StatusCleared && s.Status != StatusArchived
}

type Review struct {
	ReviewID         ReviewID      `json:"review_id,omitempty"`
	GoalID           ID            `json:"goal_id,omitempty"`
	RunID            string        `json:"run_id,omitempty"`
	ReviewerThreadID corethread.ID `json:"reviewer_thread_id,omitempty"`
	ReviewerRunID    string        `json:"reviewer_run_id,omitempty"`
	Result           review.Result `json:"result"`
}

func NormalizeStatus(status Status) Status {
	switch status {
	case StatusActive, StatusPaused, StatusRejected, StatusReached, StatusCleared, StatusArchived:
		return status
	default:
		return ""
	}
}

func ValidateText(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("goal: text is required")
	}
	return nil
}
