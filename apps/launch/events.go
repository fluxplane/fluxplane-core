package launch

import (
	"fmt"

	coreevent "github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/orchestration/eventregistry"
	"github.com/fluxplane/engine/plugins/support/eventcatalog"
)

// MustTerminalEventRegistry returns the terminal event registry used by local
// distribution CLIs.
func MustTerminalEventRegistry() *coreevent.Registry {
	registry, err := TerminalEventRegistry()
	if err != nil {
		panic(fmt.Sprintf("launch: build terminal event registry: %v", err))
	}
	return registry
}

// TerminalEventRegistry builds an event registry for first-party terminal
// renderers.
func TerminalEventRegistry() (*coreevent.Registry, error) {
	return eventregistry.New(eventregistry.Config{EventTypes: eventcatalog.All()})
}
