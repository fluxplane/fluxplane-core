// Package eventcatalog lists bundled provider event payload types linked into
// this module.
package eventcatalog

import (
	"github.com/fluxplane/fluxplane-core/contrib/human"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-event"
)

// All returns every bundled provider event payload type known to this binary.
func All() []event.Event {
	return []event.Event{
		resource.LoadError{},
		human.ClarificationRequested{},
		human.ClarificationCompleted{},
		human.NotificationSent{},
	}
}
