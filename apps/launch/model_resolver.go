package launch

import "github.com/fluxplane/agentruntime/orchestration/agentfactory"

func firstModelResolver(value agentfactory.ModelResolver, fallback agentfactory.ModelResolver) agentfactory.ModelResolver {
	if value != nil {
		return value
	}
	return fallback
}
