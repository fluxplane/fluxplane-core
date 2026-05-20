package reaction

import (
	"strconv"

	coreevidence "github.com/fluxplane/agentruntime/core/evidence"
	corereaction "github.com/fluxplane/agentruntime/core/reaction"
)

// Request describes one pure reaction planning pass.
type Request struct {
	Rules       []corereaction.Rule
	Signals     []coreevidence.Assertion
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
	Signal         coreevidence.Assertion
	Action         corereaction.Action
	ActionIndex    int
	IdempotencyKey string
}

// SkippedAction is a matching action suppressed by previous application state.
type SkippedAction struct {
	Rule           string
	Signal         coreevidence.Assertion
	Action         corereaction.Action
	ActionIndex    int
	IdempotencyKey string
	Reason         string
}

// Diagnostic describes an invalid rule or signal planning issue.
type Diagnostic struct {
	Rule    string
	Message string
}

// Plan evaluates reaction rules against the current assertion set.
func Plan(req Request) Result {
	signals := uniqueSignals(req.Signals)
	current := SignalFingerprints(signals)
	result := Result{Current: current}
	for _, rule := range req.Rules {
		if err := rule.Validate(); err != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Rule: rule.Name, Message: err.Error()})
			continue
		}
		for _, signal := range signals {
			if !rule.When.Matches(signal) {
				continue
			}
			if !shouldFire(rule, signal, req.Previous) {
				continue
			}
			for index, action := range rule.Actions {
				key := IdempotencyKey(rule, signal, index, action)
				planned := PlannedAction{
					Rule:           rule.Name,
					Signal:         signal,
					Action:         action,
					ActionIndex:    index,
					IdempotencyKey: key,
				}
				if rule.Mode != corereaction.ModeEveryTurn && req.AppliedKeys != nil && req.AppliedKeys[key] {
					result.Skipped = append(result.Skipped, SkippedAction{
						Rule:           planned.Rule,
						Signal:         planned.Signal,
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

// SignalFingerprints returns the current key -> fingerprint state for assertions.
func SignalFingerprints(signals []coreevidence.Assertion) map[string]string {
	out := map[string]string{}
	for _, signal := range uniqueSignals(signals) {
		if signal.IsZero() {
			continue
		}
		out[signal.ActivationKey()] = signal.Fingerprint()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IdempotencyKey returns the stable action key used to suppress repeated
// applications of the same rule/action for the same signal fingerprint.
func IdempotencyKey(rule corereaction.Rule, signal coreevidence.Assertion, index int, action corereaction.Action) string {
	key := rule.Name + "\x1f" + signal.ActivationKey() + "\x1f" + signal.Fingerprint() + "\x1f" + strconv.Itoa(index)
	if action.IdempotencyFragment != "" {
		key += "\x1f" + action.IdempotencyFragment
	}
	return key
}

func shouldFire(rule corereaction.Rule, signal coreevidence.Assertion, previous map[string]string) bool {
	if rule.Mode == corereaction.ModeEveryTurn {
		return true
	}
	if previous == nil {
		return true
	}
	return previous[signal.ActivationKey()] != signal.Fingerprint()
}

func uniqueSignals(signals []coreevidence.Assertion) []coreevidence.Assertion {
	seen := map[string]int{}
	var out []coreevidence.Assertion
	for _, signal := range signals {
		if signal.IsZero() {
			continue
		}
		key := signal.ActivationKey()
		if prior, ok := seen[key]; ok {
			out[prior] = signal
			continue
		}
		seen[key] = len(out)
		out = append(out, signal)
	}
	return out
}
