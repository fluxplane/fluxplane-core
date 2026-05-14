package terminalui

import (
	"fmt"
	"io"
	"strings"
)

// ReasoningDisplay controls terminal rendering for model reasoning deltas.
type ReasoningDisplay string

const (
	ReasoningDisplayOff ReasoningDisplay = "off"
	ReasoningDisplayOn  ReasoningDisplay = "on"
	ReasoningDisplayRaw ReasoningDisplay = "raw"
)

// UIState holds local terminal-only display preferences.
type UIState struct {
	Reasoning ReasoningDisplay
}

// NormalizeReasoningDisplay validates a user-facing reasoning display mode.
func NormalizeReasoningDisplay(value string) (ReasoningDisplay, error) {
	switch ReasoningDisplay(strings.ToLower(strings.TrimSpace(value))) {
	case "", ReasoningDisplayOff:
		return ReasoningDisplayOff, nil
	case ReasoningDisplayOn:
		return ReasoningDisplayOn, nil
	case ReasoningDisplayRaw:
		return ReasoningDisplayRaw, nil
	default:
		return "", fmt.Errorf("invalid /ui:reasoning mode %q, want off|on|raw", value)
	}
}

// HandleUICommand handles terminal-local /ui:* commands.
func HandleUICommand(prompt string, state *UIState, out io.Writer) (bool, error) {
	fields := strings.Fields(strings.TrimSpace(prompt))
	if len(fields) == 0 || fields[0] != "/ui:reasoning" {
		return false, nil
	}
	if state == nil {
		state = &UIState{}
	}
	if out == nil {
		out = io.Discard
	}
	if len(fields) > 2 {
		return true, fmt.Errorf("usage: /ui:reasoning <off|on|raw>")
	}
	if len(fields) == 2 {
		mode, err := NormalizeReasoningDisplay(fields[1])
		if err != nil {
			return true, err
		}
		state.Reasoning = mode
	}
	mode, _ := NormalizeReasoningDisplay(string(state.Reasoning))
	_, _ = fmt.Fprintf(out, "ui: reasoning %s\n", mode)
	return true, nil
}
