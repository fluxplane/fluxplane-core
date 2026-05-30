package secret

import (
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	shared "github.com/fluxplane/fluxplane-secret"
)

// Shared secret contract aliases. New code should import fluxplane-secret and fluxplane-auth directly.
type Scheme = shared.Scheme
type Kind = shared.Kind
type Slot = shared.Slot
type Use = shared.Use
type StoreRef = shared.StoreRef
type Ref = shared.Ref
type Material = shared.Material
type Placeholder = shared.Placeholder
type AuthRequest = auth.Request
type AuthMethodKind = auth.Method
type AuthMethodSpec = auth.MethodSpec
type EnvSpec = auth.EnvSpec
type HeaderSpec = auth.HeaderSpec
type OAuth2Spec = auth.OAuth2Spec
type SetupFieldSpec = auth.FieldSpec

const (
	SchemeEnv        = shared.SchemeEnv
	SchemePlugin     = shared.SchemePlugin
	SchemeKubernetes = shared.SchemeKubernetes

	KindAPIKey      = shared.KindAPIKey
	KindBearerToken = shared.KindBearerToken
	KindOAuth2Token = shared.KindOAuth2Token
	KindBasic       = shared.KindBasic
	KindPKI         = shared.KindPKI

	UseAuthToken = shared.UseAuthToken
	UseAPIKey    = shared.UseAPIKey
	UsePassword  = shared.UsePassword
	UseTLS       = shared.UseTLS
	UseSigning   = shared.UseSigning

	AuthMethodEnv    AuthMethodKind = auth.MethodEnv
	AuthMethodOAuth2 AuthMethodKind = auth.MethodOAuth2AuthCode
	AuthMethodStored AuthMethodKind = auth.MethodStored
)

func FromSharedRef(ref shared.Ref) Ref { return ref.Normalize() }
func Env(name string) Ref              { return shared.Env(name) }
func Plugin(plugin, instance, name string) Ref {
	return shared.Plugin(plugin, instance, shared.Slot(name))
}
func Kubernetes(namespace, secretName, key string) Ref {
	return shared.Kubernetes(namespace, secretName, shared.Slot(key))
}
func ParseRef(value string) Ref { return shared.ParseRef(value) }

func FromSharedMaterial(m shared.Material) Material { return m }

func PlaceholderFor(handle string) Placeholder     { return shared.PlaceholderFor(handle) }
func ParsePlaceholder(value string) (string, bool) { return shared.ParsePlaceholder(value) }
func ReplacePlaceholders(value string, replace func(handle string) (string, error)) (string, error) {
	return shared.ReplacePlaceholders(value, replace)
}
func RedactPlaceholders(value string) string { return shared.RedactPlaceholders(value) }

func ValidateAuthMethod(spec AuthMethodSpec) error { return auth.ValidateMethod(spec) }

// SetupFieldName returns the legacy display/key name for an auth setup field.
func SetupFieldName(spec SetupFieldSpec) string { return strings.TrimSpace(string(spec.Slot)) }
