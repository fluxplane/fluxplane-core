package openapi

import (
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	"github.com/fluxplane/fluxplane-core/core/resource"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	"github.com/getkin/kin-openapi/openapi3"
)

func authMethodsFor(ref resource.PluginRef, cfg SpecConfig, doc *openapi3.T) ([]auth.MethodSpec, map[string]auth.MethodSpec, map[string]*openapi3.SecurityScheme) {
	byScheme := map[string]auth.MethodSpec{}
	schemes := map[string]*openapi3.SecurityScheme{}
	if doc == nil || doc.Components == nil {
		return nil, byScheme, schemes
	}
	for name, schemeRef := range doc.Components.SecuritySchemes {
		if schemeRef == nil || schemeRef.Value == nil {
			continue
		}
		name = strings.TrimSpace(name)
		schemes[name] = schemeRef.Value
		configured, ok := cfg.Auth.Schemes[name]
		if !ok {
			continue
		}
		method := authMethodForScheme(ref, name, configured, schemeRef.Value)
		byScheme[name] = method
	}
	out := make([]auth.MethodSpec, 0, len(byScheme))
	for _, method := range byScheme {
		out = append(out, method)
	}
	return out, byScheme, schemes
}

func authMethodForScheme(ref resource.PluginRef, name string, cfg AuthSchemeConfig, scheme *openapi3.SecurityScheme) auth.MethodSpec {
	method := cfg.Method
	if method == "" {
		method = auth.MethodEnv
	}
	kind := cfg.Kind
	if kind == "" {
		kind = defaultSecretKind(scheme)
	}
	header := cfg.Header
	if strings.TrimSpace(header.Name) == "" {
		header = defaultHeaderSpec(scheme)
	}
	displayName := firstNonEmpty(cfg.DisplayName, "OpenAPI "+name)
	description := firstNonEmpty(cfg.Description, "Credential for OpenAPI security scheme "+name+".")
	return auth.MethodSpec{
		Name:        name,
		Method:      method,
		Kind:        kind,
		DisplayName: displayName,
		Description: description,
		Secret:      defaultSecretRef(ref, name, cfg.Secret),
		Env:         cfg.Env,
		Header:      header,
	}
}

func defaultSecretKind(scheme *openapi3.SecurityScheme) sharedsecret.Kind {
	if scheme == nil {
		return sharedsecret.KindAPIKey
	}
	if strings.EqualFold(scheme.Type, "http") {
		switch strings.ToLower(scheme.Scheme) {
		case "bearer":
			return sharedsecret.KindBearerToken
		case "basic":
			return sharedsecret.KindBasic
		}
	}
	if strings.EqualFold(scheme.Type, "oauth2") || strings.EqualFold(scheme.Type, "openIdConnect") {
		return sharedsecret.KindBearerToken
	}
	return sharedsecret.KindAPIKey
}

func defaultHeaderSpec(scheme *openapi3.SecurityScheme) auth.HeaderSpec {
	if scheme == nil {
		return auth.HeaderSpec{}
	}
	if strings.EqualFold(scheme.Type, "http") && strings.EqualFold(scheme.Scheme, "bearer") {
		return auth.HeaderSpec{Name: "Authorization", Scheme: "Bearer"}
	}
	if strings.EqualFold(scheme.Type, "apiKey") && strings.EqualFold(scheme.In, "header") {
		return auth.HeaderSpec{Name: scheme.Name}
	}
	if strings.EqualFold(scheme.Type, "oauth2") || strings.EqualFold(scheme.Type, "openIdConnect") {
		return auth.HeaderSpec{Name: "Authorization", Scheme: "Bearer"}
	}
	return auth.HeaderSpec{}
}

func defaultSecretRef(ref resource.PluginRef, name string, configured sharedsecret.Ref) sharedsecret.Ref {
	if configured.Normalize().ResourceName() != "" {
		return configured
	}
	return sharedsecret.Plugin(Name, ref.InstanceName(), sharedsecret.Slot(name))
}
