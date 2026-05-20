package image

import (
	"context"
	"strings"

	"github.com/fluxplane/agentruntime/runtime/system"
)

func env(ctx context.Context, sys system.System, key string) string {
	if sys == nil || sys.Environment() == nil {
		return ""
	}
	value, ok, err := sys.Environment().Lookup(ctx, key)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func configuredByEnv(ctx context.Context, sys system.System, keys ...string) (bool, []string) {
	var missing []string
	for _, key := range keys {
		if env(ctx, sys, key) == "" {
			missing = append(missing, key)
		}
	}
	return len(missing) == 0, missing
}
