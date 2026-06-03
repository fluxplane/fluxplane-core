// Package trigger defines inert daemon trigger specs and occurrence payloads.
package trigger

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-reaction"
)

// Kind identifies a trigger source family.
type Kind string

const (
	// KindStartup fires once when the daemon trigger host starts.
	KindStartup Kind = "startup"
	// KindSchedule fires from a daemon-local schedule.
	KindSchedule Kind = "schedule"
)

// Kinds returns the stable daemon trigger source vocabulary.
func Kinds() []Kind {
	return []Kind{KindStartup, KindSchedule}
}

// Spec declares one daemon trigger. Concrete source IO belongs outside core.
type Spec struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Kind        Kind              `json:"kind" yaml:"kind"`
	Schedule    Schedule          `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	Session     string            `json:"session" yaml:"session"`
	Actions     []reaction.Action `json:"actions,omitempty" yaml:"actions,omitempty"`
	Disabled    bool              `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Schedule declares a simple interval schedule. Cron-style schedules can be
// added as another structured field without changing trigger dispatch.
type Schedule struct {
	Every string `json:"every,omitempty" yaml:"every,omitempty"`
}

// Event is one trigger occurrence entering a session.
type Event struct {
	Name     string            `json:"name"`
	Source   string            `json:"source,omitempty"`
	Payload  any               `json:"payload,omitempty"`
	Actions  []reaction.Action `json:"actions,omitempty"`
	Metadata map[string]any    `json:"metadata,omitempty"`
}

// Validate checks that the trigger spec can be scheduled and dispatched.
func (s Spec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("trigger: name is empty")
	}
	if s.Kind == "" {
		return fmt.Errorf("trigger %q: kind is empty", s.Name)
	}
	switch s.Kind {
	case KindStartup:
	case KindSchedule:
		if strings.TrimSpace(s.Schedule.Every) == "" {
			return fmt.Errorf("trigger %q: schedule.every is empty", s.Name)
		}
	default:
		return fmt.Errorf("trigger %q: kind %q is invalid", s.Name, s.Kind)
	}
	if strings.TrimSpace(s.Session) == "" {
		return fmt.Errorf("trigger %q: session is empty", s.Name)
	}
	for i, action := range s.Actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("trigger %q actions[%d]: %w", s.Name, i, err)
		}
	}
	return nil
}
