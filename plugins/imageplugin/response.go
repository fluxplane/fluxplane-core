package imageplugin

import (
	"encoding/json"
	"fmt"
	"strings"
)

func textFromContentResponse(data []byte) string {
	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ""
	}
	var out []string
	for _, part := range decoded.Content {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			out = append(out, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(out, "\n")
}

func jsonString(data []byte, path ...string) string {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return ""
	}
	current := value
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			current = typed[segment]
		case []any:
			index := 0
			if _, err := fmt.Sscanf(segment, "%d", &index); err != nil || index < 0 || index >= len(typed) {
				return ""
			}
			current = typed[index]
		default:
			return ""
		}
	}
	if value, ok := current.(string); ok {
		return value
	}
	return ""
}
