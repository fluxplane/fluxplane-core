package imageplugin

import (
	"strings"

	"github.com/fluxplane/agentruntime/runtime/system"
)

func env(sys system.System, key string) string {
	if sys == nil || sys.Environment() == nil {
		return ""
	}
	return strings.TrimSpace(sys.Environment().Getenv(key))
}

func configuredByEnv(sys system.System, keys ...string) (bool, []string) {
	var missing []string
	for _, key := range keys {
		if env(sys, key) == "" {
			missing = append(missing, key)
		}
	}
	return len(missing) == 0, missing
}
