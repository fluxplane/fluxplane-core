package human

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-event"
)

const (
	Name         = "human"
	ClarifyOp    = "clarify"
	NotifyOp     = "notify"
	NotifySendOp = "notify_send"
)

const maxSpeechChars = 280

const EventClarificationRequested event.Name = "human.clarification.requested"
const EventNotificationSent event.Name = "human.notification.sent"

// ClarificationRequested asks a channel/UI adapter to collect structured input.
type ClarificationRequested struct {
	Prompt string          `json:"prompt"`
	Schema json.RawMessage `json:"schema,omitempty"`
}

func (ClarificationRequested) EventName() event.Name { return EventClarificationRequested }

// NotificationSent records a user-facing notification emitted by a workflow or
// agent operation.
type NotificationSent struct {
	Title    string         `json:"title,omitempty"`
	Message  string         `json:"message"`
	Level    string         `json:"level,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func (NotificationSent) EventName() event.Name { return EventNotificationSent }

// Plugin contributes human-in-the-loop operations.
type Plugin struct {
	system    system.System
	clarifier system.Clarifier
	speak     func(string) error
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns the human plugin.
func New(clarifier system.Clarifier) Plugin { return Plugin{clarifier: clarifier} }

// NewWithSystem returns the human plugin using the runtime system boundary for
// OS notifications, audio, and clarification.
func NewWithSystem(sys system.System) Plugin {
	var clarifier system.Clarifier
	if sys != nil {
		clarifier = sys.Clarifier()
	}
	return Plugin{system: sys, clarifier: clarifier}
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Human clarification operations."}
}

// Contributions returns human specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	clarify := clarifySpec()
	notify := notifySpec()
	notifySend := notifySendSpec()
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Human interaction operations.", Operations: []operation.Ref{clarify.Ref, notify.Ref, notifySend.Ref}}},
		Operations:    []operation.Spec{clarify, notify, notifySend},
		EventTypes:    []event.Event{ClarificationRequested{}, ClarificationCompleted{}, NotificationSent{}},
	}, nil
}

// Operations returns executable human operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operationruntime.NewTypedResult[clarifyInput, map[string]any](clarifySpec(), p.clarify),
		operationruntime.NewTypedResult[notifyInput, map[string]any](notifySpec(), p.notify, operationruntime.WithIntent(notifyIntent)),
		operationruntime.NewTypedResult[notifyInput, map[string]any](notifySendSpec(), p.notify, operationruntime.WithIntent(notifyIntent)),
	}, nil
}

func clarifySpec() operation.Spec {
	return operationruntime.WithTypedContract[clarifyInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: ClarifyOp},
		Description: "Ask the user for structured clarification using a prompt and optional JSON Schema.",
		Semantics:   operation.Semantics{Determinism: operation.DeterminismNonDeterministic, Effects: operation.EffectSet{operation.EffectReadExternal}, Risk: operation.RiskLow},
	})
}

type clarifyInput struct {
	Prompt   string          `json:"prompt" jsonschema:"description=Question or instruction shown to the user.,required"`
	Schema   json.RawMessage `json:"schema,omitempty" jsonschema:"description=JSON Schema describing the expected answer."`
	Defaults map[string]any  `json:"defaults,omitempty" jsonschema:"description=Optional default values for structured answers."`
}

func notifySpec() operation.Spec {
	return operationruntime.WithTypedContract[notifyInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: NotifyOp},
		Description: "Send a desktop notification and/or audio alert.",
		Semantics:   notifySemantics(),
	})
}

func notifySendSpec() operation.Spec {
	return operationruntime.WithTypedContract[notifyInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: NotifySendOp},
		Description: "Send a desktop notification and/or audio alert via notify-send, preset tones, and Piper text-to-speech.",
		Semantics:   notifySemantics(),
	})
}

func notifySemantics() operation.Semantics {
	return operation.Semantics{
		Determinism: operation.DeterminismNonDeterministic,
		Effects:     operation.EffectSet{operation.EffectWriteExternal, operation.EffectProcess},
		Risk:        operation.RiskLow,
	}
}

type notifyInput struct {
	Summary    string         `json:"summary,omitempty" jsonschema:"description=Notification title / summary. Optional when tone or speak is provided."`
	Body       string         `json:"body,omitempty" jsonschema:"description=Optional notification body text."`
	Urgency    string         `json:"urgency,omitempty" jsonschema:"enum=low,enum=normal,enum=critical,description=Urgency level. Defaults to normal."`
	ExpireTime int            `json:"expire_time,omitempty" jsonschema:"description=Auto-dismiss timeout in milliseconds. Zero uses the notification server default."`
	AppName    string         `json:"app_name,omitempty" jsonschema:"description=Application name shown in the notification. Defaults to fluxplane."`
	Icon       string         `json:"icon,omitempty" jsonschema:"description=Icon name or absolute image path. Defaults to dialog-information."`
	Category   string         `json:"category,omitempty" jsonschema:"description=Notification category hint."`
	Tone       string         `json:"tone,omitempty" jsonschema:"enum=beep,enum=alarm,enum=success,enum=error,enum=warning,enum=info,description=Sound preset to play."`
	Speak      string         `json:"speak,omitempty" jsonschema:"description=Text to speak aloud using Piper text-to-speech."`
	Title      string         `json:"title,omitempty" jsonschema:"description=Alias for summary."`
	Message    string         `json:"message,omitempty" jsonschema:"description=Alias for body."`
	Level      string         `json:"level,omitempty" jsonschema:"enum=info,enum=warning,enum=error,description=Alias mapped to urgency/icon/tone defaults."`
	Metadata   map[string]any `json:"metadata,omitempty" jsonschema:"description=Optional structured notification metadata."`
}

var validUrgencies = map[string]bool{"low": true, "normal": true, "critical": true}
var validTones = map[string]bool{"beep": true, "alarm": true, "success": true, "error": true, "warning": true, "info": true}

var toneFiles = map[string]string{
	"beep":    "/usr/share/sounds/freedesktop/stereo/bell.oga",
	"alarm":   "/usr/share/sounds/freedesktop/stereo/alarm-clock-elapsed.oga",
	"success": "/usr/share/sounds/freedesktop/stereo/complete.oga",
	"error":   "/usr/share/sounds/freedesktop/stereo/dialog-error.oga",
	"warning": "/usr/share/sounds/freedesktop/stereo/dialog-warning.oga",
	"info":    "/usr/share/sounds/freedesktop/stereo/dialog-information.oga",
}

var soxToneArgs = map[string][]string{
	"beep":    {"-n", "synth", "0.2", "sine", "880"},
	"alarm":   {"-n", "synth", "0.6", "sine", "880", "gain", "-n", "-3"},
	"success": {"-n", "synth", "0.3", "sine", "1047"},
	"error":   {"-n", "synth", "0.5", "sine", "220"},
	"warning": {"-n", "synth", "0.4", "sine", "660"},
	"info":    {"-n", "synth", "0.15", "sine", "523"},
}

func soxArgs(tone string) []string {
	return soxToneArgs[tone]
}

func notifyIntent(_ operation.Context, req notifyInput) (operation.IntentSet, error) {
	req = normalizeNotifyInput(req)
	if err := validateNotifyInput(req); err != nil {
		return operation.IntentSet{}, err
	}
	ops := []operation.IntentOperation{}
	if req.Summary != "" {
		ops = append(ops, notifyProcessIntent("notify-send", buildNotifyArgs(req), operation.IntentCertain))
	}
	if req.Tone != "" {
		if file := toneFiles[req.Tone]; file != "" {
			ops = append(ops, notifyProcessIntent("paplay", []string{file}, operation.IntentPotential))
		}
		ops = append(ops, notifyProcessIntent("play", soxArgs(req.Tone), operation.IntentPotential))
	}
	if speak := speechText(req.Speak); speak != "" {
		ops = append(ops,
			notifyProcessIntent("piper_embedded", []string{speak}, operation.IntentCertain),
			notifyProcessIntent("aplay", []string{"fluxplane-piper.wav"}, operation.IntentPotential),
			notifyProcessIntent("paplay", []string{"fluxplane-piper.wav"}, operation.IntentPotential),
		)
	}
	return operation.IntentSet{Operations: ops}, nil
}

func notifyProcessIntent(command string, args []string, certainty operation.IntentCertainty) operation.IntentOperation {
	arguments := make([]operation.Argument, 0, len(args))
	for _, arg := range args {
		arguments = append(arguments, operation.Argument(arg))
	}
	return operation.IntentOperation{
		Behavior:  operation.IntentCommandExecution,
		Target:    operation.ProcessTarget{Command: operation.Command(command), Args: arguments},
		Role:      operation.IntentRoleProcessCommand,
		Certainty: certainty,
	}
}

func (p Plugin) notify(ctx operation.Context, req notifyInput) operation.Result {
	req = normalizeNotifyInput(req)
	if err := validateNotifyInput(req); err != nil {
		return operation.Failed("invalid_notify_input", err.Error(), nil)
	}
	if p.system == nil || p.system.Process() == nil {
		return operation.Failed("notify_unavailable", "notify requires a runtime system with process execution", nil)
	}

	execCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var parts []string
	if strings.TrimSpace(req.Summary) != "" {
		msg, err := p.runNotifySend(execCtx, req)
		if err != nil {
			return operation.Failed("notify_send_failed", err.Error(), nil)
		}
		parts = append(parts, msg)
	}
	if strings.TrimSpace(req.Tone) != "" {
		if err := p.playTone(execCtx, req.Tone); err != nil {
			return operation.Failed("notify_tone_failed", err.Error(), nil)
		}
		parts = append(parts, "Tone played: "+req.Tone)
	}
	speak := speechText(req.Speak)
	if speak != "" {
		if err := p.speakMessage(ctx, speak); err != nil {
			return operation.Failed("notify_tts_failed", err.Error(), nil)
		}
		parts = append(parts, "Speaking (background): "+speak)
	}

	notification := NotificationSent{
		Title:    req.Summary,
		Message:  firstNonEmpty(req.Body, speak),
		Level:    notifyLevel(req),
		Metadata: cloneMap(req.Metadata),
	}
	ctx.Events().Emit(notification)
	return operation.OK(operation.Rendered{
		Text: strings.Join(parts, "\n"),
		Data: map[string]any{
			"summary":  req.Summary,
			"body":     req.Body,
			"urgency":  req.Urgency,
			"tone":     req.Tone,
			"speak":    speak,
			"metadata": notification.Metadata,
		},
	})
}

func normalizeNotifyInput(req notifyInput) notifyInput {
	if strings.TrimSpace(req.Summary) == "" {
		req.Summary = req.Title
	}
	if strings.TrimSpace(req.Body) == "" {
		req.Body = req.Message
	}
	req.Summary = strings.TrimSpace(req.Summary)
	req.Body = strings.TrimSpace(req.Body)
	req.Tone = strings.ToLower(strings.TrimSpace(req.Tone))
	req.Urgency = strings.ToLower(strings.TrimSpace(req.Urgency))
	req.Level = strings.ToLower(strings.TrimSpace(req.Level))
	switch req.Level {
	case "error":
		if req.Urgency == "" {
			req.Urgency = "critical"
		}
		if req.Icon == "" {
			req.Icon = "dialog-error"
		}
		if req.Tone == "" {
			req.Tone = "error"
		}
	case "warning":
		if req.Urgency == "" {
			req.Urgency = "normal"
		}
		if req.Icon == "" {
			req.Icon = "dialog-warning"
		}
		if req.Tone == "" {
			req.Tone = "warning"
		}
	case "info":
		if req.Urgency == "" {
			req.Urgency = "normal"
		}
		if req.Icon == "" {
			req.Icon = "dialog-information"
		}
	}
	return req
}

func validateNotifyInput(req notifyInput) error {
	if req.Summary == "" && req.Tone == "" && strings.TrimSpace(req.Speak) == "" {
		return fmt.Errorf("at least one of summary, tone, or speak must be provided")
	}
	if req.Urgency != "" && !validUrgencies[req.Urgency] {
		return fmt.Errorf("invalid urgency %q: must be low, normal, or critical", req.Urgency)
	}
	if req.Tone != "" && !validTones[req.Tone] {
		return fmt.Errorf("invalid tone %q: must be one of beep, alarm, success, error, warning, info", req.Tone)
	}
	switch req.Level {
	case "", "info", "warning", "error":
		return nil
	default:
		return fmt.Errorf("level must be info, warning, or error")
	}
}

func (p Plugin) runNotifySend(ctx context.Context, req notifyInput) (string, error) {
	result, err := p.system.Process().Run(ctx, system.ProcessRequest{
		Command: "notify-send",
		Args:    buildNotifyArgs(req),
		Timeout: 15 * time.Second,
	})
	if err != nil {
		return "", processError(result, err)
	}
	return fmt.Sprintf("Notification sent: %q", req.Summary), nil
}

func (p Plugin) playTone(ctx context.Context, tone string) error {
	if file := toneFiles[tone]; file != "" {
		result, err := p.system.Process().Run(ctx, system.ProcessRequest{
			Command: "paplay",
			Args:    []string{file},
			Timeout: 5 * time.Second,
		})
		if err == nil {
			return nil
		}
		_ = result
	}
	result, err := p.system.Process().Run(ctx, system.ProcessRequest{
		Command: "play",
		Args:    soxArgs(tone),
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return processError(result, err)
	}
	return nil
}

func (p Plugin) speakMessage(ctx context.Context, text string) error {
	if p.speak != nil {
		return p.speak(text)
	}
	return system.SpeakPiperBackground(ctx, text)
}

func speechText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	cleaned := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || line == "" {
			continue
		}
		line = stripMarkdownLine(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return limitSpeech(collapseWhitespace(strings.Join(cleaned, ". ")))
}

func stripMarkdownLine(line string) string {
	line = strings.TrimLeft(line, "#> \t")
	line = strings.TrimLeft(line, "-*+ \t")
	for len(line) > 0 && line[0] >= '0' && line[0] <= '9' {
		line = line[1:]
	}
	line = strings.TrimLeft(line, ". \t")
	line = stripMarkdownLinks(line)
	replacer := strings.NewReplacer(
		"**", "",
		"__", "",
		"`", "",
		"*", "",
		"_", "",
		"#", "",
	)
	return strings.TrimSpace(replacer.Replace(line))
}

func stripMarkdownLinks(line string) string {
	for {
		open := strings.Index(line, "[")
		if open < 0 {
			return line
		}
		close := strings.Index(line[open:], "](")
		if close < 0 {
			return line
		}
		close += open
		end := strings.Index(line[close+2:], ")")
		if end < 0 {
			return line
		}
		end += close + 2
		line = line[:open] + line[open+1:close] + line[end+1:]
	}
}

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func limitSpeech(value string) string {
	runes := []rune(value)
	if len(runes) <= maxSpeechChars {
		return value
	}
	cut := maxSpeechChars
	for i := maxSpeechChars; i >= 80; i-- {
		switch runes[i-1] {
		case '.', '!', '?':
			cut = i
			return strings.TrimSpace(string(runes[:cut]))
		}
	}
	return strings.TrimSpace(string(runes[:cut])) + "..."
}

func buildNotifyArgs(req notifyInput) []string {
	appName := firstNonEmpty(req.AppName, "fluxplane")
	icon := firstNonEmpty(req.Icon, "dialog-information")
	args := []string{"--app-name=" + appName, "--icon=" + icon}
	if req.Urgency != "" {
		args = append(args, "--urgency="+req.Urgency)
	}
	if req.ExpireTime > 0 {
		args = append(args, fmt.Sprintf("--expire-time=%d", req.ExpireTime))
	}
	if req.Category != "" {
		args = append(args, "--category="+req.Category)
	}
	args = append(args, req.Summary)
	if req.Body != "" {
		args = append(args, req.Body)
	}
	return args
}

func processError(result system.ProcessResult, err error) error {
	msg := strings.TrimSpace(result.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(result.Stdout)
	}
	if msg == "" && err != nil {
		msg = err.Error()
	}
	if msg == "" {
		msg = "process failed"
	}
	return fmt.Errorf("%s", msg)
}

func notifyLevel(req notifyInput) string {
	if req.Level != "" {
		return req.Level
	}
	switch req.Urgency {
	case "critical":
		return "error"
	default:
		return "info"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (p Plugin) clarify(ctx operation.Context, req clarifyInput) operation.Result {
	if strings.TrimSpace(req.Prompt) == "" {
		return operation.Failed("invalid_clarify_input", "prompt is required", nil)
	}
	ctx.Events().Emit(ClarificationRequested{Prompt: req.Prompt, Schema: req.Schema})
	if p.clarifier == nil {
		return operation.Failed("clarify_not_connected", "clarify requires a channel adapter capable of collecting user input", map[string]any{"prompt": req.Prompt})
	}
	result, err := p.clarifier.Clarify(ctx, system.ClarifyRequest{Prompt: req.Prompt, Schema: req.Schema, Defaults: req.Defaults})
	if err != nil {
		return operation.Failed("clarify_failed", err.Error(), map[string]any{"prompt": req.Prompt})
	}
	out := map[string]any{"answer": result.Answer}
	ctx.Events().Emit(ClarificationCompleted{Prompt: req.Prompt, Answer: result.Answer})
	return operation.OK(operation.Rendered{Text: renderAnswer(result.Answer), Data: out})
}

const EventClarificationCompleted event.Name = "human.clarification.completed"

// ClarificationCompleted records collected human input.
type ClarificationCompleted struct {
	Prompt string `json:"prompt"`
	Answer any    `json:"answer,omitempty"`
}

func (ClarificationCompleted) EventName() event.Name { return EventClarificationCompleted }

func renderAnswer(answer any) string {
	switch value := answer.(type) {
	case string:
		return value
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
