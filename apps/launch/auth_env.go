package launch

import (
	"context"
	"os"
)

type processAuthEnvironment struct{}

func (processAuthEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := os.LookupEnv(key)
	return value, ok, nil
}
