package command

import (
	"fmt"
	"strings"
)

// ParseSlash parses token-based slash command syntax into a command invocation.
// Non-slash input is ignored and returns ok=false.
func ParseSlash(input string) (Invocation, bool, error) {
	input = strings.TrimSpace(input)
	if input == "" || !strings.HasPrefix(input, "/") {
		return Invocation{}, false, nil
	}

	tokens := strings.Fields(input)
	if len(tokens) == 0 {
		return Invocation{}, false, nil
	}

	first := tokens[0]
	if first == "/" {
		return Invocation{}, false, fmt.Errorf("command: slash command path is empty")
	}
	if strings.HasPrefix(first, "//") {
		return Invocation{}, false, fmt.Errorf("command: slash command path segment is empty")
	}

	root := strings.TrimPrefix(first, "/")
	if err := validateSlashPathSegment(root); err != nil {
		return Invocation{}, false, err
	}

	path := Path{root}
	inputMap := map[string]any{}
	seenFlag := false

	for i := 1; i < len(tokens); i++ {
		token := tokens[i]
		if strings.HasPrefix(token, "--") {
			seenFlag = true
			nameValue := strings.TrimPrefix(token, "--")
			if nameValue == "" {
				return Invocation{}, false, fmt.Errorf("command: flag name is empty")
			}
			name, value, hasValue := strings.Cut(nameValue, "=")
			if name == "" {
				return Invocation{}, false, fmt.Errorf("command: flag name is empty")
			}
			if hasValue {
				if value == "" {
					return Invocation{}, false, fmt.Errorf("command: flag %q value is empty", name)
				}
				inputMap[name] = value
				continue
			}
			if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
				i++
				inputMap[name] = tokens[i]
				continue
			}
			inputMap[name] = true
			continue
		}
		if strings.HasPrefix(token, "-") {
			return Invocation{}, false, fmt.Errorf("command: unsupported flag syntax %q", token)
		}
		if seenFlag {
			return Invocation{}, false, fmt.Errorf("command: bare token %q after flags", token)
		}
		if err := validateSlashPathSegment(token); err != nil {
			return Invocation{}, false, err
		}
		path = append(path, token)
	}

	var commandInput any
	if len(inputMap) > 0 {
		commandInput = inputMap
	}
	return Invocation{Path: path, Input: commandInput}, true, nil
}

func validateSlashPathSegment(segment string) error {
	if segment == "" {
		return fmt.Errorf("command: slash command path segment is empty")
	}
	if strings.Contains(segment, "/") {
		return fmt.Errorf("command: slash command path segment %q contains /", segment)
	}
	return nil
}
