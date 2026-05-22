package activation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	corecontext "github.com/fluxplane/engine/core/context"
	"github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/skill"
)

// ReadModel is a compact current prepared-surface view derived from trace
// events. It is a read model only; it does not authorize or execute anything.
type ReadModel struct {
	Focus       *FocusSummary `json:"focus,omitempty"`
	Active      ActiveSurface `json:"active,omitempty"`
	Recent      []TraceEntry  `json:"recent,omitempty"`
	Diagnostics []Diagnostic  `json:"diagnostics,omitempty"`
}

// FocusSummary is the latest detected work focus.
type FocusSummary struct {
	Objective  string        `json:"objective,omitempty"`
	Intents    []string      `json:"intents,omitempty"`
	Subjects   []string      `json:"subjects,omitempty"`
	Sources    []FocusSource `json:"sources,omitempty"`
	Source     Source        `json:"source,omitempty"`
	Confidence float64       `json:"confidence,omitempty"`
}

// ActiveSurface is the prepared surface currently reflected by events.
type ActiveSurface struct {
	ActivationSets   []string                  `json:"activation_sets,omitempty"`
	Operations       []operation.Ref           `json:"operations,omitempty"`
	OperationSets    []string                  `json:"operation_sets,omitempty"`
	ContextProviders []corecontext.ProviderRef `json:"context_providers,omitempty"`
	Datasources      []datasource.Ref          `json:"datasources,omitempty"`
	Skills           []skill.Ref               `json:"skills,omitempty"`
	References       []ReferenceTarget         `json:"references,omitempty"`
	InlineContexts   []string                  `json:"inline_contexts,omitempty"`
	Lifetime         Lifetime                  `json:"lifetime,omitempty"`
}

// TraceEntry is one compact timeline item for focus/surface inspection.
type TraceEntry struct {
	Event   event.Name `json:"event"`
	Summary string     `json:"summary,omitempty"`
	Source  Source     `json:"source,omitempty"`
}

// Apply applies one typed focus/surface event to the read model.
func (m *ReadModel) Apply(ev event.Event) error {
	if ev == nil {
		return nil
	}
	return m.ApplyNamed(ev.EventName(), ev)
}

// ApplyNamed applies a focus/surface event by event name and payload. Payloads
// may be typed structs or JSON-decoded maps from a remote transport.
func (m *ReadModel) ApplyNamed(name event.Name, payload any) error {
	if m == nil {
		return nil
	}
	switch name {
	case EventFocusDetected:
		var typed FocusDetected
		if err := decodePayload(payload, &typed); err != nil {
			return err
		}
		m.applyFocusDetected(typed)
	case EventSurfacePrepareRequested:
		var typed SurfacePrepareRequested
		if err := decodePayload(payload, &typed); err != nil {
			return err
		}
		m.appendTrace(name, joinNonEmpty(typed.Objective, strings.Join(typed.Terms, " ")), typed.Source)
	case EventSurfaceResolved:
		var typed SurfaceResolved
		if err := decodePayload(payload, &typed); err != nil {
			return err
		}
		m.Diagnostics = append(m.Diagnostics, typed.Diagnostics...)
		m.Diagnostics = append(m.Diagnostics, typed.Skipped...)
		summary := joinNonEmpty(strings.Join(typed.ActivationSets, ", "), resourcesSummary(typed.Resources))
		if summary == "" && len(typed.UnmatchedTerms) > 0 {
			summary = "unmatched: " + strings.Join(typed.UnmatchedTerms, ", ")
		}
		m.appendTrace(name, summary, "")
	case EventSurfacePrepared:
		var typed SurfacePrepared
		if err := decodePayload(payload, &typed); err != nil {
			return err
		}
		m.applyPrepared(typed)
	case EventSurfacePrepareSkipped:
		var typed SurfacePrepareSkipped
		if err := decodePayload(payload, &typed); err != nil {
			return err
		}
		if typed.Diagnostic.Message != "" || typed.Diagnostic.Reason != "" {
			m.Diagnostics = append(m.Diagnostics, typed.Diagnostic)
		}
		m.appendTrace(name, joinNonEmpty(typed.ActivationSet, typed.Resource, typed.Reason), typed.Source)
	case EventSurfaceExpired:
		var typed SurfaceExpired
		if err := decodePayload(payload, &typed); err != nil {
			return err
		}
		m.applyExpired(typed)
	default:
		return nil
	}
	return nil
}

func (m *ReadModel) applyFocusDetected(ev FocusDetected) {
	subjects := make([]string, 0, len(ev.Subjects))
	for _, subject := range ev.Subjects {
		label := strings.TrimSpace(subject.Name)
		if label == "" {
			label = strings.TrimSpace(subject.ID)
		}
		if label != "" {
			subjects = append(subjects, label)
		}
	}
	m.Focus = &FocusSummary{
		Objective:  ev.Objective,
		Intents:    append([]string(nil), ev.Intents...),
		Subjects:   subjects,
		Sources:    append([]FocusSource(nil), ev.Sources...),
		Source:     ev.Source,
		Confidence: ev.Confidence,
	}
	m.appendTrace(EventFocusDetected, joinNonEmpty(ev.Objective, strings.Join(ev.Intents, ", ")), ev.Source)
}

func (m *ReadModel) applyPrepared(ev SurfacePrepared) {
	m.Active.ActivationSets = sortedStrings(mergeStrings(m.Active.ActivationSets, ev.ActivationSets))
	m.Active.Operations = mergeOperationRefs(m.Active.Operations, ev.Operations)
	m.Active.OperationSets = sortedStrings(mergeStrings(m.Active.OperationSets, ev.OperationSets))
	m.Active.ContextProviders = mergeContextProviderRefs(m.Active.ContextProviders, ev.ContextProviders)
	m.Active.Datasources = mergeDatasourceRefs(m.Active.Datasources, ev.Datasources)
	m.Active.Skills = mergeSkillRefs(m.Active.Skills, ev.Skills)
	m.Active.References = mergeReferenceTargets(m.Active.References, ev.References)
	m.Active.InlineContexts = sortedStrings(mergeStrings(m.Active.InlineContexts, ev.InlineContexts))
	if ev.Lifetime != "" {
		m.Active.Lifetime = ev.Lifetime
	}
	m.Diagnostics = append(m.Diagnostics, ev.Diagnostics...)
	m.appendTrace(EventSurfacePrepared, preparedSummary(ev), ev.Source)
}

func (m *ReadModel) applyExpired(ev SurfaceExpired) {
	m.Active.ActivationSets = removeStrings(m.Active.ActivationSets, ev.ActivationSets)
	m.Active.Operations = removeOperationRefs(m.Active.Operations, ev.Operations)
	m.Active.OperationSets = removeStrings(m.Active.OperationSets, ev.OperationSets)
	m.Active.ContextProviders = removeContextProviderRefs(m.Active.ContextProviders, ev.ContextProviders)
	m.Active.Datasources = removeDatasourceRefs(m.Active.Datasources, ev.Datasources)
	m.Active.Skills = removeSkillRefs(m.Active.Skills, ev.Skills)
	m.Active.References = removeReferenceTargets(m.Active.References, ev.References)
	m.Active.InlineContexts = removeStrings(m.Active.InlineContexts, ev.InlineContexts)
	if ev.Lifetime != "" && m.Active.Lifetime == ev.Lifetime {
		m.Active.Lifetime = ""
	}
	m.appendTrace(EventSurfaceExpired, joinNonEmpty(strings.Join(ev.ActivationSets, ", "), ev.Reason), "")
}

func (m *ReadModel) appendTrace(name event.Name, summary string, source Source) {
	m.Recent = append(m.Recent, TraceEntry{Event: name, Summary: summary, Source: source})
	if len(m.Recent) > 32 {
		m.Recent = append([]TraceEntry(nil), m.Recent[len(m.Recent)-32:]...)
	}
}

func decodePayload(payload any, out any) error {
	if payload == nil {
		return nil
	}
	switch typed := payload.(type) {
	case json.RawMessage:
		return json.Unmarshal(typed, out)
	case []byte:
		return json.Unmarshal(typed, out)
	default:
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("activation: encode payload: %w", err)
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("activation: decode payload: %w", err)
		}
		return nil
	}
}

func preparedSummary(ev SurfacePrepared) string {
	parts := []string{}
	if len(ev.ActivationSets) > 0 {
		parts = append(parts, strings.Join(ev.ActivationSets, ", "))
	}
	if len(ev.OperationSets) > 0 {
		parts = append(parts, fmt.Sprintf("operation_sets=%d", len(ev.OperationSets)))
	}
	if len(ev.Operations) > 0 {
		parts = append(parts, fmt.Sprintf("operations=%d", len(ev.Operations)))
	}
	if len(ev.ContextProviders) > 0 {
		parts = append(parts, fmt.Sprintf("context=%d", len(ev.ContextProviders)))
	}
	if len(ev.Datasources) > 0 {
		parts = append(parts, fmt.Sprintf("datasources=%d", len(ev.Datasources)))
	}
	if len(ev.Skills) > 0 {
		parts = append(parts, fmt.Sprintf("skills=%d", len(ev.Skills)))
	}
	return strings.Join(parts, " ")
}

func resourcesSummary(resources []ResolvedResource) string {
	if len(resources) == 0 {
		return ""
	}
	parts := make([]string, 0, len(resources))
	for _, resource := range resources {
		label := firstNonEmpty(resource.Alias, resource.Name, resource.Address)
		if label != "" {
			parts = append(parts, string(resource.Kind)+":"+label)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func joinNonEmpty(values ...string) string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return strings.Join(out, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func mergeStrings(base, add []string) []string {
	seen := map[string]bool{}
	for _, value := range base {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = true
		}
	}
	for _, value := range add {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = true
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	return out
}

func removeStrings(base, remove []string) []string {
	removed := map[string]bool{}
	for _, value := range remove {
		removed[strings.TrimSpace(value)] = true
	}
	var out []string
	for _, value := range base {
		if value = strings.TrimSpace(value); value != "" && !removed[value] {
			out = append(out, value)
		}
	}
	return sortedStrings(out)
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func mergeOperationRefs(base, add []operation.Ref) []operation.Ref {
	seen := map[string]operation.Ref{}
	for _, ref := range append(base, add...) {
		if key := ref.String(); key != "" {
			seen[key] = ref
		}
	}
	out := make([]operation.Ref, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func removeOperationRefs(base, remove []operation.Ref) []operation.Ref {
	removed := map[string]bool{}
	for _, ref := range remove {
		removed[ref.String()] = true
	}
	var out []operation.Ref
	for _, ref := range base {
		if key := ref.String(); key != "" && !removed[key] {
			out = append(out, ref)
		}
	}
	return mergeOperationRefs(nil, out)
}

func mergeContextProviderRefs(base, add []corecontext.ProviderRef) []corecontext.ProviderRef {
	seen := map[string]corecontext.ProviderRef{}
	for _, ref := range append(base, add...) {
		if key := strings.TrimSpace(string(ref.Name)); key != "" {
			seen[key] = ref
		}
	}
	out := make([]corecontext.ProviderRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func removeContextProviderRefs(base, remove []corecontext.ProviderRef) []corecontext.ProviderRef {
	removed := map[string]bool{}
	for _, ref := range remove {
		removed[strings.TrimSpace(string(ref.Name))] = true
	}
	var out []corecontext.ProviderRef
	for _, ref := range base {
		if key := strings.TrimSpace(string(ref.Name)); key != "" && !removed[key] {
			out = append(out, ref)
		}
	}
	return mergeContextProviderRefs(nil, out)
}

func mergeDatasourceRefs(base, add []datasource.Ref) []datasource.Ref {
	seen := map[string]datasource.Ref{}
	for _, ref := range append(base, add...) {
		if key := strings.TrimSpace(string(ref.Name)); key != "" {
			seen[key] = ref
		}
	}
	out := make([]datasource.Ref, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func removeDatasourceRefs(base, remove []datasource.Ref) []datasource.Ref {
	removed := map[string]bool{}
	for _, ref := range remove {
		removed[strings.TrimSpace(string(ref.Name))] = true
	}
	var out []datasource.Ref
	for _, ref := range base {
		if key := strings.TrimSpace(string(ref.Name)); key != "" && !removed[key] {
			out = append(out, ref)
		}
	}
	return mergeDatasourceRefs(nil, out)
}

func mergeSkillRefs(base, add []skill.Ref) []skill.Ref {
	seen := map[string]skill.Ref{}
	for _, ref := range append(base, add...) {
		if key := strings.TrimSpace(string(ref.Name)); key != "" {
			seen[key] = ref
		}
	}
	out := make([]skill.Ref, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func removeSkillRefs(base, remove []skill.Ref) []skill.Ref {
	removed := map[string]bool{}
	for _, ref := range remove {
		removed[strings.TrimSpace(string(ref.Name))] = true
	}
	var out []skill.Ref
	for _, ref := range base {
		if key := strings.TrimSpace(string(ref.Name)); key != "" && !removed[key] {
			out = append(out, ref)
		}
	}
	return mergeSkillRefs(nil, out)
}

func mergeReferenceTargets(base, add []ReferenceTarget) []ReferenceTarget {
	seen := map[string]ReferenceTarget{}
	for _, ref := range append(base, add...) {
		key := string(ref.Skill.Name) + "\x00" + ref.Path
		if strings.TrimSpace(key) != "\x00" {
			seen[key] = ref
		}
	}
	out := make([]ReferenceTarget, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		left := string(out[i].Skill.Name) + "\x00" + out[i].Path
		right := string(out[j].Skill.Name) + "\x00" + out[j].Path
		return left < right
	})
	return out
}

func removeReferenceTargets(base, remove []ReferenceTarget) []ReferenceTarget {
	removed := map[string]bool{}
	for _, ref := range remove {
		removed[string(ref.Skill.Name)+"\x00"+ref.Path] = true
	}
	var out []ReferenceTarget
	for _, ref := range base {
		key := string(ref.Skill.Name) + "\x00" + ref.Path
		if !removed[key] {
			out = append(out, ref)
		}
	}
	return mergeReferenceTargets(nil, out)
}
