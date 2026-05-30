package loki

import (
	"context"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"strings"
)

func lookupEnv(ctx context.Context, sys fpsystem.System, key string) (string, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, nil
	}
	if sys != nil && sys.Environment() != nil {
		return sys.Environment().Lookup(ctx, key)
	}
	return "", false, nil
}
