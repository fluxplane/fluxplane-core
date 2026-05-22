package goal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/engine/core/channel"
	"github.com/fluxplane/engine/core/command"
	corecontext "github.com/fluxplane/engine/core/context"
	coregoal "github.com/fluxplane/engine/core/goal"
	"github.com/fluxplane/engine/core/invocation"
	"github.com/fluxplane/engine/core/policy"
	"github.com/fluxplane/engine/core/resource"
	corethread "github.com/fluxplane/engine/core/thread"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/orchestration/session"
	"github.com/fluxplane/engine/orchestration/sessioncontrol"
	"github.com/fluxplane/engine/orchestration/sessionenv"
	runtimegoal "github.com/fluxplane/engine/runtime/goal"
	runtimethread "github.com/fluxplane/engine/runtime/thread"
)

const (
	Name                = "goal"
	Command             = "goal"
	ContextProviderName = "session_goal"
)

type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.SessionCommandContributor = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}

func New() Plugin { return Plugin{} }

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Durable session goal lifecycle and context."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		ContextProviders: []corecontext.ProviderSpec{{
			Name:             ContextProviderName,
			Description:      "Current durable thread goal.",
			Kinds:            []corecontext.BlockKind{corecontext.BlockData},
			DefaultPlacement: corecontext.PlacementSystem,
			Annotations: map[string]string{
				corecontext.AnnotationAutoContext: "true",
			},
		}},
	}, nil
}

func (Plugin) SessionCommands(context.Context, pluginhost.Context) ([]session.SessionCommandBinding, error) {
	return []session.SessionCommandBinding{{
		Spec:    CommandSpec(),
		Handler: ExecuteCommand,
	}}, nil
}

func (Plugin) ContextProviders(_ context.Context, ctx pluginhost.Context) ([]corecontext.Provider, error) {
	if ctx.EventStore == nil {
		return nil, nil
	}
	store, err := runtimethread.NewStore(ctx.EventStore)
	if err != nil {
		return nil, err
	}
	return []corecontext.Provider{ContextProvider{Store: store}}, nil
}

func CommandSpec() command.Spec {
	return command.Spec{
		Path:        command.Path{Command},
		Description: "Show, set, pause, resume, or clear the durable thread goal.",
		Target:      invocation.Target{Kind: invocation.TargetSession},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser, policy.CallerSystem},
			RequiredTrust:  policy.TrustVerified,
		},
	}
}

type commandInput struct {
	Goal []string `json:"goal,omitempty" command:"arg"`
}

type commandMode string

const (
	commandStatus commandMode = "status"
	commandSet    commandMode = "set"
	commandPause  commandMode = "pause"
	commandResume commandMode = "resume"
	commandClear  commandMode = "clear"
)

type parsedCommand struct {
	Mode commandMode
	Text string
}

func ExecuteCommand(s session.Session, ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) session.CommandResult {
	action, err := parseCommandInput(*inbound.Command)
	if err != nil {
		return session.CommandResult{
			Status: session.CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &session.CommandError{Code: "invalid_goal_command_input", Message: err.Error()},
		}
	}
	output, err := applyCommand(s, ctx, inbound, action)
	if err != nil {
		return session.CommandResult{
			Status: session.CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &session.CommandError{Code: "goal_command_failed", Message: err.Error()},
		}
	}
	return session.CommandResult{Status: session.CommandStatusOK, Spec: spec, Policy: evaluation, Output: output}
}

func parseCommandInput(inv command.Invocation) (parsedCommand, error) {
	if err := rejectLegacyFlags(inv.Input); err != nil {
		return parsedCommand{}, err
	}
	input, err := command.Bind[commandInput](inv)
	if err != nil {
		return parsedCommand{}, err
	}
	if len(inv.Args) == 0 && inv.Input != nil {
		input = structuredCommandInput(inv.Input)
	}
	return validateCommandInput(input)
}

func structuredCommandInput(value any) commandInput {
	values, ok := value.(map[string]any)
	if !ok {
		return commandInput{}
	}
	var input commandInput
	switch goal := values["goal"].(type) {
	case string:
		input.Goal = []string{goal}
	case []string:
		input.Goal = append([]string(nil), goal...)
	case []any:
		for _, item := range goal {
			input.Goal = append(input.Goal, fmt.Sprint(item))
		}
	}
	return input
}

func rejectLegacyFlags(value any) error {
	values, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"max", "max_continuations", "max-continuations"} {
		if _, ok := values[key]; ok {
			return fmt.Errorf("/goal no longer accepts %q; use /goal <goal>, /goal pause, /goal resume, or /goal clear", key)
		}
	}
	return nil
}

func validateCommandInput(input commandInput) (parsedCommand, error) {
	text := strings.TrimSpace(strings.Join(input.Goal, " "))
	if text == "" {
		return parsedCommand{Mode: commandStatus}, nil
	}
	if len(input.Goal) == 1 {
		switch strings.ToLower(strings.TrimSpace(input.Goal[0])) {
		case "status":
			return parsedCommand{Mode: commandStatus}, nil
		case "pause":
			return parsedCommand{Mode: commandPause}, nil
		case "resume":
			return parsedCommand{Mode: commandResume}, nil
		case "clear":
			return parsedCommand{Mode: commandClear}, nil
		}
	}
	if err := coregoal.ValidateText(text); err != nil {
		return parsedCommand{}, err
	}
	return parsedCommand{Mode: commandSet, Text: text}, nil
}

func applyCommand(s session.Session, ctx context.Context, inbound channel.Inbound, action parsedCommand) (any, error) {
	switch action.Mode {
	case commandStatus:
		state, err := currentState(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		return RenderStatus(state), nil
	case commandSet:
		state, err := currentState(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		goalID := newGoalID(s.Thread.ID, inbound.ID, action.Text)
		events := []sessionenv.Event{}
		if state.Visible() {
			events = append(events, coregoal.Archived{GoalID: state.ID, Reason: "superseded", SupersededBy: goalID, RunID: inbound.ID})
		}
		events = append(events,
			coregoal.Set{GoalID: goalID, ThreadID: s.Thread.ID, Text: action.Text, RunID: inbound.ID},
			coregoal.AcceptanceCriteriaGenerated{
				GoalID: goalID,
				Criteria: []coregoal.Criterion{{
					Description: "The requested goal has been completed: " + action.Text,
					Required:    true,
				}},
				RunID: inbound.ID,
			},
		)
		if err := s.AppendThreadEvents(ctx, events...); err != nil {
			return nil, err
		}
		return "Goal set.\n" + RenderStatus(coregoal.State{ID: goalID, ThreadID: s.Thread.ID, Status: coregoal.StatusActive, Text: action.Text, Revision: state.Revision + len(events)}), nil
	case commandPause:
		state, err := requireCurrent(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		if state.Status == coregoal.StatusPaused {
			return RenderStatus(state), nil
		}
		if err := s.AppendThreadEvents(ctx, coregoal.Paused{GoalID: state.ID, RunID: inbound.ID}); err != nil {
			return nil, err
		}
		state.Status = coregoal.StatusPaused
		return "Goal paused.\n" + RenderStatus(state), nil
	case commandResume:
		state, err := requireCurrent(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		if err := s.AppendThreadEvents(ctx, coregoal.Resumed{GoalID: state.ID, RunID: inbound.ID}); err != nil {
			return nil, err
		}
		state.Status = coregoal.StatusActive
		return "Goal resumed.\n" + RenderStatus(state), nil
	case commandClear:
		state, err := currentState(ctx, s.ThreadStore, s.Thread)
		if err != nil {
			return nil, err
		}
		if !state.Visible() {
			return "No current goal.", nil
		}
		if err := s.AppendThreadEvents(ctx, coregoal.Cleared{GoalID: state.ID, RunID: inbound.ID}); err != nil {
			return nil, err
		}
		return "Goal cleared.", nil
	default:
		return nil, fmt.Errorf("unknown goal command mode %q", action.Mode)
	}
}

func currentState(ctx context.Context, store corethread.Store, thread corethread.Ref) (coregoal.State, error) {
	if store == nil || thread.ID == "" {
		return coregoal.State{}, fmt.Errorf("goal requires an active thread")
	}
	snapshot, err := store.Read(ctx, corethread.ReadParams{ID: thread.ID})
	if err != nil {
		return coregoal.State{}, err
	}
	if thread.BranchID != "" {
		snapshot.BranchID = thread.BranchID
	}
	return runtimegoal.ProjectThread(snapshot)
}

func requireCurrent(ctx context.Context, store corethread.Store, thread corethread.Ref) (coregoal.State, error) {
	state, err := currentState(ctx, store, thread)
	if err != nil {
		return coregoal.State{}, err
	}
	if !state.Visible() {
		return coregoal.State{}, fmt.Errorf("no current goal")
	}
	return state, nil
}

func RenderStatus(state coregoal.State) string {
	if !state.Visible() {
		return "No current goal."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", state.Text)
	fmt.Fprintf(&b, "Status: %s", state.Status)
	if len(state.AcceptanceCriteria) > 0 {
		b.WriteString("\nAcceptance criteria:")
		for _, criterion := range state.AcceptanceCriteria {
			description := strings.TrimSpace(criterion.Description)
			if description == "" {
				continue
			}
			b.WriteString("\n- ")
			b.WriteString(description)
		}
	}
	if state.LatestReview != nil {
		result := state.LatestReview.Result
		fmt.Fprintf(&b, "\nLatest review: %s", result.Decision)
		if summary := strings.TrimSpace(result.Summary); summary != "" {
			fmt.Fprintf(&b, " - %s", summary)
		}
	}
	return b.String()
}

func newGoalID(threadID corethread.ID, runID, text string) coregoal.ID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%s\n%d", threadID, runID, text, time.Now().UTC().UnixNano())))
	return coregoal.ID("goal_" + hex.EncodeToString(sum[:])[:16])
}
