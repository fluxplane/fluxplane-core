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
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
)

// Renderer renders client events for humans.
type Renderer struct {
	Out       io.Writer
	Err       io.Writer
	ShowUsage bool

	mu     sync.Mutex
	starts map[operation.CallID]time.Time

	content  *mdterminal.LiveRenderer
	thinking *mdterminal.LiveRenderer
	debug    *mdterminal.LiveRenderer

	streamedContent bool
	inThinking      bool
}

// NewRenderer returns a terminal event renderer.
func NewRenderer(out, err io.Writer, showUsage bool) *Renderer {
	return &Renderer{
		Out:       out,
		Err:       err,
		ShowUsage: showUsage,
		starts:    map[operation.CallID]time.Time{},
		content:   newMarkdownRenderer(out),
		thinking:  newMarkdownRenderer(err),
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
	data, err := json.MarshalIndent(event, "", "  ")
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
	switch payload := event.Runtime.Payload.(type) {
	case llmagent.ModelStreamed:
		r.renderModelStream(payload.Event)
	case system.ProcessEvent:
		r.flushContent()
		renderProcessEvent(out, payload)
	case usage.Recorded:
		r.flushContent()
		if r.ShowUsage {
			if line := UsageLine(payload); line != "" {
				_, _ = fmt.Fprintln(out, line)
			}
		}
	case map[string]any:
		if string(event.Runtime.Name) == string(llmagent.EventModelStreamedName) {
			r.renderModelStreamFromMap(payload)
			return
		}
		if string(event.Runtime.Name) == "human.clarification.requested" {
			return
		}
		if string(event.Runtime.Name) == "human.clarification.completed" {
			_, _ = fmt.Fprintf(out, "clarify answer: %s\n", compact(payload["answer"], 320))
			return
		}
		if r.ShowUsage && event.Runtime.Name == usage.EventRecordedName {
			if line := usageLineFromMap(payload); line != "" {
				_, _ = fmt.Fprintln(out, line)
			}
		}
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

func (r *Renderer) renderModelStream(event llmagent.StreamEvent) {
	switch event.Kind {
	case llmagent.StreamThinkingDelta:
		if event.Text == "" {
			return
		}
		if !r.inThinking {
			r.flushContent()
			_, _ = fmt.Fprint(r.Err, ansiDim)
			r.inThinking = true
		}
		if r.thinking != nil {
			_, _ = r.thinking.Write([]byte(event.Text))
			_ = r.thinking.Flush()
		}
	case llmagent.StreamContentDelta:
		if event.Text == "" {
			return
		}
		if r.inThinking {
			r.flushThinking()
		}
		r.streamedContent = true
		r.writeContentDelta(event.Text)
	case llmagent.StreamToolCallDelta:
		if event.Tool != "" && event.Final {
			r.flushContent()
			_, _ = fmt.Fprintf(r.Err, "%stool call:%s %s\n", ansiYellow, ansiReset, event.Tool)
		}
	}
}

func (r *Renderer) renderModelStreamFromMap(payload map[string]any) {
	raw, ok := payload["event"]
	if !ok {
		return
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	var event llmagent.StreamEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return
	}
	r.renderModelStream(event)
}

func (r *Renderer) flushContent() {
	if r == nil {
		return
	}
	if r.inThinking {
		r.flushThinking()
	}
	if r.content != nil {
		_ = r.content.Flush()
		r.content = newMarkdownRenderer(r.out())
	}
}

func (r *Renderer) flushThinking() {
	if r == nil || !r.inThinking {
		return
	}
	if r.thinking != nil {
		_ = r.thinking.Flush()
	}
	_, _ = fmt.Fprintf(r.Err, "%s\n", ansiReset)
	r.thinking = newMarkdownRenderer(r.Err)
	r.inThinking = false
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
	_, _ = r.content.Write([]byte(text))
	_ = r.content.Flush()
}

func (r *Renderer) out() io.Writer {
	if r == nil || r.Out == nil {
		return io.Discard
	}
	return r.Out
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

// UsageLine renders one usage event.
func UsageLine(recorded usage.Recorded) string {
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
		parts = append(parts, "subject="+recorded.Subject.Name)
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
			parts = append(parts, "subject="+name)
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
