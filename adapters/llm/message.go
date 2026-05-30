package llm

import (
	"github.com/fluxplane/fluxplane-policy"
)

// Role describes a provider-facing chat/message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Visibility describes whether content is safe to expose as user-visible
// stream content or should remain hidden/diagnostic.
type Visibility string

const (
	VisibilityVisible Visibility = "visible"
	VisibilityHidden  Visibility = "hidden"
)

// PartKind classifies one message content part.
type PartKind string

const (
	PartText     PartKind = "text"
	PartData     PartKind = "data"
	PartThinking PartKind = "thinking"
)

// Part is one provider-neutral message content part.
type Part struct {
	Kind        PartKind           `json:"kind"`
	Text        string             `json:"text,omitempty"`
	Data        any                `json:"data,omitempty"`
	Visibility  Visibility         `json:"visibility,omitempty"`
	Sensitivity policy.Sensitivity `json:"sensitivity,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
}

// Message is a provider-facing message shape. It is an adapter helper, not a
// runtime agent decision.
type Message struct {
	Role        Role               `json:"role"`
	Parts       []Part             `json:"parts,omitempty"`
	Visibility  Visibility         `json:"visibility,omitempty"`
	Sensitivity policy.Sensitivity `json:"sensitivity,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
}

// TextMessage returns a visible text message.
func TextMessage(role Role, text string) Message {
	return Message{
		Role: role,
		Parts: []Part{{
			Kind:       PartText,
			Text:       text,
			Visibility: VisibilityVisible,
		}},
		Visibility: VisibilityVisible,
	}
}
