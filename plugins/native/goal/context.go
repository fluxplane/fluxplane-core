package goal

import (
	"context"
	"fmt"
	"strings"

	coregoal "github.com/fluxplane/fluxplane-core/core/goal"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	runtimegoal "github.com/fluxplane/fluxplane-core/runtime/goal"
)

type ContextProvider struct {
	Store corethread.Store
}

func (p ContextProvider) Spec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             ContextProviderName,
		Description:      "Current durable thread goal.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockData},
		DefaultPlacement: corecontext.PlacementSystem,
		Annotations: map[string]string{
			corecontext.AnnotationAutoContext: "true",
		},
	}
}

func (p ContextProvider) Build(ctx context.Context, req corecontext.Request) ([]corecontext.Block, error) {
	state, err := p.state(ctx, req)
	if err != nil {
		return nil, err
	}
	if !state.Visible() {
		return nil, nil
	}
	return []corecontext.Block{{
		ID:        "current",
		Provider:  ContextProviderName,
		Kind:      corecontext.BlockData,
		Placement: corecontext.PlacementSystem,
		Title:     "Current Goal",
		Content:   RenderContext(state),
		MediaType: "text/plain",
		Priority:  100,
		Freshness: corecontext.FreshnessDynamic,
		Metadata: map[string]string{
			"goal_id": string(state.ID),
			"status":  string(state.Status),
		},
	}}, nil
}

func (p ContextProvider) StateFingerprint(ctx context.Context, req corecontext.Request) (string, bool, error) {
	state, err := p.state(ctx, req)
	if err != nil {
		return "", false, err
	}
	if !state.Visible() {
		return "absent", true, nil
	}
	return fmt.Sprintf("%s:%s:%d", state.ID, state.Status, state.Revision), true, nil
}

func (p ContextProvider) state(ctx context.Context, req corecontext.Request) (coregoal.State, error) {
	if p.Store == nil || req.ThreadID == "" {
		return coregoal.State{}, nil
	}
	snapshot, err := p.Store.Read(ctx, corethread.ReadParams{ID: corethread.ID(req.ThreadID)})
	if err != nil {
		return coregoal.State{}, err
	}
	if req.BranchID != "" {
		snapshot.BranchID = corethread.BranchID(req.BranchID)
	}
	return runtimegoal.ProjectThread(snapshot)
}

func RenderContext(state coregoal.State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Goal status: %s\n", state.Status)
	fmt.Fprintf(&b, "Goal: %s\n", state.Text)
	if len(state.AcceptanceCriteria) > 0 {
		b.WriteString("Acceptance criteria:\n")
		for _, criterion := range state.AcceptanceCriteria {
			description := strings.TrimSpace(criterion.Description)
			if description == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(description)
			if criterion.Required {
				b.WriteString(" (required)")
			}
			b.WriteByte('\n')
		}
	}
	if state.LatestReview != nil {
		result := state.LatestReview.Result
		fmt.Fprintf(&b, "Latest review: %s", result.Decision)
		if summary := strings.TrimSpace(result.Summary); summary != "" {
			fmt.Fprintf(&b, " - %s", summary)
		}
		b.WriteByte('\n')
		if len(result.Suggestions) > 0 {
			b.WriteString("Reviewer suggestions:\n")
			for _, suggestion := range result.Suggestions {
				text := strings.TrimSpace(suggestion.Text)
				if text == "" {
					continue
				}
				b.WriteString("- ")
				b.WriteString(text)
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(b.String())
}
