package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	agentruntime "github.com/fluxplane/agentruntime"
	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	openaiadapter "github.com/fluxplane/agentruntime/adapters/openai"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/usage"
	"github.com/fluxplane/agentruntime/orchestration/app"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "agentsdk",
		Short:         "Run agentsdk tools",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newCoderCommand())
	return cmd
}

type coderOptions struct {
	model string
	debug bool
	usage bool
}

func newCoderCommand() *cobra.Command {
	var opts coderOptions
	cmd := &cobra.Command{
		Use:   "coder [prompt]",
		Short: "Run the first-party coding agent",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.TrimSpace(strings.Join(args, " "))
			if prompt == "" {
				return errors.New("prompt is empty")
			}
			return runCoder(cmd.Context(), opts, prompt)
		},
	}
	cmd.PersistentFlags().StringVar(&opts.model, "model", coder.DefaultModel, "OpenAI model")
	cmd.PersistentFlags().BoolVar(&opts.debug, "debug", false, "print run events as JSON")
	cmd.PersistentFlags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	cmd.AddCommand(newCoderReplCommand(&opts))
	return cmd
}

func newCoderReplCommand(opts *coderOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "repl",
		Short: "Run coder in an interactive session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCoderREPL(cmd.Context(), *opts)
		},
	}
}

func runCoder(ctx context.Context, opts coderOptions, prompt string) error {
	session, err := openCoderSession(ctx, opts)
	if err != nil {
		return err
	}
	return sendCoderPrompt(ctx, session, opts, prompt)
}

func runCoderREPL(ctx context.Context, opts coderOptions) error {
	session, err := openCoderSession(ctx, opts)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stderr, "agentsdk coder repl. Type /exit or /quit to stop.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		_, _ = fmt.Fprint(os.Stdout, "coder> ")
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/exit", "/quit":
			return nil
		}
		if err := sendCoderPrompt(ctx, session, opts, prompt); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func openCoderSession(ctx context.Context, opts coderOptions) (agentruntime.Session, error) {
	model, err := openaiadapter.New(openaiadapter.Config{
		Model:             opts.model,
		ParallelToolCalls: true,
		Redactor:          debugRedactor(opts.debug),
	})
	if err != nil {
		return nil, err
	}
	bundle := coderBundle(opts.model)
	composition, err := app.Compose(app.Config{
		Bundles:    []agentruntime.ResourceBundle{bundle},
		Operations: []operation.Operation{shellOperation(), httpRequestOperation()},
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:        localSandbox{},
			ACL:            localACL{},
			CommandRisk:    commandRisk{},
			MaxCommandRisk: operation.RiskMedium,
			AllowPure:      true,
		})),
	})
	if err != nil {
		return nil, err
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModel:        model,
		LLMStreamPolicy: debugStreamPolicy(opts.debug),
		ToolProjection: agentruntime.ToolProjectionConfig{
			AllowSideEffects: true,
			MaxRisk:          operation.RiskMedium,
		},
		Channel: channel.Ref{Name: "local"},
		Caller: policy.Caller{
			Kind: policy.CallerUser,
			Principal: policy.Principal{
				Kind: "user",
				ID:   "agentsdk",
				Name: "agentsdk",
			},
		},
		Trust: policy.Trust{
			Kind:  policy.TrustInvocation,
			Level: policy.TrustVerified,
		},
	})
	if err != nil {
		return nil, err
	}
	session, err := service.Open(ctx, agentruntime.OpenRequest{
		Session:      agentruntime.SessionRef{Name: coder.SessionName},
		Conversation: channel.ConversationRef{ID: "agentsdk-coder"},
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

func coderBundle(model string) agentruntime.ResourceBundle {
	bundle := coder.Bundle()
	if model == "" {
		return bundle
	}
	for i := range bundle.Agents {
		if bundle.Agents[i].Name == coder.AgentName {
			bundle.Agents[i].Inference.Model = model
		}
	}
	for i := range bundle.Apps {
		if bundle.Apps[i].Name == coder.AppName {
			bundle.Apps[i].Model.Model = model
		}
	}
	return bundle
}

func sendCoderPrompt(ctx context.Context, session agentruntime.Session, opts coderOptions, prompt string) error {
	run, err := session.SendInput(ctx, agentruntime.Input{Text: prompt})
	if err != nil {
		return err
	}
	var usageDone <-chan []string
	if opts.debug {
		go printEvents(run.Events())
	} else if opts.usage {
		usageDone = collectUsageLines(run.Events())
	}
	result, err := run.Wait(ctx)
	if result.Outbound != nil && result.Outbound.Message != nil {
		_, _ = fmt.Fprintln(os.Stdout, result.Outbound.Message.Content)
	}
	if usageDone != nil {
		for _, line := range <-usageDone {
			_, _ = fmt.Fprintln(os.Stdout, line)
		}
	}
	if err != nil {
		return err
	}
	return nil
}

func printEvents(events <-chan agentruntime.Event) {
	encoder := json.NewEncoder(os.Stderr)
	encoder.SetIndent("", "  ")
	for event := range events {
		fmt.Fprintln(os.Stderr, "\n[event]")
		_ = encoder.Encode(event)
	}
}

func collectUsageLines(events <-chan agentruntime.Event) <-chan []string {
	done := make(chan []string, 1)
	go func() {
		var lines []string
		for event := range events {
			if event.Kind != agentruntime.EventRuntimeEmitted || event.Runtime == nil || event.Runtime.Name != usage.EventRecordedName {
				continue
			}
			if line := usageLine(event.Runtime.Payload); line != "" {
				lines = append(lines, line)
			}
		}
		done <- lines
		close(done)
	}()
	return done
}

func usageLine(payload any) string {
	switch value := payload.(type) {
	case usage.Recorded:
		return usageLineFromRecorded(value)
	case map[string]any:
		return usageLineFromMap(value)
	default:
		return ""
	}
}

func usageLineFromRecorded(recorded usage.Recorded) string {
	if recorded.Empty() {
		return ""
	}
	parts := []string{"usage:"}
	if recorded.Source != "" {
		parts = append(parts, "source="+recorded.Source)
	}
	if recorded.Subject.Provider != "" {
		parts = append(parts, "provider="+recorded.Subject.Provider)
	}
	if recorded.Subject.Name != "" {
		parts = append(parts, "model="+recorded.Subject.Name)
	}
	for _, measurement := range recorded.Measurements {
		parts = append(parts, fmt.Sprintf("%s=%s", measurement.Metric, formatQuantity(measurement.Quantity)))
	}
	return strings.Join(parts, " ")
}

func usageLineFromMap(payload map[string]any) string {
	parts := []string{"usage:"}
	if source, ok := payload["source"].(string); ok && source != "" {
		parts = append(parts, "source="+source)
	}
	if subject, ok := payload["subject"].(map[string]any); ok {
		if provider, ok := subject["provider"].(string); ok && provider != "" {
			parts = append(parts, "provider="+provider)
		}
		if name, ok := subject["name"].(string); ok && name != "" {
			parts = append(parts, "model="+name)
		}
	}
	measurements, ok := payload["measurements"].([]any)
	if !ok || len(measurements) == 0 {
		return ""
	}
	for _, raw := range measurements {
		measurement, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		metric, _ := measurement["metric"].(string)
		quantity, _ := measurement["quantity"].(float64)
		if metric == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", metric, formatQuantity(quantity)))
	}
	if len(parts) == 1 {
		return ""
	}
	return strings.Join(parts, " ")
}

func formatQuantity(quantity float64) string {
	if quantity == float64(int64(quantity)) {
		return fmt.Sprintf("%d", int64(quantity))
	}
	return fmt.Sprintf("%.2f", quantity)
}

func shellOperation() operation.Operation {
	spec := coder.ShellSpec()
	return operation.New(spec, func(ctx operation.Context, input operation.Value) operation.Result {
		req := shellRequestFrom(input)
		if req.Command == "" {
			return operation.Failed("invalid_shell_input", "command is empty", nil)
		}
		args := req.Args
		command := req.Command
		if len(args) == 0 {
			parts := strings.Fields(command)
			if len(parts) > 0 {
				command = parts[0]
				args = parts[1:]
			}
		}
		if deniedCommand(command) {
			return operation.Rejected("shell_command_denied", "command is blocked by coder safety policy", map[string]any{"command": command})
		}
		timeout := boundedDuration(req.TimeoutMS, 30*time.Second)
		cmdCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		execCmd := exec.CommandContext(cmdCtx, command, args...)
		out, err := execCmd.CombinedOutput()
		output := truncate(string(out), 64*1024)
		if cmdCtx.Err() != nil {
			return operation.Failed("shell_timeout", cmdCtx.Err().Error(), map[string]any{"command": command, "output": output})
		}
		result := map[string]any{
			"command": command,
			"args":    args,
			"output":  output,
		}
		if err != nil {
			result["error"] = err.Error()
			if exit, ok := err.(*exec.ExitError); ok {
				result["exit_code"] = exit.ExitCode()
			}
			return operation.Failed("shell_failed", err.Error(), result)
		}
		result["exit_code"] = 0
		return operation.OK(result)
	})
}

func httpRequestOperation() operation.Operation {
	spec := coder.HTTPRequestSpec()
	return operation.New(spec, func(ctx operation.Context, input operation.Value) operation.Result {
		req := httpRequestFrom(input)
		parsed, err := url.Parse(req.URL)
		if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return operation.Failed("invalid_http_url", "url must be absolute http or https", map[string]any{"url": req.URL})
		}
		timeout := boundedDuration(req.TimeoutMS, 30*time.Second)
		maxBytes := req.MaxBytes
		if maxBytes <= 0 || maxBytes > 64*1024 {
			maxBytes = 64 * 1024
		}
		client := &http.Client{Timeout: timeout}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return operation.Failed("http_request_failed", err.Error(), nil)
		}
		resp, err := client.Do(httpReq)
		if err != nil {
			return operation.Failed("http_request_failed", err.Error(), nil)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
		if err != nil {
			return operation.Failed("http_read_failed", err.Error(), nil)
		}
		truncated := len(body) > maxBytes
		if truncated {
			body = body[:maxBytes]
		}
		return operation.OK(map[string]any{
			"url":         parsed.String(),
			"status":      resp.Status,
			"status_code": resp.StatusCode,
			"headers":     resp.Header,
			"body":        string(body),
			"truncated":   truncated,
		})
	})
}

type shellRequest struct {
	Command   string
	Args      []string
	TimeoutMS int
}

func shellRequestFrom(input any) shellRequest {
	switch value := input.(type) {
	case map[string]any:
		req := shellRequest{Command: stringValue(value["command"]), TimeoutMS: intValue(value["timeout_ms"])}
		if rawArgs, ok := value["args"].([]any); ok {
			for _, arg := range rawArgs {
				if text, ok := arg.(string); ok {
					req.Args = append(req.Args, text)
				}
			}
		}
		return req
	case string:
		return shellRequest{Command: value}
	default:
		return shellRequest{}
	}
}

type httpRequest struct {
	URL       string
	MaxBytes  int
	TimeoutMS int
}

func httpRequestFrom(input any) httpRequest {
	switch value := input.(type) {
	case map[string]any:
		return httpRequest{
			URL:       stringValue(value["url"]),
			MaxBytes:  intValue(value["max_bytes"]),
			TimeoutMS: intValue(value["timeout_ms"]),
		}
	case string:
		return httpRequest{URL: value}
	default:
		return httpRequest{}
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func boundedDuration(ms int, fallback time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	duration := time.Duration(ms) * time.Millisecond
	if duration > 30*time.Second {
		return 30 * time.Second
	}
	return duration
}

func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[:max]
}

func deniedCommand(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "rm", "sudo", "su", "chmod", "chown", "mkfs", "dd", "shutdown", "reboot", "kill", "pkill":
		return true
	default:
		return false
	}
}

func debugStreamPolicy(debug bool) llmagent.StreamPolicy {
	if !debug {
		return llmagent.StreamPolicy{}
	}
	return llmagent.StreamPolicy{EmitContent: true, EmitThinking: true, EmitToolCall: true}
}

func debugRedactor(debug bool) adapterllm.Redactor {
	if !debug {
		return adapterllm.Redactor{}
	}
	return adapterllm.Redactor{ExposeThinking: true, ExposeToolArgs: true}
}

type localSandbox struct{}

func (localSandbox) Check(operation.Context, operation.Spec, operation.Value) error {
	return nil
}

type localACL struct{}

func (localACL) Authorize(operation.Context, operation.Spec, operation.Value) error {
	return nil
}

type commandRisk struct{}

func (commandRisk) Classify(_ operation.Context, spec operation.Spec, input operation.Value) (operationruntime.CommandRisk, error) {
	if !spec.Semantics.Effects.Has(operation.EffectProcess) {
		return operationruntime.CommandRisk{Level: spec.Semantics.Risk}, nil
	}
	req := shellRequestFrom(input)
	if deniedCommand(req.Command) {
		return operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "blocked command"}, nil
	}
	return operationruntime.CommandRisk{Level: operation.RiskMedium, Reason: "local command without shell interpreter"}, nil
}
