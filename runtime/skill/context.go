package skill

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	coreskill "github.com/fluxplane/fluxplane-core/core/skill"
	"github.com/fluxplane/fluxplane-core/core/tool"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
)

const ContextProviderName corecontext.ProviderName = "skills"

type stateContextKey struct{}

// ContextWithState attaches skill activation state to a runtime context.
func ContextWithState(ctx context.Context, state *ActivationState) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, stateContextKey{}, state)
}

// StateFromContext returns the skill activation state attached to ctx.
func StateFromContext(ctx context.Context) (*ActivationState, bool) {
	if ctx == nil {
		return nil, false
	}
	state, ok := ctx.Value(stateContextKey{}).(*ActivationState)
	return state, ok && state != nil
}

// StatefulAgent is implemented by agent wrappers that own skill state.
type StatefulAgent interface {
	SkillActivationState() *ActivationState
}

type statefulAgent struct {
	agent.Agent
	state *ActivationState
}

// WrapAgent attaches skill state to an existing agent implementation.
func WrapAgent(runtime agent.Agent, state *ActivationState) agent.Agent {
	if runtime == nil || state == nil {
		return runtime
	}
	return statefulAgent{Agent: runtime, state: state}
}

func (a statefulAgent) SkillActivationState() *ActivationState { return a.state }

// StepWithTools forwards per-turn tool projections through the skill wrapper.
func (a statefulAgent) StepWithTools(ctx agent.Context, input agent.StepInput, tools []tool.Spec) agent.StepResult {
	carrier, ok := a.Agent.(interface {
		StepWithTools(agent.Context, agent.StepInput, []tool.Spec) agent.StepResult
	})
	if !ok || carrier == nil {
		return a.Step(ctx, input)
	}
	return carrier.StepWithTools(ctx, input, tools)
}

// ContextProviders forwards session-level context materialization support from
// the wrapped agent.
func (a statefulAgent) ContextProviders() []corecontext.Provider {
	carrier, ok := a.Agent.(interface {
		ContextProviders() []corecontext.Provider
	})
	if !ok || carrier == nil {
		return nil
	}
	return carrier.ContextProviders()
}

// ProviderIdentity forwards provider transcript identity from the wrapped
// agent.
func (a statefulAgent) ProviderIdentity() coreconversation.ProviderIdentity {
	carrier, ok := a.Agent.(interface {
		ProviderIdentity() coreconversation.ProviderIdentity
	})
	if !ok || carrier == nil {
		return coreconversation.ProviderIdentity{}
	}
	return carrier.ProviderIdentity()
}

// StateFromAgent returns the activation state attached to agent, when present.
func StateFromAgent(runtime agent.Agent) (*ActivationState, bool) {
	carrier, ok := runtime.(StatefulAgent)
	if !ok {
		return nil, false
	}
	state := carrier.SkillActivationState()
	return state, state != nil
}

type contextProvider struct {
	repo  *Repository
	state *ActivationState
}

// NewContextProvider returns the model-visible skill inventory provider.
func NewContextProvider(repo *Repository, state *ActivationState) corecontext.Provider {
	return contextProvider{repo: repo, state: state}
}

func (p contextProvider) Spec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:        ContextProviderName,
		Description: "Lists available skills and renders active skill references.",
		Kinds:       []corecontext.BlockKind{corecontext.BlockText, corecontext.BlockData},
	}
}

func (p contextProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	repo := p.repo
	if repo == nil && p.state != nil {
		repo = p.state.Repository()
	}
	if repo == nil {
		return nil, nil
	}
	var blocks []corecontext.Block
	for _, spec := range repo.List() {
		status := StatusInactive
		if p.state != nil {
			status = p.state.Status(string(spec.Name))
		}
		content := renderSkillMetadata(spec, status)
		if status != StatusInactive && strings.TrimSpace(spec.Body) != "" {
			content += "\n\n" + strings.TrimSpace(spec.Body)
		}
		blocks = append(blocks, corecontext.Block{
			ID:        "skills/catalog/" + sanitize(string(spec.Name)),
			Provider:  ContextProviderName,
			Kind:      corecontext.BlockText,
			Title:     "Skill " + string(spec.Name),
			Content:   content,
			Freshness: corecontext.FreshnessDynamic,
			Metadata: map[string]string{
				"skill":  string(spec.Name),
				"status": string(status),
			},
		})
		if p.state == nil {
			continue
		}
		for _, ref := range p.state.ActiveReferences(string(spec.Name)) {
			blocks = append(blocks, corecontext.Block{
				ID:        "skills/references/" + sanitize(string(spec.Name)) + "/" + sanitize(ref.Path),
				Provider:  ContextProviderName,
				Kind:      corecontext.BlockText,
				Title:     string(spec.Name) + " " + ref.Path,
				Content:   renderReference(spec, ref),
				Freshness: corecontext.FreshnessDynamic,
				Metadata: map[string]string{
					"skill":     string(spec.Name),
					"reference": ref.Path,
				},
			})
		}
	}
	return blocks, nil
}

func renderSkillMetadata(spec coreskill.Spec, status Status) string {
	var b strings.Builder
	writeLine(&b, "skill", string(spec.Name))
	writeLine(&b, "description", spec.Description)
	writeLine(&b, "source", spec.Source.URI)
	writeLine(&b, "status", string(status))
	writeLine(&b, "triggers", strings.Join(spec.Triggers, ", "))
	writeMap(&b, spec.Metadata)
	if len(spec.References) > 0 {
		paths := make([]string, 0, len(spec.References))
		for _, ref := range spec.References {
			paths = append(paths, ref.Path)
		}
		sort.Strings(paths)
		writeLine(&b, "references", strings.Join(paths, ", "))
	}
	return strings.TrimSpace(b.String())
}

func renderReference(spec coreskill.Spec, ref coreskill.ReferenceSpec) string {
	var b strings.Builder
	writeLine(&b, "skill", string(spec.Name))
	writeLine(&b, "path", ref.Path)
	writeLine(&b, "triggers", strings.Join(ref.Triggers, ", "))
	writeMap(&b, ref.Metadata)
	if strings.TrimSpace(ref.Body) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(ref.Body))
	}
	return strings.TrimSpace(b.String())
}

func writeMap(b *strings.Builder, values map[string]string) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeLine(b, key, values[key])
	}
}

func writeLine(b *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "%s: %s", key, value)
}

func sanitize(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Trim(value, "/")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, " ", "_")
	if value == "" {
		return "unknown"
	}
	return value
}
