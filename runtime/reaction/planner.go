package reaction

import (
	"strconv"

	coreevidence "github.com/fluxplane/fluxplane-evidence"
	corereaction "github.com/fluxplane/fluxplane-reaction"
)

// Request describes one pure reaction planning pass.
type Request struct {
	Rules       []corereaction.Rule
	Assertions  []coreevidence.Assertion
	Previous    map[string]string
	AppliedKeys map[string]bool
}

// Result is the output of one planning pass.
type Result struct {
	Current     map[string]string
	Planned     []PlannedAction
	Skipped     []SkippedAction
	Diagnostics []Diagnostic
}

// PlannedAction is an action selected by a matching rule.
type PlannedAction struct {
	Rule           string
	Assertion      coreevidence.Assertion
	Action         corereaction.Action
	ActionIndex    int
	IdempotencyKey string
}

// SkippedAction is a matching action suppressed by previous application state.
type SkippedAction struct {
	Rule           string
	Assertion      coreevidence.Assertion
	Action         corereaction.Action
	ActionIndex    int
	IdempotencyKey string
	Reason         string
}

// Diagnostic describes an invalid rule or assertion planning issue.
type Diagnostic struct {
	Rule    string
	Message string
}

// Plan evaluates reaction rules against the current assertion set.
func Plan(req Request) Result {
	assertions := uniqueAssertions(req.Assertions)
	current := AssertionFingerprints(assertions)
	result := Result{Current: current}
	for _, rule := range req.Rules {
		if err := rule.Validate(); err != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Rule: rule.Name, Message: err.Error()})
			continue
		}
		for _, assertion := range assertions {
			if !rule.When.Matches(assertion) {
				continue
			}
			if !shouldFire(rule, assertion, req.Previous) {
				continue
			}
			for index, action := range rule.Actions {
				key := IdempotencyKey(rule, assertion, index, action)
				planned := PlannedAction{
					Rule:           rule.Name,
					Assertion:      assertion,
					Action:         action,
					ActionIndex:    index,
					IdempotencyKey: key,
				}
				if rule.Mode != corereaction.ModeEveryTurn && req.AppliedKeys != nil && req.AppliedKeys[key] {
					result.Skipped = append(result.Skipped, SkippedAction{
						Rule:           planned.Rule,
						Assertion:      planned.Assertion,
						Action:         planned.Action,
						ActionIndex:    planned.ActionIndex,
						IdempotencyKey: planned.IdempotencyKey,
						Reason:         "already_applied",
					})
					continue
				}
				result.Planned = append(result.Planned, planned)
			}
		}
	}
	return result
}

// AssertionFingerprints returns the current key -> fingerprint state for assertions.
func AssertionFingerprints(assertions []coreevidence.Assertion) map[string]string {
	out := map[string]string{}
	for _, assertion := range uniqueAssertions(assertions) {
		if assertion.IsZero() {
			continue
		}
		out[assertion.ActivationKey()] = assertion.Fingerprint()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IdempotencyKey returns the stable action key used to suppress repeated
// applications of the same rule/action for the same assertion fingerprint.
func IdempotencyKey(rule corereaction.Rule, assertion coreevidence.Assertion, index int, action corereaction.Action) string {
	key := rule.Name + "\x1f" + assertion.ActivationKey() + "\x1f" + assertion.Fingerprint() + "\x1f" + strconv.Itoa(index)
	if action.IdempotencyFragment != "" {
		key += "\x1f" + action.IdempotencyFragment
	}
	return key
}

func shouldFire(rule corereaction.Rule, assertion coreevidence.Assertion, previous map[string]string) bool {
	if rule.Mode == corereaction.ModeEveryTurn {
		return true
	}
	if previous == nil {
		return true
	}
	return previous[assertion.ActivationKey()] != assertion.Fingerprint()
}

func uniqueAssertions(assertions []coreevidence.Assertion) []coreevidence.Assertion {
	seen := map[string]int{}
	var out []coreevidence.Assertion
	for _, assertion := range assertions {
		if assertion.IsZero() {
			continue
		}
		key := assertion.ActivationKey()
		if prior, ok := seen[key]; ok {
			out[prior] = assertion
			continue
		}
		seen[key] = len(out)
		out = append(out, assertion)
	}
	return out
}
