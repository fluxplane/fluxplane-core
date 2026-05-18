package secret

import (
	"fmt"
	"regexp"
	"strings"
)

// Scheme identifies how a secret ref is addressed.
type Scheme string

const (
	SchemeEnv    Scheme = "env"
	SchemePlugin Scheme = "plugin"
)

// Kind identifies the material shape stored or resolved for a secret.
type Kind string

const (
	KindAPIKey      Kind = "api_key"
	KindBearerToken Kind = "bearer_token"
	KindOAuth2Token Kind = "oauth2_token"
	KindBasic       Kind = "basic"
	KindPKI         Kind = "pki"
)

// AuthMethodKind identifies how credential material may be obtained.
type AuthMethodKind string

const (
	AuthMethodEnv    AuthMethodKind = "env"
	AuthMethodOAuth2 AuthMethodKind = "oauth2"
	AuthMethodStored AuthMethodKind = "stored"
)

// Ref identifies one secret without carrying its value.
type Ref struct {
	Scheme   Scheme `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	Plugin   string `json:"plugin,omitempty" yaml:"plugin,omitempty"`
	Instance string `json:"instance,omitempty" yaml:"instance,omitempty"`
	Name     string `json:"name" yaml:"name"`
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
func (r Ref) ResourceName() string {
	r = r.Normalize()
	switch r.Scheme {
	case SchemeEnv:
		if r.Name == "" {
			return "env/*"
		}
		return "env/" + r.Name
	case SchemePlugin:
		parts := []string{"plugin", r.Plugin}
		if r.Instance != "" {
			parts = append(parts, r.Instance)
		}
		if r.Name != "" {
			parts = append(parts, r.Name)
		}
		return strings.Join(nonEmpty(parts), "/")
	default:
		return strings.Trim(strings.Join(nonEmpty([]string{string(r.Scheme), r.Plugin, r.Instance, r.Name}), "/"), "/")
	}
}

// Env returns an env secret ref.
func Env(name string) Ref {
	return Ref{Scheme: SchemeEnv, Name: strings.TrimSpace(name)}
}

// Plugin returns a plugin-scoped secret ref.
func Plugin(plugin, instance, name string) Ref {
	return Ref{Scheme: SchemePlugin, Plugin: plugin, Instance: instance, Name: name}.Normalize()
}

// Material is secret material available only to trusted runtime code.
type Material struct {
	Kind  Kind   `json:"kind,omitempty"`
	Value string `json:"-"`
}

// AuthRequest asks runtime to obtain usable credential material for one plugin
// instance without exposing raw values to the model.
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

// AuthMethodSpec describes a way a plugin can authenticate without carrying
// credentials.
type AuthMethodSpec struct {
	Name        string         `json:"name" yaml:"name"`
	Method      AuthMethodKind `json:"method" yaml:"method"`
	Kind        Kind           `json:"kind" yaml:"kind"`
	DisplayName string         `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Secret      Ref            `json:"secret,omitempty" yaml:"secret,omitempty"`
	Env         EnvSpec        `json:"env,omitempty" yaml:"env,omitempty"`
	Header      HeaderSpec     `json:"header,omitempty" yaml:"header,omitempty"`
	OAuth2      OAuth2Spec     `json:"oauth2,omitempty" yaml:"oauth2,omitempty"`
}

// EnvSpec describes an environment-variable backed auth method. Name is the
// configured variable for the instance; Aliases are setup suggestions.
type EnvSpec struct {
	Name    string   `json:"name,omitempty" yaml:"name,omitempty"`
	Aliases []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
}

// HeaderSpec describes where an API key/token is applied.
type HeaderSpec struct {
	Name   string `json:"name,omitempty" yaml:"name,omitempty"`
	Scheme string `json:"scheme,omitempty" yaml:"scheme,omitempty"`
}

// OAuth2Spec describes OAuth2 authorization-code endpoints and scopes.
type OAuth2Spec struct {
	AuthorizeURL string   `json:"authorize_url,omitempty" yaml:"authorize_url,omitempty"`
	TokenURL     string   `json:"token_url,omitempty" yaml:"token_url,omitempty"`
	RefreshURL   string   `json:"refresh_url,omitempty" yaml:"refresh_url,omitempty"`
	Scopes       []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

// Placeholder is the model-visible opaque secret token.
type Placeholder string

var placeholderRE = regexp.MustCompile(`\$\{secret:([A-Za-z0-9._~+/=-]+)\}`)

// PlaceholderFor renders a handle as a model-visible placeholder.
func PlaceholderFor(handle string) Placeholder {
	return Placeholder("${secret:" + strings.TrimSpace(handle) + "}")
}

// ParsePlaceholder parses a complete placeholder.
func ParsePlaceholder(value string) (string, bool) {
	matches := placeholderRE.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 2 || matches[0] != strings.TrimSpace(value) {
		return "", false
	}
	return matches[1], true
}

// ReplacePlaceholders replaces all placeholders in value.
func ReplacePlaceholders(value string, replace func(handle string) (string, error)) (string, error) {
	if replace == nil {
		return value, nil
	}
	var first error
	out := placeholderRE.ReplaceAllStringFunc(value, func(match string) string {
		if first != nil {
			return match
		}
		handle, ok := ParsePlaceholder(match)
		if !ok {
			return match
		}
		replacement, err := replace(handle)
		if err != nil {
			first = err
			return match
		}
		return replacement
	})
	if first != nil {
		return "", first
	}
	return out, nil
}

// RedactPlaceholders removes model-visible secret handles from value.
func RedactPlaceholders(value string) string {
	return placeholderRE.ReplaceAllString(value, "${secret:redacted}")
}

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
