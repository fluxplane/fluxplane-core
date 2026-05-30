package loki

import (
	"context"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"strings"
)

func lookupEnv(ctx context.Context, env fpsystem.Environment, key string) (string, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, nil
	}
	if env != nil {
		return env.Lookup(ctx, key)
	}
	return "", false, nil
}
