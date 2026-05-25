package anthropicmessages

import (
	"encoding/json"
	"fmt"
	"strings"

	adapterllm "github.com/fluxplane/fluxplane-core/adapters/llm"
	"github.com/fluxplane/fluxplane-core/core/agent"
	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/core/usage"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	runtimeusage "github.com/fluxplane/fluxplane-core/runtime/usage"
)

type streamState struct {
	responseState
	toolIDs   map[int]string
	toolNames map[int]string
	toolArgs  map[int]*strings.Builder
}

type responseState struct {
	model      string
	messageID  string
	blocks     []contentBlock
	usage      usageWire
	assembler  adapterllm.ToolCallAssembler
	operations []agent.OperationRequest
}

func newStreamState(tools []adapterllm.ToolSpec) *streamState {
	return &streamState{
		responseState: responseState{assembler: adapterllm.NewToolCallAssembler(tools)},
		toolIDs:       map[int]string{},
		toolNames:     map[int]string{},
		toolArgs:      map[int]*strings.Builder{},
	}
}

func (s *streamState) modelName() string {
	return s.model
}

func (s *streamState) applyFrame(frame sseFrame) ([]adapterllm.StreamEvent, error) {
	var envelope eventEnvelope
	if err := json.Unmarshal(frame.Data, &envelope); err != nil {
		return nil, err
	}
	eventType := envelope.Type
	if eventType == "" {
		eventType = frame.Event
	}
	switch eventType {
	case "message_start":
		var ev messageStartEvent
		if err := json.Unmarshal(frame.Data, &ev); err != nil {
			return nil, err
		}
		s.messageID = ev.Message.ID
		s.model = ev.Message.Model
		s.usage = mergeUsage(s.usage, ev.Message.Usage)
	case "content_block_start":
		var ev contentBlockStartEvent
		if err := json.Unmarshal(frame.Data, &ev); err != nil {
			return nil, err
		}
		return s.contentBlockStart(ev), nil
	case "content_block_delta":
		var ev contentBlockDeltaEvent
		if err := json.Unmarshal(frame.Data, &ev); err != nil {
			return nil, err
		}
		return s.contentBlockDelta(ev), nil
	case "content_block_stop":
		var ev contentBlockStopEvent
		if err := json.Unmarshal(frame.Data, &ev); err != nil {
			return nil, err
		}
		return s.contentBlockStop(ev)
	case "message_delta":
		var ev messageDeltaEvent
		if err := json.Unmarshal(frame.Data, &ev); err != nil {
			return nil, err
		}
		if ev.Usage != nil {
			s.usage = mergeUsage(s.usage, *ev.Usage)
		}
	case "message_stop", "ping":
		return nil, nil
	case "error":
		var ev errorEvent
		if err := json.Unmarshal(frame.Data, &ev); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("anthropic messages: stream error: %s: %s", ev.Error.Type, ev.Error.Message)
	}
	return nil, nil
}

func (s *streamState) contentBlockStart(ev contentBlockStartEvent) []adapterllm.StreamEvent {
	block := ev.ContentBlock
	ensureBlock(&s.blocks, ev.Index).Type = block.Type
	switch block.Type {
	case "text":
		s.blocks[ev.Index] = block
		if block.Text == "" {
			return nil
		}
		return []adapterllm.StreamEvent{{Kind: adapterllm.StreamContentDelta, Text: block.Text, Index: ev.Index}}
	case "thinking":
		s.blocks[ev.Index] = block
		if block.Thinking == "" {
			return nil
		}
		return []adapterllm.StreamEvent{{Kind: adapterllm.StreamThinkingDelta, Text: block.Thinking, Index: ev.Index, Sensitivity: policy.SensitivityInternal}}
	case "tool_use":
		s.blocks[ev.Index] = block
		s.toolIDs[ev.Index] = block.ID
		s.toolNames[ev.Index] = block.Name
		s.toolArgs[ev.Index] = &strings.Builder{}
		events := []adapterllm.StreamEvent{{Kind: adapterllm.StreamToolCallStart, Tool: tool.Name(block.Name), ToolCallID: block.ID, Index: ev.Index}}
		if len(block.Input) > 0 && string(block.Input) != "null" && string(block.Input) != "{}" {
			s.toolArgs[ev.Index].WriteString(string(block.Input))
			events = append(events, adapterllm.StreamEvent{Kind: adapterllm.StreamToolCallDelta, Tool: tool.Name(block.Name), ToolCallID: block.ID, Index: ev.Index, Arguments: string(block.Input)})
		}
		return events
	default:
		s.blocks[ev.Index] = block
		return nil
	}
}

func (s *streamState) contentBlockDelta(ev contentBlockDeltaEvent) []adapterllm.StreamEvent {
	block := ensureBlock(&s.blocks, ev.Index)
	switch ev.Delta.Type {
	case "text_delta":
		block.Type = "text"
		block.Text += ev.Delta.Text
		return []adapterllm.StreamEvent{{Kind: adapterllm.StreamContentDelta, Text: ev.Delta.Text, Index: ev.Index}}
	case "thinking_delta":
		block.Type = "thinking"
		block.Thinking += ev.Delta.Thinking
		return []adapterllm.StreamEvent{{Kind: adapterllm.StreamThinkingDelta, Text: ev.Delta.Thinking, Index: ev.Index, Sensitivity: policy.SensitivityInternal}}
	case "signature_delta":
		block.Type = "thinking"
		block.Signature = ev.Delta.Signature
		return nil
	case "input_json_delta":
		builder := s.toolArgs[ev.Index]
		if builder == nil {
			builder = &strings.Builder{}
			s.toolArgs[ev.Index] = builder
		}
		builder.WriteString(ev.Delta.PartialJSON)
		block.Type = "tool_use"
		if id := s.toolIDs[ev.Index]; id != "" {
			block.ID = id
		}
		if name := s.toolNames[ev.Index]; name != "" {
			block.Name = name
		}
		if builder.Len() > 0 {
			block.Input = json.RawMessage(builder.String())
		}
		return []adapterllm.StreamEvent{{
			Kind:       adapterllm.StreamToolCallDelta,
			Tool:       tool.Name(s.toolNames[ev.Index]),
			ToolCallID: s.toolIDs[ev.Index],
			Index:      ev.Index,
			Arguments:  ev.Delta.PartialJSON,
		}}
	default:
		return nil
	}
}

func (s *streamState) contentBlockStop(ev contentBlockStopEvent) ([]adapterllm.StreamEvent, error) {
	block := ensureBlock(&s.blocks, ev.Index)
	if block.Type != "tool_use" {
		return nil, nil
	}
	args := "{}"
	if len(block.Input) > 0 && strings.TrimSpace(string(block.Input)) != "" && strings.TrimSpace(string(block.Input)) != "null" {
		args = string(block.Input)
	}
	if builder := s.toolArgs[ev.Index]; builder != nil && builder.Len() > 0 {
		args = builder.String()
	}
	block.Type = "tool_use"
	if id := s.toolIDs[ev.Index]; id != "" {
		block.ID = id
	}
	if name := s.toolNames[ev.Index]; name != "" {
		block.Name = name
	}
	block.Input = json.RawMessage(args)
	delete(s.toolArgs, ev.Index)
	event := adapterllm.StreamEvent{
		Kind:       adapterllm.StreamToolCallDone,
		Tool:       tool.Name(block.Name),
		ToolCallID: block.ID,
		Index:      ev.Index,
		Final:      true,
	}
	reqs, err := s.assembler.Apply(adapterllm.StreamEvent{Kind: adapterllm.StreamToolCallDone, Tool: event.Tool, ToolCallID: event.ToolCallID, Index: event.Index, Arguments: args})
	if err != nil {
		return nil, err
	}
	s.operations = append(s.operations, reqs...)
	return []adapterllm.StreamEvent{event}, nil
}

func (s responseState) response(provider coreconversation.ProviderIdentity, prices []corellm.PricingSpec) (llmagent.Response, error) {
	return s.toRuntime(provider, prices)
}

func (s responseState) toRuntime(provider coreconversation.ProviderIdentity, prices []corellm.PricingSpec) (llmagent.Response, error) {
	operations := append([]agent.OperationRequest(nil), s.operations...)
	completed, err := s.assembler.Complete()
	if err != nil {
		return llmagent.Response{}, err
	}
	operations = append(operations, completed...)
	recordedUsage := usageFromAnthropic(s.usage, provider, s.messageID, prices)
	transcript := s.transcript(provider)
	if len(operations) > 0 {
		out := llmagent.OperationResponse(operations...)
		out.Usage = recordedUsage
		out.Transcript = transcript
		return out, nil
	}
	if text := strings.TrimSpace(blocksText(s.blocks)); text != "" {
		out := llmagent.MessageResponse(text)
		out.Usage = recordedUsage
		out.Transcript = transcript
		return out, nil
	}
	return llmagent.Response{Usage: recordedUsage, Transcript: transcript}, nil
}

func (s responseState) transcript(provider coreconversation.ProviderIdentity) coreconversation.Transcript {
	msg := message{Role: "assistant", Content: compactBlocks(s.blocks)}
	raw, _ := json.Marshal(msg)
	item := coreconversation.Item{
		Provider:  provider,
		Kind:      coreconversation.ItemOutput,
		Role:      "assistant",
		ID:        s.messageID,
		ToolCalls: toolCallsFromBlocks(s.blocks),
		Content:   blocksText(s.blocks),
		Native:    raw,
	}
	return coreconversation.Transcript{
		Provider: provider,
		Items:    []coreconversation.Item{item},
		Mode:     coreconversation.ProjectionFullReplay,
	}
}

func toolCallsFromBlocks(blocks []contentBlock) []coreconversation.ToolCallRef {
	var out []coreconversation.ToolCallRef
	for _, block := range blocks {
		if block.Type != "tool_use" || strings.TrimSpace(block.ID) == "" {
			continue
		}
		out = append(out, coreconversation.ToolCallRef{
			CallID: strings.TrimSpace(block.ID),
			Name:   block.Name,
			Type:   "tool_use",
			Input:  json.RawMessage(block.Input),
		})
	}
	return out
}

func usageFromAnthropic(raw usageWire, provider coreconversation.ProviderIdentity, id string, prices []corellm.PricingSpec) []usage.Recorded {
	if raw.InputTokens == 0 && raw.CacheReadInputTokens == 0 && raw.CacheCreationInputTokens == 0 && raw.OutputTokens == 0 && raw.ReasoningOutputTokens == 0 {
		return nil
	}
	recorded := usage.Recorded{
		Source: provider.Provider,
		Subject: usage.Subject{
			Kind:     usage.SubjectLLM,
			Provider: provider.Provider,
			Name:     provider.Model,
			ID:       id,
			Attributes: map[string]string{
				"api": provider.API,
			},
		},
	}
	add := func(metric usage.MetricName, quantity int64, direction usage.Direction, dimensions map[string]string) {
		if quantity <= 0 {
			return
		}
		recorded.Measurements = append(recorded.Measurements, usage.Measurement{Metric: metric, Quantity: float64(quantity), Unit: usage.UnitToken, Direction: direction, Dimensions: dimensions})
	}
	add(usage.MetricLLMInputTokens, raw.InputTokens, usage.DirectionInput, nil)
	add(usage.MetricLLMCachedTokens, raw.CacheReadInputTokens, usage.DirectionCached, nil)
	add(usage.MetricLLMCacheWriteTokens, raw.CacheCreationInputTokens, usage.DirectionWrite, nil)
	add(usage.MetricLLMOutputTokens, raw.OutputTokens, usage.DirectionOutput, nil)
	add(usage.MetricLLMReasoningTokens, raw.ReasoningOutputTokens, usage.DirectionOutput, nil)
	total := raw.InputTokens + raw.CacheReadInputTokens + raw.CacheCreationInputTokens + raw.OutputTokens
	add(usage.MetricLLMTotalTokens, total, "", nil)
	return runtimeusage.EnrichCosts([]usage.Recorded{recorded}, prices)
}

func ensureBlock(blocks *[]contentBlock, index int) *contentBlock {
	for len(*blocks) <= index {
		*blocks = append(*blocks, contentBlock{})
	}
	return &(*blocks)[index]
}

func blocksText(blocks []contentBlock) string {
	var out strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String()
}

func compactBlocks(blocks []contentBlock) []contentBlock {
	out := make([]contentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "" {
			out = append(out, block)
		}
	}
	return out
}

func mergeUsage(a, b usageWire) usageWire {
	if b.InputTokens != 0 {
		a.InputTokens = b.InputTokens
	}
	if b.CacheCreationInputTokens != 0 {
		a.CacheCreationInputTokens = b.CacheCreationInputTokens
	}
	if b.CacheReadInputTokens != 0 {
		a.CacheReadInputTokens = b.CacheReadInputTokens
	}
	if b.OutputTokens != 0 {
		a.OutputTokens = b.OutputTokens
	}
	if b.ReasoningOutputTokens != 0 {
		a.ReasoningOutputTokens = b.ReasoningOutputTokens
	}
	return a
}

type messageStartEvent struct {
	Type    string          `json:"type"`
	Message messageResponse `json:"message"`
}

type contentBlockStartEvent struct {
	Type         string       `json:"type"`
	Index        int          `json:"index"`
	ContentBlock contentBlock `json:"content_block"`
}

type contentBlockDeltaEvent struct {
	Type  string    `json:"type"`
	Index int       `json:"index"`
	Delta deltaWire `json:"delta"`
}

type deltaWire struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
}

type contentBlockStopEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type messageDeltaEvent struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta"`
	Usage *usageWire `json:"usage,omitempty"`
}

type errorEvent struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
