// Package anthropicmessages implements Anthropic-compatible Messages API
// adapters for direct Anthropic-style HTTP/SSE providers.
package anthropicmessages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/core/usage"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/fluxplane/agentruntime/runtime/httptransport"
)

const (
	DefaultVersion         = "2023-06-01"
	DefaultMaxOutputTokens = 4096
)

var ErrModelMissing = errors.New("anthropic messages: model is empty")

// RequestProcessor mutates the provider wire request before it is encoded.
type RequestProcessor func(context.Context, *MessageRequest) error

// HeaderFunc mutates the HTTP request after default Anthropic headers are set.
type HeaderFunc func(context.Context, *http.Request, MessageRequest) error

// Config configures a generic Anthropic-compatible Messages model.
type Config struct {
	Model             string
	APIKey            string
	BaseURL           string
	ProviderName      string
	APIName           string
	Version           string
	AuthHeader        string
	AuthScheme        string
	Headers           map[string]string
	MaxOutputTokens   int
	PromptCache       bool
	Pricing           []corellm.PricingSpec
	Thinking          string
	ReasoningEffort   string
	Redactor          adapterllm.Redactor
	HTTPClient        *http.Client
	Query             map[string]string
	RequestProcessors []RequestProcessor
	HeaderFuncs       []HeaderFunc
}

// Model implements the llmagent model ports over /v1/messages.
type Model struct {
	client            *http.Client
	model             string
	baseURL           string
	provider          string
	api               string
	version           string
	apiKey            string
	authHeader        string
	authScheme        string
	headers           map[string]string
	maxOutputTokens   int
	promptCache       bool
	pricing           []corellm.PricingSpec
	thinking          string
	reasoningEffort   string
	redactor          adapterllm.Redactor
	query             map[string]string
	requestProcessors []RequestProcessor
	headerFuncs       []HeaderFunc
}

// New returns a generic Anthropic Messages model.
func New(cfg Config) (*Model, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("anthropic messages: base URL is empty")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = httptransport.CloneDefaultHTTPClient()
	}
	maxOutput := cfg.MaxOutputTokens
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputTokens
	}
	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = DefaultVersion
	}
	authHeader := strings.TrimSpace(cfg.AuthHeader)
	if authHeader == "" {
		authHeader = "x-api-key"
	}
	headers := map[string]string{}
	for key, value := range cfg.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			headers[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	query := map[string]string{}
	for key, value := range cfg.Query {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			query[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return &Model{
		client:            client,
		model:             strings.TrimSpace(cfg.Model),
		baseURL:           baseURL,
		provider:          firstNonEmpty(strings.TrimSpace(cfg.ProviderName), "anthropic"),
		api:               firstNonEmpty(strings.TrimSpace(cfg.APIName), "anthropic.messages"),
		version:           version,
		apiKey:            strings.TrimSpace(cfg.APIKey),
		authHeader:        authHeader,
		authScheme:        strings.TrimSpace(cfg.AuthScheme),
		headers:           headers,
		maxOutputTokens:   maxOutput,
		promptCache:       cfg.PromptCache,
		pricing:           append([]corellm.PricingSpec(nil), cfg.Pricing...),
		thinking:          strings.TrimSpace(cfg.Thinking),
		reasoningEffort:   strings.TrimSpace(cfg.ReasoningEffort),
		redactor:          cfg.Redactor,
		query:             query,
		requestProcessors: append([]RequestProcessor(nil), cfg.RequestProcessors...),
		headerFuncs:       append([]HeaderFunc(nil), cfg.HeaderFuncs...),
	}, nil
}

// Complete calls /v1/messages without streaming.
func (m *Model) Complete(ctx context.Context, req llmagent.Request) (llmagent.Response, error) {
	if m == nil {
		return llmagent.Response{}, errors.New("anthropic messages: model is nil")
	}
	wire, tools, sentItems, err := m.messageRequest(req, false)
	if err != nil {
		return llmagent.Response{}, err
	}
	collector := &httpUsageCollector{}
	body, statusModel, err := m.doJSON(ctx, wire, collector)
	if err != nil {
		return llmagent.Response{}, err
	}
	var resp messageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return llmagent.Response{}, fmt.Errorf("%s: decode response: %w", m.provider, err)
	}
	if resp.Model == "" {
		resp.Model = statusModel
	}
	out, err := m.responseFromMessage(resp, tools, sentItems, collector)
	if err != nil {
		if partial, ok := llmagent.PartialResponse(err); ok {
			return partial, err
		}
		return llmagent.Response{}, err
	}
	return out, nil
}

// Stream calls /v1/messages with stream=true and emits provider-neutral deltas.
func (m *Model) Stream(ctx context.Context, req llmagent.Request, emit llmagent.StreamFunc) (llmagent.Response, error) {
	if m == nil {
		return llmagent.Response{}, errors.New("anthropic messages: model is nil")
	}
	wire, tools, sentItems, err := m.messageRequest(req, true)
	if err != nil {
		return llmagent.Response{}, err
	}
	collector := &httpUsageCollector{}
	stream, err := m.doStream(ctx, wire, collector)
	if err != nil {
		return llmagent.Response{}, err
	}
	defer func() { _ = stream.Close() }()
	state := newStreamState(tools)
	decoder := newSSEDecoder(stream)
	for {
		frame, err := decoder.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return llmagent.Response{}, fmt.Errorf("%s: stream decode: %w", m.provider, err)
		}
		events, err := state.applyFrame(frame)
		if err != nil {
			return anthropicPartialResponse(state, sentItems, m.providerIdentity(state.modelName()), m.pricing, collector, err)
		}
		for _, evt := range events {
			if runtimeEvent, ok := m.redactor.ToRuntimeStream(evt); ok && emit != nil {
				emit(runtimeEvent)
			}
		}
	}
	out, err := state.response(m.providerIdentity(state.modelName()), m.pricing)
	if err != nil {
		return llmagent.Response{}, err
	}
	out.Transcript.Items = append(sentItems, out.Transcript.Items...)
	out.Usage = append(out.Usage, httpUsageRecord(m.providerIdentity(state.modelName()), collector)...)
	return out, nil
}

func (m *Model) messageRequest(req llmagent.Request, stream bool) (messageRequest, []adapterllm.ToolSpec, []coreconversation.Item, error) {
	model := m.modelName(req)
	if model == "" {
		return messageRequest{}, nil, nil, ErrModelMissing
	}
	maxTokens := m.maxOutputTokens
	if req.Driver.Inference.MaxOutputTokens > 0 {
		maxTokens = req.Driver.Inference.MaxOutputTokens
	} else if req.Agent.Inference.MaxOutputTokens > 0 {
		maxTokens = req.Agent.Inference.MaxOutputTokens
	}
	wire := messageRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Stream:    stream,
	}
	if req.Driver.Inference.Temperature > 0 {
		wire.Temperature = &req.Driver.Inference.Temperature
	} else if req.Agent.Inference.Temperature > 0 {
		wire.Temperature = &req.Agent.Inference.Temperature
	}
	thinking := normalizeThinking(firstNonEmpty(m.thinking, req.Agent.Inference.Thinking))
	effort := firstNonEmpty(m.reasoningEffort, req.Driver.Inference.ReasoningEffort, req.Agent.Inference.ReasoningEffort)
	if thinking == "on" {
		wire.Thinking = &thinkingConfig{Type: "enabled", BudgetTokens: 1024}
		wire.Effort = effort
		if wire.MaxTokens <= 1024 {
			wire.MaxTokens = 1025
		}
	}
	var sentItems []coreconversation.Item
	if req.Transcript != nil && !req.Transcript.Empty() {
		provider := m.providerIdentity(model)
		messages, system, recorded, err := messagesFromTranscript(provider, req.Transcript.Items)
		if err != nil {
			return messageRequest{}, nil, nil, err
		}
		wire.Messages = messages
		wire.System = system
		_, _, sentItems, err = messagesFromTranscript(provider, req.Transcript.NewItems)
		if err != nil {
			return messageRequest{}, nil, nil, err
		}
		_ = recorded
	} else {
		prompt := promptFromRequest(req)
		if strings.TrimSpace(prompt) == "" {
			prompt = "Continue."
		}
		wire.Messages = []message{{Role: "user", Content: []contentBlock{{Type: "text", Text: prompt}}}}
	}
	if instructions := strings.TrimSpace(firstNonEmpty(req.Driver.Instructions, req.Agent.System)); instructions != "" {
		wire.System = append([]contentBlock{{Type: "text", Text: instructions}}, wire.System...)
	}
	tools, err := adapterllm.ToolsFromCore(req.Tools)
	if err != nil {
		return messageRequest{}, nil, nil, err
	}
	wire.Tools, err = toolDefinitions(tools)
	if err != nil {
		return messageRequest{}, nil, nil, err
	}
	if m.promptCache {
		applyPromptCache(&wire)
	}
	return wire, tools, sentItems, nil
}

// ProviderIdentity reports the provider identity this adapter will use.
func (m *Model) ProviderIdentity(req llmagent.Request) coreconversation.ProviderIdentity {
	if m == nil {
		return coreconversation.ProviderIdentity{}
	}
	return m.providerIdentity(m.modelName(req))
}

func (m *Model) providerIdentity(model string) coreconversation.ProviderIdentity {
	return coreconversation.ProviderIdentity{
		Provider: m.provider,
		API:      m.api,
		Family:   "anthropic.messages",
		Model:    model,
	}
}

func (m *Model) modelName(req llmagent.Request) string {
	if m.model != "" {
		return m.model
	}
	if req.Driver.Model.Model != "" {
		return strings.TrimSpace(req.Driver.Model.Model)
	}
	return strings.TrimSpace(req.Agent.Inference.Model)
}

func (m *Model) doJSON(ctx context.Context, wire messageRequest, collector *httpUsageCollector) ([]byte, string, error) {
	wire.Stream = false
	if err := m.processRequest(ctx, &wire); err != nil {
		return nil, "", err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, "", err
	}
	resp, err := m.do(ctx, wire, body, collector)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	collector.downloadBytes.Add(int64(len(data)))
	if resp.StatusCode >= 400 {
		return nil, "", m.httpError(resp, data)
	}
	return data, wire.Model, nil
}

func (m *Model) doStream(ctx context.Context, wire messageRequest, collector *httpUsageCollector) (io.ReadCloser, error) {
	wire.Stream = true
	if err := m.processRequest(ctx, &wire); err != nil {
		return nil, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	resp, err := m.do(ctx, wire, body, collector)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
		collector.downloadBytes.Add(int64(len(data)))
		return nil, m.httpError(resp, data)
	}
	return countingReadCloser{ReadCloser: resp.Body, add: func(n int64) { collector.downloadBytes.Add(n) }}, nil
}

func (m *Model) processRequest(ctx context.Context, wire *messageRequest) error {
	for _, processor := range m.requestProcessors {
		if processor == nil {
			continue
		}
		if err := processor(ctx, (*MessageRequest)(wire)); err != nil {
			return err
		}
	}
	return nil
}

func (m *Model) do(ctx context.Context, wire messageRequest, body []byte, collector *httpUsageCollector) (*http.Response, error) {
	url := strings.TrimRight(m.baseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if len(m.query) > 0 {
		q := req.URL.Query()
		for key, value := range m.query {
			q.Set(key, value)
		}
		req.URL.RawQuery = q.Encode()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("anthropic-version", m.version)
	for key, value := range m.headers {
		req.Header.Set(key, value)
	}
	mergeAnthropicBetaHeader(req.Header, wire.Betas)
	if m.apiKey != "" && m.authHeader != "" {
		value := m.apiKey
		if m.authScheme != "" {
			value = m.authScheme + " " + m.apiKey
		}
		req.Header.Set(m.authHeader, value)
	}
	for _, fn := range m.headerFuncs {
		if fn == nil {
			continue
		}
		if err := fn(req.Context(), req, wire); err != nil {
			return nil, err
		}
	}
	if collector != nil {
		collector.uploadBytes.Add(int64(len(body)))
	}
	return m.client.Do(req)
}

func (m *Model) httpError(resp *http.Response, body []byte) error {
	text := strings.TrimSpace(string(body))
	if text == "" {
		text = "<empty response body>"
	}
	return fmt.Errorf("%s: HTTP %s: %s", m.provider, resp.Status, text)
}

func (m *Model) responseFromMessage(resp messageResponse, tools []adapterllm.ToolSpec, sentItems []coreconversation.Item, collector *httpUsageCollector) (llmagent.Response, error) {
	provider := m.providerIdentity(resp.Model)
	state := responseState{
		model:     resp.Model,
		messageID: resp.ID,
		blocks:    append([]contentBlock(nil), resp.Content...),
		usage:     resp.Usage,
		assembler: adapterllm.NewToolCallAssembler(tools),
	}
	for index, block := range resp.Content {
		if block.Type != "tool_use" {
			continue
		}
		args := "{}"
		if len(block.Input) > 0 && string(block.Input) != "null" {
			args = string(block.Input)
		}
		reqs, err := state.assembler.Apply(adapterllm.StreamEvent{Kind: adapterllm.StreamToolCallDone, Tool: tool.Name(block.Name), ToolCallID: block.ID, Index: index, Arguments: args})
		if err != nil {
			resp := llmagent.Response{
				Usage:      usageFromAnthropic(state.usage, provider, state.messageID, m.pricing),
				Transcript: state.transcript(provider),
			}
			resp.Transcript.Items = append(sentItems, resp.Transcript.Items...)
			resp.Usage = append(resp.Usage, httpUsageRecord(provider, collector)...)
			return llmagent.Response{}, llmagent.PartialError(resp, err)
		}
		state.operations = append(state.operations, reqs...)
	}
	out, err := state.toRuntime(provider, m.pricing)
	if err != nil {
		return llmagent.Response{}, err
	}
	out.Transcript.Items = append(sentItems, out.Transcript.Items...)
	out.Usage = append(out.Usage, httpUsageRecord(provider, collector)...)
	return out, nil
}

func anthropicPartialResponse(state *streamState, sentItems []coreconversation.Item, provider coreconversation.ProviderIdentity, prices []corellm.PricingSpec, collector *httpUsageCollector, err error) (llmagent.Response, error) {
	if provider.Model == "" {
		provider.Model = state.modelName()
	}
	resp := llmagent.Response{
		Usage:      usageFromAnthropic(state.usage, provider, state.messageID, prices),
		Transcript: state.transcript(provider),
	}
	resp.Transcript.Items = append(sentItems, resp.Transcript.Items...)
	resp.Usage = append(resp.Usage, httpUsageRecord(provider, collector)...)
	return resp, llmagent.PartialError(resp, err)
}

type httpUsageCollector struct {
	uploadBytes   atomic.Int64
	downloadBytes atomic.Int64
}

type countingReadCloser struct {
	io.ReadCloser
	add func(int64)
}

func (r countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.add != nil {
		r.add(int64(n))
	}
	return n, err
}

func httpUsageRecord(provider coreconversation.ProviderIdentity, collector *httpUsageCollector) []usage.Recorded {
	if collector == nil {
		return nil
	}
	upload := collector.uploadBytes.Load()
	download := collector.downloadBytes.Load()
	if upload == 0 && download == 0 {
		return nil
	}
	recorded := usage.Recorded{
		Source: provider.Provider + ".http",
		Subject: usage.Subject{
			Kind:     usage.SubjectNetwork,
			Provider: provider.Provider,
			Name:     provider.Model,
			Attributes: map[string]string{
				"api": provider.API,
			},
		},
	}
	if upload > 0 {
		recorded.Measurements = append(recorded.Measurements, usage.Measurement{Metric: usage.MetricNetworkBytes, Quantity: float64(upload), Unit: usage.UnitByte, Direction: usage.DirectionUpload})
	}
	if download > 0 {
		recorded.Measurements = append(recorded.Measurements, usage.Measurement{Metric: usage.MetricNetworkBytes, Quantity: float64(download), Unit: usage.UnitByte, Direction: usage.DirectionDownload})
	}
	return []usage.Recorded{recorded}
}

func promptFromRequest(req llmagent.Request) string {
	var b strings.Builder
	writeSection(&b, "Agent", string(req.Agent.Name))
	writeSection(&b, "Goal", req.Goal)
	if req.Objective.Role != "" || req.Objective.Instructions != "" || req.Objective.Success != "" {
		writeSection(&b, "Objective role", req.Objective.Role)
		writeSection(&b, "Objective instructions", req.Objective.Instructions)
		writeSection(&b, "Objective success", req.Objective.Success)
	}
	for _, obs := range req.Observations {
		if obs.Content != nil {
			writeJSONSection(&b, "Observation", obs.Content)
		}
	}
	for _, block := range req.Context {
		if strings.TrimSpace(block.Content) != "" {
			writeJSONSection(&b, "Context", block.Content)
		}
	}
	return strings.TrimSpace(b.String())
}

func writeSection(b *strings.Builder, title, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%s:\n%s\n\n", title, value)
}

func writeJSONSection(b *strings.Builder, title string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeSection(b, title, fmt.Sprint(value))
		return
	}
	writeSection(b, title, string(data))
}
