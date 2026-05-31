// Package secret is a compatibility shim for the shared auth/secret contracts.
//
// New code should import github.com/fluxplane/fluxplane-auth/authsecret,
// github.com/fluxplane/fluxplane-auth, or github.com/fluxplane/fluxplane-secret directly.
package secret

import (
	"context"

	coresecret "github.com/fluxplane/fluxplane-auth/authsecret"
	shared "github.com/fluxplane/fluxplane-secret"
)

// Shared secret contract aliases.
type Scheme = coresecret.Scheme
type Kind = coresecret.Kind
type Slot = coresecret.Slot
type Use = coresecret.Use
type StoreRef = coresecret.StoreRef
type Ref = coresecret.Ref
type Material = coresecret.Material
type Placeholder = coresecret.Placeholder
type Resolver = coresecret.Resolver
type ResolverFunc = coresecret.ResolverFunc
type EnvResolver = coresecret.EnvResolver
type ChainResolver = coresecret.ChainResolver
type Registry = coresecret.Registry

// Shared auth contract aliases.
type AuthRequest = coresecret.AuthRequest
type AuthMethodKind = coresecret.AuthMethodKind
type AuthMethodSpec = coresecret.AuthMethodSpec
type EnvSpec = coresecret.EnvSpec
type HeaderSpec = coresecret.HeaderSpec
type OAuth2Spec = coresecret.OAuth2Spec
type SetupFieldSpec = coresecret.SetupFieldSpec

type Broker = coresecret.Broker
type Scope = coresecret.Scope
type Resolution = coresecret.Resolution

const (
	SchemeEnv        = coresecret.SchemeEnv
	SchemePlugin     = coresecret.SchemePlugin
	SchemeKubernetes = coresecret.SchemeKubernetes

	KindAPIKey      = coresecret.KindAPIKey
	KindBearerToken = coresecret.KindBearerToken
	KindOAuth2Token = coresecret.KindOAuth2Token
	KindBasic       = coresecret.KindBasic
	KindPKI         = coresecret.KindPKI

	UseAuthToken = coresecret.UseAuthToken
	UseAPIKey    = coresecret.UseAPIKey
	UsePassword  = coresecret.UsePassword
	UseTLS       = coresecret.UseTLS
	UseSigning   = coresecret.UseSigning

	AuthMethodEnv    AuthMethodKind = coresecret.AuthMethodEnv
	AuthMethodOAuth2 AuthMethodKind = coresecret.AuthMethodOAuth2
	AuthMethodStored AuthMethodKind = coresecret.AuthMethodStored
)

func NewBroker(resolver Resolver) *Broker { return coresecret.NewBroker(resolver) }
func ContextWithScope(ctx context.Context, scope Scope) context.Context {
	return coresecret.ContextWithScope(ctx, scope)
}
func ScopeFromContext(ctx context.Context) Scope  { return coresecret.ScopeFromContext(ctx) }
func NewRegistry(resolvers ...Resolver) *Registry { return coresecret.NewRegistry(resolvers...) }
func FromSharedRef(ref shared.Ref) Ref            { return coresecret.FromSharedRef(ref) }
func Env(name string) Ref                         { return coresecret.Env(name) }
func EnvWildcard() Ref                            { return coresecret.EnvWildcard() }
func Plugin(plugin, instance, name string) Ref    { return coresecret.Plugin(plugin, instance, name) }
func Kubernetes(namespace, secretName, key string) Ref {
	return coresecret.Kubernetes(namespace, secretName, key)
}
func ParseRef(value string) Ref                     { return coresecret.ParseRef(value) }
func FromSharedMaterial(m shared.Material) Material { return coresecret.FromSharedMaterial(m) }
func PlaceholderFor(handle string) Placeholder      { return coresecret.PlaceholderFor(handle) }
func ParsePlaceholder(value string) (string, bool)  { return coresecret.ParsePlaceholder(value) }
func ReplacePlaceholders(value string, replace func(handle string) (string, error)) (string, error) {
	return coresecret.ReplacePlaceholders(value, replace)
}
func RedactPlaceholders(value string) string       { return coresecret.RedactPlaceholders(value) }
func ValidateAuthMethod(spec AuthMethodSpec) error { return coresecret.ValidateAuthMethod(spec) }
func SetupFieldName(spec SetupFieldSpec) string    { return coresecret.SetupFieldName(spec) }
