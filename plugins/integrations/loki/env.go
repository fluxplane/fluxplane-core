package loki

import (
	"context"
	"strings"

	"github.com/fluxplane/agentruntime/runtime/system"
)

func lookupEnv(ctx context.Context, sys system.System, key string) (string, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, nil
	}
	if sys != nil && sys.Environment() != nil {
		return sys.Environment().Lookup(ctx, key)
	}
	return "", false, nil
}
