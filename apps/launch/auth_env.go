package launch

import (
	"context"
	"os"
	"strings"

	runtimesecret "github.com/fluxplane/engine/runtime/secret"
	"github.com/fluxplane/engine/runtime/system"
)

// PluginAuthOptions configures native plugin credential resolution for local
// launch surfaces.
type PluginAuthOptions struct {
	System             system.System
	AuthPath           string
	AllowPluginAuthEnv bool
}

// PluginAuthContext is the native plugin auth store and resolver pair shared by
// plugin construction, auth evidence, channel identity, and direct operation
// execution.
type PluginAuthContext struct {
	Store    runtimesecret.FileStore
	Resolver runtimesecret.Resolver
}

// NewPluginAuthContext builds the native plugin auth context used by local
// runtime surfaces. Process environment credentials are included only when
// explicitly allowed.
func NewPluginAuthContext(opts PluginAuthOptions) PluginAuthContext {
	store := runtimesecret.NewFileStore(pluginAuthPath(opts.AuthPath))
	return PluginAuthContext{
		Store:    store,
		Resolver: pluginAuthResolver(opts.System, store, opts.AllowPluginAuthEnv),
	}
}

func pluginAuthPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return runtimesecret.DefaultFileStorePath
	}
	return path
}

func pluginAuthResolver(sys system.System, store runtimesecret.FileStore, allowProcessEnvironment bool) runtimesecret.Resolver {
	resolver := runtimesecret.ChainResolver{store}
	if sys != nil && sys.Environment() != nil {
		resolver = append(resolver, runtimesecret.EnvResolver{Environment: sys.Environment()})
	}
	if allowProcessEnvironment {
		resolver = append(resolver, runtimesecret.EnvResolver{Environment: processAuthEnvironment{}})
	}
	return resolver
}

type processAuthEnvironment struct{}

func (processAuthEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := os.LookupEnv(key)
	return value, ok, nil
}
