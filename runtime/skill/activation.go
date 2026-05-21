package skill

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/engine/core/event"
	coreskill "github.com/fluxplane/engine/core/skill"
)

// Status describes how a skill is active in one session.
type Status string

const (
	StatusInactive Status = "inactive"
	StatusBase     Status = "base"
	StatusDynamic  Status = "dynamic"
)

// ActivationState is mutable session-local skill activation state.
type ActivationState struct {
	repo       *Repository
	base       map[string]bool
	dynamic    map[string]bool
	references map[string]map[string]bool
}

// NewActivationState validates and activates base agent skills.
func NewActivationState(repo *Repository, baseSkills []coreskill.Ref) (*ActivationState, error) {
	if repo == nil {
		return nil, fmt.Errorf("skill: repository is nil")
	}
	state := &ActivationState{
		repo:       repo,
		base:       map[string]bool{},
		dynamic:    map[string]bool{},
		references: map[string]map[string]bool{},
	}
	for _, ref := range baseSkills {
		name := strings.TrimSpace(string(ref.Name))
		if name == "" {
			continue
		}
		if _, ok := repo.Get(name); !ok {
			return nil, fmt.Errorf("skill: %q not found", name)
		}
		state.base[name] = true
	}
	return state, nil
}

// Repository returns the underlying immutable catalog.
func (s *ActivationState) Repository() *Repository {
	if s == nil {
		return nil
	}
	return s.repo
}

// Status returns the current activation status for a skill.
func (s *ActivationState) Status(name string) Status {
	if s == nil {
		return StatusInactive
	}
	name = strings.TrimSpace(name)
	if s.base[name] {
		return StatusBase
	}
	if s.dynamic[name] {
		return StatusDynamic
	}
	return StatusInactive
}

// IsActive reports whether a skill is active.
func (s *ActivationState) IsActive(name string) bool {
	status := s.Status(name)
	return status == StatusBase || status == StatusDynamic
}

// ActivateSkill dynamically activates a discovered skill.
func (s *ActivationState) ActivateSkill(name string) (Status, bool, error) {
	if s == nil || s.repo == nil {
		return StatusInactive, false, fmt.Errorf("skill: activation state is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return StatusInactive, false, fmt.Errorf("skill: skill name is required")
	}
	if _, ok := s.repo.Get(name); !ok {
		return StatusInactive, false, fmt.Errorf("skill: %q not found", name)
	}
	before := s.Status(name)
	if before != StatusInactive {
		return before, false, nil
	}
	s.dynamic[name] = true
	return StatusDynamic, true, nil
}

// ActivateReferences activates exact references for an already-active skill.
func (s *ActivationState) ActivateReferences(skillName string, paths []string) ([]string, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("skill: activation state is nil")
	}
	skillName = strings.TrimSpace(skillName)
	if !s.IsActive(skillName) {
		return nil, fmt.Errorf("skill: references for %q require the skill to be active first", skillName)
	}
	seen := map[string]bool{}
	var requested []string
	for _, raw := range paths {
		path := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if !coreskill.ValidReferencePath(path) {
			return nil, fmt.Errorf("skill: invalid reference path %q", raw)
		}
		if _, ok := s.repo.GetReference(skillName, path); !ok {
			return nil, fmt.Errorf("skill: reference %q not found for skill %q", path, skillName)
		}
		requested = append(requested, path)
	}
	if len(requested) == 0 {
		return nil, nil
	}
	var activated []string
	for _, path := range requested {
		if s.references[skillName] == nil {
			s.references[skillName] = map[string]bool{}
		}
		if s.references[skillName][path] {
			continue
		}
		s.references[skillName][path] = true
		activated = append(activated, path)
	}
	sort.Strings(activated)
	return activated, nil
}

// ActiveSkills returns active skills ordered by name.
func (s *ActivationState) ActiveSkills() []coreskill.Spec {
	if s == nil || s.repo == nil {
		return nil
	}
	var out []coreskill.Spec
	for _, spec := range s.repo.List() {
		if s.IsActive(string(spec.Name)) {
			out = append(out, spec)
		}
	}
	return out
}

// ActiveSkillNames returns active skill names ordered by name.
func (s *ActivationState) ActiveSkillNames() []string {
	active := s.ActiveSkills()
	out := make([]string, 0, len(active))
	for _, spec := range active {
		out = append(out, string(spec.Name))
	}
	return out
}

// ActiveReferences returns active references for one skill ordered by path.
func (s *ActivationState) ActiveReferences(skillName string) []coreskill.ReferenceSpec {
	if s == nil || s.repo == nil {
		return nil
	}
	spec, ok := s.repo.Get(skillName)
	if !ok {
		return nil
	}
	set := s.references[strings.TrimSpace(skillName)]
	if len(set) == 0 {
		return nil
	}
	var out []coreskill.ReferenceSpec
	for _, ref := range spec.References {
		if set[ref.Path] {
			out = append(out, ref)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ApplyNamedEvent replays a persisted skill runtime event by event name.
func (s *ActivationState) ApplyNamedEvent(name event.Name, payload any) error {
	switch name {
	case coreskill.EventSkillActivated, coreskill.EventSkillReferenceActivated:
		return s.ApplyEvent(payload)
	default:
		return nil
	}
}

// ApplyEvent replays a persisted skill activation event.
func (s *ActivationState) ApplyEvent(evt any) error {
	switch payload := evt.(type) {
	case coreskill.SkillActivated:
		_, _, err := s.ActivateSkill(payload.Skill)
		return err
	case *coreskill.SkillActivated:
		if payload == nil {
			return nil
		}
		_, _, err := s.ActivateSkill(payload.Skill)
		return err
	case coreskill.SkillReferenceActivated:
		_, err := s.ActivateReferences(payload.Skill, []string{payload.Path})
		return err
	case *coreskill.SkillReferenceActivated:
		if payload == nil {
			return nil
		}
		_, err := s.ActivateReferences(payload.Skill, []string{payload.Path})
		return err
	case map[string]any:
		skillName, _ := payload["skill"].(string)
		path, _ := payload["path"].(string)
		if path != "" {
			_, err := s.ActivateReferences(skillName, []string{path})
			return err
		}
		_, _, err := s.ActivateSkill(skillName)
		return err
	default:
		return nil
	}
}
