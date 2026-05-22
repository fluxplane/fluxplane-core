package review

import (
	"strings"
	"testing"
)

func TestRequestValidateRequiresSubject(t *testing.T) {
	err := (Request{}).Validate()
	if err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("Validate error = %v, want subject kind error", err)
	}
}

func TestResultValidateAcceptedRequiresEvidence(t *testing.T) {
	err := (Result{Decision: DecisionAccepted}).Validate()
	if err == nil || !strings.Contains(err.Error(), "requires evidence") {
		t.Fatalf("Validate error = %v, want evidence requirement", err)
	}
}

func TestResultValidateRejectedRequiresSuggestions(t *testing.T) {
	err := (Result{Decision: DecisionRejected}).Validate()
	if err == nil || !strings.Contains(err.Error(), "requires suggestions") {
		t.Fatalf("Validate error = %v, want suggestion requirement", err)
	}
}
