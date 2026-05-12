package openaiadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	"github.com/fluxplane/agentruntime/core/agent"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/core/usage"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

var (
	// ErrModelMissing is returned when neither the adapter nor the agent request
	// provides an OpenAI model name.
	ErrModelMissing = errors.New("openai: model is empty")
)

// Config configures an OpenAI Responses API backed model.
type Config struct {
	// Model overrides the model declared by an agent spec. Leave empty to use
	// req.Driver.Model.Model or req.Agent.Inference.Model.
	Model string

	// APIKey overrides OPENAI_API_KEY when set.
	APIKey string

	// BaseURL overrides OPENAI_BASE_URL when set. Only use trusted endpoints.
	BaseURL string

	// Store controls OpenAI response storage. The adapter sends this explicitly
	// and defaults to false.
	Store bool

	// ParallelToolCalls enables provider-level parallel function calls. The
	// runtime already accepts multiple operation requests in one agent response.
	ParallelToolCalls bool

	// Redactor controls which provider stream details may be exposed through
	// runtime stream events.
	Redactor adapterllm.Redactor
}

// Model implements runtime/agent/llmagent.Model using OpenAI Responses.
type Model struct {
	client            openai.Client
	model             string
	store             bool
	parallelToolCalls bool
	redactor          adapterllm.Redactor
}

// New returns an OpenAI Responses API model adapter.
func New(cfg Config) (*Model, error) {
	opts := make([]option.RequestOption, 0, 2)
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return &Model{
		client:            openai.NewClient(opts...),
		model:             strings.TrimSpace(cfg.Model),
		store:             cfg.Store,
		parallelToolCalls: cfg.ParallelToolCalls,
		redactor:          cfg.Redactor,
	}, nil
}

// Complete calls the OpenAI Responses API and converts the result into one
// provider-neutral agent response.
func (m *Model) Complete(ctx context.Context, req llmagent.Request) (llmagent.Response, error) {
	if m == nil {
		return llmagent.Response{}, errors.New("openai: model is nil")
	}
	params, tools, sentItems, err := m.responseParams(req)
	if err != nil {
		return llmagent.Response{}, err
	}
	resp, err := m.client.Responses.New(ctx, params)
	if err != nil {
		return llmagent.Response{}, err
	}
	if resp == nil {
		return llmagent.Response{}, errors.New("openai: nil response")
	}
	out, err := responseFromOpenAI(*resp, tools, openAIProviderIdentity(m.modelName(req)), m.store)
	if err != nil {
		return llmagent.Response{}, err
	}
	out.Transcript.Items = append(sentItems, out.Transcript.Items...)
	return out, nil
}

// Stream calls the OpenAI Responses streaming API and emits provider-neutral
// deltas while still returning the final normalized response.
func (m *Model) Stream(ctx context.Context, req llmagent.Request, emit llmagent.StreamFunc) (llmagent.Response, error) {
	if m == nil {
		return llmagent.Response{}, errors.New("openai: model is nil")
	}
	params, tools, sentItems, err := m.responseParams(req)
	if err != nil {
		return llmagent.Response{}, err
	}
	stream := m.client.Responses.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	toolNames := map[int]tool.Name{}
	var final responses.Response
	for stream.Next() {
		evt := stream.Current()
		if evt.Type == "response.completed" {
			final = evt.AsResponseCompleted().Response
		}
		for _, normalized := range m.streamEvents(evt, toolNames) {
			if runtimeEvent, ok := m.redactor.ToRuntimeStream(normalized); ok && emit != nil {
				emit(runtimeEvent)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return llmagent.Response{}, err
	}
	if final.ID == "" {
		return llmagent.Response{}, errors.New("openai: stream completed without final response")
	}
	out, err := responseFromOpenAI(final, tools, openAIProviderIdentity(m.modelName(req)), m.store)
	if err != nil {
		return llmagent.Response{}, err
	}
	out.Transcript.Items = append(sentItems, out.Transcript.Items...)
	return out, nil
}

func (m *Model) responseParams(req llmagent.Request) (responses.ResponseNewParams, []adapterllm.ToolSpec, []coreconversation.Item, error) {
	model := m.modelName(req)
	if model == "" {
		return responses.ResponseNewParams{}, nil, nil, ErrModelMissing
	}
	params := responses.ResponseNewParams{
		Model:             shared.ResponsesModel(model),
		Store:             openai.Bool(m.store),
		ParallelToolCalls: openai.Bool(m.parallelToolCalls),
	}
	var sentItems []coreconversation.Item
	if req.Transcript != nil && !req.Transcript.Empty() {
		inputItems, items, err := inputItemsFromTranscript(*req.Transcript)
		if err != nil {
			return responses.ResponseNewParams{}, nil, nil, err
		}
		params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: responses.ResponseInputParam(inputItems)}
		sentItems = items
		if req.Transcript.Continuation != nil && req.Transcript.Continuation.SupportsPreviousResponseID() {
			params.PreviousResponseID = openai.String(req.Transcript.Continuation.ResponseID)
		}
	} else {
		prompt := promptFromRequest(req)
		if strings.TrimSpace(prompt) == "" {
			prompt = "Continue."
		}
		params.Input = responses.ResponseNewParamsInputUnion{OfString: openai.String(prompt)}
	}
	if req.Driver.Instructions != "" {
		params.Instructions = openai.String(req.Driver.Instructions)
	}
	if req.Driver.Inference.MaxOutputTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(req.Driver.Inference.MaxOutputTokens))
	}
	if req.Driver.Inference.Temperature > 0 {
		params.Temperature = openai.Float(req.Driver.Inference.Temperature)
	}
	tools, err := adapterllm.ToolsFromCore(req.Tools)
	if err != nil {
		return responses.ResponseNewParams{}, nil, nil, err
	}
	params.Tools, err = toolParams(tools)
	if err != nil {
		return responses.ResponseNewParams{}, nil, nil, err
	}
	return params, tools, sentItems, nil
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

func toolParams(tools []adapterllm.ToolSpec) ([]responses.ToolUnionParam, error) {
	out := make([]responses.ToolUnionParam, 0, len(tools))
	for _, spec := range tools {
		params, err := schemaParams(spec.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("openai: tool %q schema: %w", spec.Name, err)
		}
		toolParam := responses.ToolParamOfFunction(string(spec.Name), params, false)
		if spec.Description != "" {
			toolParam.OfFunction.Description = openai.String(spec.Description)
		}
		out = append(out, toolParam)
	}
	return out, nil
}

func schemaParams(schema operation.Schema) (map[string]any, error) {
	if len(schema.Data) == 0 {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		}, nil
	}
	var params map[string]any
	if err := json.Unmarshal(schema.Data, &params); err != nil {
		return nil, err
	}
	if params == nil {
		params = map[string]any{"type": "object"}
	}
	return params, nil
}

func inputItemsFromTranscript(transcript coreconversation.Transcript) ([]responses.ResponseInputItemUnionParam, []coreconversation.Item, error) {
	out := make([]responses.ResponseInputItemUnionParam, 0, len(transcript.Items))
	recorded := make([]coreconversation.Item, 0, len(transcript.Items))
	for i, item := range transcript.Items {
		paramItem, recordedItem, err := inputItemFromTranscriptItem(transcript.Provider, item)
		if err != nil {
			return nil, nil, fmt.Errorf("openai: transcript item %d: %w", i, err)
		}
		out = append(out, paramItem)
		recorded = append(recorded, recordedItem)
	}
	return out, recorded, nil
}

func inputItemFromTranscriptItem(provider coreconversation.ProviderIdentity, item coreconversation.Item) (responses.ResponseInputItemUnionParam, coreconversation.Item, error) {
	if len(item.Native) > 0 {
		return param.Override[responses.ResponseInputItemUnionParam](json.RawMessage(item.Native)), item, nil
	}
	if item.Provider.Provider == "" {
		item.Provider = provider
	}
	switch item.Kind {
	case coreconversation.ItemInput:
		role := strings.TrimSpace(item.Role)
		if role == "" {
			role = "user"
		}
		if role != "user" && role != "system" && role != "developer" {
			return responses.ResponseInputItemUnionParam{}, coreconversation.Item{}, fmt.Errorf("unsupported input role %q without native payload", role)
		}
		paramItem := responses.ResponseInputItemParamOfInputMessage(
			responses.ResponseInputMessageContentListParam{responses.ResponseInputContentParamOfInputText(transcriptContentString(item.Content))},
			role,
		)
		return paramItem, itemWithNative(item, paramItem), nil
	case coreconversation.ItemToolResult:
		if strings.TrimSpace(item.CallID) == "" {
			return responses.ResponseInputItemUnionParam{}, coreconversation.Item{}, errors.New("tool result call_id is empty")
		}
		paramItem := responses.ResponseInputItemParamOfFunctionCallOutput(item.CallID, transcriptContentString(item.Content))
		return paramItem, itemWithNative(item, paramItem), nil
	default:
		return responses.ResponseInputItemUnionParam{}, coreconversation.Item{}, fmt.Errorf("unsupported transcript item kind %q without native payload", item.Kind)
	}
}

func itemWithNative(item coreconversation.Item, paramItem responses.ResponseInputItemUnionParam) coreconversation.Item {
	if len(item.Native) > 0 {
		return item
	}
	if raw, err := json.Marshal(paramItem); err == nil {
		item.Native = raw
	}
	return item
}

func transcriptContentString(content any) string {
	switch typed := content.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func responseFromOpenAI(resp responses.Response, tools []adapterllm.ToolSpec, provider coreconversation.ProviderIdentity, store bool) (llmagent.Response, error) {
	recordedUsage := usageFromOpenAI(resp)
	transcript := responseTranscript(resp, provider, store)
	assembler := adapterllm.NewToolCallAssembler(tools)
	var operations []agent.OperationRequest
	for i, item := range resp.Output {
		if item.Type != "function_call" {
			continue
		}
		call := item.AsFunctionCall()
		reqs, err := assembler.Apply(adapterllm.StreamEvent{
			Kind:       adapterllm.StreamToolCallDone,
			Tool:       tool.Name(call.Name),
			ToolCallID: callID(call, i),
			Index:      i,
			Arguments:  call.Arguments,
		})
		if err != nil {
			return llmagent.Response{}, err
		}
		operations = append(operations, reqs...)
	}
	if len(operations) > 0 {
		out := llmagent.OperationResponse(operations...)
		out.Usage = recordedUsage
		out.Transcript = transcript
		return out, nil
	}
	if text := strings.TrimSpace(resp.OutputText()); text != "" {
		out := llmagent.MessageResponse(text)
		out.Usage = recordedUsage
		out.Transcript = transcript
		return out, nil
	}
	if resp.Error.Message != "" {
		return llmagent.Response{}, fmt.Errorf("openai: %s: %s", resp.Error.Code, resp.Error.Message)
	}
	return llmagent.Response{Usage: recordedUsage, Transcript: transcript}, nil
}

func responseTranscript(resp responses.Response, provider coreconversation.ProviderIdentity, store bool) coreconversation.Transcript {
	items := make([]coreconversation.Item, 0, len(resp.Output))
	for _, output := range resp.Output {
		item := coreconversation.Item{
			Provider: provider,
			Kind:     transcriptKindFromOutputType(output.Type),
			Role:     outputRole(output),
			ID:       output.ID,
			CallID:   output.CallID,
			Name:     output.Name,
			Native:   json.RawMessage(output.RawJSON()),
		}
		if output.Type == "message" {
			item.Content = resp.OutputText()
		}
		items = append(items, item)
	}
	out := coreconversation.Transcript{
		Provider: provider,
		Items:    items,
		Mode:     coreconversation.ProjectionFullReplay,
	}
	if store && resp.ID != "" {
		out.Continuation = &coreconversation.ContinuationHandle{
			Provider:   provider,
			Mode:       coreconversation.ContinuationPreviousResponseID,
			Transport:  coreconversation.TransportHTTPSSE,
			ResponseID: resp.ID,
		}
		out.Mode = coreconversation.ProjectionNativeContinuation
	}
	return out
}

func transcriptKindFromOutputType(outputType string) coreconversation.ItemKind {
	switch outputType {
	case "reasoning":
		return coreconversation.ItemReasoning
	default:
		return coreconversation.ItemOutput
	}
}

func outputRole(output responses.ResponseOutputItemUnion) string {
	if output.Type == "message" {
		return "assistant"
	}
	return ""
}

func openAIProviderIdentity(model string) coreconversation.ProviderIdentity {
	return coreconversation.ProviderIdentity{
		Provider: "openai",
		API:      "openai.responses",
		Family:   "responses",
		Model:    model,
	}
}

func (m *Model) streamEvents(evt responses.ResponseStreamEventUnion, toolNames map[int]tool.Name) []adapterllm.StreamEvent {
	switch evt.Type {
	case "response.output_text.delta":
		return []adapterllm.StreamEvent{{
			Kind:  adapterllm.StreamContentDelta,
			Text:  evt.AsResponseOutputTextDelta().Delta,
			Index: int(evt.OutputIndex),
		}}
	case "response.reasoning_text.delta":
		return []adapterllm.StreamEvent{{
			Kind:        adapterllm.StreamThinkingDelta,
			Text:        evt.AsResponseReasoningTextDelta().Delta,
			Index:       int(evt.OutputIndex),
			Sensitivity: "restricted",
		}}
	case "response.reasoning_summary_text.delta":
		return []adapterllm.StreamEvent{{
			Kind:        adapterllm.StreamThinkingDelta,
			Text:        evt.AsResponseReasoningSummaryTextDelta().Delta,
			Index:       int(evt.OutputIndex),
			Sensitivity: "internal",
		}}
	case "response.output_item.added":
		added := evt.AsResponseOutputItemAdded()
		if added.Item.Type != "function_call" {
			return nil
		}
		name := tool.Name(added.Item.Name)
		toolNames[int(added.OutputIndex)] = name
		return []adapterllm.StreamEvent{{
			Kind:       adapterllm.StreamToolCallStart,
			Tool:       name,
			ToolCallID: firstNonEmpty(added.Item.CallID, added.Item.ID),
			Index:      int(added.OutputIndex),
		}}
	case "response.function_call_arguments.delta":
		delta := evt.AsResponseFunctionCallArgumentsDelta()
		return []adapterllm.StreamEvent{{
			Kind:       adapterllm.StreamToolCallDelta,
			Tool:       toolNames[int(delta.OutputIndex)],
			ToolCallID: delta.ItemID,
			Index:      int(delta.OutputIndex),
			Arguments:  delta.Delta,
		}}
	case "response.function_call_arguments.done":
		done := evt.AsResponseFunctionCallArgumentsDone()
		name := tool.Name(done.Name)
		if name == "" {
			name = toolNames[int(done.OutputIndex)]
		}
		return []adapterllm.StreamEvent{{
			Kind:       adapterllm.StreamToolCallDone,
			Tool:       name,
			ToolCallID: done.ItemID,
			Index:      int(done.OutputIndex),
			Arguments:  done.Arguments,
			Final:      true,
		}}
	default:
		return nil
	}
}

func callID(call responses.ResponseFunctionToolCall, index int) string {
	if call.CallID != "" {
		return call.CallID
	}
	if call.ID != "" {
		return call.ID
	}
	return fmt.Sprintf("index:%d", index)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ProviderSpec returns the static OpenAI provider capabilities known to this
// adapter. Pricing is intentionally supplied through catalogs/config so it can
// be updated without changing provider transport code.
func ProviderSpec() corellm.ProviderSpec {
	return corellm.ProviderSpec{
		Name:        "openai",
		DisplayName: "OpenAI",
		Models: []corellm.ModelSpec{{
			Ref: corellm.ModelRef{
				Provider: "openai",
				Name:     "gpt-4.1-mini",
			},
			InputModalities:  []corellm.Modality{corellm.ModalityText, corellm.ModalityImage},
			OutputModalities: []corellm.Modality{corellm.ModalityText},
			Capabilities: corellm.CapabilitySet{
				corellm.CapabilityToolCalling,
				corellm.CapabilityParallelTools,
				corellm.CapabilityStreaming,
				corellm.CapabilityPromptCaching,
				corellm.CapabilityStructuredJSON,
				corellm.CapabilityVision,
			},
		}},
	}
}

func usageFromOpenAI(resp responses.Response) []usage.Recorded {
	if resp.Usage.InputTokens == 0 &&
		resp.Usage.InputTokensDetails.CachedTokens == 0 &&
		resp.Usage.OutputTokens == 0 &&
		resp.Usage.OutputTokensDetails.ReasoningTokens == 0 &&
		resp.Usage.TotalTokens == 0 {
		return nil
	}
	recorded := usage.Recorded{
		Source: "adapters/openai",
		Subject: usage.Subject{
			Kind:     usage.SubjectLLM,
			Provider: "openai",
			Name:     string(resp.Model),
			ID:       resp.ID,
		},
	}
	addMeasurement := func(metric usage.MetricName, quantity int64, direction usage.Direction) {
		if quantity <= 0 {
			return
		}
		recorded.Measurements = append(recorded.Measurements, usage.Measurement{
			Metric:    metric,
			Quantity:  float64(quantity),
			Unit:      usage.UnitToken,
			Direction: direction,
		})
	}
	addMeasurement(usage.MetricLLMInputTokens, resp.Usage.InputTokens, usage.DirectionInput)
	addMeasurement(usage.MetricLLMCachedTokens, resp.Usage.InputTokensDetails.CachedTokens, usage.DirectionCached)
	addMeasurement(usage.MetricLLMOutputTokens, resp.Usage.OutputTokens, usage.DirectionOutput)
	addMeasurement(usage.MetricLLMReasoningTokens, resp.Usage.OutputTokensDetails.ReasoningTokens, usage.DirectionOutput)
	addMeasurement(usage.MetricLLMTotalTokens, resp.Usage.TotalTokens, "")
	if recorded.Empty() {
		return nil
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
