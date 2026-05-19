package codershell

import "strings"

// IntentKind is the controller-level interpretation of submitted input.
type IntentKind string

const (
	IntentBlank   IntentKind = "blank"
	IntentCommand IntentKind = "command"
	IntentAsk     IntentKind = "ask"
	IntentSlash   IntentKind = "slash"
	IntentCD      IntentKind = "cd"
)

// InputIntent is a parsed input submission before client dispatch.
type InputIntent struct {
	Kind IntentKind
	Text string
	Arg  string
}

func classifyInput(line string, mode InputMode) InputIntent {
	line = strings.TrimSpace(line)
	if line == "" {
		return InputIntent{Kind: IntentBlank}
	}
	if mode == InputModeAsk {
		return InputIntent{Kind: IntentAsk, Text: line}
	}
	if strings.HasPrefix(line, "/") {
		return InputIntent{Kind: IntentSlash, Text: line}
	}
	if target, ok := parseCD(line); ok {
		return InputIntent{Kind: IntentCD, Text: line, Arg: target}
	}
	return InputIntent{Kind: IntentCommand, Text: line}
}
