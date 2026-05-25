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

func TestEvidenceVocabulariesAndZeroValues(t *testing.T) {
	phases := ObservationPhases()
	wantPhases := []ObservationPhase{PhaseStartup, PhaseSessionOpen, PhaseTurn, PhaseToolFollowup, PhaseLazy}
	if len(phases) != len(wantPhases) {
		t.Fatalf("ObservationPhases len = %d, want %d", len(phases), len(wantPhases))
	}
	for i, want := range wantPhases {
		if phases[i] != want {
			t.Fatalf("ObservationPhases[%d] = %q, want %q", i, phases[i], want)
		}
	}
	phases[0] = "mutated"
	if got := ObservationPhases()[0]; got != PhaseStartup {
		t.Fatalf("ObservationPhases returned shared slice; first = %q", got)
	}

	kinds := SubjectKinds()
	wantKinds := []SubjectKind{SubjectLanguage, SubjectToolchain, SubjectIntegration, SubjectEndpoint, SubjectCapability, SubjectProvider, SubjectTrigger}
	if len(kinds) != len(wantKinds) {
		t.Fatalf("SubjectKinds len = %d, want %d", len(kinds), len(wantKinds))
	}
	for i, want := range wantKinds {
		if kinds[i] != want {
			t.Fatalf("SubjectKinds[%d] = %q, want %q", i, kinds[i], want)
		}
	}
	kinds[0] = "mutated"
	if got := SubjectKinds()[0]; got != SubjectLanguage {
		t.Fatalf("SubjectKinds returned shared slice; first = %q", got)
	}

	if !((Subject{Kind: " ", Name: "\t", ID: "\n"}).IsZero()) {
		t.Fatal("blank subject is not zero")
	}
	if (Subject{Kind: SubjectLanguage}).IsZero() || (Subject{Name: "go"}).IsZero() || (Subject{ID: "lang:go"}).IsZero() {
		t.Fatal("populated subject reported zero")
	}

	if !(Assertion{Kind: " ", Target: "\t", Subject: Subject{Name: " "}, Scope: "\n", Source: " "}.IsZero()) {
		t.Fatal("blank assertion is not zero")
	}
	if (Assertion{Kind: "toolchain.available"}).IsZero() || (Assertion{Target: "go"}).IsZero() || (Assertion{Subject: Subject{Name: "go"}}).IsZero() || (Assertion{Scope: "workspace"}).IsZero() || (Assertion{Source: "test"}).IsZero() {
		t.Fatal("populated assertion reported zero")
	}
}

func TestAssertionFingerprintNormalizesWhitespaceAndCopiesMetadata(t *testing.T) {
	base := Assertion{
		Kind:        " toolchain.available ",
		Target:      " go ",
		Subject:     Subject{Kind: " toolchain ", Name: " go ", ID: " id "},
		Scope:       " workspace:/repo ",
		Source:      " observer ",
		Environment: Ref{Name: "local"},
		Metadata:    map[string]string{"version": "go1.25"},
	}
	trimmed := Assertion{
		Kind:        "toolchain.available",
		Target:      "go",
		Subject:     Subject{Kind: SubjectToolchain, Name: "go", ID: "id"},
		Scope:       "workspace:/repo",
		Source:      "observer",
		Environment: Ref{Name: "local"},
		Metadata:    map[string]string{"version": "go1.25"},
	}
	if base.Fingerprint() != trimmed.Fingerprint() {
		t.Fatal("Fingerprint should normalize matching whitespace")
	}

	before := base.Fingerprint()
	base.Metadata["version"] = "go1.26"
	if before == base.Fingerprint() {
		t.Fatal("Fingerprint did not change after mutating source metadata")
	}
}
