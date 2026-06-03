package session

import (
	"context"
	"fmt"
	"sort"
	"strings"

	coreactivation "github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/orchestration/sessioncontrol"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	runtimeskill "github.com/fluxplane/fluxplane-core/runtime/skill"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-operation"
	coreskill "github.com/fluxplane/fluxplane-skill"
)

type activateCommandInput struct {
	Terms    []string                `json:"terms,omitempty" command:"arg"`
	Duration coreactivation.Lifetime `json:"duration,omitempty" command:"flag=duration,default=run"`
}

type surfacePreparation struct {
	Request     coreactivation.SurfacePrepareRequested `json:"request"`
	Resolved    coreactivation.SurfaceResolved         `json:"resolved"`
	Prepared    coreactivation.SurfacePrepared         `json:"prepared"`
	Skipped     []coreactivation.SurfacePrepareSkipped `json:"skipped,omitempty"`
	Active      coreactivation.ActiveSurface           `json:"active,omitempty"`
	Diagnostics []coreactivation.Diagnostic            `json:"diagnostics,omitempty"`
}

func (s Session) executeActivateCommand(ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	input, err := parseActivateCommand(*inbound.Command)
	if err != nil {
		return CommandResult{
			Status: CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &CommandError{Code: "invalid_activate_command_input", Message: err.Error()},
		}
	}
	if len(input.Terms) == 0 {
		return CommandResult{
			Status: CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &CommandError{Code: "activate_terms_empty", Message: "activate requires at least one term"},
		}
	}
	prepared := s.prepareSurface(ctx, surfacePrepareRequest{
		Terms:    input.Terms,
		Lifetime: input.Duration,
		Source:   coreactivation.SourceUserCommand,
		RunID:    inbound.ID,
	})
	text := renderSurfacePreparation(prepared)
	return CommandResult{Status: CommandStatusOK, Spec: spec, Policy: evaluation, Output: operation.Rendered{
		Text: text,
		Data: prepared,
	}}
}

func parseActivateCommand(inv command.Invocation) (activateCommandInput, error) {
	input, err := command.Bind[activateCommandInput](inv)
	if err != nil {
		return activateCommandInput{}, err
	}
	if inv.Input != nil {
		structured, err := decodeCommandInput[activateCommandInput](inv.Input)
		if err != nil {
			return activateCommandInput{}, err
		}
		if len(structured.Terms) > 0 {
			input.Terms = structured.Terms
		}
		if structured.Duration != "" {
			input.Duration = structured.Duration
		}
	}
	input.Terms = cleanTerms(input.Terms)
	if input.Duration == "" {
		input.Duration = coreactivation.LifetimeRun
	}
	switch input.Duration {
	case coreactivation.LifetimeTurn, coreactivation.LifetimeRun, coreactivation.LifetimeSession:
	default:
		return activateCommandInput{}, fmt.Errorf("unsupported duration %q", input.Duration)
	}
	return input, nil
}

type surfacePrepareRequest struct {
	Terms    []string
	Lifetime coreactivation.Lifetime
	Source   coreactivation.Source
	RunID    string
}

func (s Session) prepareSurface(ctx context.Context, req surfacePrepareRequest) surfacePreparation {
	return s.prepareSurfaceWithEmit(ctx, req, s.emitLive)
}

func (s Session) prepareSurfaceWithEmit(ctx context.Context, req surfacePrepareRequest, emit func(sessionenv.Event)) surfacePreparation {
	if emit == nil {
		emit = s.emitLive
	}
	if req.Lifetime == "" {
		req.Lifetime = coreactivation.LifetimeRun
	}
	terms := cleanTerms(req.Terms)
	requested := coreactivation.SurfacePrepareRequested{
		Terms:    terms,
		Lifetime: req.Lifetime,
		Source:   req.Source,
	}
	emit(requested)

	resolved := s.resolveSurfaceTerms(terms)
	emit(resolved)

	var active sessionenv.ActiveState
	prepared := coreactivation.SurfacePrepared{
		Lifetime: req.Lifetime,
		Source:   req.Source,
	}
	var skipped []coreactivation.SurfacePrepareSkipped
	for _, set := range resolved.ActivationSets {
		prepared.ActivationSets = append(prepared.ActivationSets, set)
		active.EnableActivationSet(set)
	}
	diagnostics := append([]coreactivation.Diagnostic(nil), resolved.Diagnostics...)
	diagnostics = append(diagnostics, resolved.Skipped...)
	for _, target := range resolvedTargetList(resolved, s.ActivationSets) {
		if diag, ok := s.applyActivationTarget(ctx, target, &active, &prepared); ok {
			diagnostics = append(diagnostics, diag)
			skip := coreactivation.SurfacePrepareSkipped{
				Reason:     diag.Reason,
				Source:     req.Source,
				Diagnostic: diag,
			}
			skipped = append(skipped, skip)
			emit(skip)
		}
	}
	prepared.Diagnostics = diagnostics
	sortSurfacePrepared(&prepared)
	emit(prepared)
	return surfacePreparation{
		Request:     requested,
		Resolved:    resolved,
		Prepared:    prepared,
		Skipped:     skipped,
		Active:      active.ActiveSurface(),
		Diagnostics: diagnostics,
	}
}

func (s Session) resolveSurfaceTerms(terms []string) coreactivation.SurfaceResolved {
	resolved := coreactivation.SurfaceResolved{}
	activationSetsByName := map[string]coreactivation.Set{}
	activationAliases := map[string]string{}
	for _, set := range s.ActivationSets {
		name := strings.TrimSpace(set.Name)
		if name == "" {
			continue
		}
		activationSetsByName[name] = set
		for _, alias := range set.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				activationAliases[alias] = name
			}
		}
	}
	operationSets := map[string]bool{}
	for _, set := range s.OperationSets {
		if name := strings.TrimSpace(set.Name); name != "" {
			operationSets[name] = true
		}
	}
	contextProviders := map[string]bool{}
	for _, provider := range s.ContextProviders {
		if provider == nil {
			continue
		}
		if name := strings.TrimSpace(string(provider.Spec().Name)); name != "" {
			contextProviders[name] = true
		}
	}
	datasources := map[string]bool{}
	for _, spec := range s.Datasources {
		if name := strings.TrimSpace(string(spec.Name)); name != "" {
			datasources[name] = true
		}
	}
	if s.Agent != nil {
		for _, ref := range s.Agent.Spec().Datasources {
			if name := strings.TrimSpace(string(ref.Name)); name != "" {
				datasources[name] = true
			}
		}
	}

	seenSets := map[string]bool{}
	seenResources := map[string]bool{}
	for _, term := range terms {
		resolvedTerm := s.resolveSurfaceTerm(term, activationSetsByName, activationAliases, operationSets, contextProviders, datasources)
		switch {
		case resolvedTerm.ActivationSet != "":
			name := resolvedTerm.ActivationSet
			if !seenSets[name] {
				resolved.ActivationSets = append(resolved.ActivationSets, name)
				seenSets[name] = true
			}
		case resolvedTerm.Resource.Kind != "":
			key := string(resolvedTerm.Resource.Kind) + ":" + resolvedTerm.Resource.Name
			if !seenResources[key] {
				resolved.Resources = append(resolved.Resources, resolvedTerm.Resource)
				seenResources[key] = true
			}
		default:
			resolved.UnmatchedTerms = append(resolved.UnmatchedTerms, term)
			resolved.Diagnostics = append(resolved.Diagnostics, coreactivation.Diagnostic{
				Term:    term,
				Reason:  "not_selected",
				Message: fmt.Sprintf("no selected activation set or resource matched %q", term),
			})
		}
	}
	sort.Strings(resolved.ActivationSets)
	sort.Slice(resolved.Resources, func(i, j int) bool {
		if resolved.Resources[i].Kind == resolved.Resources[j].Kind {
			return resolved.Resources[i].Name < resolved.Resources[j].Name
		}
		return resolved.Resources[i].Kind < resolved.Resources[j].Kind
	})
	return resolved
}

type resolvedSurfaceTerm struct {
	ActivationSet string
	Resource      coreactivation.ResolvedResource
}

func (s Session) resolveSurfaceTerm(term string, activationSetsByName map[string]coreactivation.Set, activationAliases map[string]string, operationSets, contextProviders, datasources map[string]bool) resolvedSurfaceTerm {
	if name := resolveActivationSetTerm(term, activationSetsByName, activationAliases); name != "" {
		return resolvedSurfaceTerm{ActivationSet: name}
	}
	if resource, ok := s.resolveResourceSurfaceTerm(term, "", operationSets, contextProviders, datasources); ok {
		return resolvedSurfaceTerm{Resource: resource}
	}
	for _, phrase := range surfaceTypedTermPhrases(term) {
		if name := resolveActivationSetTerm(phrase.Name, activationSetsByName, activationAliases); name != "" && phrase.Kind == "surface" {
			return resolvedSurfaceTerm{ActivationSet: name}
		}
		if resource, ok := s.resolveResourceSurfaceTerm(phrase.Name, phrase.Kind, operationSets, contextProviders, datasources); ok {
			return resolvedSurfaceTerm{Resource: resource}
		}
	}
	return resolvedSurfaceTerm{}
}

func resolveActivationSetTerm(term string, activationSetsByName map[string]coreactivation.Set, activationAliases map[string]string) string {
	if activationSetsByName[term].Name != "" {
		return term
	}
	return activationAliases[term]
}

func (s Session) resolveResourceSurfaceTerm(term, kindHint string, operationSets, contextProviders, datasources map[string]bool) (coreactivation.ResolvedResource, bool) {
	switch kindHint {
	case "operation_set":
		if operationSets[term] {
			return coreactivation.ResolvedResource{Kind: coreactivation.TargetOperationSet, Name: term}, true
		}
		return coreactivation.ResolvedResource{}, false
	case "context_provider":
		if contextProviders[term] {
			return coreactivation.ResolvedResource{Kind: coreactivation.TargetContextProvider, Name: term}, true
		}
		return coreactivation.ResolvedResource{}, false
	case "datasource":
		if datasources[term] {
			return coreactivation.ResolvedResource{Kind: coreactivation.TargetDatasource, Name: term}, true
		}
		return coreactivation.ResolvedResource{}, false
	case "operation":
		if s.operationExists(operation.Ref{Name: operation.Name(term)}) {
			return coreactivation.ResolvedResource{Kind: coreactivation.TargetOperation, Name: term}, true
		}
		return coreactivation.ResolvedResource{}, false
	case "skill":
		if s.skillExists(term) {
			return coreactivation.ResolvedResource{Kind: coreactivation.TargetSkill, Name: term}, true
		}
		return coreactivation.ResolvedResource{}, false
	}
	switch {
	case operationSets[term]:
		return coreactivation.ResolvedResource{Kind: coreactivation.TargetOperationSet, Name: term}, true
	case contextProviders[term]:
		return coreactivation.ResolvedResource{Kind: coreactivation.TargetContextProvider, Name: term}, true
	case datasources[term]:
		return coreactivation.ResolvedResource{Kind: coreactivation.TargetDatasource, Name: term}, true
	case s.operationExists(operation.Ref{Name: operation.Name(term)}):
		return coreactivation.ResolvedResource{Kind: coreactivation.TargetOperation, Name: term}, true
	case s.skillExists(term):
		return coreactivation.ResolvedResource{Kind: coreactivation.TargetSkill, Name: term}, true
	}
	return coreactivation.ResolvedResource{}, false
}

type surfaceTypedTermPhrase struct {
	Name string
	Kind string
}

func surfaceTypedTermPhrases(term string) []surfaceTypedTermPhrase {
	normalized := strings.TrimSpace(term)
	if normalized == "" {
		return nil
	}
	var phrases []surfaceTypedTermPhrase
	for _, prefix := range []surfaceTypedTermPhrase{
		{Name: "activation set:", Kind: "surface"},
		{Name: "activation-set:", Kind: "surface"},
		{Name: "surface:", Kind: "surface"},
		{Name: "operation set:", Kind: "operation_set"},
		{Name: "operation-set:", Kind: "operation_set"},
		{Name: "op set:", Kind: "operation_set"},
		{Name: "context provider:", Kind: "context_provider"},
		{Name: "context-provider:", Kind: "context_provider"},
		{Name: "datasource:", Kind: "datasource"},
		{Name: "data source:", Kind: "datasource"},
		{Name: "operation:", Kind: "operation"},
		{Name: "tool:", Kind: "operation"},
		{Name: "skill:", Kind: "skill"},
	} {
		if name, ok := strings.CutPrefix(normalized, prefix.Name); ok {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				phrases = append(phrases, surfaceTypedTermPhrase{Name: trimmed, Kind: prefix.Kind})
			}
		}
	}
	for _, suffix := range []surfaceTypedTermPhrase{
		{Name: " activation set", Kind: "surface"},
		{Name: " activation-set", Kind: "surface"},
		{Name: " surface", Kind: "surface"},
		{Name: " operation set", Kind: "operation_set"},
		{Name: " operation-set", Kind: "operation_set"},
		{Name: " op set", Kind: "operation_set"},
		{Name: " context provider", Kind: "context_provider"},
		{Name: " context-provider", Kind: "context_provider"},
		{Name: " datasource", Kind: "datasource"},
		{Name: " data source", Kind: "datasource"},
		{Name: " operation", Kind: "operation"},
		{Name: " tool", Kind: "operation"},
		{Name: " skill", Kind: "skill"},
	} {
		if name, ok := strings.CutSuffix(normalized, suffix.Name); ok {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				phrases = append(phrases, surfaceTypedTermPhrase{Name: trimmed, Kind: suffix.Kind})
			}
		}
	}
	return phrases
}

func resolvedTargetList(resolved coreactivation.SurfaceResolved, sets []coreactivation.Set) []coreactivation.Target {
	byName := map[string]coreactivation.Set{}
	for _, set := range sets {
		byName[strings.TrimSpace(set.Name)] = set
	}
	var out []coreactivation.Target
	for _, name := range resolved.ActivationSets {
		set := byName[name]
		out = append(out, set.Targets...)
	}
	for _, resource := range resolved.Resources {
		switch resource.Kind {
		case coreactivation.TargetOperation:
			out = append(out, coreactivation.Target{Kind: coreactivation.TargetOperation, Operation: operation.Ref{Name: operation.Name(resource.Name)}})
		case coreactivation.TargetOperationSet:
			out = append(out, coreactivation.Target{Kind: coreactivation.TargetOperationSet, OperationSet: resource.Name})
		case coreactivation.TargetContextProvider:
			out = append(out, coreactivation.Target{Kind: coreactivation.TargetContextProvider, ContextProvider: corecontext.ProviderRef{Name: corecontext.ProviderName(resource.Name)}})
		case coreactivation.TargetDatasource:
			out = append(out, coreactivation.Target{Kind: coreactivation.TargetDatasource, Datasource: coredatasource.Ref{Name: coredatasource.Name(resource.Name)}})
		case coreactivation.TargetSkill:
			out = append(out, coreactivation.Target{Kind: coreactivation.TargetSkill, Skill: coreskill.Ref{Name: coreskill.Name(resource.Name)}})
		}
	}
	return out
}

func (s Session) applyActivationTarget(ctx context.Context, target coreactivation.Target, active *sessionenv.ActiveState, prepared *coreactivation.SurfacePrepared) (coreactivation.Diagnostic, bool) {
	switch target.Kind {
	case coreactivation.TargetOperation:
		if !s.operationExists(target.Operation) {
			return activationDiagnostic(target, "not_selected", "operation is not selected"), true
		}
		if active.EnableOperation(target.Operation) {
			prepared.Operations = append(prepared.Operations, target.Operation)
		}
	case coreactivation.TargetOperationSet:
		if !s.operationSetExists(target.OperationSet) {
			return activationDiagnostic(target, "not_selected", "operation set is not selected"), true
		}
		if active.EnableOperationSet(target.OperationSet) {
			prepared.OperationSets = append(prepared.OperationSets, target.OperationSet)
		}
	case coreactivation.TargetContextProvider:
		if !s.contextProviderExists(target.ContextProvider.Name) {
			return activationDiagnostic(target, "not_selected", "context provider is not selected"), true
		}
		if active.EnableContextProvider(target.ContextProvider.Name) {
			prepared.ContextProviders = append(prepared.ContextProviders, target.ContextProvider)
		}
	case coreactivation.TargetDatasource:
		if !s.datasourceExists(target.Datasource.Name) {
			return activationDiagnostic(target, "not_selected", "datasource is not selected"), true
		}
		if active.EnableDatasource(target.Datasource.Name) {
			prepared.Datasources = append(prepared.Datasources, target.Datasource)
		}
	case coreactivation.TargetSkill:
		if err := s.activateSurfaceSkill(target.Skill.Name); err != nil {
			return activationDiagnostic(target, "unavailable", err.Error()), true
		}
		prepared.Skills = append(prepared.Skills, target.Skill)
	case coreactivation.TargetReference:
		if err := s.activateSurfaceReference(target.Reference); err != nil {
			return activationDiagnostic(target, "unavailable", err.Error()), true
		}
		prepared.References = append(prepared.References, target.Reference)
	case coreactivation.TargetInlineContext:
		block, err := inlineContextBlock(target.InlineContext)
		if err != nil {
			return activationDiagnostic(target, "invalid", err.Error()), true
		}
		if active.EnableInlineContext(block) {
			prepared.InlineContexts = append(prepared.InlineContexts, block.ID)
		}
	case coreactivation.TargetCommand, coreactivation.TargetWorkflow, coreactivation.TargetResource:
		return activationDiagnostic(target, "unsupported_target_kind", fmt.Sprintf("target kind %q is not supported for preparation yet", target.Kind)), true
	default:
		return activationDiagnostic(target, "unsupported_target_kind", fmt.Sprintf("target kind %q is not supported", target.Kind)), true
	}
	return coreactivation.Diagnostic{}, false
}

func (s Session) activateSurfaceSkill(name coreskill.Name) error {
	state, ok := runtimeskill.StateFromAgent(s.Agent)
	if !ok {
		return fmt.Errorf("skill activation state is unavailable")
	}
	_, changed, err := state.ActivateSkill(string(name))
	if err != nil {
		return err
	}
	if changed {
		s.emitLive(coreskill.SkillActivated{Skill: string(name)})
	}
	return nil
}

func (s Session) activateSurfaceReference(ref coreactivation.ReferenceTarget) error {
	if err := s.activateSurfaceSkill(ref.Skill.Name); err != nil {
		return err
	}
	state, ok := runtimeskill.StateFromAgent(s.Agent)
	if !ok {
		return fmt.Errorf("skill activation state is unavailable")
	}
	activated, err := state.ActivateReferences(string(ref.Skill.Name), []string{ref.Path})
	if err != nil {
		return err
	}
	for _, path := range activated {
		s.emitLive(coreskill.SkillReferenceActivated{Skill: string(ref.Skill.Name), Path: path})
	}
	return nil
}

func inlineContextBlock(target *coreactivation.ContextTarget) (corecontext.Block, error) {
	if target == nil {
		return corecontext.Block{}, fmt.Errorf("inline context is empty")
	}
	content := strings.TrimSpace(target.Content)
	if content == "" {
		content = strings.TrimSpace(target.Template)
	}
	if content == "" {
		return corecontext.Block{}, fmt.Errorf("inline context content is empty")
	}
	return corecontext.Block{
		ID:        strings.TrimSpace(target.ID),
		Provider:  "surface.inline",
		Kind:      corecontext.BlockText,
		Placement: corecontext.NormalizePlacement(target.Placement),
		Title:     target.Title,
		Content:   content,
		MediaType: target.MediaType,
		Metadata:  cloneStringMap(target.Annotations),
	}, nil
}

func activationDiagnostic(target coreactivation.Target, reason, message string) coreactivation.Diagnostic {
	return coreactivation.Diagnostic{
		Target:  activationTargetLabel(target),
		Reason:  reason,
		Message: message,
	}
}

func activationTargetLabel(target coreactivation.Target) string {
	switch target.Kind {
	case coreactivation.TargetOperation:
		return target.Operation.String()
	case coreactivation.TargetOperationSet:
		return target.OperationSet
	case coreactivation.TargetContextProvider:
		return string(target.ContextProvider.Name)
	case coreactivation.TargetDatasource:
		return string(target.Datasource.Name)
	case coreactivation.TargetSkill:
		return string(target.Skill.Name)
	case coreactivation.TargetReference:
		return string(target.Reference.Skill.Name) + ":" + target.Reference.Path
	case coreactivation.TargetInlineContext:
		if target.InlineContext != nil {
			return target.InlineContext.ID
		}
	}
	return string(target.Kind)
}

func (s Session) operationExists(ref operation.Ref) bool {
	if ref.Name == "" {
		return false
	}
	if len(s.OperationCatalog) > 0 {
		_, err := s.OperationCatalog.Resolve(ref.String(), sessioncontrol.ResourceID{})
		return err == nil
	}
	if s.Operations == nil {
		return false
	}
	_, ok := s.Operations.Resolve(ref)
	return ok
}

func (s Session) operationSetExists(name string) bool {
	name = strings.TrimSpace(name)
	for _, set := range s.OperationSets {
		if strings.TrimSpace(set.Name) == name {
			return true
		}
	}
	return false
}

func (s Session) contextProviderExists(name corecontext.ProviderName) bool {
	for _, provider := range s.ContextProviders {
		if provider != nil && provider.Spec().Name == name {
			return true
		}
	}
	return false
}

func (s Session) datasourceExists(name coredatasource.Name) bool {
	if s.Agent != nil {
		for _, ref := range s.Agent.Spec().Datasources {
			if ref.Name == name {
				return true
			}
		}
	}
	for _, spec := range s.Datasources {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func (s Session) skillExists(name string) bool {
	state, ok := runtimeskill.StateFromAgent(s.Agent)
	if !ok || state.Repository() == nil {
		return false
	}
	_, ok = state.Repository().Get(name)
	return ok
}

func sortSurfacePrepared(prepared *coreactivation.SurfacePrepared) {
	sort.Strings(prepared.ActivationSets)
	sort.Slice(prepared.Operations, func(i, j int) bool { return prepared.Operations[i].String() < prepared.Operations[j].String() })
	sort.Strings(prepared.OperationSets)
	sort.Slice(prepared.ContextProviders, func(i, j int) bool { return prepared.ContextProviders[i].Name < prepared.ContextProviders[j].Name })
	sort.Slice(prepared.Datasources, func(i, j int) bool { return prepared.Datasources[i].Name < prepared.Datasources[j].Name })
	sort.Slice(prepared.Skills, func(i, j int) bool { return prepared.Skills[i].Name < prepared.Skills[j].Name })
	sort.Slice(prepared.References, func(i, j int) bool {
		left := string(prepared.References[i].Skill.Name) + "\x00" + prepared.References[i].Path
		right := string(prepared.References[j].Skill.Name) + "\x00" + prepared.References[j].Path
		return left < right
	})
	sort.Strings(prepared.InlineContexts)
}

func renderSurfacePreparation(prepared surfacePreparation) string {
	var b strings.Builder
	b.WriteString("Surface prepared\n")
	if len(prepared.Prepared.ActivationSets) > 0 {
		b.WriteString("  activation sets: ")
		b.WriteString(strings.Join(prepared.Prepared.ActivationSets, ", "))
		b.WriteByte('\n')
	}
	if len(prepared.Prepared.Operations) > 0 {
		b.WriteString("  operations: ")
		b.WriteString(strings.Join(surfaceOperationRefs(prepared.Prepared.Operations), ", "))
		b.WriteByte('\n')
	}
	if len(prepared.Prepared.OperationSets) > 0 {
		b.WriteString("  operation sets: ")
		b.WriteString(strings.Join(prepared.Prepared.OperationSets, ", "))
		b.WriteByte('\n')
	}
	if len(prepared.Prepared.ContextProviders) > 0 {
		b.WriteString("  context providers: ")
		b.WriteString(strings.Join(surfaceContextRefs(prepared.Prepared.ContextProviders), ", "))
		b.WriteByte('\n')
	}
	if len(prepared.Prepared.Datasources) > 0 {
		b.WriteString("  datasources: ")
		b.WriteString(strings.Join(surfaceDatasourceRefs(prepared.Prepared.Datasources), ", "))
		b.WriteByte('\n')
	}
	if len(prepared.Prepared.Skills) > 0 {
		b.WriteString("  skills: ")
		b.WriteString(strings.Join(surfaceSkillRefs(prepared.Prepared.Skills), ", "))
		b.WriteByte('\n')
	}
	if len(prepared.Diagnostics) > 0 {
		b.WriteString("  diagnostics: ")
		var messages []string
		for _, diagnostic := range prepared.Diagnostics {
			messages = append(messages, firstNonEmptyString(diagnostic.Message, diagnostic.Reason, diagnostic.Target, diagnostic.Term))
		}
		b.WriteString(strings.Join(messages, "; "))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func cleanTerms(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

type inlineSurfaceContextProvider struct {
	blocks map[string]corecontext.Block
}

func (p inlineSurfaceContextProvider) Spec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             "surface.inline",
		Description:      "Inline context prepared by the active surface.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockText},
		DefaultPlacement: corecontext.PlacementDeveloper,
	}
}

func (p inlineSurfaceContextProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	ids := make([]string, 0, len(p.blocks))
	for id := range p.blocks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]corecontext.Block, 0, len(ids))
	for _, id := range ids {
		block := p.blocks[id]
		if block.Provider == "" {
			block.Provider = "surface.inline"
		}
		if block.Kind == "" {
			block.Kind = corecontext.BlockText
		}
		if block.Placement == "" {
			block.Placement = corecontext.PlacementDeveloper
		}
		out = append(out, block)
	}
	return out, nil
}

func (s Session) activeStateFromSurface(surface coreactivation.ActiveSurface) sessionenv.ActiveState {
	var active sessionenv.ActiveState
	for _, name := range surface.ActivationSets {
		active.EnableActivationSet(name)
		for _, set := range s.ActivationSets {
			if strings.TrimSpace(set.Name) != name {
				continue
			}
			for _, target := range set.Targets {
				applyActiveTargetSilent(target, &active)
			}
		}
	}
	for _, ref := range surface.Operations {
		active.EnableOperation(ref)
	}
	for _, name := range surface.OperationSets {
		active.EnableOperationSet(name)
	}
	for _, ref := range surface.ContextProviders {
		active.EnableContextProvider(ref.Name)
	}
	for _, ref := range surface.Datasources {
		active.EnableDatasource(ref.Name)
	}
	return active
}

func mergeActiveState(left, right sessionenv.ActiveState) sessionenv.ActiveState {
	out := left.Clone()
	for name, active := range right.ActivationSets {
		if active {
			out.EnableActivationSet(name)
		}
	}
	for ref, active := range right.Operations {
		if active {
			out.EnableOperation(ref)
		}
	}
	for name, active := range right.OperationSets {
		if active {
			out.EnableOperationSet(name)
		}
	}
	for name, active := range right.ContextProviders {
		if active {
			out.EnableContextProvider(name)
		}
	}
	for name, active := range right.Datasources {
		if active {
			out.EnableDatasource(name)
		}
	}
	for _, block := range right.InlineContexts {
		out.EnableInlineContext(block)
	}
	return out
}

func applyActiveTargetSilent(target coreactivation.Target, active *sessionenv.ActiveState) {
	switch target.Kind {
	case coreactivation.TargetOperation:
		active.EnableOperation(target.Operation)
	case coreactivation.TargetOperationSet:
		active.EnableOperationSet(target.OperationSet)
	case coreactivation.TargetContextProvider:
		active.EnableContextProvider(target.ContextProvider.Name)
	case coreactivation.TargetDatasource:
		active.EnableDatasource(target.Datasource.Name)
	case coreactivation.TargetInlineContext:
		block, err := inlineContextBlock(target.InlineContext)
		if err == nil {
			active.EnableInlineContext(block)
		}
	}
}

func (s Session) expireRunSurface(ctx context.Context) {
	model, err := s.surfaceReadModel(ctx)
	if err != nil || model.Active.Lifetime != coreactivation.LifetimeRun {
		return
	}
	expired := coreactivation.SurfaceExpired{
		ActivationSets:   append([]string(nil), model.Active.ActivationSets...),
		Operations:       append([]operation.Ref(nil), model.Active.Operations...),
		OperationSets:    append([]string(nil), model.Active.OperationSets...),
		ContextProviders: append([]corecontext.ProviderRef(nil), model.Active.ContextProviders...),
		Datasources:      append([]coredatasource.Ref(nil), model.Active.Datasources...),
		Skills:           append([]coreskill.Ref(nil), model.Active.Skills...),
		References:       append([]coreactivation.ReferenceTarget(nil), model.Active.References...),
		InlineContexts:   append([]string(nil), model.Active.InlineContexts...),
		Lifetime:         coreactivation.LifetimeRun,
		Reason:           "run_complete",
	}
	if len(expired.ActivationSets)+len(expired.Operations)+len(expired.OperationSets)+len(expired.ContextProviders)+len(expired.Datasources)+len(expired.Skills)+len(expired.References)+len(expired.InlineContexts) == 0 {
		return
	}
	s.emitLive(expired)
}
