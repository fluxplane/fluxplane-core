package session

import (
	"fmt"

	"github.com/fluxplane/fluxplane-core/orchestration/sessioncontrol"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
)

// SessionBinding binds a configured session profile to its canonical resource
// identity.
type SessionBinding struct {
	ID   sessioncontrol.ResourceID
	Spec sessionenv.SessionSpec
}

// SessionCatalog contains configured session profiles keyed by canonical
// resource ID address.
type SessionCatalog map[string]SessionBinding

// Resolve resolves a local or qualified session ref against configured
// profiles.
func (c SessionCatalog) Resolve(ref string) (SessionBinding, error) {
	if len(c) == 0 {
		return SessionBinding{}, fmt.Errorf("session catalog is empty")
	}
	index := sessioncontrol.NewResourceIndex()
	for _, binding := range c {
		index.Add(binding.ID)
	}
	resolver := sessioncontrol.NewResolver(index)
	id, err := sessioncontrol.ResolveResource(resolver, "session", ref)
	if err != nil {
		return SessionBinding{}, err
	}
	binding, ok := c[id.Address()]
	if !ok {
		return SessionBinding{}, fmt.Errorf("session %q is not bound", id.Address())
	}
	return binding, nil
}
