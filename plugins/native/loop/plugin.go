package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	coreoperation "github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-core/orchestration/sessioncontrol"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionrun"
	runtimeoperation "github.com/fluxplane/fluxplane-core/runtime/operation"
)

const (
	Name             = "loop"
	Command          = "loop"
	outputPreviewLen = 180
)

type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.SessionCommandContributor = Plugin{}

func New() Plugin { return Plugin{} }

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Repeat a prompt in fresh session contexts."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (Plugin) SessionCommands(context.Context, pluginhost.Context) ([]session.SessionCommandBinding, error) {
	return []session.SessionCommandBinding{{
		Spec:    CommandSpec(),
		Handler: ExecuteCommand,
	}}, nil
}

func CommandSpec() command.Spec {
	return command.Spec{
		Path:        command.Path{Command},
		Description: "Repeat a prompt in fresh session contexts.",
		Target:      invocation.Target{Kind: invocation.TargetSession},
		Input:       runtimeoperation.TypeOf[commandInput]("loop_input"),
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
		Annotations: map[string]string{
			command.CompletionFlagsAnnotation: strings.Join(command.FlagNamesFor[commandInput](), ","),
		},
	}
}

type commandInput struct {
	Prompt      []string `json:"prompt,omitempty" command:"arg" jsonschema:"description=Prompt to submit on every iteration."`
	Count       int      `json:"count,omitempty" command:"flag=count" jsonschema:"description=Number of iterations to run."`
	Forever     bool     `json:"forever,omitempty" command:"flag=forever" jsonschema:"description=Run until cancelled."`
	Delay       string   `json:"delay,omitempty" command:"flag=delay" jsonschema:"description=Duration to wait between iterations, for example 30s or 5m."`
	StopOnError bool     `json:"stop_on_error,omitempty" command:"flag=stop-on-error,default=true" jsonschema:"description=Stop after the first failed iteration."`
	Session     string   `json:"session,omitempty" command:"flag=session" jsonschema:"description=Session profile to open for each iteration."`
}

type parsedCommand struct {
	Prompt      string
	Count       int
	Forever     bool
	Delay       time.Duration
	StopOnError bool
	Session     coresession.Ref
}

type Output struct {
	Prompt      string            `json:"prompt,omitempty"`
	Count       int               `json:"count,omitempty"`
	Forever     bool              `json:"forever,omitempty"`
	Delay       string            `json:"delay,omitempty"`
	Success     int               `json:"success"`
	Failed      int               `json:"failed"`
	Iterations  []IterationResult `json:"iterations,omitempty"`
	Stopped     bool              `json:"stopped,omitempty"`
	StopReason  string            `json:"stop_reason,omitempty"`
	Target      coresession.Ref   `json:"target,omitempty"`
	ParentRunID string            `json:"parent_run_id,omitempty"`
}

type IterationResult struct {
	Iteration     int           `json:"iteration"`
	ID            sessionrun.ID `json:"id"`
	Status        string        `json:"status"`
	ChildThreadID corethread.ID `json:"child_thread_id,omitempty"`
	ChildRunID    string        `json:"child_run_id,omitempty"`
	Output        string        `json:"output,omitempty"`
	Error         string        `json:"error,omitempty"`
}

func ExecuteCommand(s session.Session, ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) session.CommandResult {
	if inbound.Command == nil {
		return failedResult(spec, evaluation, "invalid_loop_command_input", "command invocation is required")
	}
	input, err := parseCommandInput(*inbound.Command)
	if err != nil {
		return failedResult(spec, evaluation, "invalid_loop_command_input", err.Error())
	}
	output, err := runLoop(s, ctx, inbound, input)
	rendered := renderOutput(output)
	if rendered.Text != "" {
		if appendErr := s.AppendThreadEvents(ctx, sessionenv.OutboundProduced{
			RunID:   inbound.ID,
			Message: channel.Message{Content: rendered.ModelText()},
		}); appendErr != nil && err == nil {
			err = appendErr
		}
	}
	if err != nil {
		return session.CommandResult{
			Status: session.CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Output: rendered,
			Error:  &session.CommandError{Code: "loop_command_failed", Message: err.Error()},
		}
	}
	status := session.CommandStatusOK
	if output.Failed > 0 {
		status = session.CommandStatusFailed
	}
	return session.CommandResult{Status: status, Spec: spec, Policy: evaluation, Output: rendered}
}

func parseCommandInput(inv command.Invocation) (parsedCommand, error) {
	input, err := command.Bind[commandInput](inv)
	if err != nil {
		return parsedCommand{}, err
	}
	mergeStructuredInput(&input, inv.Input, len(inv.Args) == 0)
	return validateCommandInput(input)
}

func mergeStructuredInput(input *commandInput, value any, allowPrompt bool) {
	values, ok := value.(map[string]any)
	if !ok {
		return
	}
	if allowPrompt {
		if prompt, ok := stringSliceValue(values["prompt"]); ok {
			input.Prompt = prompt
		}
	}
	if count, ok := intValue(values["count"]); ok {
		input.Count = count
	}
	if forever, ok := boolValue(values["forever"]); ok {
		input.Forever = forever
	}
	if delay, ok := stringValue(values["delay"]); ok {
		input.Delay = delay
	}
	if stop, ok := boolValue(values["stop_on_error"]); ok {
		input.StopOnError = stop
	}
	if stop, ok := boolValue(values["stop-on-error"]); ok {
		input.StopOnError = stop
	}
	if target, ok := stringValue(values["session"]); ok {
		input.Session = target
	}
}

func validateCommandInput(input commandInput) (parsedCommand, error) {
	prompt := strings.TrimSpace(strings.Join(input.Prompt, " "))
	if prompt == "" {
		return parsedCommand{}, fmt.Errorf("prompt is required")
	}
	if input.Count < 0 {
		return parsedCommand{}, fmt.Errorf("--count must be positive")
	}
	if input.Count == 0 && !input.Forever {
		return parsedCommand{}, fmt.Errorf("either --count N or --forever is required")
	}
	if input.Count > 0 && input.Forever {
		return parsedCommand{}, fmt.Errorf("--count and --forever are mutually exclusive")
	}
	var delay time.Duration
	if strings.TrimSpace(input.Delay) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(input.Delay))
		if err != nil {
			return parsedCommand{}, fmt.Errorf("invalid --delay: %w", err)
		}
		if parsed < 0 {
			return parsedCommand{}, fmt.Errorf("--delay must not be negative")
		}
		delay = parsed
	}
	if input.Forever && delay <= 0 {
		return parsedCommand{}, fmt.Errorf("--forever requires a positive --delay")
	}
	return parsedCommand{
		Prompt:      prompt,
		Count:       input.Count,
		Forever:     input.Forever,
		Delay:       delay,
		StopOnError: input.StopOnError,
		Session:     coresession.Ref{Name: coresession.Name(strings.TrimSpace(input.Session))},
	}, nil
}

func runLoop(s session.Session, ctx context.Context, inbound channel.Inbound, input parsedCommand) (Output, error) {
	output := Output{
		Prompt:      input.Prompt,
		Count:       input.Count,
		Forever:     input.Forever,
		Delay:       input.Delay.String(),
		Target:      targetSession(s, input),
		ParentRunID: inbound.ID,
	}
	if input.Delay == 0 {
		output.Delay = ""
	}
	if s.SessionRuns == nil {
		return output, fmt.Errorf("session-run runner is not configured")
	}
	if output.Target.Name == "" {
		return output, fmt.Errorf("target session is required")
	}

	for iteration := 1; input.Forever || iteration <= input.Count; iteration++ {
		select {
		case <-ctx.Done():
			output.Stopped = true
			output.StopReason = ctx.Err().Error()
			return output, ctx.Err()
		default:
		}
		result := executeIteration(s, ctx, inbound, input, output.Target, iteration)
		output.Iterations = append(output.Iterations, result)
		if result.Status == "ok" {
			output.Success++
		} else {
			output.Failed++
			if input.StopOnError {
				output.Stopped = true
				output.StopReason = result.Error
				return output, fmt.Errorf("iteration %d failed: %s", iteration, result.Error)
			}
		}
		if shouldDelay(input, iteration) {
			if err := waitDelay(ctx, input.Delay); err != nil {
				output.Stopped = true
				output.StopReason = err.Error()
				return output, err
			}
		}
	}
	return output, nil
}

func executeIteration(s session.Session, ctx context.Context, inbound channel.Inbound, input parsedCommand, target coresession.Ref, iteration int) IterationResult {
	id := sessionrun.ID(fmt.Sprintf("%s:loop:%d", inbound.ID, iteration))
	result := IterationResult{Iteration: iteration, ID: id, Status: "ok"}
	runResult, err := s.SessionRuns.Run(ctx, sessionrun.Request{
		ID:             id,
		Session:        target,
		Input:          input.Prompt,
		InputMetadata:  map[string]any{"loop_id": inbound.ID, "loop_iteration": iteration},
		ParentThreadID: s.Thread.ID,
		ParentRunID:    inbound.ID,
		ParentCallID:   coreoperation.CallID(id),
		TaskID:         fmt.Sprintf("%s:%d", inbound.ID, iteration),
		Metadata:       iterationMetadata(inbound.ID, input, iteration),
		Policy:         s.Delegation,
		EnforcePolicy:  target.Name != s.Profile.Name,
		Events:         s.Events,
		Approver:       sessionenv.ApproverFromExecutor(s.OperationExecutor),
	})
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	result.ChildThreadID = runResult.ChildThreadID
	result.ChildRunID = runResult.ChildRunID
	result.Output = runResult.Output
	return result
}

func targetSession(s session.Session, input parsedCommand) coresession.Ref {
	if input.Session.Name != "" {
		return input.Session
	}
	if s.Profile.Name != "" {
		return coresession.Ref{Name: s.Profile.Name}
	}
	return coresession.Ref{}
}

func iterationMetadata(parentRunID string, input parsedCommand, iteration int) map[string]string {
	sum := sha256.Sum256([]byte(input.Prompt))
	metadata := map[string]string{
		"loop_id":            parentRunID,
		"loop_iteration":     strconv.Itoa(iteration),
		"loop_prompt_sha256": hex.EncodeToString(sum[:]),
	}
	if input.Count > 0 {
		metadata["loop_count"] = strconv.Itoa(input.Count)
	}
	if input.Forever {
		metadata["loop_forever"] = "true"
	}
	return metadata
}

func shouldDelay(input parsedCommand, iteration int) bool {
	if input.Delay <= 0 {
		return false
	}
	return input.Forever || iteration < input.Count
}

func waitDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func renderOutput(output Output) coreoperation.Rendered {
	text := renderText(output)
	return coreoperation.Rendered{Text: text, Model: text, Data: output}
}

func renderText(output Output) string {
	var b strings.Builder
	switch {
	case output.Stopped && output.Failed > 0:
		b.WriteString("Loop stopped with errors\n")
	case output.Failed > 0:
		b.WriteString("Loop completed with errors\n")
	default:
		b.WriteString("Loop completed\n")
	}
	fmt.Fprintf(&b, "Iterations: %d\n", len(output.Iterations))
	fmt.Fprintf(&b, "Success: %d\n", output.Success)
	fmt.Fprintf(&b, "Failed: %d\n", output.Failed)
	if output.Target.Name != "" {
		fmt.Fprintf(&b, "Session: %s\n", output.Target.Name)
	}
	if output.StopReason != "" {
		fmt.Fprintf(&b, "Stop reason: %s\n", output.StopReason)
	}
	if len(output.Iterations) == 0 {
		return strings.TrimRight(b.String(), "\n")
	}
	b.WriteString("\n")
	for _, iteration := range output.Iterations {
		fmt.Fprintf(&b, "%d. %s", iteration.Iteration, iteration.Status)
		if iteration.ChildThreadID != "" {
			fmt.Fprintf(&b, " thread=%s", iteration.ChildThreadID)
		}
		if summary := iterationSummary(iteration); summary != "" {
			fmt.Fprintf(&b, " - %s", summary)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func iterationSummary(iteration IterationResult) string {
	if iteration.Error != "" {
		return compact(iteration.Error, outputPreviewLen)
	}
	return compact(iteration.Output, outputPreviewLen)
}

func compact(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func failedResult(spec command.Spec, evaluation sessioncontrol.PolicyEvaluation, code, message string) session.CommandResult {
	return session.CommandResult{
		Status: session.CommandStatusFailed,
		Spec:   spec,
		Policy: evaluation,
		Error:  &session.CommandError{Code: code, Message: message},
	}
}

func stringSliceValue(value any) ([]string, bool) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, false
		}
		return []string{typed}, true
	case []string:
		if len(typed) == 0 {
			return nil, false
		}
		return append([]string(nil), typed...), true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}

func stringValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case fmt.Stringer:
		return typed.String(), true
	default:
		return "", false
	}
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(typed)
		if err != nil {
			return false, false
		}
		return parsed, true
	default:
		return false, false
	}
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(typed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
