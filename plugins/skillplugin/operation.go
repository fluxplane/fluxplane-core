package skillplugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/agentruntime/core/operation"
	coreskill "github.com/fluxplane/agentruntime/core/skill"
	runtimeskill "github.com/fluxplane/agentruntime/runtime/skill"
)

type actionInput struct {
	Actions []action `json:"actions" jsonschema:"description=Skill activation actions.,required"`
}

type action struct {
	Action     string   `json:"action" jsonschema:"description=Action name. Supported value: activate.,required"`
	Skill      string   `json:"skill,omitempty" jsonschema:"description=Skill name to activate."`
	References []string `json:"references,omitempty" jsonschema:"description=Exact relative reference paths such as references/tradeoffs.md."`
}

type actionOutput struct {
	Results      []actionResult `json:"results"`
	ActiveSkills []string       `json:"active_skills,omitempty"`
}

type actionResult struct {
	Action            string   `json:"action"`
	Skill             string   `json:"skill,omitempty"`
	SkillStatus       string   `json:"skill_status,omitempty"`
	ActivatedRefs     []string `json:"activated_references,omitempty"`
	AlreadyActiveRefs []string `json:"already_active_references,omitempty"`
	Error             string   `json:"error,omitempty"`
}

func runSkillOperation(ctx operation.Context, input actionInput) operation.Result {
	if len(input.Actions) == 0 {
		return operation.Failed("invalid_skill_input", "at least one action is required", nil)
	}
	state, ok := runtimeskill.StateFromContext(ctx)
	if !ok {
		return operation.Failed("skill_state_missing", "skill activation state is not available in this session", nil)
	}
	out := actionOutput{Results: make([]actionResult, 0, len(input.Actions))}
	for _, item := range input.Actions {
		result := actionResult{Action: strings.TrimSpace(item.Action), Skill: strings.TrimSpace(item.Skill)}
		switch result.Action {
		case "activate":
			applyActivate(ctx, state, item, &result)
		default:
			result.Error = fmt.Sprintf("unsupported action %q", item.Action)
		}
		out.Results = append(out.Results, result)
	}
	out.ActiveSkills = state.ActiveSkillNames()
	return operation.OK(operation.Rendered{Text: renderActionOutput(out), Data: out})
}

func applyActivate(ctx operation.Context, state *runtimeskill.ActivationState, item action, result *actionResult) {
	if result.Skill == "" {
		result.Error = "skill is required"
		return
	}
	before := state.Status(result.Skill)
	if len(item.References) > 0 && before == runtimeskill.StatusInactive {
		result.Error = fmt.Sprintf("references for %q require the skill to be active first", result.Skill)
		return
	}
	status, changed, err := state.ActivateSkill(result.Skill)
	if err != nil {
		result.Error = err.Error()
		return
	}
	result.SkillStatus = string(status)
	if changed {
		result.SkillStatus = "activated"
		ctx.Events().Emit(coreskill.SkillActivated{Skill: result.Skill})
	} else if before != runtimeskill.StatusInactive {
		result.SkillStatus = "already_active"
	}
	if len(item.References) == 0 {
		return
	}
	beforeRefs := activeRefSet(state.ActiveReferences(result.Skill))
	activated, err := state.ActivateReferences(result.Skill, item.References)
	if err != nil {
		result.Error = err.Error()
		return
	}
	result.ActivatedRefs = activated
	result.AlreadyActiveRefs = alreadyActiveRefs(beforeRefs, item.References, activated)
	for _, ref := range activated {
		ctx.Events().Emit(coreskill.SkillReferenceActivated{Skill: result.Skill, Path: ref})
	}
}

func activeRefSet(refs []coreskill.ReferenceSpec) map[string]bool {
	out := make(map[string]bool, len(refs))
	for _, ref := range refs {
		out[ref.Path] = true
	}
	return out
}

func alreadyActiveRefs(before map[string]bool, requested, activated []string) []string {
	activatedSet := map[string]bool{}
	for _, ref := range activated {
		activatedSet[ref] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, ref := range requested {
		ref = strings.TrimSpace(strings.ReplaceAll(ref, "\\", "/"))
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		if before[ref] && !activatedSet[ref] {
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	return out
}

func renderActionOutput(out actionOutput) string {
	var lines []string
	for _, result := range out.Results {
		if result.Error != "" {
			lines = append(lines, fmt.Sprintf("%s %q: %s", result.Action, result.Skill, result.Error))
			continue
		}
		line := fmt.Sprintf("%s %q: %s", result.Action, result.Skill, result.SkillStatus)
		if len(result.ActivatedRefs) > 0 {
			line += " refs=" + strings.Join(result.ActivatedRefs, ", ")
		}
		if len(result.AlreadyActiveRefs) > 0 {
			line += " already_active_refs=" + strings.Join(result.AlreadyActiveRefs, ", ")
		}
		lines = append(lines, line)
	}
	if len(out.ActiveSkills) > 0 {
		lines = append(lines, "active skills: "+strings.Join(out.ActiveSkills, ", "))
	}
	return strings.Join(lines, "\n")
}
