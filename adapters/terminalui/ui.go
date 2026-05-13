// Package terminalui renders runtime events and human prompts for terminal apps.
package terminalui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/codewandler/markdown/stream"
	mdterminal "github.com/codewandler/markdown/terminal"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	ansiReset  = "\x1b[0m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
	ansiDim    = "\x1b[2m"
)

// Renderer renders client events for humans.
type Renderer struct {
	Out       io.Writer
	Err       io.Writer
	ShowUsage bool

	mu     sync.Mutex
	starts map[operation.CallID]time.Time

	content *mdterminal.LiveRenderer
	debug   *mdterminal.LiveRenderer

	streamedContent bool
}

// NewRenderer returns a terminal event renderer.
func NewRenderer(out, err io.Writer, showUsage bool) *Renderer {
	return &Renderer{
		Out:       out,
		Err:       err,
		ShowUsage: showUsage,
		starts:    map[operation.CallID]time.Time{},
		content:   newMarkdownRenderer(out),
		debug:     newMarkdownRenderer(err),
	}
}

// Render renders one event.
func (r *Renderer) Render(event clientapi.Event) {
	if r == nil {
		return
	}
	out := r.Err
	if out == nil {
		out = io.Discard
	}
	switch event.Kind {
	case clientapi.EventOperationRequested:
		r.flushContent()
		if event.Operation == nil {
			return
		}
		r.mu.Lock()
		r.starts[event.Operation.CallID] = time.Now()
		r.mu.Unlock()
		_, _ = fmt.Fprintf(out, "%stool start:%s %s%s", ansiYellow, ansiReset, ansiCyan, event.Operation.Operation.String())
		if summary := compact(event.Operation.Input, 320); summary != "" {
			_, _ = fmt.Fprintf(out, " %s", summary)
		}
		_, _ = fmt.Fprintf(out, "%s\n", ansiReset)
	case clientapi.EventOperationCompleted:
		r.flushContent()
		if event.Operation == nil || event.Operation.Result == nil {
			return
		}
		duration := r.duration(event.Operation.CallID)
		status := event.Operation.Result.Status
		if status == "" {
			status = operation.StatusOK
		}
		color := ansiGreen
		if event.Operation.Result.Error != nil || status != operation.StatusOK {
			color = ansiRed
		}
		_, _ = fmt.Fprintf(out, "%stool end:%s %s status=%s duration=%s", color, ansiReset, event.Operation.Operation.String(), status, duration.Round(time.Millisecond))
		if event.Operation.Result.Error != nil {
			_, _ = fmt.Fprintf(out, " error=%s", event.Operation.Result.Error.Message)
		} else if summary := resultSummary(*event.Operation.Result); summary != "" {
			_, _ = fmt.Fprintf(out, " %s", summary)
		}
		_, _ = fmt.Fprintln(out)
	case clientapi.EventRuntimeEmitted:
		r.renderRuntime(out, event)
	}
}

// Finish flushes streaming markdown state.
func (r *Renderer) Finish() {
	r.flushContent()
	if r.debug != nil {
		_ = r.debug.Flush()
		r.debug = newMarkdownRenderer(r.Err)
	}
}

// HasStreamedContent reports whether assistant content was rendered as deltas.
func (r *Renderer) HasStreamedContent() bool {
	if r == nil {
		return false
	}
	return r.streamedContent
}

// RenderDebug renders a client event as syntax-highlighted fenced JSON.
func (r *Renderer) RenderDebug(event clientapi.Event) {
	if r == nil || r.debug == nil {
		return
	}
	data, err := json.MarshalIndent(redactedDebugEvent(event), "", "  ")
	if err != nil {
		data = []byte(fmt.Sprintf("%#v", event))
	}
	_, _ = r.debug.Write([]byte("```json\n"))
	_, _ = r.debug.Write(data)
	_, _ = r.debug.Write([]byte("\n```\n\n"))
	_ = r.debug.Flush()
	r.debug = newMarkdownRenderer(r.Err)
}

func (r *Renderer) duration(callID operation.CallID) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	start, ok := r.starts[callID]
	if !ok {
		return 0
	}
	delete(r.starts, callID)
	return time.Since(start)
}

func (r *Renderer) renderRuntime(out io.Writer, event clientapi.Event) {
	if event.Runtime == nil {
		return
	}
	if r.renderPlanRuntime(out, string(event.Runtime.Name), event.Runtime.Payload) {
		return
	}
	switch payload := event.Runtime.Payload.(type) {
	case llmagent.ModelStreamed:
		r.renderModelStream(payload.Event)
	case system.ProcessEvent:
		r.flushContent()
		renderProcessEvent(out, payload)
	case usage.Recorded:
		r.flushContent()
		if r.ShowUsage {
			RenderUsageSnapshot(out, usage.NewSnapshot(payload))
		}
	case subagent.Started:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%sdelegate start:%s %s %s[%s]%s\n", ansiCyan, ansiReset, payload.WorkerID, ansiDim, payload.Profile.Name, ansiReset)
	case subagent.Completed:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%sdelegate done:%s %s %s\n", ansiGreen, ansiReset, payload.WorkerID, compact(payload.Output, 160))
	case subagent.Failed:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%sdelegate failed:%s %s %s\n", ansiRed, ansiReset, payload.WorkerID, payload.Error)
	default:
		r.flushContent()
		if string(event.Runtime.Name) == "human.clarification.requested" {
			return
		}
		if string(event.Runtime.Name) == "human.clarification.completed" {
			_, _ = fmt.Fprintf(out, "clarify answer: %s\n", field(payload, "Answer"))
		}
	}
}

func (r *Renderer) renderPlanCreated(out io.Writer, payload terminalPlanCreated) {
	r.flushContent()
	_, _ = fmt.Fprintf(out, "\n%splan:%s %s %s(%d steps)%s\n", ansiCyan, ansiReset, payload.Spec.Title, ansiDim, len(payload.Spec.Steps), ansiReset)
	for _, step := range payload.Spec.Steps {
		profile := step.Profile
		if profile == "" {
			profile = "worker"
		}
		_, _ = fmt.Fprintf(out, "  %s◌%s %s %s[%s]%s\n", ansiDim, ansiReset, step.Title, ansiDim, profile, ansiReset)
	}
}

func (r *Renderer) renderPlanRuntime(out io.Writer, name string, payload any) bool {
	switch name {
	case "plan.created":
		var typed terminalPlanCreated
		if decodeTypedPayload(payload, &typed) == nil {
			r.renderPlanCreated(out, typed)
			return true
		}
	case "plan.step.dispatched":
		var typed terminalPlanStepDispatched
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			_, _ = fmt.Fprintf(out, "%splan step:%s %s %s[%s]%s\n", ansiCyan, ansiReset, typed.StepID, ansiDim, typed.Profile, ansiReset)
			return true
		}
	case "plan.step.progressed":
		var typed terminalPlanStepProgressed
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			_, _ = fmt.Fprintf(out, "%splan progress:%s %s %s%s%s\n", ansiCyan, ansiReset, typed.StepID, ansiDim, typed.Message, ansiReset)
			return true
		}
	case "plan.step.completed":
		var typed terminalPlanStepCompleted
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			_, _ = fmt.Fprintf(out, "%splan done:%s %s %s\n", ansiGreen, ansiReset, typed.StepID, compact(typed.Output, 160))
			return true
		}
	case "plan.step.failed":
		var typed terminalPlanStepFailed
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			_, _ = fmt.Fprintf(out, "%splan failed:%s %s %s\n", ansiRed, ansiReset, typed.StepID, typed.Error)
			return true
		}
	case "plan.completed":
		var typed terminalPlanCompleted
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			_, _ = fmt.Fprintf(out, "%splan completed:%s %s\n", ansiGreen, ansiReset, typed.Summary)
			return true
		}
	case "plan.failed":
		var typed terminalPlanFailed
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			_, _ = fmt.Fprintf(out, "%splan failed:%s %s\n", ansiRed, ansiReset, typed.Reason)
			return true
		}
	case "plan.cancelled":
		var typed terminalPlanCancelled
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			_, _ = fmt.Fprintf(out, "%splan cancelled:%s %s\n", ansiYellow, ansiReset, typed.Reason)
			return true
		}
	}
	return false
}

type terminalPlanCreated struct {
	PlanID string           `json:"plan_id"`
	Spec   terminalPlanSpec `json:"spec"`
}

type terminalPlanSpec struct {
	Title string             `json:"title,omitempty"`
	Steps []terminalStepSpec `json:"steps,omitempty"`
}

type terminalStepSpec struct {
	ID      string `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
	Profile string `json:"profile,omitempty"`
}

type terminalPlanStepDispatched struct {
	StepID  string `json:"step_id"`
	Profile string `json:"profile,omitempty"`
}

type terminalPlanStepProgressed struct {
	StepID  string `json:"step_id"`
	Message string `json:"message,omitempty"`
}

type terminalPlanStepCompleted struct {
	StepID string `json:"step_id"`
	Output string `json:"output,omitempty"`
}

type terminalPlanStepFailed struct {
	StepID string `json:"step_id"`
	Error  string `json:"error,omitempty"`
}

type terminalPlanCompleted struct {
	Summary string `json:"summary,omitempty"`
}

type terminalPlanFailed struct {
	Reason string `json:"reason,omitempty"`
}

type terminalPlanCancelled struct {
	Reason string `json:"reason,omitempty"`
}

func decodeTypedPayload(payload any, out any) error {
	switch payload.(type) {
	case json.RawMessage, map[string]any, []byte:
		return fmt.Errorf("terminalui: untyped runtime payload")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (r *Renderer) renderModelStream(event llmagent.StreamEvent) {
	switch event.Kind {
	case llmagent.StreamThinkingDelta:
		return
	case llmagent.StreamContentDelta:
		if event.Text == "" {
			return
		}
		r.writeContentDelta(event.Text)
	case llmagent.StreamToolCallDelta:
		if event.Tool != "" && event.Final {
			r.flushContent()
			_, _ = fmt.Fprintf(r.Err, "%stool call:%s %s\n", ansiYellow, ansiReset, event.Tool)
		}
	}
}

func (r *Renderer) flushContent() {
	if r == nil {
		return
	}
	if r.content != nil {
		_ = r.content.Flush()
		r.content = newMarkdownRenderer(r.out())
	}
}

func newMarkdownRenderer(w io.Writer) *mdterminal.LiveRenderer {
	if w == nil {
		w = io.Discard
	}
	return mdterminal.NewLiveRenderer(w, markdownRendererOptions()...)
}

func markdownRendererOptions() []mdterminal.RendererOption {
	return []mdterminal.RendererOption{
		mdterminal.WithAnsi(mdterminal.AnsiOn),
		mdterminal.WithParserOptions(stream.WithGFMAutolinks()),
	}
}

func (r *Renderer) writeContentDelta(text string) {
	if r.content == nil {
		r.content = newMarkdownRenderer(r.out())
	}
	if _, err := r.content.Write([]byte(text)); err == nil {
		r.streamedContent = true
	}
}

func (r *Renderer) out() io.Writer {
	if r == nil || r.Out == nil {
		return io.Discard
	}
	return r.Out
}

// RenderMarkdown renders one complete Markdown document to w.
func RenderMarkdown(w io.Writer, text string) error {
	renderer := newMarkdownRenderer(w)
	if _, err := renderer.Write([]byte(text)); err != nil {
		return err
	}
	return renderer.Flush()
}

func redactedDebugEvent(event clientapi.Event) clientapi.Event {
	if event.Runtime == nil || event.Runtime.Name != llmagent.EventModelStreamedName {
		return event
	}
	out := event
	runtimeEvent := *event.Runtime
	out.Runtime = &runtimeEvent
	if payload, ok := event.Runtime.Payload.(llmagent.ModelStreamed); ok {
		if payload.Event.Kind == llmagent.StreamThinkingDelta && payload.Event.Text != "" {
			payload.Event.Redaction = fmt.Sprintf("thinking_delta:%d_bytes", len(payload.Event.Text))
			payload.Event.Text = ""
			runtimeEvent.Payload = payload
		}
	}
	return out
}

func field(value any, name string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ""
	}
	if found, ok := decoded[strings.ToLower(name)]; ok {
		return compact(found, 320)
	}
	if found, ok := decoded[name]; ok {
		return compact(found, 320)
	}
	return ""
}

func renderProcessEvent(out io.Writer, event system.ProcessEvent) {
	switch event.Kind {
	case "started":
		_, _ = fmt.Fprintf(out, "process start: %s\n", event.ProcessID)
	case "exited":
		_, _ = fmt.Fprintf(out, "process exit: %s code=%s\n", event.ProcessID, strings.TrimSpace(event.Data))
	case "output":
		prefix := "stdout"
		if event.Stream != "" {
			prefix = event.Stream
		}
		for _, line := range strings.SplitAfter(event.Data, "\n") {
			if line == "" {
				continue
			}
			_, _ = fmt.Fprintf(out, "%s: %s", prefix, line)
			if !strings.HasSuffix(line, "\n") {
				_, _ = fmt.Fprintln(out)
			}
		}
	}
}

func resultSummary(result operation.Result) string {
	switch value := result.Output.(type) {
	case operation.Rendered:
		if value.Text != "" {
			return compact(value.Text, 240)
		}
		return compact(value.Data, 240)
	case map[string]any:
		if text, ok := value["text"].(string); ok && text != "" {
			return compact(text, 240)
		}
		return compact(value, 240)
	default:
		return compact(value, 240)
	}
}

func compact(value any, limit int) string {
	if value == nil {
		return ""
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			text = fmt.Sprint(typed)
		} else {
			text = string(data)
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

// RenderUsageSnapshot renders grouped usage totals.
func RenderUsageSnapshot(w io.Writer, snapshot usage.Snapshot) {
	if w == nil || snapshot.Empty() {
		return
	}
	_, _ = fmt.Fprintf(w, "%sTotal usage%s\n", ansiCyan, ansiReset)
	for _, subject := range snapshot.Subjects {
		if len(subject.Totals) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "%s %s%s%s\n", subjectIcon(subject.Subject.Kind), ansiCyan, subjectLabel(subject.Subject), ansiReset)
		for _, measurement := range subject.Totals {
			if line := measurementLine(measurement); line != "" {
				_, _ = fmt.Fprintf(w, "  %s\n", line)
			}
		}
	}
}

func subjectIcon(kind usage.SubjectKind) string {
	switch kind {
	case usage.SubjectLLM:
		return "🧠"
	case usage.SubjectNetwork:
		return "🌐"
	case usage.SubjectFile:
		return "📄"
	case usage.SubjectProcess:
		return "⚙"
	case usage.SubjectMoney:
		return "💵"
	default:
		return "•"
	}
}

func subjectLabel(subject usage.Subject) string {
	if subject.Provider != "" && subject.Name != "" {
		return subject.Provider + "/" + subject.Name
	}
	if subject.Name != "" {
		return subject.Name
	}
	if subject.Provider != "" {
		return subject.Provider
	}
	if subject.Kind != "" {
		return string(subject.Kind)
	}
	return "usage"
}

func measurementLine(measurement usage.Measurement) string {
	switch measurement.Metric {
	case usage.MetricLLMInputTokens:
		if measurement.Dimensions != nil && measurement.Dimensions["cache_creation"] == "true" {
			return "↥ cache write tokens " + formatHumanNumber(measurement.Quantity)
		}
		return "↑ input tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricLLMCachedTokens:
		return "↻ cached input tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricLLMOutputTokens:
		return "↓ output tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricLLMReasoningTokens:
		return "✦ reasoning tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricLLMTotalTokens:
		return "∑ total tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricNetworkBytes:
		return networkBytesLine(measurement)
	case usage.MetricFileBytes:
		return "↔ file bytes " + formatBytes(measurement.Quantity)
	case usage.MetricRequests:
		return "• requests " + formatHumanNumber(measurement.Quantity)
	case usage.MetricWallTime:
		return "◷ wall time " + formatDurationQuantity(measurement.Quantity, measurement.Unit)
	case usage.MetricCost:
		return "💵 estimated cost " + formatCost(measurement)
	default:
		return string(measurement.Metric) + " " + formatHumanQuantity(measurement)
	}
}

func networkBytesLine(measurement usage.Measurement) string {
	switch measurement.Direction {
	case usage.DirectionUpload, usage.DirectionInput, usage.DirectionWrite:
		return "↑ uploaded " + formatBytes(measurement.Quantity)
	case usage.DirectionDownload, usage.DirectionOutput, usage.DirectionRead:
		return "↓ downloaded " + formatBytes(measurement.Quantity)
	default:
		return "↔ transferred " + formatBytes(measurement.Quantity)
	}
}

func formatHumanQuantity(measurement usage.Measurement) string {
	switch measurement.Unit {
	case usage.UnitByte:
		return formatBytes(measurement.Quantity)
	case usage.UnitCurrency:
		return formatCost(measurement)
	default:
		return formatHumanNumber(measurement.Quantity)
	}
}

func formatHumanNumber(quantity float64) string {
	if quantity != float64(int64(quantity)) {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", quantity), "0"), ".")
	}
	value := int64(quantity)
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	text := fmt.Sprintf("%d", value)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return sign + text
}

func formatBytes(quantity float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := quantity
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return formatHumanNumber(value) + " " + units[unit]
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".") + " " + units[unit]
}

func formatCost(measurement usage.Measurement) string {
	currency := "USD"
	if measurement.Dimensions != nil && measurement.Dimensions["currency"] != "" {
		currency = measurement.Dimensions["currency"]
	}
	prefix := currency + " "
	if currency == "USD" {
		prefix = "$"
	}
	quantity := measurement.Quantity
	switch {
	case quantity >= 1:
		return prefix + fmt.Sprintf("%.2f", quantity)
	case quantity >= 0.01:
		return prefix + strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", quantity), "0"), ".")
	default:
		return prefix + strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", quantity), "0"), ".")
	}
}

func formatDurationQuantity(quantity float64, unit usage.Unit) string {
	if unit == usage.UnitMillisecond {
		return time.Duration(quantity * float64(time.Millisecond)).Round(time.Millisecond).String()
	}
	return formatHumanNumber(quantity)
}

// Prompter collects clarify answers from a terminal.
type Prompter struct {
	In  io.Reader
	Out io.Writer
}

// Clarify implements system.Clarifier.
func (p Prompter) Clarify(ctx context.Context, req system.ClarifyRequest) (system.ClarifyResult, error) {
	if p.In == nil {
		return system.ClarifyResult{}, fmt.Errorf("terminalui: input is nil")
	}
	out := p.Out
	if out == nil {
		out = io.Discard
	}
	_, _ = fmt.Fprintf(out, "\nclarify: %s\n", req.Prompt)
	fields := schemaFields(req.Schema)
	reader := bufio.NewReader(p.In)
	if len(fields) == 0 {
		_, _ = fmt.Fprint(out, "> ")
		text, err := readLine(ctx, reader)
		if err != nil {
			return system.ClarifyResult{}, err
		}
		var decoded any
		if err := json.Unmarshal([]byte(text), &decoded); err == nil {
			return system.ClarifyResult{Answer: decoded}, nil
		}
		return system.ClarifyResult{Answer: text}, nil
	}
	answer := map[string]any{}
	for _, field := range fields {
		prompt := field.Name
		if field.Enum != "" {
			prompt += " " + field.Enum
		}
		if value, ok := req.Defaults[field.Name]; ok {
			prompt += fmt.Sprintf(" [%v]", value)
		}
		_, _ = fmt.Fprintf(out, "%s: ", prompt)
		text, err := readLine(ctx, reader)
		if err != nil {
			return system.ClarifyResult{}, err
		}
		if strings.TrimSpace(text) == "" {
			if value, ok := req.Defaults[field.Name]; ok {
				answer[field.Name] = value
			}
			continue
		}
		answer[field.Name] = text
	}
	return system.ClarifyResult{Answer: answer}, nil
}

type schemaField struct {
	Name string
	Enum string
}

func schemaFields(raw json.RawMessage) []schemaField {
	if len(raw) == 0 {
		return nil
	}
	var schema struct {
		Properties map[string]struct {
			Enum []any `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil
	}
	names := append([]string(nil), schema.Required...)
	for name := range schema.Properties {
		if !contains(names, name) {
			names = append(names, name)
		}
	}
	fields := make([]schemaField, 0, len(names))
	for _, name := range names {
		var enum string
		if prop, ok := schema.Properties[name]; ok && len(prop.Enum) > 0 {
			values := make([]string, 0, len(prop.Enum))
			for _, value := range prop.Enum {
				values = append(values, fmt.Sprint(value))
			}
			enum = "(" + strings.Join(values, "|") + ")"
		}
		fields = append(fields, schemaField{Name: name, Enum: enum})
	}
	return fields
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func readLine(ctx context.Context, reader *bufio.Reader) (string, error) {
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := reader.ReadString('\n')
		done <- result{text: strings.TrimSpace(text), err: err}
	}()
	select {
	case result := <-done:
		if result.err != nil && result.err != io.EOF {
			return "", result.err
		}
		return result.text, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
