package launch

import (
	"context"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"os"
	"strings"
)

// PluginAuthOptions configures native plugin credential resolution for local
// launch surfaces.
type PluginAuthOptions struct {
	Environment        fpsystem.Environment
	AuthPath           string
	AllowPluginAuthEnv bool
}

// PluginAuthContext is the native plugin auth store and resolver pair shared by
// plugin construction, auth evidence, channel identity, and direct operation
// execution.
type PluginAuthContext struct {
	Store    sharedsecret.FileStore
	Resolver sharedsecret.Resolver
}

// NewPluginAuthContext builds the native plugin auth context used by local
// runtime surfaces. Process environment credentials are included only when
// explicitly allowed.
func NewPluginAuthContext(opts PluginAuthOptions) PluginAuthContext {
	store := sharedsecret.NewFileStore(pluginAuthPath(opts.AuthPath))
	return PluginAuthContext{
		Store:    store,
		Resolver: pluginAuthResolver(opts.Environment, store, opts.AllowPluginAuthEnv),
	}
}

func pluginAuthPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return sharedsecret.DefaultFileStorePath
	}
	return path
}

func pluginAuthResolver(env fpsystem.Environment, store sharedsecret.FileStore, allowProcessEnvironment bool) sharedsecret.Resolver {
	resolver := sharedsecret.ChainResolver{store}
	if env != nil {
		resolver = append(resolver, sharedsecret.EnvResolver{Environment: env})
	}
	if allowProcessEnvironment {
		resolver = append(resolver, sharedsecret.EnvResolver{Environment: processAuthEnvironment{}})
	}
	return resolver
}

func pluginAuthEnvironment(sys fpsystem.System) fpsystem.Environment {
	if sys == nil {
		return nil
	}
	return sys.Environment()
}

type processAuthEnvironment struct{}

func (processAuthEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := os.LookupEnv(key)
	return value, ok, nil
}
