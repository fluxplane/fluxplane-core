package reaction

import (
	"testing"

	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	coreevidence "github.com/fluxplane/fluxplane-evidence"
	"github.com/fluxplane/fluxplane-skill"
)

func TestPlanFiresOnNewAssertion(t *testing.T) {
	rule := testRule()
	assertion := testAssertion("go1.24")
	result := Plan(Request{Rules: []corereaction.Rule{rule}, Assertions: []coreevidence.Assertion{assertion}})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", result.Diagnostics)
	}
	if len(result.Planned) != 1 {
		t.Fatalf("planned len = %d, want 1", len(result.Planned))
	}
	if result.Planned[0].Rule != "go-toolchain" {
		t.Fatalf("planned rule = %q, want go-toolchain", result.Planned[0].Rule)
	}
	if result.Current[assertion.ActivationKey()] != assertion.Fingerprint() {
		t.Fatalf("current = %#v, missing assertion fingerprint", result.Current)
	}
}

func TestPlanSuppressesUnchangedAssertion(t *testing.T) {
	rule := testRule()
	assertion := testAssertion("go1.24")
	result := Plan(Request{
		Rules:      []corereaction.Rule{rule},
		Assertions: []coreevidence.Assertion{assertion},
		Previous:   map[string]string{assertion.ActivationKey(): assertion.Fingerprint()},
	})
	if len(result.Planned) != 0 {
		t.Fatalf("planned len = %d, want 0", len(result.Planned))
	}
}

func TestPlanFiresWhenAssertionFingerprintChanges(t *testing.T) {
	rule := testRule()
	previous := testAssertion("go1.24")
	current := testAssertion("go1.25")
	result := Plan(Request{
		Rules:      []corereaction.Rule{rule},
		Assertions: []coreevidence.Assertion{current},
		Previous:   map[string]string{previous.ActivationKey(): previous.Fingerprint()},
	})
	if len(result.Planned) != 1 {
		t.Fatalf("planned len = %d, want 1", len(result.Planned))
	}
}

func TestPlanEveryTurnFiresForUnchangedAssertion(t *testing.T) {
	rule := testRule()
	rule.Mode = corereaction.ModeEveryTurn
	assertion := testAssertion("go1.24")
	result := Plan(Request{
		Rules:      []corereaction.Rule{rule},
		Assertions: []coreevidence.Assertion{assertion},
		Previous:   map[string]string{assertion.ActivationKey(): assertion.Fingerprint()},
	})
	if len(result.Planned) != 1 {
		t.Fatalf("planned len = %d, want 1", len(result.Planned))
	}
}

func TestPlanEveryTurnIgnoresAlreadyAppliedIdempotencyKey(t *testing.T) {
	rule := testRule()
	rule.Mode = corereaction.ModeEveryTurn
	assertion := testAssertion("go1.24")
	key := IdempotencyKey(rule, assertion, 0, rule.Actions[0])
	result := Plan(Request{
		Rules:       []corereaction.Rule{rule},
		Assertions:  []coreevidence.Assertion{assertion},
		Previous:    map[string]string{assertion.ActivationKey(): assertion.Fingerprint()},
		AppliedKeys: map[string]bool{key: true},
	})
	if len(result.Planned) != 1 {
		t.Fatalf("planned len = %d, want 1", len(result.Planned))
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("skipped = %#v, want none", result.Skipped)
	}
}

func TestPlanSkipsAlreadyAppliedIdempotencyKey(t *testing.T) {
	rule := testRule()
	assertion := testAssertion("go1.24")
	key := IdempotencyKey(rule, assertion, 0, rule.Actions[0])
	result := Plan(Request{
		Rules:       []corereaction.Rule{rule},
		Assertions:  []coreevidence.Assertion{assertion},
		AppliedKeys: map[string]bool{key: true},
	})
	if len(result.Planned) != 0 {
		t.Fatalf("planned len = %d, want 0", len(result.Planned))
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Reason != "already_applied" {
		t.Fatalf("skipped = %#v, want already_applied", result.Skipped)
	}
}

func TestPlanReportsInvalidRuleDiagnostic(t *testing.T) {
	result := Plan(Request{Rules: []corereaction.Rule{{Name: "bad"}}})
	if len(result.Diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1", len(result.Diagnostics))
	}
}

func testRule() corereaction.Rule {
	return corereaction.Rule{
		Name: "go-toolchain",
		When: corereaction.Matcher{Assertion: "toolchain.available", Target: "go"},
		Actions: []corereaction.Action{{
			Kind:  corereaction.ActionActivateSkill,
			Skill: skill.Ref{Name: "go"},
		}},
	}
}

func testAssertion(version string) coreevidence.Assertion {
	return coreevidence.Assertion{
		Kind:     "toolchain.available",
		Target:   "go",
		Scope:    "workspace:/repo",
		Source:   "toolchain.status",
		Metadata: map[string]string{"version": version},
	}
}
