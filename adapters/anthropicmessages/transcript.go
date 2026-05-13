package anthropicmessages

import (
	"encoding/json"
	"fmt"
	"strings"

	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
)

func messagesFromTranscript(provider coreconversation.ProviderIdentity, items []coreconversation.Item) ([]message, []contentBlock, []coreconversation.Item, error) {
	var messages []message
	var system []contentBlock
	recorded := make([]coreconversation.Item, 0, len(items))
	for i, item := range items {
		msgs, sys, recordedItem, err := messageFromTranscriptItem(provider, item)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("anthropic messages: transcript item %d: %w", i, err)
		}
		messages = append(messages, msgs...)
		system = append(system, sys...)
		recorded = append(recorded, recordedItem)
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
		msg := message{Role: "assistant", Content: []contentBlock{{Type: "text", Text: transcriptContentString(item.Content)}}}
		return []message{msg}, nil, itemWithNativeMessage(item, msg), nil
	case coreconversation.ItemToolResult:
		if strings.TrimSpace(item.CallID) == "" {
			return nil, nil, coreconversation.Item{}, fmt.Errorf("tool result call_id is empty")
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
		return []message{msg}, nil, itemWithNativeMessage(item, msg), nil
	default:
		return nil, nil, coreconversation.Item{}, fmt.Errorf("unsupported transcript item kind %q", item.Kind)
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
