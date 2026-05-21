package operation

import (
	"path"
	"strings"
)

// Matches reports whether selector allows candidate in a selection context.
// Operation execution remains exact-ref only; wildcard refs are for projection
// and capability selection.
func (selector Ref) Matches(candidate Ref) bool {
	pattern := strings.TrimSpace(string(selector.Name))
	name := strings.TrimSpace(string(candidate.Name))
	if pattern == "" || name == "" {
		return false
	}
	if selector.Version != "" && selector.Version != candidate.Version {
		return false
	}
	if !HasSelectorMeta(selector) {
		return pattern == name
	}
	matched, err := path.Match(pattern, name)
	return err == nil && matched
}

// HasSelectorMeta reports whether ref uses wildcard selector syntax.
func HasSelectorMeta(ref Ref) bool {
	return strings.ContainsAny(strings.TrimSpace(string(ref.Name)), "*?[")
}
