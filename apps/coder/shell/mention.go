package codershell

import "strings"

type completionKind string

const (
	completionMention completionKind = "mention"
	completionCommand completionKind = "command"
	completionOption  completionKind = "option"
)

// MentionState tracks the lightweight resource mention picker.
type MentionState struct {
	Open        bool
	Kind        completionKind
	Query       string
	CommandPath string
	Results     []ResourceSearchResult
	Index       int
	Loading     bool
}

func (m MentionState) activeResult() (ResourceSearchResult, bool) {
	if !m.Open || len(m.Results) == 0 || m.Index < 0 || m.Index >= len(m.Results) {
		return ResourceSearchResult{}, false
	}
	return m.Results[m.Index], true
}

func mentionQuery(input string) (string, bool) {
	idx := strings.LastIndex(input, "@")
	if idx < 0 {
		return "", false
	}
	fragment := input[idx+1:]
	if strings.ContainsAny(fragment, " \t\n") {
		return "", false
	}
	return fragment, true
}

func slashCompletionQuery(input string) (MentionState, bool) {
	if !strings.HasPrefix(strings.TrimSpace(input), "/") || strings.TrimLeft(input, " \t") != input {
		return MentionState{}, false
	}
	trimmed := strings.TrimRight(input, " \t")
	if trimmed == "" {
		return MentionState{}, false
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return MentionState{}, false
	}
	last := fields[len(fields)-1]
	if strings.HasPrefix(last, "--") {
		commandPath := strings.TrimSpace(strings.Join(fields[:len(fields)-1], " "))
		if commandPath == "" {
			return MentionState{}, false
		}
		return MentionState{Open: true, Kind: completionOption, Query: strings.TrimPrefix(last, "--"), CommandPath: commandPath}, true
	}
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") {
			return MentionState{}, false
		}
	}
	return MentionState{Open: true, Kind: completionCommand, Query: strings.TrimPrefix(strings.Join(fields, " "), "/")}, true
}

func replaceMentionFragment(input string, result ResourceSearchResult) string {
	idx := strings.LastIndex(input, "@")
	if idx < 0 {
		return input
	}
	insert := strings.TrimSpace(result.InsertText)
	if insert == "" {
		insert = "@" + result.Label
	}
	return input[:idx] + insert + " "
}

func replaceCommandFragment(input string, result ResourceSearchResult) string {
	insert := strings.TrimSpace(result.InsertText)
	if insert == "" {
		insert = strings.TrimSpace(result.Label)
	}
	if insert == "" {
		return input
	}
	if !strings.HasPrefix(insert, "/") {
		insert = "/" + insert
	}
	return insert + " "
}

func replaceOptionFragment(input string, result ResourceSearchResult) string {
	insert := strings.TrimSpace(result.InsertText)
	if insert == "" {
		insert = strings.TrimSpace(result.Label)
	}
	if insert == "" {
		return input
	}
	if !strings.HasPrefix(insert, "--") {
		insert = "--" + strings.TrimPrefix(insert, "-")
	}
	idx := strings.LastIndex(input, "--")
	if idx < 0 {
		if strings.HasSuffix(input, " ") {
			return input + insert + " "
		}
		return input + " " + insert + " "
	}
	return input[:idx] + insert + " "
}
