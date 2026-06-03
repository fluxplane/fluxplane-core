package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coreskill "github.com/fluxplane/fluxplane-core/core/skill"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	coreevidence "github.com/fluxplane/fluxplane-evidence"
)

const (
	skillTriggerDeriverName       = "skill.triggers"
	assertionSkillRequested       = "skill.requested"
	assertionSkillReferenceNeeded = "skill.reference.requested"
)

type skillTriggerAssertionDeriver struct {
	skills []coreskill.Spec
}

func newSkillTriggerAssertionDeriver(skills []coreskill.Spec) runtimeevidence.AssertionDeriver {
	if !hasSkillTriggers(skills) {
		return nil
	}
	return skillTriggerAssertionDeriver{skills: append([]coreskill.Spec(nil), skills...)}
}

func (d skillTriggerAssertionDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             skillTriggerDeriverName,
		Description:      "Derives skill and reference request assertions from channel message observations.",
		ObservationKinds: []string{"channel.message", "session.continuation"},
	}
}

func (d skillTriggerAssertionDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if observation.Kind != "channel.message" && observation.Kind != "session.continuation" {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(skillTriggerText(observation.Content)))
		if text == "" {
			continue
		}
		for _, spec := range d.skills {
			skillName := strings.TrimSpace(string(spec.Name))
			if skillName == "" {
				continue
			}
			if triggerMatches(text, spec.Triggers) {
				out = append(out, coreevidence.Assertion{
					Kind:           assertionSkillRequested,
					Target:         skillName,
					Scope:          observation.Scope,
					Environment:    observation.Environment,
					Confidence:     1,
					ObservationIDs: observationIDs(observation.ID),
				})
			}
			for _, ref := range spec.References {
				if !triggerMatches(text, ref.Triggers) {
					continue
				}
				out = append(out, coreevidence.Assertion{
					Kind:           assertionSkillReferenceNeeded,
					Target:         ref.Path,
					Scope:          observation.Scope,
					Environment:    observation.Environment,
					Confidence:     1,
					ObservationIDs: observationIDs(observation.ID),
					Metadata: map[string]string{
						"skill": skillName,
					},
				})
			}
		}
	}
	return out, nil
}

func skillTriggerReactionBindings(skills []coreskill.Spec) []reactionRuleBinding {
	if !hasSkillTriggers(skills) {
		return nil
	}
	source := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "runtime/skill-triggers"}
	var out []reactionRuleBinding
	for _, spec := range skills {
		skillName := strings.TrimSpace(string(spec.Name))
		if skillName == "" {
			continue
		}
		if len(spec.Triggers) > 0 {
			out = append(out, reactionRuleBinding{Source: source, Rule: corereaction.Rule{
				Name: "skill.trigger." + skillName,
				When: corereaction.Matcher{
					Assertion: assertionSkillRequested,
					Target:    skillName,
				},
				Actions: []corereaction.Action{{
					Kind:  corereaction.ActionActivateSkill,
					Skill: coreskill.Ref{Name: spec.Name},
				}},
			}})
		}
		for _, ref := range spec.References {
			if len(ref.Triggers) == 0 {
				continue
			}
			out = append(out, reactionRuleBinding{Source: source, Rule: corereaction.Rule{
				Name: "skill.reference.trigger." + skillName + "." + ref.Path,
				When: corereaction.Matcher{
					Assertion: assertionSkillReferenceNeeded,
					Target:    ref.Path,
					Meta:      map[string]string{"skill": skillName},
				},
				Actions: []corereaction.Action{{
					Kind: corereaction.ActionActivateReference,
					Reference: corereaction.ReferenceAction{
						Skill: coreskill.Ref{Name: spec.Name},
						Path:  ref.Path,
					},
				}},
			}})
		}
	}
	return out
}

func hasSkillTriggers(skills []coreskill.Spec) bool {
	for _, spec := range skills {
		if len(spec.Triggers) > 0 {
			return true
		}
		for _, ref := range spec.References {
			if len(ref.Triggers) > 0 {
				return true
			}
		}
	}
	return false
}

func triggerMatches(text string, triggers []string) bool {
	for _, trigger := range triggers {
		trigger = strings.ToLower(strings.TrimSpace(trigger))
		if trigger != "" && strings.Contains(text, trigger) {
			return true
		}
	}
	return false
}

func skillTriggerText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return strings.TrimSpace(fmt.Sprint(typed))
		}
		return string(data)
	}
}

func observationIDs(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}
