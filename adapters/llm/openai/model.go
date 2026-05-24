package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	adapterllm "github.com/fluxplane/engine/adapters/llm"
	"github.com/fluxplane/engine/core/agent"
	coreconversation "github.com/fluxplane/engine/core/conversation"
	corellm "github.com/fluxplane/engine/core/llm"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/tool"
	"github.com/fluxplane/engine/core/usage"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	llmagent "github.com/fluxplane/engine/runtime/agent/llmagent"
	"github.com/fluxplane/engine/runtime/httptransport"
	runtimeusage "github.com/fluxplane/engine/runtime/usage"
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

	// ProviderName identifies the provider in transcripts and usage records.
	// Empty defaults to openai.
	ProviderName string

	// APIName identifies the provider API in transcripts. Empty defaults to
	// openai.responses.
	APIName string

	// Runtime controls shared OpenAI Responses-compatible behavior.
	Runtime ResponsesRuntimeConfig

	// Pricing enriches emitted LLM usage records with estimated cost.
	Pricing []corellm.PricingSpec

	// ReasoningEffort sets Responses reasoning.effort when the provider/model
	// supports it.
	ReasoningEffort string

	// ReasoningSummary sets Responses reasoning.summary. Empty defaults to auto.
	ReasoningSummary string

	// Store controls OpenAI response storage. The adapter sends this explicitly
	// and defaults to false.
	Store bool

	// ParallelToolCalls enables provider-level parallel function calls. The
	// runtime already accepts multiple operation requests in one agent response.
	ParallelToolCalls bool

	// Redactor controls which provider stream details may be exposed through
	// runtime stream events.
	Redactor adapterllm.Redactor

	// HTTPClient overrides the runtime default outbound HTTP client.
	HTTPClient *http.Client

	// RequestOptions appends low-level OpenAI SDK options such as provider
	// middleware. Prefer higher-level config when adding new behavior.
	RequestOptions []option.RequestOption
}

// Model implements runtime/agent/llmagent.Model using OpenAI Responses.
type Model struct {
	client            openai.Client
	model             string
	provider          string
	api               string
	runtime           ResponsesRuntimeConfig
	pricing           []corellm.PricingSpec
	reasoningEffort   string
	reasoningSummary  string
	store             bool
	parallelToolCalls bool
	redactor          adapterllm.Redactor
}

// New returns an OpenAI Responses API model adapter.
func New(cfg Config) (*Model, error) {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = httptransport.CloneDefaultHTTPClient()
	}
	opts := make([]option.RequestOption, 0, 3)
	opts = append(opts, option.WithHTTPClient(httpClient))
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	opts = append(opts, cfg.RequestOptions...)
	opts = append(opts, option.WithMiddleware(httpUsageMiddleware()))
	runtime := cfg.Runtime.withDefaults()
	store := cfg.Store
	if runtime.Continuation != ResponsesContinuationReplay {
		store = true
	}
	return &Model{
		client:            openai.NewClient(opts...),
		model:             strings.TrimSpace(cfg.Model),
		provider:          normalizeProvider(cfg.ProviderName),
		api:               firstNonEmpty(strings.TrimSpace(cfg.APIName), "openai.responses"),
		runtime:           runtime,
		pricing:           append([]corellm.PricingSpec(nil), cfg.Pricing...),
		reasoningEffort:   strings.TrimSpace(cfg.ReasoningEffort),
		reasoningSummary:  firstNonEmpty(strings.TrimSpace(cfg.ReasoningSummary), string(shared.ReasoningSummaryAuto)),
		store:             store,
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
	httpUsage := newHTTPUsageCollector()
	ctx = contextWithHTTPUsage(ctx, httpUsage)
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
	out, err := responseFromOpenAI(*resp, tools, m.providerIdentity(m.modelName(req)), m.store, m.pricing)
	if err != nil {
		return attachOpenAIPartial(err, sentItems, m.providerIdentity(m.modelName(req)), httpUsage)
	}
	out.Transcript.Items = append(sentItems, out.Transcript.Items...)
	out.Usage = append(out.Usage, httpUsageRecord(m.providerIdentity(m.modelName(req)), httpUsage)...)
	return out, nil
}

// Stream calls the OpenAI Responses streaming API and emits provider-neutral
// deltas while still returning the final normalized response.
func (m *Model) Stream(ctx context.Context, req llmagent.Request, emit llmagent.StreamFunc) (llmagent.Response, error) {
	if m == nil {
		return llmagent.Response{}, errors.New("openai: model is nil")
	}
	httpUsage := newHTTPUsageCollector()
	ctx = contextWithHTTPUsage(ctx, httpUsage)
	params, tools, sentItems, err := m.responseParams(req)
	if err != nil {
		return llmagent.Response{}, err
	}
	return m.streamWithParams(ctx, req, emit, params, tools, sentItems, httpUsage)
}

func (m *Model) streamWithParams(ctx context.Context, req llmagent.Request, emit llmagent.StreamFunc, params responses.ResponseNewParams, tools []adapterllm.ToolSpec, sentItems []coreconversation.Item, httpUsage *httpUsageCollector) (llmagent.Response, error) {
	stream := m.client.Responses.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	streamState := openAIStreamState{
		toolNames:    map[int]tool.Name{},
		outputPhases: map[int]string{},
	}
	streamAssembler := adapterllm.NewToolCallAssembler(tools)
	var streamedOperations []agent.OperationRequest
	var streamedOutputItems []responses.ResponseOutputItemUnion
	var final responses.Response
	for stream.Next() {
		evt := stream.Current()
		if evt.Type == "response.completed" {
			final = evt.AsResponseCompleted().Response
		}
		if m.runtime.Output == ResponsesOutputStreamItems && evt.Type == "response.output_item.done" {
			streamedOutputItems = append(streamedOutputItems, evt.AsResponseOutputItemDone().Item)
		}
		for _, normalized := range m.streamEvents(evt, &streamState) {
			if normalized.Kind != adapterllm.StreamToolCallDelta {
				// Responses done events carry the complete argument body; deltas
				// are only for live display.
				completed, err := streamAssembler.Apply(normalized)
				if err != nil {
					provider := m.providerIdentity(m.modelName(req))
					items := append([]coreconversation.Item(nil), sentItems...)
					if item, ok := streamToolCallTranscriptItem(provider, normalized); ok {
						items = append(items, item)
					}
					resp := llmagent.Response{
						Usage: append([]usage.Recorded(nil), httpUsageRecord(provider, httpUsage)...),
						Transcript: coreconversation.Transcript{
							Provider: provider,
							Items:    items,
							Mode:     coreconversation.ProjectionFullReplay,
						},
					}
					return resp, llmagent.PartialError(resp, err)
				}
				streamedOperations = append(streamedOperations, completed...)
			}
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
	provider := m.providerIdentity(m.modelName(req))
	m.emitBufferedStreamContent(final, &streamState, emit)
	source := responseForOutputMode(final, streamedOutputItems, m.runtime.Output)
	out, err := responseFromOpenAI(source, tools, provider, m.store, m.pricing)
	if err != nil {
		return attachOpenAIPartial(err, sentItems, provider, httpUsage)
	}
	usedStreamedOperations := len(out.Operations) == 0 && len(streamedOperations) > 0
	out = applyStreamedOperationsFallback(out, streamedOperations)
	if usedStreamedOperations {
		out.Transcript.Items = append(out.Transcript.Items, streamedOperationTranscriptItems(provider, streamedOperations)...)
	}
	out.Transcript.Items = append(sentItems, out.Transcript.Items...)
	out = applyStreamedContentFallback(out, provider, &streamState)
	out.Usage = append(out.Usage, httpUsageRecord(provider, httpUsage)...)
	return out, nil
}

func streamToolCallTranscriptItem(provider coreconversation.ProviderIdentity, evt adapterllm.StreamEvent) (coreconversation.Item, bool) {
	if evt.Kind != adapterllm.StreamToolCallDone && evt.Kind != adapterllm.StreamToolCallStart {
		return coreconversation.Item{}, false
	}
	callType := strings.TrimSpace(evt.CallType)
	if callType == "" {
		callType = "function_call"
	}
	return coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemOutput,
		CallID:   strings.TrimSpace(evt.ToolCallID),
		Name:     string(evt.Tool),
		ToolCalls: []coreconversation.ToolCallRef{{
			CallID: strings.TrimSpace(evt.ToolCallID),
			Name:   string(evt.Tool),
			Type:   callType,
			Input:  evt.Arguments,
		}},
		Metadata: map[string]string{"provider_call_type": callType},
	}, true
}

func attachOpenAIPartial(err error, sentItems []coreconversation.Item, provider coreconversation.ProviderIdentity, httpUsage *httpUsageCollector) (llmagent.Response, error) {
	resp, ok := llmagent.PartialResponse(err)
	if !ok {
		return llmagent.Response{}, err
	}
	resp.Transcript.Items = append(append([]coreconversation.Item(nil), sentItems...), resp.Transcript.Items...)
	resp.Usage = append(resp.Usage, httpUsageRecord(provider, httpUsage)...)
	return resp, llmagent.PartialError(resp, err)
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
		Reasoning: shared.ReasoningParam{
			Summary: shared.ReasoningSummary(m.reasoningSummary),
		},
	}
	if m.reasoningEffort != "" {
		params.Reasoning.Effort = shared.ReasoningEffort(m.reasoningEffort)
	}
	if m.runtime.Cache == ResponsesCacheMax {
		params.PromptCacheKey = openai.String(promptCacheKey(m.provider, model, req))
		params.PromptCacheRetention = responses.ResponseNewParamsPromptCacheRetention24h
		params.Include = append(params.Include, responses.ResponseIncludableReasoningEncryptedContent)
	}
	var sentItems []coreconversation.Item
	if req.Transcript != nil && !req.Transcript.Empty() {
		provider := m.providerIdentity(model)
		transcript := *req.Transcript
		transcript.Provider = provider
		usePreviousResponse := m.runtime.Continuation != ResponsesContinuationReplay && req.Transcript.Continuation != nil && req.Transcript.Continuation.SupportsPreviousResponseID()
		inputItems, _, err := inputItemsFromTranscript(transcript.Provider, transcript.Items)
		if err != nil {
			return responses.ResponseNewParams{}, nil, nil, err
		}
		params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: responses.ResponseInputParam(inputItems)}
		recordItems := transcript.NewItems
		if len(recordItems) == 0 && transcript.Mode == "" {
			recordItems = transcript.Items
		}
		_, sentItems, err = inputItemsFromTranscript(transcript.Provider, recordItems)
		if err != nil {
			return responses.ResponseNewParams{}, nil, nil, err
		}
		if usePreviousResponse {
			params.PreviousResponseID = openai.String(req.Transcript.Continuation.ResponseID)
		}
	} else {
		prompt := promptFromRequest(req)
		if strings.TrimSpace(prompt) == "" {
			prompt = "Continue."
		}
		inputItems := responses.ResponseInputParam{responses.ResponseInputItemParamOfInputMessage(
			responses.ResponseInputMessageContentListParam{responses.ResponseInputContentParamOfInputText(prompt)},
			"user",
		)}
		params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: inputItems}
	}
	if instructions := firstNonEmpty(req.Driver.Instructions, req.Agent.System); instructions != "" {
		params.Instructions = openai.String(instructions)
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

// ProviderIdentity reports the provider identity this adapter will use for req.
func (m *Model) ProviderIdentity(req llmagent.Request) coreconversation.ProviderIdentity {
	if m == nil {
		return coreconversation.ProviderIdentity{}
	}
	return m.providerIdentity(m.modelName(req))
}

func (m *Model) providerIdentity(model string) coreconversation.ProviderIdentity {
	api := m.api
	if api == "" {
		api = "openai.responses"
	}
	return coreconversation.ProviderIdentity{
		Provider: m.provider,
		API:      api,
		Family:   "responses",
		Model:    model,
	}
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

func inputItemsFromTranscript(provider coreconversation.ProviderIdentity, items []coreconversation.Item) ([]responses.ResponseInputItemUnionParam, []coreconversation.Item, error) {
	out := make([]responses.ResponseInputItemUnionParam, 0, len(items))
	recorded := make([]coreconversation.Item, 0, len(items))
	for i, item := range items {
		paramItem, recordedItem, err := inputItemFromTranscriptItem(provider, item)
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
	case coreconversation.ItemOutput:
		if calls := item.ToolCallRefs(); len(calls) > 0 {
			if len(calls) != 1 {
				return responses.ResponseInputItemUnionParam{}, coreconversation.Item{}, fmt.Errorf("output item has %d tool calls; openai replay expects one per item", len(calls))
			}
			paramItem, recorded := toolCallInputItem(provider, item, calls[0])
			return paramItem, recorded, nil
		}
		role := strings.TrimSpace(item.Role)
		if role == "" {
			role = "assistant"
		}
		if role != "assistant" {
			return responses.ResponseInputItemUnionParam{}, coreconversation.Item{}, fmt.Errorf("unsupported output role %q without native payload", role)
		}
		paramItem := responses.ResponseInputItemParamOfMessage(transcriptContentString(item.Content), responses.EasyInputMessageRoleAssistant)
		return paramItem, itemWithNative(item, paramItem), nil
	case coreconversation.ItemToolResult:
		if strings.TrimSpace(item.CallID) == "" {
			return responses.ResponseInputItemUnionParam{}, coreconversation.Item{}, errors.New("tool result call_id is empty")
		}
		var paramItem responses.ResponseInputItemUnionParam
		if item.Metadata["provider_call_type"] == "custom_tool_call" {
			paramItem = responses.ResponseInputItemParamOfCustomToolCallOutput(item.CallID, transcriptContentString(item.Content))
		} else {
			paramItem = responses.ResponseInputItemParamOfFunctionCallOutput(item.CallID, transcriptContentString(item.Content))
		}
		return paramItem, itemWithNative(item, paramItem), nil
	default:
		return responses.ResponseInputItemUnionParam{}, coreconversation.Item{}, fmt.Errorf("unsupported transcript item kind %q without native payload", item.Kind)
	}
}

func toolCallInputItem(provider coreconversation.ProviderIdentity, item coreconversation.Item, call coreconversation.ToolCallRef) (responses.ResponseInputItemUnionParam, coreconversation.Item) {
	if item.Provider.Provider == "" {
		item.Provider = provider
	}
	callID := strings.TrimSpace(call.CallID)
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = strings.TrimSpace(item.Name)
	}
	callType := strings.TrimSpace(call.Type)
	if callType == "" && item.Metadata != nil {
		callType = strings.TrimSpace(item.Metadata["provider_call_type"])
	}
	if callType == "" {
		callType = "function_call"
	}
	arguments := transcriptContentString(call.Input)
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	if item.Metadata == nil {
		item.Metadata = map[string]string{}
	}
	item.Metadata["provider_call_type"] = callType
	item.CallID = callID
	item.Name = name
	item.ToolCalls = []coreconversation.ToolCallRef{{
		CallID: callID,
		Name:   name,
		Type:   callType,
		Input:  call.Input,
	}}
	var paramItem responses.ResponseInputItemUnionParam
	switch callType {
	case "custom_tool_call":
		paramItem = responses.ResponseInputItemParamOfCustomToolCall(callID, arguments, name)
	default:
		callType = "function_call"
		item.Metadata["provider_call_type"] = callType
		item.ToolCalls[0].Type = callType
		paramItem = responses.ResponseInputItemParamOfFunctionCall(arguments, callID, name)
	}
	return paramItem, itemWithNative(item, paramItem)
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

func responseFromOpenAI(resp responses.Response, tools []adapterllm.ToolSpec, provider coreconversation.ProviderIdentity, store bool, prices []corellm.PricingSpec) (llmagent.Response, error) {
	recordedUsage := usageFromOpenAI(resp, provider, prices)
	transcript := responseTranscript(resp, provider, store)
	assembler := adapterllm.NewToolCallAssembler(tools)
	var operations []agent.OperationRequest
	for i, item := range resp.Output {
		switch item.Type {
		case "function_call":
			call := item.AsFunctionCall()
			reqs, err := assembler.Apply(adapterllm.StreamEvent{
				Kind:       adapterllm.StreamToolCallDone,
				Tool:       tool.Name(call.Name),
				ToolCallID: callID(call, i),
				CallType:   "function_call",
				Index:      i,
				Arguments:  call.Arguments,
			})
			if err != nil {
				return llmagent.Response{}, llmagent.PartialError(llmagent.Response{Usage: recordedUsage, Transcript: transcript}, err)
			}
			operations = append(operations, reqs...)
		case "custom_tool_call":
			call := item.AsCustomToolCall()
			reqs, err := assembler.Apply(adapterllm.StreamEvent{
				Kind:       adapterllm.StreamToolCallDone,
				Tool:       tool.Name(call.Name),
				ToolCallID: firstNonEmpty(call.CallID, call.ID),
				CallType:   "custom_tool_call",
				Index:      i,
				Arguments:  call.Input,
			})
			if err != nil {
				return llmagent.Response{}, llmagent.PartialError(llmagent.Response{Usage: recordedUsage, Transcript: transcript}, err)
			}
			operations = append(operations, reqs...)
		default:
			continue
		}
	}
	if len(operations) > 0 {
		out := llmagent.OperationResponse(operations...)
		out.Usage = recordedUsage
		out.Transcript = transcript
		return out, nil
	}
	if text := strings.TrimSpace(responseFinalText(resp)); text != "" {
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

func responseForOutputMode(final responses.Response, streamed []responses.ResponseOutputItemUnion, mode ResponsesOutputMode) responses.Response {
	if mode != ResponsesOutputStreamItems || len(streamed) == 0 {
		return final
	}
	final.Output = append([]responses.ResponseOutputItemUnion(nil), streamed...)
	return final
}

func responseTranscript(resp responses.Response, provider coreconversation.ProviderIdentity, store bool) coreconversation.Transcript {
	items := make([]coreconversation.Item, 0, len(resp.Output))
	for i, output := range resp.Output {
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
			item.Content = outputMessageText(output)
			item.Phase = outputPhase(output)
		}
		switch output.Type {
		case "function_call":
			call := output.AsFunctionCall()
			id := callID(call, i)
			item.CallID = id
			item.Name = call.Name
			item.Metadata = map[string]string{"provider_call_type": output.Type}
			item.ToolCalls = []coreconversation.ToolCallRef{{
				CallID: id,
				Name:   call.Name,
				Type:   output.Type,
				Input:  call.Arguments,
			}}
		case "custom_tool_call":
			call := output.AsCustomToolCall()
			id := firstNonEmpty(call.CallID, call.ID)
			item.CallID = id
			item.Name = call.Name
			item.Metadata = map[string]string{"provider_call_type": output.Type}
			item.ToolCalls = []coreconversation.ToolCallRef{{
				CallID: id,
				Name:   call.Name,
				Type:   output.Type,
				Input:  call.Input,
			}}
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

func outputPhase(output responses.ResponseOutputItemUnion) string {
	if output.Type != "message" {
		return ""
	}
	return string(output.AsMessage().Phase)
}

func outputMessageText(output responses.ResponseOutputItemUnion) string {
	if output.Type != "message" {
		return ""
	}
	var out strings.Builder
	for _, part := range output.AsMessage().Content {
		if part.Type == "output_text" && part.Text != "" {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}

func responseFinalText(resp responses.Response) string {
	hasFinalPhase := false
	var final strings.Builder
	var unphased strings.Builder
	for _, output := range resp.Output {
		if output.Type != "message" {
			continue
		}
		text := outputMessageText(output)
		switch outputPhase(output) {
		case "final_answer":
			hasFinalPhase = true
			final.WriteString(text)
		case "":
			unphased.WriteString(text)
		}
	}
	if hasFinalPhase {
		return final.String()
	}
	return unphased.String()
}

func openAIProviderIdentity(model string) coreconversation.ProviderIdentity {
	return (&Model{provider: "openai", api: "openai.responses"}).providerIdentity(model)
}

type openAIStreamState struct {
	toolNames          map[int]tool.Name
	outputPhases       map[int]string
	completedToolCalls map[int]bool
	contentDeltas      map[int]*strings.Builder
	unphasedDeltas     map[int]*strings.Builder
	flushedUnphased    map[int]bool
}

func (s *openAIStreamState) ensure() {
	if s == nil {
		return
	}
	if s.toolNames == nil {
		s.toolNames = map[int]tool.Name{}
	}
	if s.outputPhases == nil {
		s.outputPhases = map[int]string{}
	}
	if s.completedToolCalls == nil {
		s.completedToolCalls = map[int]bool{}
	}
	if s.contentDeltas == nil {
		s.contentDeltas = map[int]*strings.Builder{}
	}
	if s.unphasedDeltas == nil {
		s.unphasedDeltas = map[int]*strings.Builder{}
	}
	if s.flushedUnphased == nil {
		s.flushedUnphased = map[int]bool{}
	}
}

func (s *openAIStreamState) phase(index int) string {
	if s == nil || s.outputPhases == nil {
		return ""
	}
	return s.outputPhases[index]
}

func (s *openAIStreamState) setPhase(index int, output responses.ResponseOutputItemUnion) {
	if s == nil || output.Type != "message" {
		return
	}
	s.ensure()
	if phase := outputPhase(output); phase != "" {
		s.outputPhases[index] = phase
	}
}

func (s *openAIStreamState) appendContent(index int, text string) {
	if s == nil || text == "" {
		return
	}
	s.ensure()
	builder := s.contentDeltas[index]
	if builder == nil {
		builder = &strings.Builder{}
		s.contentDeltas[index] = builder
	}
	builder.WriteString(text)
}

func (s *openAIStreamState) appendUnphased(index int, text string) {
	if s == nil || text == "" {
		return
	}
	s.ensure()
	builder := s.unphasedDeltas[index]
	if builder == nil {
		builder = &strings.Builder{}
		s.unphasedDeltas[index] = builder
	}
	builder.WriteString(text)
}

func (s *openAIStreamState) flushUnphased(index int, kind adapterllm.StreamKind) []adapterllm.StreamEvent {
	if s == nil {
		return nil
	}
	s.ensure()
	if s.flushedUnphased[index] {
		return nil
	}
	builder := s.unphasedDeltas[index]
	if builder == nil || builder.Len() == 0 {
		return nil
	}
	text := builder.String()
	s.flushedUnphased[index] = true
	if kind == adapterllm.StreamContentDelta {
		s.appendContent(index, text)
		return []adapterllm.StreamEvent{{Kind: adapterllm.StreamContentDelta, Text: text, Index: index}}
	}
	return []adapterllm.StreamEvent{{
		Kind:        adapterllm.StreamThinkingDelta,
		Text:        text,
		Index:       index,
		Sensitivity: "internal",
	}}
}

func (s *openAIStreamState) flushAllUnphased(kind adapterllm.StreamKind) []adapterllm.StreamEvent {
	if s == nil || len(s.unphasedDeltas) == 0 {
		return nil
	}
	var maxIndex int
	for index := range s.unphasedDeltas {
		if index > maxIndex {
			maxIndex = index
		}
	}
	var out []adapterllm.StreamEvent
	for index := 0; index <= maxIndex; index++ {
		out = append(out, s.flushUnphased(index, kind)...)
	}
	return out
}

func (s *openAIStreamState) finalContent() string {
	if s == nil || len(s.contentDeltas) == 0 {
		return ""
	}
	var maxIndex int
	for index := range s.contentDeltas {
		if index > maxIndex {
			maxIndex = index
		}
	}
	var out strings.Builder
	for index := 0; index <= maxIndex; index++ {
		if builder := s.contentDeltas[index]; builder != nil {
			out.WriteString(builder.String())
		}
	}
	return out.String()
}

func applyStreamedContentFallback(out llmagent.Response, provider coreconversation.ProviderIdentity, state *openAIStreamState) llmagent.Response {
	if out.Message != nil || len(out.Operations) > 0 || out.Completion != nil {
		return out
	}
	text := state.finalContent()
	if strings.TrimSpace(text) == "" {
		return out
	}
	out.Message = &agent.Message{Content: text}
	out.Transcript.Provider = provider
	item := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemOutput,
		Role:     "assistant",
		Content:  text,
	}
	paramItem := responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleAssistant)
	out.Transcript.Items = append(out.Transcript.Items, itemWithNative(item, paramItem))
	return out
}

func applyStreamedOperationsFallback(out llmagent.Response, streamed []agent.OperationRequest) llmagent.Response {
	if len(out.Operations) > 0 || len(streamed) == 0 {
		return out
	}
	out.Operations = append([]agent.OperationRequest(nil), streamed...)
	return out
}

func streamedOperationTranscriptItems(provider coreconversation.ProviderIdentity, streamed []agent.OperationRequest) []coreconversation.Item {
	out := make([]coreconversation.Item, 0, len(streamed))
	for _, request := range streamed {
		if strings.TrimSpace(request.ProviderCallID) == "" {
			continue
		}
		callType := request.ProviderCallType
		if callType == "" {
			callType = "function_call"
		}
		name := request.Operation.String()
		arguments := transcriptContentString(request.Input)
		var paramItem responses.ResponseInputItemUnionParam
		switch callType {
		case "custom_tool_call":
			paramItem = responses.ResponseInputItemParamOfCustomToolCall(request.ProviderCallID, arguments, name)
		default:
			callType = "function_call"
			paramItem = responses.ResponseInputItemParamOfFunctionCall(arguments, request.ProviderCallID, name)
		}
		out = append(out, itemWithNative(coreconversation.Item{
			Provider: provider,
			Kind:     coreconversation.ItemOutput,
			CallID:   request.ProviderCallID,
			Name:     name,
			ToolCalls: []coreconversation.ToolCallRef{{
				CallID: request.ProviderCallID,
				Name:   name,
				Type:   callType,
				Input:  request.Input,
			}},
			Metadata: map[string]string{"provider_call_type": callType},
		}, paramItem))
	}
	return out
}

func (m *Model) streamEvents(evt responses.ResponseStreamEventUnion, state *openAIStreamState) []adapterllm.StreamEvent {
	if state != nil {
		state.ensure()
	}
	switch evt.Type {
	case "response.output_text.delta":
		delta := evt.AsResponseOutputTextDelta()
		switch state.phase(int(delta.OutputIndex)) {
		case "commentary":
			return []adapterllm.StreamEvent{{
				Kind:        adapterllm.StreamThinkingDelta,
				Text:        delta.Delta,
				Index:       int(delta.OutputIndex),
				Sensitivity: "internal",
			}}
		case "final_answer":
			state.appendContent(int(delta.OutputIndex), delta.Delta)
			return []adapterllm.StreamEvent{{
				Kind:  adapterllm.StreamContentDelta,
				Text:  delta.Delta,
				Index: int(delta.OutputIndex),
			}}
		default:
			state.appendUnphased(int(delta.OutputIndex), delta.Delta)
			return nil
		}
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
		state.setPhase(int(added.OutputIndex), added.Item)
		if added.Item.Type != "function_call" && added.Item.Type != "custom_tool_call" {
			return nil
		}
		name := tool.Name(added.Item.Name)
		state.toolNames[int(added.OutputIndex)] = name
		return []adapterllm.StreamEvent{{
			Kind:       adapterllm.StreamToolCallStart,
			Tool:       name,
			ToolCallID: firstNonEmpty(added.Item.CallID, added.Item.ID),
			CallType:   added.Item.Type,
			Index:      int(added.OutputIndex),
		}}
	case "response.output_item.done":
		done := evt.AsResponseOutputItemDone()
		state.setPhase(int(done.OutputIndex), done.Item)
		if done.Item.Type == "message" {
			switch state.phase(int(done.OutputIndex)) {
			case "commentary":
				return state.flushUnphased(int(done.OutputIndex), adapterllm.StreamThinkingDelta)
			case "final_answer":
				return state.flushUnphased(int(done.OutputIndex), adapterllm.StreamContentDelta)
			default:
				return nil
			}
		}
		if done.Item.Type != "function_call" && done.Item.Type != "custom_tool_call" || state.completedToolCalls[int(done.OutputIndex)] {
			return nil
		}
		name := tool.Name(done.Item.Name)
		if name == "" {
			name = state.toolNames[int(done.OutputIndex)]
		}
		arguments := done.Item.Arguments.OfString
		if done.Item.Type == "custom_tool_call" {
			arguments = done.Item.Input
		}
		state.completedToolCalls[int(done.OutputIndex)] = true
		events := state.flushAllUnphased(adapterllm.StreamThinkingDelta)
		events = append(events, adapterllm.StreamEvent{
			Kind:       adapterllm.StreamToolCallDone,
			Tool:       name,
			ToolCallID: firstNonEmpty(done.Item.CallID, done.Item.ID),
			CallType:   done.Item.Type,
			Index:      int(done.OutputIndex),
			Arguments:  arguments,
			Final:      true,
		})
		return events
	case "response.function_call_arguments.delta":
		delta := evt.AsResponseFunctionCallArgumentsDelta()
		return []adapterllm.StreamEvent{{
			Kind:       adapterllm.StreamToolCallDelta,
			Tool:       state.toolNames[int(delta.OutputIndex)],
			ToolCallID: delta.ItemID,
			CallType:   "function_call",
			Index:      int(delta.OutputIndex),
			Arguments:  delta.Delta,
		}}
	case "response.function_call_arguments.done":
		done := evt.AsResponseFunctionCallArgumentsDone()
		if state.completedToolCalls[int(done.OutputIndex)] {
			return nil
		}
		name := tool.Name(done.Name)
		if name == "" {
			name = state.toolNames[int(done.OutputIndex)]
		}
		state.completedToolCalls[int(done.OutputIndex)] = true
		events := state.flushAllUnphased(adapterllm.StreamThinkingDelta)
		events = append(events, adapterllm.StreamEvent{
			Kind:       adapterllm.StreamToolCallDone,
			Tool:       name,
			ToolCallID: done.ItemID,
			CallType:   "function_call",
			Index:      int(done.OutputIndex),
			Arguments:  done.Arguments,
			Final:      true,
		})
		return events
	case "response.custom_tool_call_input.delta":
		delta := evt.AsResponseCustomToolCallInputDelta()
		return []adapterllm.StreamEvent{{
			Kind:       adapterllm.StreamToolCallDelta,
			Tool:       state.toolNames[int(delta.OutputIndex)],
			ToolCallID: delta.ItemID,
			CallType:   "custom_tool_call",
			Index:      int(delta.OutputIndex),
			Arguments:  delta.Delta,
		}}
	case "response.custom_tool_call_input.done":
		done := evt.AsResponseCustomToolCallInputDone()
		if state.completedToolCalls[int(done.OutputIndex)] {
			return nil
		}
		name := state.toolNames[int(done.OutputIndex)]
		state.completedToolCalls[int(done.OutputIndex)] = true
		events := state.flushAllUnphased(adapterllm.StreamThinkingDelta)
		events = append(events, adapterllm.StreamEvent{
			Kind:       adapterllm.StreamToolCallDone,
			Tool:       name,
			ToolCallID: done.ItemID,
			CallType:   "custom_tool_call",
			Index:      int(done.OutputIndex),
			Arguments:  done.Input,
			Final:      true,
		})
		return events
	default:
		return nil
	}
}

func (m *Model) emitBufferedStreamContent(final responses.Response, state *openAIStreamState, emit llmagent.StreamFunc) {
	if state == nil || emit == nil {
		return
	}
	kind := adapterllm.StreamContentDelta
	for _, item := range final.Output {
		if item.Type == "function_call" {
			kind = adapterllm.StreamThinkingDelta
			break
		}
	}
	for _, normalized := range state.flushAllUnphased(kind) {
		if runtimeEvent, ok := m.redactor.ToRuntimeStream(normalized); ok {
			emit(runtimeEvent)
		}
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
				Name:     "gpt-5.5",
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

func usageFromOpenAI(resp responses.Response, provider coreconversation.ProviderIdentity, prices []corellm.PricingSpec) []usage.Recorded {
	if resp.Usage.InputTokens == 0 &&
		resp.Usage.InputTokensDetails.CachedTokens == 0 &&
		resp.Usage.OutputTokens == 0 &&
		resp.Usage.OutputTokensDetails.ReasoningTokens == 0 &&
		resp.Usage.TotalTokens == 0 {
		return nil
	}
	recorded := usage.Recorded{
		Source: "adapters/llm/openai",
		Subject: usage.Subject{
			Kind:     usage.SubjectLLM,
			Provider: provider.Provider,
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
	return runtimeusage.EnrichCosts([]usage.Recorded{recorded}, prices)
}

func promptCacheKey(provider, model string, req llmagent.Request) string {
	parts := []string{"fluxplane", normalizeProvider(provider), strings.TrimSpace(model)}
	if req.Agent.Name != "" {
		parts = append(parts, string(req.Agent.Name))
	}
	return strings.Join(parts, ":")
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
