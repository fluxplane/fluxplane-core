package review

import "testing"

func TestSubjectValidateAcceptsTextKind(t *testing.T) {
	s := Subject{Kind: SubjectText, Text: "some review text"}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubjectValidateAcceptsThreadKind(t *testing.T) {
	s := Subject{Kind: SubjectThread, Refs: []Ref{{Kind: "thread", URI: "thread-1"}}}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubjectValidateAcceptsDiffKind(t *testing.T) {
	s := Subject{Kind: SubjectDiff, Refs: []Ref{{Kind: "diff", URI: "diff-abc"}}}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubjectValidateAcceptsGoalKind(t *testing.T) {
	s := Subject{Kind: SubjectGoal, Text: "Review goal completion"}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubjectValidateAcceptsTaskKind(t *testing.T) {
	s := Subject{Kind: SubjectTask, Refs: []Ref{{Kind: "task", URI: "task-1"}}}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubjectValidateAcceptsResourceKind(t *testing.T) {
	s := Subject{Kind: SubjectResource, Refs: []Ref{{Kind: "file", URI: "file.go"}}}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubjectValidateRejectsInvalidKind(t *testing.T) {
	s := Subject{Kind: "bogus", Text: "some text"}
	err := s.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want invalid kind error")
	}
}

func TestSubjectValidateRejectsEmptyTextAndNoRefs(t *testing.T) {
	s := Subject{Kind: SubjectText}
	err := s.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want text/refs required error")
	}
	if err.Error() != "text or refs is required" {
		t.Errorf("Validate error = %q, want %q", err.Error(), "text or refs is required")
	}
}

func TestSubjectValidateRejectsWhitespaceTextOnly(t *testing.T) {
	s := Subject{Kind: SubjectText, Text: "   "}
	err := s.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want text/refs required error")
	}
}

func TestSubjectValidateAcceptsRefsOnly(t *testing.T) {
	s := Subject{Kind: SubjectDiff, Refs: []Ref{{Kind: "diff", URI: "diff-1"}}}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubjectValidateWithMetadata(t *testing.T) {
	s := Subject{
		Kind:     SubjectText,
		Text:     "some text",
		Metadata: map[string]string{"source": "agent", "priority": "high"},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCriterionValidateAcceptsValidCriterion(t *testing.T) {
	c := Criterion{ID: "c1", Description: "Code follows style guide", Required: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCriterionValidateRejectsEmptyDescription(t *testing.T) {
	c := Criterion{Description: ""}
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want description required error")
	}
	if err.Error() != "description is required" {
		t.Errorf("Validate error = %q, want %q", err.Error(), "description is required")
	}
}

func TestCriterionValidateRejectsWhitespaceDescription(t *testing.T) {
	c := Criterion{Description: "   "}
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want description required error")
	}
}

func TestCriterionValidateWithMetadata(t *testing.T) {
	c := Criterion{Description: "Check for bugs", Metadata: map[string]string{"severity": "high"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCriterionResultValidateAcceptsMetStatus(t *testing.T) {
	r := CriterionResult{CriterionID: "c1", Status: "met"}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCriterionResultValidateAcceptsUnmetStatus(t *testing.T) {
	r := CriterionResult{CriterionID: "c1", Status: "unmet"}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCriterionResultValidateAcceptsUnknownStatus(t *testing.T) {
	r := CriterionResult{CriterionID: "c1", Status: "unknown"}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCriterionResultValidateAcceptsStatusWithWhitespace(t *testing.T) {
	r := CriterionResult{CriterionID: "c1", Status: "  met  "}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCriterionResultValidateRejectsInvalidStatus(t *testing.T) {
	r := CriterionResult{CriterionID: "c1", Status: "bogus"}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want invalid status error")
	}
}

func TestRequestValidateAcceptsValidRequest(t *testing.T) {
	r := Request{
		ID:           "req-1",
		Subject:      Subject{Kind: SubjectText, Text: "Review my code"},
		Criteria:     []Criterion{{ID: "c1", Description: "Style guide compliance"}},
		Instructions: "Be thorough",
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRequestValidateAcceptsRequestWithEvidenceRefs(t *testing.T) {
	r := Request{
		Subject: Subject{Kind: SubjectDiff, Refs: []Ref{{Kind: "diff", URI: "diff-1"}}},
		Evidence: []EvidenceRef{
			{Kind: "test", URI: "test-result.json", Summary: "All tests pass"},
		},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRequestValidateRejectsInvalidSubject(t *testing.T) {
	r := Request{
		Subject: Subject{Kind: "bogus"},
	}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want subject error")
	}
}

func TestRequestValidateRejectsInvalidCriterion(t *testing.T) {
	r := Request{
		Subject:  Subject{Kind: SubjectText, Text: "Review"},
		Criteria: []Criterion{{Description: ""}},
	}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want criteria error")
	}
}

func TestResultValidateAcceptsAcceptedWithEvidence(t *testing.T) {
	r := Result{
		Decision: DecisionAccepted,
		Evidence: []Evidence{{Kind: "test", Summary: "All tests pass"}},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestResultValidateAcceptsRejectedWithSuggestions(t *testing.T) {
	r := Result{
		Decision:    DecisionRejected,
		Suggestions: []Suggestion{{Text: "Add more tests"}},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestResultValidateAcceptsInconclusive(t *testing.T) {
	r := Result{Decision: DecisionInconclusive}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestResultValidateRejectsInvalidDecision(t *testing.T) {
	r := Result{Decision: "bogus"}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want invalid decision error")
	}
}

func TestResultValidateRejectsAcceptedWithNoEvidence(t *testing.T) {
	r := Result{Decision: DecisionAccepted}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want evidence requirement error")
	}
}

func TestResultValidateRejectsRejectedWithNoSuggestions(t *testing.T) {
	r := Result{Decision: DecisionRejected}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want suggestion requirement error")
	}
}

func TestResultValidateWithCriteriaResults(t *testing.T) {
	r := Result{
		Decision: DecisionAccepted,
		Evidence: []Evidence{{Summary: "Tests pass"}},
		CriteriaResults: []CriterionResult{
			{CriterionID: "c1", Status: "met"},
			{CriterionID: "c2", Status: "met"},
		},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestResultValidateRejectsInvalidCriteriaResultStatus(t *testing.T) {
	r := Result{
		Decision: DecisionAccepted,
		Evidence: []Evidence{{Summary: "Tests pass"}},
		CriteriaResults: []CriterionResult{
			{CriterionID: "c1", Status: "bogus"},
		},
	}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want criteria_results status error")
	}
}

func TestResultValidateWithFindings(t *testing.T) {
	r := Result{
		Decision:    DecisionRejected,
		Suggestions: []Suggestion{{Text: "Fix issues"}},
		Findings: []Finding{
			{Severity: "high", Message: "Memory leak detected"},
		},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestResultValidateWithFullFields(t *testing.T) {
	r := Result{
		ID:              "result-1",
		RequestID:       "req-1",
		Decision:        DecisionAccepted,
		Summary:         "Code looks good",
		CriteriaResults: []CriterionResult{{CriterionID: "c1", Status: "met"}},
		Findings:        []Finding{{Severity: "info", Message: "Minor suggestion"}},
		Evidence:        []Evidence{{Kind: "test", Summary: "All pass"}},
		Suggestions:     []Suggestion{{Text: "Consider refactoring"}},
		Risk:            "low",
		Metadata:        map[string]string{"reviewer": "agent"},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDecisionConstants(t *testing.T) {
	if DecisionAccepted != "accepted" {
		t.Errorf("DecisionAccepted = %q, want %q", DecisionAccepted, "accepted")
	}
	if DecisionRejected != "rejected" {
		t.Errorf("DecisionRejected = %q, want %q", DecisionRejected, "rejected")
	}
	if DecisionInconclusive != "inconclusive" {
		t.Errorf("DecisionInconclusive = %q, want %q", DecisionInconclusive, "inconclusive")
	}
}

func TestSubjectKindConstants(t *testing.T) {
	if SubjectText != "text" {
		t.Errorf("SubjectText = %q, want %q", SubjectText, "text")
	}
	if SubjectThread != "thread" {
		t.Errorf("SubjectThread = %q, want %q", SubjectThread, "thread")
	}
	if SubjectDiff != "diff" {
		t.Errorf("SubjectDiff = %q, want %q", SubjectDiff, "diff")
	}
	if SubjectGoal != "goal" {
		t.Errorf("SubjectGoal = %q, want %q", SubjectGoal, "goal")
	}
	if SubjectTask != "task" {
		t.Errorf("SubjectTask = %q, want %q", SubjectTask, "task")
	}
	if SubjectResource != "resource" {
		t.Errorf("SubjectResource = %q, want %q", SubjectResource, "resource")
	}
}
