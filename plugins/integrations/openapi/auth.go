package openapi

import (
	"strings"

	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	"github.com/getkin/kin-openapi/openapi3"
)

func authMethodsFor(ref resource.PluginRef, cfg SpecConfig, doc *openapi3.T) ([]coresecret.AuthMethodSpec, map[string]coresecret.AuthMethodSpec, map[string]*openapi3.SecurityScheme) {
	byScheme := map[string]coresecret.AuthMethodSpec{}
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
	out := make([]coresecret.AuthMethodSpec, 0, len(byScheme))
	for _, method := range byScheme {
		out = append(out, method)
	}
	return out, byScheme, schemes
}

func authMethodForScheme(ref resource.PluginRef, name string, cfg AuthSchemeConfig, scheme *openapi3.SecurityScheme) coresecret.AuthMethodSpec {
	method := cfg.Method
	if method == "" {
		method = coresecret.AuthMethodEnv
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
	return coresecret.AuthMethodSpec{
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

func defaultSecretKind(scheme *openapi3.SecurityScheme) coresecret.Kind {
	if scheme == nil {
		return coresecret.KindAPIKey
	}
	if strings.EqualFold(scheme.Type, "http") {
		switch strings.ToLower(scheme.Scheme) {
		case "bearer":
			return coresecret.KindBearerToken
		case "basic":
			return coresecret.KindBasic
		}
	}
	if strings.EqualFold(scheme.Type, "oauth2") || strings.EqualFold(scheme.Type, "openIdConnect") {
		return coresecret.KindBearerToken
	}
	return coresecret.KindAPIKey
}

func defaultHeaderSpec(scheme *openapi3.SecurityScheme) coresecret.HeaderSpec {
	if scheme == nil {
		return coresecret.HeaderSpec{}
	}
	if strings.EqualFold(scheme.Type, "http") && strings.EqualFold(scheme.Scheme, "bearer") {
		return coresecret.HeaderSpec{Name: "Authorization", Scheme: "Bearer"}
	}
	if strings.EqualFold(scheme.Type, "apiKey") && strings.EqualFold(scheme.In, "header") {
		return coresecret.HeaderSpec{Name: scheme.Name}
	}
	if strings.EqualFold(scheme.Type, "oauth2") || strings.EqualFold(scheme.Type, "openIdConnect") {
		return coresecret.HeaderSpec{Name: "Authorization", Scheme: "Bearer"}
	}
	return coresecret.HeaderSpec{}
}

func defaultSecretRef(ref resource.PluginRef, name string, configured coresecret.Ref) coresecret.Ref {
	if configured.Normalize().ResourceName() != "" {
		return configured
	}
	return coresecret.Plugin(Name, ref.InstanceName(), name)
}
