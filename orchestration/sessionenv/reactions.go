package sessionenv

import (
	"fmt"
	"sort"
	"strings"

	coreactivation "github.com/fluxplane/engine/core/activation"
	corecontext "github.com/fluxplane/engine/core/context"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	coreevidence "github.com/fluxplane/engine/core/evidence"
	"github.com/fluxplane/engine/core/operation"
	corereaction "github.com/fluxplane/engine/core/reaction"
	coreskill "github.com/fluxplane/engine/core/skill"
	"github.com/fluxplane/engine/runtime/skill"
)

// ReactionAction is one planned reaction action ready for session-local
// application.
type ReactionAction struct {
	Rule           string
	Assertion      coreevidence.Assertion
	Action         corereaction.Action
	IdempotencyKey string
}

// ReactionApplyResult reports which planned reaction actions were applied and
// which could not be applied by the current session runtime.
type ReactionApplyResult struct {
	AppliedKeys []string
	Diagnostics []ReactionDiagnostic
}

// ActiveState records capabilities activated for the current session by
// reactions.
type ActiveState struct {
	ActivationSets   map[string]bool
	Operations       map[operation.Ref]bool
	OperationSets    map[string]bool
	Datasources      map[coredatasource.Name]bool
	ContextProviders map[corecontext.ProviderName]bool
	InlineContexts   map[string]corecontext.Block
}

// EnableActivationSet records an active activation set and reports whether
// state changed.
func (s *ActiveState) EnableActivationSet(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || s == nil {
		return false
	}
	if s.ActivationSets == nil {
		s.ActivationSets = map[string]bool{}
	}
	if s.ActivationSets[name] {
		return false
	}
	s.ActivationSets[name] = true
	return true
}

// EnableOperation records an active operation and reports whether state
// changed.
func (s *ActiveState) EnableOperation(ref operation.Ref) bool {
	if strings.TrimSpace(string(ref.Name)) == "" || s == nil {
		return false
	}
	if s.Operations == nil {
		s.Operations = map[operation.Ref]bool{}
	}
	if s.Operations[ref] {
		return false
	}
	s.Operations[ref] = true
	return true
}

// EnableOperationSet records an active operation set and reports whether state
// changed.
func (s *ActiveState) EnableOperationSet(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || s == nil {
		return false
	}
	if s.OperationSets == nil {
		s.OperationSets = map[string]bool{}
	}
	if s.OperationSets[name] {
		return false
	}
	s.OperationSets[name] = true
	return true
}

// EnableDatasource records an active datasource and reports whether state
// changed.
func (s *ActiveState) EnableDatasource(name coredatasource.Name) bool {
	if strings.TrimSpace(string(name)) == "" || s == nil {
		return false
	}
	if s.Datasources == nil {
		s.Datasources = map[coredatasource.Name]bool{}
	}
	if s.Datasources[name] {
		return false
	}
	s.Datasources[name] = true
	return true
}

// EnableContextProvider records an active context provider and reports whether
// state changed.
func (s *ActiveState) EnableContextProvider(name corecontext.ProviderName) bool {
	if strings.TrimSpace(string(name)) == "" || s == nil {
		return false
	}
	if s.ContextProviders == nil {
		s.ContextProviders = map[corecontext.ProviderName]bool{}
	}
	if s.ContextProviders[name] {
		return false
	}
	s.ContextProviders[name] = true
	return true
}

// EnableInlineContext records an active inline context block and reports
// whether state changed.
func (s *ActiveState) EnableInlineContext(block corecontext.Block) bool {
	id := strings.TrimSpace(block.ID)
	if id == "" || s == nil {
		return false
	}
	if s.InlineContexts == nil {
		s.InlineContexts = map[string]corecontext.Block{}
	}
	if _, exists := s.InlineContexts[id]; exists {
		return false
	}
	s.InlineContexts[id] = block
	return true
}

// ActiveSurface returns a read-model compatible view of active state.
func (s ActiveState) ActiveSurface() coreactivation.ActiveSurface {
	out := coreactivation.ActiveSurface{
		ActivationSets:   sortedBoolMapKeys(s.ActivationSets),
		Operations:       sortedOperationRefs(s.Operations),
		OperationSets:    sortedBoolMapKeys(s.OperationSets),
		ContextProviders: sortedContextProviderRefs(s.ContextProviders),
		Datasources:      sortedDatasourceRefs(s.Datasources),
		InlineContexts:   sortedInlineContextIDs(s.InlineContexts),
	}
	return out
}

// Clone returns a detached copy of active state.
func (s ActiveState) Clone() ActiveState {
	out := ActiveState{}
	if len(s.ActivationSets) > 0 {
		out.ActivationSets = map[string]bool{}
		for name, active := range s.ActivationSets {
			if active {
				out.ActivationSets[name] = true
			}
		}
	}
	if len(s.Operations) > 0 {
		out.Operations = map[operation.Ref]bool{}
		for ref, active := range s.Operations {
			if active {
				out.Operations[ref] = true
			}
		}
	}
	if len(s.OperationSets) > 0 {
		out.OperationSets = map[string]bool{}
		for name, active := range s.OperationSets {
			if active {
				out.OperationSets[name] = true
			}
		}
	}
	if len(s.Datasources) > 0 {
		out.Datasources = map[coredatasource.Name]bool{}
		for name, active := range s.Datasources {
			if active {
				out.Datasources[name] = true
			}
		}
	}
	if len(s.ContextProviders) > 0 {
		out.ContextProviders = map[corecontext.ProviderName]bool{}
		for name, active := range s.ContextProviders {
			if active {
				out.ContextProviders[name] = true
			}
		}
	}
	if len(s.InlineContexts) > 0 {
		out.InlineContexts = map[string]corecontext.Block{}
		for id, block := range s.InlineContexts {
			out.InlineContexts[id] = block
		}
	}
	return out
}

func sortedBoolMapKeys(values map[string]bool) []string {
	var out []string
	for value, active := range values {
		if active && strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func sortedOperationRefs(values map[operation.Ref]bool) []operation.Ref {
	var out []operation.Ref
	for ref, active := range values {
		if active && strings.TrimSpace(string(ref.Name)) != "" {
			out = append(out, ref)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func sortedContextProviderRefs(values map[corecontext.ProviderName]bool) []corecontext.ProviderRef {
	var out []corecontext.ProviderRef
	for name, active := range values {
		if active && strings.TrimSpace(string(name)) != "" {
			out = append(out, corecontext.ProviderRef{Name: name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedDatasourceRefs(values map[coredatasource.Name]bool) []coredatasource.Ref {
	var out []coredatasource.Ref
	for name, active := range values {
		if active && strings.TrimSpace(string(name)) != "" {
			out = append(out, coredatasource.Ref{Name: name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedInlineContextIDs(values map[string]corecontext.Block) []string {
	var out []string
	for id := range values {
		if strings.TrimSpace(id) != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// ReactionDiagnostic describes a reaction action that could not be applied.
type ReactionDiagnostic struct {
	Rule    string
	Action  corereaction.ActionKind
	Message string
}

// ApplyReactionActions applies low-risk session activation reactions. Effectful
// actions are left as diagnostics until they can enter the approval-gated
// operation/workflow paths.
func ApplyReactionActions(actions []ReactionAction, cfg Config) ReactionApplyResult {
	var result ReactionApplyResult
	for _, planned := range actions {
		if err := applyReactionAction(planned.Action, cfg); err != nil {
			result.Diagnostics = append(result.Diagnostics, ReactionDiagnostic{
				Rule:    planned.Rule,
				Action:  planned.Action.Kind,
				Message: err.Error(),
			})
			continue
		}
		if planned.IdempotencyKey != "" {
			result.AppliedKeys = append(result.AppliedKeys, planned.IdempotencyKey)
			emitRuntimeEvent(cfg, corereaction.ActionApplied{
				Rule:                 planned.Rule,
				Action:               planned.Action.Kind,
				IdempotencyKey:       planned.IdempotencyKey,
				Target:               reactionActionTarget(planned.Action),
				Assertion:            planned.Assertion.Kind,
				AssertionTarget:      planned.Assertion.Target,
				AssertionSubjectKind: string(planned.Assertion.Subject.Kind),
				AssertionSubjectName: planned.Assertion.Subject.Name,
				AssertionSubjectID:   planned.Assertion.Subject.ID,
				AssertionScope:       planned.Assertion.Scope,
				AssertionSource:      planned.Assertion.Source,
				ObservationIDs:       append([]string(nil), planned.Assertion.ObservationIDs...),
			})
		}
	}
	return result
}

func applyReactionAction(action corereaction.Action, cfg Config) error {
	switch action.Kind {
	case corereaction.ActionActivateSkill:
		return activateReactionSkill(action.Skill.Name, cfg)
	case corereaction.ActionActivateReference:
		return activateReactionReference(action.Reference, cfg)
	case corereaction.ActionEnableOperationSet:
		return activateReactionOperationSet(action.OperationSet, cfg)
	case corereaction.ActionEnableDatasource:
		return activateReactionDatasource(action.Datasource.Name, cfg)
	case corereaction.ActionEnableContext:
		return activateReactionContextProvider(action.ContextProvider.Name, cfg)
	case corereaction.ActionEnableActivationSet:
		return fmt.Errorf("reaction action %q is not supported yet", action.Kind)
	case corereaction.ActionRunWorkflow,
		corereaction.ActionRunOperation,
		corereaction.ActionRunCommand:
		return fmt.Errorf("reaction action %q is not supported yet", action.Kind)
	default:
		return fmt.Errorf("reaction action %q is invalid", action.Kind)
	}
}

func activateReactionSkill(name coreskill.Name, cfg Config) error {
	state, ok := skill.StateFromAgent(cfg.Agent)
	if !ok {
		return fmt.Errorf("skill activation state is unavailable")
	}
	_, changed, err := state.ActivateSkill(string(name))
	if err != nil {
		return err
	}
	if changed {
		emitRuntimeEvent(cfg, coreskill.SkillActivated{Skill: string(name)})
	}
	return nil
}

func activateReactionOperationSet(name string, cfg Config) error {
	if cfg.Active == nil {
		return fmt.Errorf("reaction active state is unavailable")
	}
	cfg.Active.EnableOperationSet(name)
	return nil
}

func activateReactionDatasource(name coredatasource.Name, cfg Config) error {
	if cfg.Active == nil {
		return fmt.Errorf("reaction active state is unavailable")
	}
	cfg.Active.EnableDatasource(name)
	return nil
}

func activateReactionContextProvider(name corecontext.ProviderName, cfg Config) error {
	if cfg.Active == nil {
		return fmt.Errorf("reaction active state is unavailable")
	}
	cfg.Active.EnableContextProvider(name)
	return nil
}

func reactionActionTarget(action corereaction.Action) string {
	switch action.Kind {
	case corereaction.ActionActivateSkill:
		return string(action.Skill.Name)
	case corereaction.ActionActivateReference:
		if action.Reference.Path == "" {
			return string(action.Reference.Skill.Name)
		}
		return string(action.Reference.Skill.Name) + ":" + action.Reference.Path
	case corereaction.ActionEnableActivationSet:
		return strings.TrimSpace(action.ActivationSet)
	case corereaction.ActionEnableOperationSet:
		return strings.TrimSpace(action.OperationSet)
	case corereaction.ActionEnableDatasource:
		return string(action.Datasource.Name)
	case corereaction.ActionEnableContext:
		return string(action.ContextProvider.Name)
	case corereaction.ActionRunWorkflow:
		return string(action.Workflow.Name)
	case corereaction.ActionRunOperation:
		return action.Operation.Operation.String()
	case corereaction.ActionRunCommand:
		return action.Command.Path.String()
	default:
		return ""
	}
}

func activateReactionReference(ref corereaction.ReferenceAction, cfg Config) error {
	state, ok := skill.StateFromAgent(cfg.Agent)
	if !ok {
		return fmt.Errorf("skill activation state is unavailable")
	}
	skillName := string(ref.Skill.Name)
	_, changed, err := state.ActivateSkill(skillName)
	if err != nil {
		return err
	}
	if changed {
		emitRuntimeEvent(cfg, coreskill.SkillActivated{Skill: skillName})
	}
	activated, err := state.ActivateReferences(skillName, []string{ref.Path})
	if err != nil {
		return err
	}
	for _, path := range activated {
		emitRuntimeEvent(cfg, coreskill.SkillReferenceActivated{Skill: skillName, Path: path})
	}
	return nil
}

func emitRuntimeEvent(cfg Config, payload Event) {
	if payload == nil {
		return
	}
	events := cfg.Events
	if events == nil {
		events = DiscardEvents()
	}
	events.Emit(payload)
}
