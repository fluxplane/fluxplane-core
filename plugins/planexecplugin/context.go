package planexecplugin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
)

const ContextProviderName corecontext.ProviderName = "planexec"

type contextProvider struct {
	plugin *Plugin
}

func contextSpec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             ContextProviderName,
		Description:      "Delegation profiles and current plan state for plan execution.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockText},
		DefaultPlacement: corecontext.PlacementSystem,
		Annotations:      map[string]string{corecontext.AnnotationAutoContext: "true"},
	}
}

func (p contextProvider) Spec() corecontext.ProviderSpec { return contextSpec() }

func (p contextProvider) Build(ctx context.Context, _ corecontext.Request) ([]corecontext.Block, error) {
	content := p.render(ctx)
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	return []corecontext.Block{{
		ID:        "planexec/current",
		Provider:  ContextProviderName,
		Kind:      corecontext.BlockText,
		Placement: corecontext.PlacementSystem,
		Title:     "Delegation and Planning",
		Content:   content,
		Freshness: corecontext.FreshnessDynamic,
	}}, nil
}

func (p contextProvider) render(ctx context.Context) string {
	var lines []string
	lines = append(lines, "Delegation and planning:")
	scope, ok := subagent.ScopeFromContext(ctx)
	if !ok {
		lines = append(lines, "- delegation: unavailable in this session")
	} else if profiles := profileNames(scope.Policy.AllowedProfiles); len(profiles) > 0 {
		lines = append(lines, "- delegation profiles: "+strings.Join(profiles, ", "))
		lines = append(lines, "- use delegate with profile set to one of: "+strings.Join(profiles, ", "))
	} else {
		lines = append(lines, "- delegation: unavailable; no profiles are allowed")
	}
	if p.plugin != nil {
		state := p.plugin.stateForContext(operation.NewContext(ctx, nil))
		lines = append(lines, renderPlanContext(state)...)
	}
	return strings.Join(lines, "\n")
}

func profileNames(profiles []coresession.Ref) []string {
	out := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Name != "" {
			out = append(out, string(profile.Name))
		}
	}
	sort.Strings(out)
	return out
}

func renderPlanContext(state PlanState) []string {
	if state.ID == "" {
		return []string{"- plan: none"}
	}
	lines := []string{
		"- plan id: " + state.ID,
		"- plan phase: " + string(state.Phase),
	}
	if strings.TrimSpace(state.Spec.Title) != "" {
		lines = append(lines, "- plan title: "+strings.TrimSpace(state.Spec.Title))
	}
	if strings.TrimSpace(state.Error) != "" {
		lines = append(lines, "- plan error: "+strings.TrimSpace(state.Error))
	}
	if len(state.Spec.Steps) == 0 {
		return lines
	}
	lines = append(lines, "- plan steps:")
	for _, step := range state.Spec.Steps {
		exec := state.Steps[step.ID]
		status := string(exec.Status)
		if status == "" {
			status = string(StepStatusWaiting)
		}
		label := strings.TrimSpace(step.Title)
		if label == "" {
			label = step.ID
		}
		profile := firstNonEmpty(exec.Profile, step.Profile)
		detail := fmt.Sprintf("  - %s: %s", step.ID, status)
		if label != step.ID {
			detail += " - " + label
		}
		if strings.TrimSpace(profile) != "" {
			detail += " (profile: " + strings.TrimSpace(profile) + ")"
		}
		lines = append(lines, detail)
	}
	return lines
}
