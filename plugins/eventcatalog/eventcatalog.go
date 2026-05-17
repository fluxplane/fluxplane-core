// Package eventcatalog lists first-party plugin event payload types linked into
// this module.
package eventcatalog

import (
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/core/project"
	"github.com/fluxplane/agentruntime/plugins/humanplugin"
)

// All returns every first-party plugin event payload type known to this binary.
func All() []event.Event {
	return []event.Event{
		project.SignalsObserved{},
		language.ToolchainStatusObserved{},
		humanplugin.ClarificationRequested{},
		humanplugin.ClarificationCompleted{},
	}
}
