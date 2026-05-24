package anthropicmessages

import (
	"encoding/json"
	"fmt"
	"strings"

	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
)

func messagesFromTranscript(provider coreconversation.ProviderIdentity, items []coreconversation.Item) ([]message, []contentBlock, []coreconversation.Item, error) {
	var messages []message
	var system []contentBlock
	recorded := make([]coreconversation.Item, 0, len(items))
	consumedToolResults := map[string]bool{}
	for i := 0; i < len(items); i++ {
		item := items[i]
		if item.Kind == coreconversation.ItemToolResult && consumedToolResults[strings.TrimSpace(item.CallID)] {
			continue
		}
		msgs, sys, recordedItem, err := messageFromTranscriptItem(provider, item)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("anthropic messages: transcript item %d: %w", i, err)
		}
		messages = append(messages, msgs...)
		system = append(system, sys...)
		recorded = append(recorded, recordedItem)
		if len(msgs) == 0 {
			continue
		}
		for _, msg := range msgs {
			uses := toolUseBlocks(msg)
			if len(uses) == 0 {
				continue
			}
			var resultBlocks []contentBlock
			for i+1 < len(items) && items[i+1].Kind == coreconversation.ItemToolResult {
				next := items[i+1]
				if !toolUseIDKnown(uses, next.CallID) {
					break
				}
				block, recordedResult, err := toolResultBlock(provider, next)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("anthropic messages: transcript item %d: %w", i+1, err)
				}
				resultBlocks = append(resultBlocks, block)
				recorded = append(recorded, recordedResult)
				consumedToolResults[strings.TrimSpace(next.CallID)] = true
				i++
			}
			if len(resultBlocks) > 0 {
				messages = append(messages, message{Role: "user", Content: resultBlocks})
			}
		}
	}
	return messages, system, recorded, nil
}

func messageFromTranscriptItem(provider coreconversation.ProviderIdentity, item coreconversation.Item) ([]message, []contentBlock, coreconversation.Item, error) {
	if len(item.Native) > 0 {
		var msg message
		if err := json.Unmarshal(item.Native, &msg); err == nil && msg.Role != "" {
			return []message{msg}, nil, item, nil
		}
		var blocks []contentBlock
		if err := json.Unmarshal(item.Native, &blocks); err == nil && len(blocks) > 0 {
			return nil, blocks, item, nil
		}
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
		block := contentBlock{Type: "text", Text: transcriptContentString(item.Content)}
		if role == "system" || role == "developer" {
			native, _ := json.Marshal([]contentBlock{block})
			item.Native = native
			return nil, []contentBlock{block}, item, nil
		}
		if role != "user" && role != "assistant" {
			return nil, nil, coreconversation.Item{}, fmt.Errorf("unsupported role %q", role)
		}
		msg := message{Role: role, Content: []contentBlock{block}}
		return []message{msg}, nil, itemWithNativeMessage(item, msg), nil
	case coreconversation.ItemOutput:
		if blocks := toolUseBlocksFromItem(item); len(blocks) > 0 {
			msg := message{Role: "assistant", Content: blocks}
			return []message{msg}, nil, itemWithNativeMessage(item, msg), nil
		}
		msg := message{Role: "assistant", Content: []contentBlock{{Type: "text", Text: transcriptContentString(item.Content)}}}
		return []message{msg}, nil, itemWithNativeMessage(item, msg), nil
	case coreconversation.ItemToolResult:
		block, recorded, err := toolResultBlock(provider, item)
		if err != nil {
			return nil, nil, coreconversation.Item{}, err
		}
		msg := message{Role: "user", Content: []contentBlock{block}}
		return []message{msg}, nil, recorded, nil
	default:
		return nil, nil, coreconversation.Item{}, fmt.Errorf("unsupported transcript item kind %q", item.Kind)
	}
}

func toolResultBlock(provider coreconversation.ProviderIdentity, item coreconversation.Item) (contentBlock, coreconversation.Item, error) {
	if strings.TrimSpace(item.CallID) == "" {
		return contentBlock{}, coreconversation.Item{}, fmt.Errorf("tool result call_id is empty")
	}
	if item.Provider.Provider == "" {
		item.Provider = provider
	}
	block := contentBlock{
		Type:      "tool_result",
		ToolUseID: item.CallID,
		Content:   transcriptContentString(item.Content),
	}
	if item.Metadata != nil && item.Metadata["is_error"] == "true" {
		block.IsError = true
	}
	msg := message{Role: "user", Content: []contentBlock{block}}
	return block, itemWithNativeMessage(item, msg), nil
}

func toolUseBlocks(msg message) []contentBlock {
	if msg.Role != "assistant" {
		return nil
	}
	var out []contentBlock
	for _, block := range msg.Content {
		if block.Type == "tool_use" && strings.TrimSpace(block.ID) != "" {
			out = append(out, block)
		}
	}
	return out
}

func toolUseIDKnown(uses []contentBlock, id string) bool {
	id = strings.TrimSpace(id)
	for _, use := range uses {
		if strings.TrimSpace(use.ID) == id {
			return true
		}
	}
	return false
}

func toolUseBlocksFromItem(item coreconversation.Item) []contentBlock {
	calls := item.ToolCallRefs()
	if len(calls) == 0 {
		return nil
	}
	var blocks []contentBlock
	if text := strings.TrimSpace(transcriptContentString(item.Content)); text != "" {
		blocks = append(blocks, contentBlock{Type: "text", Text: text})
	}
	for _, call := range calls {
		blocks = append(blocks, contentBlock{
			Type:  "tool_use",
			ID:    strings.TrimSpace(call.CallID),
			Name:  call.Name,
			Input: rawToolInput(call.Input),
		})
	}
	return blocks
}

func rawToolInput(input any) json.RawMessage {
	switch typed := input.(type) {
	case nil:
		return json.RawMessage(`{}`)
	case json.RawMessage:
		if len(typed) == 0 || string(typed) == "null" {
			return json.RawMessage(`{}`)
		}
		return typed
	case []byte:
		if len(typed) == 0 || string(typed) == "null" {
			return json.RawMessage(`{}`)
		}
		return json.RawMessage(typed)
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return json.RawMessage(`{}`)
		}
		if json.Valid([]byte(text)) {
			return json.RawMessage(text)
		}
		raw, _ := json.Marshal(map[string]string{"input": typed})
		return raw
	default:
		raw, err := json.Marshal(typed)
		if err != nil || len(raw) == 0 || string(raw) == "null" {
			return json.RawMessage(`{}`)
		}
		return raw
	}
}

func itemWithNativeMessage(item coreconversation.Item, msg message) coreconversation.Item {
	if len(item.Native) == 0 {
		if raw, err := json.Marshal(msg); err == nil {
			item.Native = raw
		}
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
