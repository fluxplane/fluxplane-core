// Package secret is a compatibility shim for the shared auth/secret runtime.
//
// New code should import github.com/fluxplane/fluxplane-auth/authsecret,
// github.com/fluxplane/fluxplane-auth, or github.com/fluxplane/fluxplane-secret directly.
package secret

import (
	"context"

	coresecret "github.com/fluxplane/fluxplane-auth/authsecret"
)

type Environment = coresecret.Environment
type Resolver = coresecret.Resolver
type ResolverFunc = coresecret.ResolverFunc
type EnvResolver = coresecret.EnvResolver
type ChainResolver = coresecret.ChainResolver
type Registry = coresecret.Registry

type Scope = coresecret.Scope
type Broker = coresecret.Broker
type Resolution = coresecret.Resolution

func NewRegistry(resolvers ...Resolver) *Registry { return coresecret.NewRegistry(resolvers...) }
func NewBroker(resolver Resolver) *Broker         { return coresecret.NewBroker(resolver) }
func ContextWithScope(ctx context.Context, scope Scope) context.Context {
	return coresecret.ContextWithScope(ctx, scope)
}
func ScopeFromContext(ctx context.Context) Scope { return coresecret.ScopeFromContext(ctx) }
