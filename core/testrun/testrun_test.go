package testrun

import "testing"

func TestHasFailureKind(t *testing.T) {
	event := Event{Failures: []Failure{{Kind: FailureBuild}}}
	if !HasFailureKind(event, FailureBuild) {
		t.Fatal("HasFailureKind returned false for present failure kind")
	}
	if HasFailureKind(event, FailureAssertion) {
		t.Fatal("HasFailureKind returned true for absent failure kind")
	}
}
