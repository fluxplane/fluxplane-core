package reaction

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/environment"
	corereaction "github.com/fluxplane/agentruntime/core/reaction"
	"github.com/fluxplane/agentruntime/core/skill"
)

func TestPlanFiresOnNewSignal(t *testing.T) {
	rule := testRule()
	signal := testSignal("go1.24")
	result := Plan(Request{Rules: []corereaction.Rule{rule}, Signals: []environment.Signal{signal}})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", result.Diagnostics)
	}
	if len(result.Planned) != 1 {
		t.Fatalf("planned len = %d, want 1", len(result.Planned))
	}
	if result.Planned[0].Rule != "go-toolchain" {
		t.Fatalf("planned rule = %q, want go-toolchain", result.Planned[0].Rule)
	}
	if result.Current[signal.ActivationKey()] != signal.Fingerprint() {
		t.Fatalf("current = %#v, missing signal fingerprint", result.Current)
	}
}

func TestPlanSuppressesUnchangedSignal(t *testing.T) {
	rule := testRule()
	signal := testSignal("go1.24")
	result := Plan(Request{
		Rules:    []corereaction.Rule{rule},
		Signals:  []environment.Signal{signal},
		Previous: map[string]string{signal.ActivationKey(): signal.Fingerprint()},
	})
	if len(result.Planned) != 0 {
		t.Fatalf("planned len = %d, want 0", len(result.Planned))
	}
}

func TestPlanFiresWhenSignalFingerprintChanges(t *testing.T) {
	rule := testRule()
	previous := testSignal("go1.24")
	current := testSignal("go1.25")
	result := Plan(Request{
		Rules:    []corereaction.Rule{rule},
		Signals:  []environment.Signal{current},
		Previous: map[string]string{previous.ActivationKey(): previous.Fingerprint()},
	})
	if len(result.Planned) != 1 {
		t.Fatalf("planned len = %d, want 1", len(result.Planned))
	}
}

func TestPlanEveryTurnFiresForUnchangedSignal(t *testing.T) {
	rule := testRule()
	rule.Mode = corereaction.ModeEveryTurn
	signal := testSignal("go1.24")
	result := Plan(Request{
		Rules:    []corereaction.Rule{rule},
		Signals:  []environment.Signal{signal},
		Previous: map[string]string{signal.ActivationKey(): signal.Fingerprint()},
	})
	if len(result.Planned) != 1 {
		t.Fatalf("planned len = %d, want 1", len(result.Planned))
	}
}

func TestPlanEveryTurnIgnoresAlreadyAppliedIdempotencyKey(t *testing.T) {
	rule := testRule()
	rule.Mode = corereaction.ModeEveryTurn
	signal := testSignal("go1.24")
	key := IdempotencyKey(rule, signal, 0, rule.Actions[0])
	result := Plan(Request{
		Rules:       []corereaction.Rule{rule},
		Signals:     []environment.Signal{signal},
		Previous:    map[string]string{signal.ActivationKey(): signal.Fingerprint()},
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
	signal := testSignal("go1.24")
	key := IdempotencyKey(rule, signal, 0, rule.Actions[0])
	result := Plan(Request{
		Rules:       []corereaction.Rule{rule},
		Signals:     []environment.Signal{signal},
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
		When: corereaction.Matcher{Signal: "toolchain.available", Target: "go"},
		Actions: []corereaction.Action{{
			Kind:  corereaction.ActionActivateSkill,
			Skill: skill.Ref{Name: "go"},
		}},
	}
}

func testSignal(version string) environment.Signal {
	return environment.Signal{
		Kind:     "toolchain.available",
		Target:   "go",
		Scope:    "workspace:/repo",
		Source:   "toolchain.status",
		Metadata: map[string]string{"version": version},
	}
}
