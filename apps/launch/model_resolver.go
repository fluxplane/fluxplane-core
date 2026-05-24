package launch

import "github.com/fluxplane/fluxplane-core/orchestration/agentfactory"

func firstModelResolver(value agentfactory.ModelResolver, fallback agentfactory.ModelResolver) agentfactory.ModelResolver {
	if value != nil {
		return value
	}
	return fallback
}
