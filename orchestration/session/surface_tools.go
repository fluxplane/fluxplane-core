package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	coreactivation "github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/environment"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-operation"
)

const surfaceSchemaProviderName = corecontext.ProviderName("surface.schema")

const (
	maxSurfaceSchemaBlocks = 12
	maxSurfaceSchemaBytes  = 12000
)

var (
	sessionFocusOperationRef   = operation.Ref{Name: "session_focus"}
	surfaceInfoOperationRef    = operation.Ref{Name: "surface_info"}
	surfacePrepareOperationRef = operation.Ref{Name: "surface_prepare"}
	surfaceCallOperationRef    = operation.Ref{Name: "surface_call"}
)

type sessionFocusInput struct {
	Objective        string                  `json:"objective,omitempty"`
	Intents          []string                `json:"intents,omitempty"`
	Subjects         []coreevidence.Subject  `json:"subjects,omitempty"`
	Summary          string                  `json:"summary,omitempty"`
	Rationale        string                  `json:"rationale,omitempty"`
	Confidence       float64                 `json:"confidence,omitempty"`
	RequestedSurface []string                `json:"requested_surface,omitempty"`
	Lifetime         coreactivation.Lifetime `json:"lifetime,omitempty"`
}

type surfaceInfoInput struct {
	Terms []string `json:"terms,omitempty"`
}

type surfaceInfoOutput struct {
	Surface  coreactivation.ReadModel       `json:"surface"`
	Resolved coreactivation.SurfaceResolved `json:"resolved,omitempty"`
}

type surfacePrepareInput struct {
	Terms    []string                `json:"terms,omitempty" jsonschema:"required"`
	Lifetime coreactivation.Lifetime `json:"lifetime,omitempty"`
}

type surfaceCallInput struct {
	Operation string          `json:"operation,omitempty" jsonschema:"required"`
	Input     operation.Value `json:"input,omitempty"`
}

func (s Session) applySurfaceOperation(ctx operation.Context, ref operation.Ref, input operation.Value, callID operation.CallID) (environment.EffectResult, bool) {
	op, ok := s.surfaceOperation(ref)
	if !ok {
		return environment.EffectResult{}, false
	}
	return s.executeOperation(ctx, op, input, callID), true
}

func (s Session) surfaceOperation(ref operation.Ref) (operation.Operation, bool) {
	switch ref.Name {
	case sessionFocusOperationRef.Name:
		return s.sessionFocusOperation(), true
	case surfaceInfoOperationRef.Name:
		return s.surfaceInfoOperation(), true
	case surfacePrepareOperationRef.Name:
		return s.surfacePrepareOperation(), true
	case surfaceCallOperationRef.Name:
		return s.surfaceCallOperation(), true
	default:
		return nil, false
	}
}

func (s Session) surfaceToolSpecs() []tool.Spec {
	ops := []operation.Operation{
		s.sessionFocusOperation(),
		s.surfaceInfoOperation(),
		s.surfacePrepareOperation(),
		s.surfaceCallOperation(),
	}
	out := make([]tool.Spec, 0, len(ops))
	for _, op := range ops {
		spec := op.Spec()
		out = append(out, tool.Spec{
			Name:        tool.Name(spec.Ref.Name),
			Description: spec.Description,
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: spec.Ref,
			},
			Input:       spec.Input,
			Output:      spec.Output,
			Semantics:   spec.Semantics,
			Annotations: cloneStringMap(spec.Annotations),
		})
	}
	return out
}

func (s Session) sessionFocusOperation() operation.Operation {
	return operationruntime.NewTypedResult[sessionFocusInput, operation.Rendered](operation.Spec{
		Ref:         sessionFocusOperationRef,
		Description: "Declare the current work focus and optionally request matching surface preparation.",
		Semantics:   surfaceToolSemantics(),
		Annotations: surfaceToolAnnotations(),
	}, func(ctx operation.Context, input sessionFocusInput) operation.Result {
		focus := coreactivation.FocusDetected{
			Objective:  strings.TrimSpace(input.Objective),
			Intents:    cleanTerms(input.Intents),
			Subjects:   append([]coreevidence.Subject(nil), input.Subjects...),
			Source:     coreactivation.SourceModelFocus,
			Summary:    strings.TrimSpace(input.Summary),
			Rationale:  strings.TrimSpace(input.Rationale),
			Confidence: input.Confidence,
		}
		ctx.Events().Emit(focus)
		var prepared *surfacePreparation
		if terms := cleanTerms(input.RequestedSurface); len(terms) > 0 {
			lifetime := normalizeSurfaceLifetime(input.Lifetime)
			result := s.prepareSurfaceWithEmit(ctx, surfacePrepareRequest{
				Terms:    terms,
				Lifetime: lifetime,
				Source:   coreactivation.SourceModelFocus,
			}, ctx.Events().Emit)
			prepared = &result
		}
		model := s.surfaceModelWithContext(ctx)
		data := map[string]any{
			"focus":   focus,
			"surface": model,
		}
		if prepared != nil {
			data["prepared"] = *prepared
		}
		return operation.OK(operation.Rendered{
			Text:  renderSurfaceFocusOperation(focus, prepared),
			Model: renderSurfaceFocusOperation(focus, prepared),
			Data:  data,
		})
	})
}

func (s Session) surfaceInfoOperation() operation.Operation {
	return operationruntime.NewTypedResult[surfaceInfoInput, operation.Rendered](operation.Spec{
		Ref:         surfaceInfoOperationRef,
		Description: "Inspect the current focus and prepared surface, optionally resolving surface terms.",
		Semantics:   surfaceToolSemantics(),
		Annotations: surfaceToolAnnotations(),
	}, func(ctx operation.Context, input surfaceInfoInput) operation.Result {
		out := surfaceInfoOutput{Surface: s.surfaceModelWithContext(ctx)}
		if terms := cleanTerms(input.Terms); len(terms) > 0 {
			out.Resolved = s.resolveSurfaceTerms(terms)
		}
		return operation.OK(operation.Rendered{
			Text:  renderSurfaceInfoOutput(out),
			Model: renderSurfaceInfoOutput(out),
			Data:  out,
		})
	})
}

func (s Session) surfacePrepareOperation() operation.Operation {
	return operationruntime.NewTypedResult[surfacePrepareInput, operation.Rendered](operation.Spec{
		Ref:         surfacePrepareOperationRef,
		Description: "Prepare activation sets or selected resources for this run.",
		Semantics:   surfaceToolSemantics(),
		Annotations: surfaceToolAnnotations(),
	}, func(ctx operation.Context, input surfacePrepareInput) operation.Result {
		terms := cleanTerms(input.Terms)
		if len(terms) == 0 {
			return operation.Failed("surface_prepare_terms_empty", "surface_prepare requires at least one term", nil)
		}
		prepared := s.prepareSurfaceWithEmit(ctx, surfacePrepareRequest{
			Terms:    terms,
			Lifetime: normalizeSurfaceLifetime(input.Lifetime),
			Source:   coreactivation.SourceModelPrepare,
		}, ctx.Events().Emit)
		return operation.OK(operation.Rendered{
			Text:  renderSurfacePreparation(prepared),
			Model: renderSurfacePreparation(prepared),
			Data:  prepared,
		})
	})
}

func (s Session) surfaceCallOperation() operation.Operation {
	return operationruntime.NewTypedResult[surfaceCallInput, operation.Value](operation.Spec{
		Ref: surfaceCallOperationRef,
		Description: "Call an operation that is active on the prepared surface. " +
			"Active operation schemas are listed in the developer context as 'Surface operation schema: <ref>' blocks; " +
			"pass that ref as `operation` and the matching JSON payload as `input`. " +
			"If the operation isn't active yet, call surface_prepare first (or session_focus with requested_surface).",
		Semantics:   surfaceToolSemantics(),
		Annotations: surfaceToolAnnotations(),
	}, func(ctx operation.Context, input surfaceCallInput) operation.Result {
		ref := parseSurfaceCallOperationRef(input.Operation)
		if ref.IsZero() {
			return operation.Failed("surface_call_operation_empty", "surface_call requires an operation name", nil)
		}
		if isSurfaceOperationRef(ref) {
			return operation.Rejected("surface_call_reserved_operation", "surface_call cannot call session surface operations", map[string]any{
				"operation": ref.String(),
			})
		}
		active, ok := sessionenv.ActiveStateFromContext(ctx)
		if !ok || !s.surfaceOperationActive(active, ref) {
			return operation.Rejected("surface_call_operation_not_active", "operation is not active on the prepared surface", map[string]any{
				"operation": ref.String(),
			})
		}
		effect := s.applyOperation(ctx, ref, input.Input, operation.CallIDFromContext(ctx))
		return effect.Result
	})
}

func surfaceToolSemantics() operation.Semantics {
	return operation.Semantics{
		Determinism: operation.DeterminismNonDeterministic,
		Effects:     operation.EffectSet{operation.EffectNone},
		Idempotency: operation.IdempotencyIdempotent,
		Risk:        operation.RiskLow,
	}
}

func surfaceToolAnnotations() map[string]string {
	return map[string]string{"projection": "session_surface"}
}

func (s Session) surfaceModelWithContext(ctx context.Context) coreactivation.ReadModel {
	model, err := s.surfaceReadModel(ctx)
	if err != nil {
		return coreactivation.ReadModel{}
	}
	if active, ok := sessionenv.ActiveStateFromContext(ctx); ok {
		model.Active = mergeActiveSurfaces(model.Active, active.ActiveSurface())
	}
	return model
}

func mergeActiveSurfaces(left, right coreactivation.ActiveSurface) coreactivation.ActiveSurface {
	state := sessionenv.ActiveState{}
	state = mergeActiveState(state, activeStateFromSurfaceValue(left))
	state = mergeActiveState(state, activeStateFromSurfaceValue(right))
	out := state.ActiveSurface()
	out.Lifetime = firstSurfaceLifetime(left.Lifetime, right.Lifetime)
	return out
}

func activeStateFromSurfaceValue(surface coreactivation.ActiveSurface) sessionenv.ActiveState {
	var active sessionenv.ActiveState
	for _, name := range surface.ActivationSets {
		active.EnableActivationSet(name)
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
	for _, id := range surface.InlineContexts {
		active.EnableInlineContext(corecontext.Block{ID: id})
	}
	return active
}

func firstSurfaceLifetime(values ...coreactivation.Lifetime) coreactivation.Lifetime {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s Session) activeStateFromSurfaceToolResult(result operation.Result) (sessionenv.ActiveState, bool) {
	if result.Status != operation.StatusOK {
		return sessionenv.ActiveState{}, false
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		return sessionenv.ActiveState{}, false
	}
	prepared, ok := rendered.Data.(surfacePreparation)
	if !ok {
		if data, isMap := rendered.Data.(map[string]any); isMap {
			if typed, isPrepared := data["prepared"].(surfacePreparation); isPrepared {
				prepared = typed
				ok = true
			}
		}
	}
	if !ok {
		return sessionenv.ActiveState{}, false
	}
	return s.activeStateFromSurface(prepared.Active), true
}

func (s Session) surfaceOperationActive(active sessionenv.ActiveState, ref operation.Ref) bool {
	if active.Operations[ref] {
		return true
	}
	for name, enabled := range active.OperationSets {
		if !enabled {
			continue
		}
		for _, set := range s.OperationSets {
			if strings.TrimSpace(set.Name) != name {
				continue
			}
			for _, selector := range set.Operations {
				if selector.Matches(ref) {
					return true
				}
			}
		}
	}
	return false
}

type surfaceSchemaContextProvider struct {
	session Session
	active  sessionenv.ActiveState
}

func (p surfaceSchemaContextProvider) Spec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             surfaceSchemaProviderName,
		Description:      "Operation schemas for the active prepared surface.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockText},
		DefaultPlacement: corecontext.PlacementDeveloper,
		Annotations: map[string]string{
			corecontext.AnnotationAutoContext: "true",
		},
	}
}

func (p surfaceSchemaContextProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	refs := p.activeOperationRefs()
	if len(refs) > maxSurfaceSchemaBlocks {
		refs = refs[:maxSurfaceSchemaBlocks]
	}
	var blocks []corecontext.Block
	usedBytes := 0
	for _, ref := range refs {
		op, ok := p.session.resolveOperation(ref)
		if !ok || op == nil {
			continue
		}
		block := surfaceSchemaBlock(op.Spec())
		if usedBytes+len(block.Content) > maxSurfaceSchemaBytes {
			break
		}
		usedBytes += len(block.Content)
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func (p surfaceSchemaContextProvider) activeOperationRefs() []operation.Ref {
	seen := map[string]operation.Ref{}
	for ref, enabled := range p.active.Operations {
		if enabled && !ref.IsZero() {
			seen[ref.String()] = ref
		}
	}
	for name, enabled := range p.active.OperationSets {
		if !enabled {
			continue
		}
		for _, set := range p.session.OperationSets {
			if strings.TrimSpace(set.Name) != name {
				continue
			}
			for _, selector := range set.Operations {
				for _, op := range p.session.matchingOperations(selector) {
					if op == nil {
						continue
					}
					ref := op.Spec().Ref
					if !ref.IsZero() {
						seen[ref.String()] = ref
					}
				}
			}
		}
	}
	refs := make([]operation.Ref, 0, len(seen))
	for _, ref := range seen {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	return refs
}

func surfaceSchemaBlock(spec operation.Spec) corecontext.Block {
	return corecontext.Block{
		ID:        "surface/schema/" + spec.Ref.String(),
		Provider:  surfaceSchemaProviderName,
		Kind:      corecontext.BlockText,
		Placement: corecontext.PlacementDeveloper,
		Title:     "Surface operation schema: " + spec.Ref.String(),
		Content:   renderSurfaceOperationSchema(spec),
		Freshness: corecontext.FreshnessDynamic,
		Metadata: map[string]string{
			"operation": spec.Ref.String(),
		},
	}
}

func renderSurfaceOperationSchema(spec operation.Spec) string {
	var b strings.Builder
	b.WriteString("Operation: ")
	b.WriteString(spec.Ref.String())
	b.WriteByte('\n')
	if spec.Description != "" {
		b.WriteString("Description: ")
		b.WriteString(spec.Description)
		b.WriteByte('\n')
	}
	if spec.Semantics.Risk != "" || len(spec.Semantics.Effects) > 0 {
		b.WriteString("Semantics:")
		if spec.Semantics.Risk != "" {
			b.WriteString(" risk=")
			b.WriteString(string(spec.Semantics.Risk))
		}
		if len(spec.Semantics.Effects) > 0 {
			b.WriteString(" effects=")
			b.WriteString(strings.Join(operationEffects(spec.Semantics.Effects), ","))
		}
		b.WriteByte('\n')
	}
	b.WriteString("Call with surface_call using operation ")
	b.WriteString(strconvQuote(spec.Ref.String()))
	b.WriteString(".\n")
	if !spec.Input.IsZero() {
		b.WriteString("Input type: ")
		b.WriteString(spec.Input.Name)
		b.WriteByte('\n')
		if len(spec.Input.Schema.Data) > 0 {
			b.WriteString("Input JSON Schema:\n")
			b.WriteString(compactJSON(spec.Input.Schema.Data))
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func operationEffects(effects operation.EffectSet) []string {
	out := make([]string, 0, len(effects))
	for _, effect := range effects {
		out = append(out, string(effect))
	}
	sort.Strings(out)
	return out
}

func compactJSON(raw []byte) string {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}

func strconvQuote(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `"` + value + `"`
	}
	return string(raw)
}

func normalizeSurfaceLifetime(value coreactivation.Lifetime) coreactivation.Lifetime {
	switch value {
	case coreactivation.LifetimeTurn, coreactivation.LifetimeRun, coreactivation.LifetimeSession:
		return value
	default:
		return coreactivation.LifetimeRun
	}
}

func parseSurfaceCallOperationRef(value string) operation.Ref {
	value = strings.TrimSpace(value)
	if value == "" {
		return operation.Ref{}
	}
	name, version, ok := strings.Cut(value, "@")
	if !ok {
		return operation.Ref{Name: operation.Name(value)}
	}
	return operation.Ref{Name: operation.Name(strings.TrimSpace(name)), Version: operation.Version(strings.TrimSpace(version))}
}

func isSurfaceOperationRef(ref operation.Ref) bool {
	switch ref.Name {
	case sessionFocusOperationRef.Name, surfaceInfoOperationRef.Name, surfacePrepareOperationRef.Name, surfaceCallOperationRef.Name:
		return true
	default:
		return false
	}
}

func renderSurfaceFocusOperation(focus coreactivation.FocusDetected, prepared *surfacePreparation) string {
	var b strings.Builder
	b.WriteString("Focus recorded")
	if focus.Objective != "" {
		b.WriteString(": ")
		b.WriteString(focus.Objective)
	}
	if prepared != nil {
		b.WriteByte('\n')
		b.WriteString(renderSurfacePreparation(*prepared))
	}
	return b.String()
}

func renderSurfaceInfoOutput(out surfaceInfoOutput) string {
	text := renderSurfaceReadModel(out.Surface)
	if len(out.Resolved.ActivationSets)+len(out.Resolved.Resources)+len(out.Resolved.UnmatchedTerms)+len(out.Resolved.Diagnostics) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\nResolved\n")
	if len(out.Resolved.ActivationSets) > 0 {
		b.WriteString("  activation sets: ")
		b.WriteString(strings.Join(out.Resolved.ActivationSets, ", "))
		b.WriteByte('\n')
	}
	if len(out.Resolved.Resources) > 0 {
		var resources []string
		for _, resource := range out.Resolved.Resources {
			resources = append(resources, fmt.Sprintf("%s:%s", resource.Kind, resource.Name))
		}
		b.WriteString("  resources: ")
		b.WriteString(strings.Join(resources, ", "))
		b.WriteByte('\n')
	}
	if len(out.Resolved.UnmatchedTerms) > 0 {
		b.WriteString("  unmatched: ")
		b.WriteString(strings.Join(out.Resolved.UnmatchedTerms, ", "))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
