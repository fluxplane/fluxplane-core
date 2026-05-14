package llmagent

import (
	"context"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	corellmagent "github.com/fluxplane/agentruntime/core/agent/llmagent"
	corecontext "github.com/fluxplane/agentruntime/core/context"
)

// SelfContextProviderName identifies the runtime-injected agent self context.
const SelfContextProviderName corecontext.ProviderName = "agent.self"

type selfContextProvider struct {
	spec   agent.Spec
	driver corellmagent.Spec
	model  Model
}

func newSelfContextProvider(spec agent.Spec, driver corellmagent.Spec, model Model) corecontext.Provider {
	return selfContextProvider{spec: spec, driver: driver, model: model}
}

func (p selfContextProvider) Spec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             SelfContextProviderName,
		Description:      "Runtime identity and model information for the current agent.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockText},
		DefaultPlacement: corecontext.PlacementSystem,
		Annotations:      map[string]string{corecontext.AnnotationAutoContext: "true"},
	}
}

func (p selfContextProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	lines := []string{
		"Current agent:",
		"- name: " + fallbackString(strings.TrimSpace(string(p.spec.Name)), "(unknown)"),
		"- model: " + p.modelLabel(),
	}
	return []corecontext.Block{{
		ID:        "agent.self/current",
		Provider:  SelfContextProviderName,
		Kind:      corecontext.BlockText,
		Placement: corecontext.PlacementSystem,
		Title:     "Current Agent",
		Content:   strings.Join(lines, "\n"),
		Freshness: corecontext.FreshnessDynamic,
	}}, nil
}

func (p selfContextProvider) modelLabel() string {
	req := Request{Agent: p.spec, Driver: p.driver}
	model := strings.TrimSpace(modelName(req))
	provider := ""
	if identified, ok := p.model.(ProviderIdentityModel); ok {
		identity := identified.ProviderIdentity(req)
		provider = strings.TrimSpace(identity.Provider)
		if strings.TrimSpace(identity.Model) != "" {
			model = strings.TrimSpace(identity.Model)
		}
	}
	switch {
	case provider != "" && model != "":
		return provider + "/" + model
	case model != "":
		return model
	case provider != "":
		return provider
	default:
		return "(unknown)"
	}
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
