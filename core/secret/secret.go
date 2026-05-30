package secret

import (
	"fmt"
	"regexp"
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	shared "github.com/fluxplane/fluxplane-secret"
)

// Shared secret contract aliases.
type Scheme = shared.Scheme

type Kind = shared.Kind

type Slot = shared.Slot

type Use = shared.Use

type StoreRef = shared.StoreRef

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
)

// AuthMethodKind is retained for core compatibility and maps onto fluxplane-auth Method.
type AuthMethodKind = auth.Method

const (
	AuthMethodEnv    AuthMethodKind = auth.MethodEnv
	AuthMethodOAuth2 AuthMethodKind = auth.MethodOAuth2AuthCode
	AuthMethodStored AuthMethodKind = auth.MethodStored
)

// Ref identifies one secret without carrying its value.
type Ref struct {
	Scheme   Scheme `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	Plugin   string `json:"plugin,omitempty" yaml:"plugin,omitempty"`
	Instance string `json:"instance,omitempty" yaml:"instance,omitempty"`
	Name     string `json:"name" yaml:"name"`
}

// FromSharedRef converts a shared fluxplane-secret ref to the compatibility shape.
func FromSharedRef(ref shared.Ref) Ref {
	ref = ref.Normalize()
	return Ref{Scheme: ref.Scheme, Plugin: ref.Plugin, Instance: ref.Instance, Name: string(ref.Slot)}.Normalize()
}

// Shared returns the canonical shared fluxplane-secret ref.
func (r Ref) Shared() shared.Ref {
	r = r.Normalize()
	return shared.Ref{Scheme: r.Scheme, Plugin: r.Plugin, Instance: r.Instance, Slot: shared.Slot(r.Name)}.Normalize()
}

// Normalize returns a trimmed ref.
func (r Ref) Normalize() Ref {
	r.Scheme = Scheme(strings.TrimSpace(string(r.Scheme)))
	r.Plugin = strings.TrimSpace(r.Plugin)
	r.Instance = strings.TrimSpace(r.Instance)
	r.Name = strings.TrimSpace(r.Name)
	return r
}

// ResourceName returns the authorization resource name for the ref.
func (r Ref) ResourceName() string { return r.Shared().ResourceName() }

// Env returns an env secret ref.
func Env(name string) Ref { return FromSharedRef(shared.Env(name)) }

// Plugin returns a plugin-scoped secret ref.
func Plugin(plugin, instance, name string) Ref {
	return FromSharedRef(shared.Plugin(plugin, instance, shared.Slot(name)))
}

// Kubernetes returns a Kubernetes Secret key ref.
func Kubernetes(namespace, secretName, key string) Ref {
	return FromSharedRef(shared.Kubernetes(namespace, secretName, shared.Slot(key)))
}

// ParseRef parses a canonical secret resource name into a Ref.
func ParseRef(value string) Ref { return FromSharedRef(shared.ParseRef(value)) }

// Material is secret material available only to trusted runtime code.
type Material struct {
	Kind  Kind   `json:"kind,omitempty"`
	Value string `json:"-"`
}

// Shared returns the canonical shared fluxplane-secret material.
func (m Material) Shared() shared.Material {
	return shared.Material{Kind: m.Kind, Value: []byte(m.Value)}
}

// FromSharedMaterial converts shared material to the compatibility shape.
func FromSharedMaterial(m shared.Material) Material {
	return Material{Kind: m.Kind, Value: string(m.Value)}
}

// AuthRequest asks runtime to obtain usable credential material for one plugin instance.
type AuthRequest struct {
	Plugin   string           `json:"plugin" yaml:"plugin"`
	Instance string           `json:"instance,omitempty" yaml:"instance,omitempty"`
	Purpose  string           `json:"purpose" yaml:"purpose"`
	Methods  []AuthMethodSpec `json:"methods,omitempty" yaml:"methods,omitempty"`
}

// Normalize returns a trimmed request.
func (r AuthRequest) Normalize() AuthRequest {
	r.Plugin = strings.TrimSpace(r.Plugin)
	r.Instance = strings.TrimSpace(r.Instance)
	r.Purpose = strings.TrimSpace(r.Purpose)
	return r
}

// SecretRef returns the logical plugin secret protected by secret.use.
func (r AuthRequest) SecretRef() Ref {
	r = r.Normalize()
	return Plugin(r.Plugin, r.Instance, r.Purpose)
}

// AuthMethodSpec describes a way a plugin can authenticate without carrying credentials.
type AuthMethodSpec struct {
	Name        string            `json:"name" yaml:"name"`
	Method      AuthMethodKind    `json:"method" yaml:"method"`
	Kind        Kind              `json:"kind" yaml:"kind"`
	DisplayName string            `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Secret      Ref               `json:"secret,omitempty" yaml:"secret,omitempty"`
	Env         EnvSpec           `json:"env,omitempty" yaml:"env,omitempty"`
	Header      HeaderSpec        `json:"header,omitempty" yaml:"header,omitempty"`
	OAuth2      OAuth2Spec        `json:"oauth2,omitempty" yaml:"oauth2,omitempty"`
	SetupFields []SetupFieldSpec  `json:"setup_fields,omitempty" yaml:"setup_fields,omitempty"`
}

func (s AuthMethodSpec) Shared() auth.MethodSpec {
	fields := make([]auth.FieldSpec, 0, len(s.SetupFields))
	for _, field := range s.SetupFields {
		fields = append(fields, field.Shared())
	}
	return auth.MethodSpec{Name: s.Name, Method: s.Method, Kind: s.Kind, DisplayName: s.DisplayName, Description: s.Description, Secret: s.Secret.Shared(), Env: auth.EnvSpec(s.Env), Header: auth.HeaderSpec(s.Header), OAuth2: auth.OAuth2Spec(s.OAuth2), SetupFields: fields, Annotations: cloneMap(s.Metadata)}.Normalize()
}

// EnvSpec describes an environment-variable backed auth method.
type EnvSpec = auth.EnvSpec

// HeaderSpec describes where an API key/token is applied.
type HeaderSpec = auth.HeaderSpec

// OAuth2Spec describes OAuth2 authorization-code endpoints and scopes.
type OAuth2Spec = auth.OAuth2Spec

// SetupFieldSpec describes non-model-visible setup inputs needed to establish an auth method.
type SetupFieldSpec struct {
	Name          string  `json:"name" yaml:"name"`
	DisplayName   string  `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Description   string  `json:"description,omitempty" yaml:"description,omitempty"`
	Required      bool    `json:"required,omitempty" yaml:"required,omitempty"`
	RequiredGroup string  `json:"required_group,omitempty" yaml:"required_group,omitempty"`
	Sensitive     bool    `json:"sensitive,omitempty" yaml:"sensitive,omitempty"`
	Env           EnvSpec `json:"env,omitempty" yaml:"env,omitempty"`
}

func (s SetupFieldSpec) Shared() auth.FieldSpec {
	return auth.FieldSpec{Slot: shared.Slot(strings.TrimSpace(s.Name)), DisplayName: s.DisplayName, Description: s.Description, Required: s.Required, RequiredGroup: s.RequiredGroup, Sensitive: s.Sensitive, Env: auth.EnvSpec(s.Env)}.Normalize()
}

// Placeholder is the model-visible opaque secret token.
type Placeholder = shared.Placeholder

// PlaceholderFor renders a handle as a model-visible placeholder.
func PlaceholderFor(handle string) Placeholder { return shared.PlaceholderFor(handle) }

// ParsePlaceholder parses a complete placeholder.
func ParsePlaceholder(value string) (string, bool) { return shared.ParsePlaceholder(value) }

// ReplacePlaceholders replaces all placeholders in value.
func ReplacePlaceholders(value string, replace func(handle string) (string, error)) (string, error) {
	return shared.ReplacePlaceholders(value, replace)
}

// RedactPlaceholders removes model-visible secret handles from value.
func RedactPlaceholders(value string) string { return shared.RedactPlaceholders(value) }

// ValidateAuthMethod returns an error for incomplete method specs.
func ValidateAuthMethod(spec AuthMethodSpec) error {
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" {
		return fmt.Errorf("secret auth method name is empty")
	}
	if spec.Method == "" {
		return fmt.Errorf("secret auth method %q method is empty", spec.Name)
	}
	if spec.Kind == "" {
		return fmt.Errorf("secret auth method %q kind is empty", spec.Name)
	}
	switch spec.Method {
	case AuthMethodEnv:
		if strings.TrimSpace(spec.Env.Name) == "" && len(nonEmpty(spec.Env.Aliases)) == 0 {
			return fmt.Errorf("secret auth method %q env config is empty", spec.Name)
		}
	case AuthMethodOAuth2:
		if strings.TrimSpace(spec.OAuth2.AuthorizeURL) == "" {
			return fmt.Errorf("secret auth method %q oauth2 authorize_url is empty", spec.Name)
		}
		if strings.TrimSpace(spec.OAuth2.TokenURL) == "" {
			return fmt.Errorf("secret auth method %q oauth2 token_url is empty", spec.Name)
		}
		if spec.Secret.Normalize().ResourceName() == "" {
			return fmt.Errorf("secret auth method %q secret ref is empty", spec.Name)
		}
	case AuthMethodStored:
		if spec.Secret.Normalize().ResourceName() == "" {
			return fmt.Errorf("secret auth method %q secret ref is empty", spec.Name)
		}
	default:
		return fmt.Errorf("secret auth method %q method %q is unsupported", spec.Name, spec.Method)
	}
	return nil
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

var placeholderRE = regexp.MustCompile(`\$\{secret:([A-Za-z0-9._~+/=-]+)\}`)

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
