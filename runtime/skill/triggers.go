package skill

import (
	"strings"

	coreevent "github.com/fluxplane/agentruntime/core/event"
	coreskill "github.com/fluxplane/agentruntime/core/skill"
)

// ActivateTriggers activates skills and references whose trigger phrases appear
// in text. Matching is intentionally plain text and case-insensitive.
func (s *ActivationState) ActivateTriggers(text string, sink coreevent.Sink) ([]coreevent.Event, error) {
	text = strings.ToLower(strings.TrimSpace(text))
	if s == nil || s.repo == nil || text == "" {
		return nil, nil
	}
	var emitted []coreevent.Event
	emit := func(evt coreevent.Event) {
		if evt == nil {
			return
		}
		emitted = append(emitted, evt)
		if sink != nil {
			sink.Emit(evt)
		}
	}
	for _, spec := range s.repo.List() {
		name := string(spec.Name)
		if triggerMatches(text, spec.Triggers) {
			_, changed, err := s.ActivateSkill(name)
			if err != nil {
				return emitted, err
			}
			if changed {
				emit(coreskill.SkillActivated{Skill: name})
			}
		}
		for _, ref := range spec.References {
			if !triggerMatches(text, ref.Triggers) {
				continue
			}
			_, changed, err := s.ActivateSkill(name)
			if err != nil {
				return emitted, err
			}
			if changed {
				emit(coreskill.SkillActivated{Skill: name})
			}
			activated, err := s.ActivateReferences(name, []string{ref.Path})
			if err != nil {
				return emitted, err
			}
			for _, path := range activated {
				emit(coreskill.SkillReferenceActivated{Skill: name, Path: path})
			}
		}
	}
	return emitted, nil
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
