// Package review defines inert review request and result contracts shared by
// review-oriented plugins and goal verification.
package review

import (
	"fmt"
	"strings"
)

type ID string
type RequestID string
type CriterionID string

type SubjectKind string

const (
	SubjectText     SubjectKind = "text"
	SubjectThread   SubjectKind = "thread"
	SubjectDiff     SubjectKind = "diff"
	SubjectGoal     SubjectKind = "goal"
	SubjectTask     SubjectKind = "task"
	SubjectResource SubjectKind = "resource"
)

type Request struct {
	ID           RequestID         `json:"id,omitempty"`
	Subject      Subject           `json:"subject"`
	Criteria     []Criterion       `json:"criteria,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Evidence     []EvidenceRef     `json:"evidence,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func (r Request) Validate() error {
	if err := r.Subject.Validate(); err != nil {
		return fmt.Errorf("review: subject: %w", err)
	}
	for i, criterion := range r.Criteria {
		if err := criterion.Validate(); err != nil {
			return fmt.Errorf("review: criteria[%d]: %w", i, err)
		}
	}
	return nil
}

type Subject struct {
	Kind     SubjectKind       `json:"kind"`
	Text     string            `json:"text,omitempty"`
	Refs     []Ref             `json:"refs,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (s Subject) Validate() error {
	switch s.Kind {
	case SubjectText, SubjectThread, SubjectDiff, SubjectGoal, SubjectTask, SubjectResource:
	default:
		return fmt.Errorf("kind %q is invalid", s.Kind)
	}
	if strings.TrimSpace(s.Text) == "" && len(s.Refs) == 0 {
		return fmt.Errorf("text or refs is required")
	}
	return nil
}

type Ref struct {
	Kind string `json:"kind,omitempty"`
	URI  string `json:"uri,omitempty"`
	Name string `json:"name,omitempty"`
}

type Criterion struct {
	ID          CriterionID       `json:"id,omitempty"`
	Description string            `json:"description"`
	Required    bool              `json:"required,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (c Criterion) Validate() error {
	if strings.TrimSpace(c.Description) == "" {
		return fmt.Errorf("description is required")
	}
	return nil
}

type EvidenceRef struct {
	Kind     string            `json:"kind,omitempty"`
	URI      string            `json:"uri,omitempty"`
	Summary  string            `json:"summary,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Decision string

const (
	DecisionAccepted     Decision = "accepted"
	DecisionRejected     Decision = "rejected"
	DecisionInconclusive Decision = "inconclusive"
)

type Result struct {
	ID              ID                `json:"id,omitempty"`
	RequestID       RequestID         `json:"request_id,omitempty"`
	Decision        Decision          `json:"decision"`
	Summary         string            `json:"summary,omitempty"`
	CriteriaResults []CriterionResult `json:"criteria_results,omitempty"`
	Findings        []Finding         `json:"findings,omitempty"`
	Evidence        []Evidence        `json:"evidence,omitempty"`
	Suggestions     []Suggestion      `json:"suggestions,omitempty"`
	Risk            string            `json:"risk,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

func (r Result) Validate() error {
	switch r.Decision {
	case DecisionAccepted, DecisionRejected, DecisionInconclusive:
	default:
		return fmt.Errorf("review: decision %q is invalid", r.Decision)
	}
	for i, result := range r.CriteriaResults {
		if err := result.Validate(); err != nil {
			return fmt.Errorf("review: criteria_results[%d]: %w", i, err)
		}
	}
	if r.Decision == DecisionAccepted && len(r.Evidence) == 0 {
		return fmt.Errorf("review: accepted result requires evidence")
	}
	if r.Decision == DecisionRejected && len(r.Suggestions) == 0 {
		return fmt.Errorf("review: rejected result requires suggestions")
	}
	return nil
}

type CriterionResult struct {
	CriterionID CriterionID `json:"criterion_id,omitempty"`
	Description string      `json:"description,omitempty"`
	Status      string      `json:"status"`
	Notes       string      `json:"notes,omitempty"`
}

func (r CriterionResult) Validate() error {
	switch strings.TrimSpace(r.Status) {
	case "met", "unmet", "unknown":
		return nil
	default:
		return fmt.Errorf("status %q is invalid", r.Status)
	}
}

type Finding struct {
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message"`
	Ref      Ref    `json:"ref,omitempty"`
}

type Evidence struct {
	Kind    string `json:"kind,omitempty"`
	Summary string `json:"summary"`
	Ref     Ref    `json:"ref,omitempty"`
}

type Suggestion struct {
	Text     string `json:"text"`
	Priority string `json:"priority,omitempty"`
}
