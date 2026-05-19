package environment

import (
	"context"
	"os"
	osuser "os/user"
	"time"

	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
)

const (
	BaselineObserverName = "runtime.baseline"

	ObservationSystemTime   = "system.time"
	ObservationSystemLocale = "system.locale"
	ObservationSystemUser   = "system.user"
)

type baselineObserver struct{}

// BaselineObserver reports cheap, non-secret local runtime facts.
func BaselineObserver() Observer {
	return baselineObserver{}
}

func (baselineObserver) Spec() coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{
		Name:        BaselineObserverName,
		Description: "Reports cheap non-secret local runtime facts such as time, locale, and username.",
		Environment: coreenvironment.Ref{
			Name: "local",
		},
		Phase: coreenvironment.PhaseTurn,
		ObservableKinds: []string{
			ObservationSystemTime,
			ObservationSystemLocale,
			ObservationSystemUser,
		},
		Dynamic: true,
	}
}

func (baselineObserver) Observe(context.Context, ObservationRequest) ([]coreenvironment.Observation, error) {
	now := time.Now()
	out := []coreenvironment.Observation{{
		ID:          "system:time",
		Kind:        ObservationSystemTime,
		Scope:       string(coreenvironment.ScopeSession),
		Content:     systemTimeContent(now),
		At:          now.UTC(),
		Environment: coreenvironment.Ref{Name: "local"},
	}}
	if locale := systemLocaleContent(); len(locale) > 0 {
		out = append(out, coreenvironment.Observation{
			ID:          "system:locale",
			Kind:        ObservationSystemLocale,
			Scope:       string(coreenvironment.ScopeSession),
			Content:     locale,
			At:          now.UTC(),
			Environment: coreenvironment.Ref{Name: "local"},
		})
	}
	if current, err := osuser.Current(); err == nil && current != nil && current.Username != "" {
		out = append(out, coreenvironment.Observation{
			ID:    "system:user",
			Kind:  ObservationSystemUser,
			Scope: string(coreenvironment.ScopeSession),
			Content: map[string]any{
				"username": current.Username,
				"uid":      current.Uid,
				"gid":      current.Gid,
			},
			At:          now.UTC(),
			Environment: coreenvironment.Ref{Name: "local"},
		})
	}
	return out, nil
}

func systemTimeContent(now time.Time) map[string]any {
	name, offset := now.Zone()
	return map[string]any{
		"time":       now.Format(time.RFC3339),
		"utc_time":   now.UTC().Format(time.RFC3339),
		"timezone":   now.Location().String(),
		"zone":       name,
		"utc_offset": offset,
	}
}

func systemLocaleContent() map[string]any {
	out := map[string]any{}
	for _, key := range []string{"LANG", "LC_ALL", "LC_CTYPE"} {
		if value := os.Getenv(key); value != "" {
			out[key] = value
		}
	}
	return out
}
