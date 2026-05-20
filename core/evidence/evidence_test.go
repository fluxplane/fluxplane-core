package evidence

import "testing"

func TestAssertionActivationKeyUsesStableMatchingFields(t *testing.T) {
	assertion := Assertion{
		Kind:       " language.detected ",
		Target:     " go ",
		Subject:    Subject{Kind: SubjectLanguage, Name: " go "},
		Scope:      " workspace:/repo ",
		Source:     " project.inventory ",
		Confidence: 1,
		Metadata:   map[string]string{"ignored": "for-key"},
	}
	if got, want := assertion.ActivationKey(), "language.detected\x1fgo\x1flanguage\x1fgo\x1f\x1fworkspace:/repo\x1fproject.inventory"; got != want {
		t.Fatalf("ActivationKey = %q, want %q", got, want)
	}
}

func TestAssertionFingerprintChangesWhenContentChanges(t *testing.T) {
	base := Assertion{
		Kind:           "toolchain.available",
		Target:         "go",
		Subject:        Subject{Kind: SubjectToolchain, Name: "go"},
		Scope:          "workspace:/repo",
		Source:         "toolchain.status",
		ObservationIDs: []string{"toolchain:go"},
		Metadata:       map[string]string{"version": "go1.24"},
	}
	changed := base
	changed.Metadata = map[string]string{"version": "go1.25"}
	if base.Fingerprint() == changed.Fingerprint() {
		t.Fatal("Fingerprint did not change after metadata changed")
	}
	changedSubject := base
	changedSubject.Subject = Subject{Kind: SubjectToolchain, Name: "node"}
	if base.Fingerprint() == changedSubject.Fingerprint() {
		t.Fatal("Fingerprint did not change after subject changed")
	}
}

func TestAssertionFingerprintIsStableForMapOrder(t *testing.T) {
	left := Assertion{
		Kind:     "integration.available",
		Target:   "kubernetes",
		Scope:    "integration:kubernetes:default",
		Source:   "kubernetes.context",
		Metadata: map[string]string{"context": "k3d-ai", "namespace": "ai-bots"},
	}
	right := Assertion{
		Kind:     "integration.available",
		Target:   "kubernetes",
		Scope:    "integration:kubernetes:default",
		Source:   "kubernetes.context",
		Metadata: map[string]string{"namespace": "ai-bots", "context": "k3d-ai"},
	}
	if left.Fingerprint() != right.Fingerprint() {
		t.Fatal("Fingerprint differs for equivalent metadata")
	}
}
