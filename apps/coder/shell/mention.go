package codershell

import "strings"

// MentionState tracks the lightweight resource mention picker.
type MentionState struct {
	Open    bool
	Query   string
	Results []ResourceSearchResult
	Index   int
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
