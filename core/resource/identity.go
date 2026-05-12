package resource

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Namespace is an origin-local path that scopes a resource. It is rendered as
// "/"-joined segments for display and suffix matching.
type Namespace struct {
	segments []string
}

// NewNamespace returns a namespace from non-empty trimmed segments.
func NewNamespace(segments ...string) Namespace {
	clean := make([]string, 0, len(segments))
	for _, segment := range segments {
		for _, part := range strings.Split(segment, "/") {
			part = strings.TrimSpace(part)
			if part != "" {
				clean = append(clean, part)
			}
		}
	}
	return Namespace{segments: clean}
}

// Segments returns a copy of namespace segments.
func (n Namespace) Segments() []string {
	return append([]string(nil), n.segments...)
}

// Len returns the number of namespace segments.
func (n Namespace) Len() int { return len(n.segments) }

// Last returns the last namespace segment.
func (n Namespace) Last() string {
	if len(n.segments) == 0 {
		return ""
	}
	return n.segments[len(n.segments)-1]
}

// String renders the namespace as "/"-joined segments.
func (n Namespace) String() string {
	return strings.Join(n.segments, "/")
}

// MarshalJSON encodes namespace as its display string.
func (n Namespace) MarshalJSON() ([]byte, error) {
	return json.Marshal(n.String())
}

// UnmarshalJSON decodes namespace from its display string.
func (n *Namespace) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*n = NewNamespace(value)
	return nil
}

// IsEmpty reports whether namespace has no segments.
func (n Namespace) IsEmpty() bool { return len(n.segments) == 0 }

// Equal reports whether namespaces have identical segments.
func (n Namespace) Equal(other Namespace) bool {
	if len(n.segments) != len(other.segments) {
		return false
	}
	for i, segment := range n.segments {
		if segment != other.segments[i] {
			return false
		}
	}
	return true
}

// SuffixMatch reports whether suffix matches the end of namespace.
func (n Namespace) SuffixMatch(suffix []string) bool {
	if len(suffix) > len(n.segments) {
		return false
	}
	offset := len(n.segments) - len(suffix)
	for i, segment := range suffix {
		if n.segments[offset+i] != segment {
			return false
		}
	}
	return true
}

// Append returns a namespace with additional segments.
func (n Namespace) Append(segments ...string) Namespace {
	out := make([]string, 0, len(n.segments)+len(segments))
	out = append(out, n.segments...)
	out = append(out, NewNamespace(segments...).segments...)
	return Namespace{segments: out}
}

// ResourceID is the canonical identity of a contributed resource. Kind scopes
// resolution; origin and namespace describe where the contribution came from.
type ResourceID struct {
	Kind      string    `json:"kind,omitempty"`
	Origin    string    `json:"origin,omitempty"`
	Namespace Namespace `json:"namespace,omitempty"`
	Name      string    `json:"name,omitempty"`
}

// Address returns the canonical display address.
func (r ResourceID) Address() string {
	ns := r.Namespace.String()
	switch {
	case r.Origin == "":
		return r.Name
	case ns == "":
		return r.Origin + ":" + r.Name
	default:
		return r.Origin + ":" + ns + ":" + r.Name
	}
}

// String returns the canonical display address.
func (r ResourceID) String() string { return r.Address() }

// IsZero reports whether id has no origin and no name.
func (r ResourceID) IsZero() bool { return r.Origin == "" && r.Name == "" }

// Equal reports whether resource IDs are identical.
func (r ResourceID) Equal(other ResourceID) bool {
	return r.Kind == other.Kind &&
		r.Origin == other.Origin &&
		r.Namespace.Equal(other.Namespace) &&
		r.Name == other.Name
}

// MatchesRef reports whether this ID matches a user/local ref. The ref uses
// ":" qualifiers. The final segment is the name; preceding segments match
// origin+namespace or a namespace suffix.
func (r ResourceID) MatchesRef(ref string) bool {
	parts := splitRef(ref)
	if len(parts) == 0 {
		return false
	}
	if parts[len(parts)-1] != r.Name {
		return false
	}
	qualifier := parts[:len(parts)-1]
	if len(qualifier) == 0 {
		return true
	}

	full := make([]string, 0, 1+r.Namespace.Len())
	if r.Origin != "" {
		full = append(full, r.Origin)
	}
	full = append(full, r.Namespace.segments...)
	if suffixSliceMatch(full, qualifier) {
		return true
	}
	if qualifier[0] == r.Origin {
		nsSuffix := qualifier[1:]
		if len(nsSuffix) == 0 {
			return true
		}
		return r.Namespace.SuffixMatch(nsSuffix)
	}
	return false
}

// DeriveOrigin returns the default origin for source.
func DeriveOrigin(source SourceRef) string {
	switch source.Scope {
	case ScopeProject:
		return "local"
	case ScopeUser:
		return "user"
	case ScopeEmbedded:
		if source.Ecosystem != "" {
			return source.Ecosystem
		}
		return "embedded"
	case ScopeRemote:
		return "remote"
	case ScopeExplicit:
		return "explicit"
	default:
		if source.Ecosystem != "" {
			return source.Ecosystem
		}
		return "unknown"
	}
}

// DeriveNamespace returns the default namespace for source.
func DeriveNamespace(source SourceRef) Namespace {
	switch source.Scope {
	case ScopeUser:
		return NewNamespace("global")
	case ScopeProject:
		if source.Location != "" {
			dir := filepath.Dir(source.Location)
			if dir != "." && dir != string(filepath.Separator) {
				return NewNamespace(filepath.Base(dir))
			}
		}
	case ScopeEmbedded:
		if source.Location != "" {
			location := strings.TrimPrefix(source.Location, ".")
			location = strings.TrimPrefix(location, "/")
			return NewNamespace(location)
		}
	}
	if source.Ref != "" {
		return NewNamespace(source.Ref)
	}
	if source.Location != "" {
		return NewNamespace(source.Location)
	}
	return NewNamespace()
}

// DeriveResourceID builds a canonical ID from source, kind, and local name.
func DeriveResourceID(source SourceRef, kind, name string) ResourceID {
	return ResourceID{
		Kind:      kind,
		Origin:    DeriveOrigin(source),
		Namespace: DeriveNamespace(source),
		Name:      strings.TrimSpace(name),
	}
}

func splitRef(ref string) []string {
	raw := strings.Split(strings.TrimSpace(ref), ":")
	parts := make([]string, 0, len(raw))
	for i, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i == len(raw)-1 {
			parts = append(parts, part)
			continue
		}
		for _, segment := range strings.Split(part, "/") {
			segment = strings.TrimSpace(segment)
			if segment != "" {
				parts = append(parts, segment)
			}
		}
	}
	return parts
}

func suffixSliceMatch(full, suffix []string) bool {
	if len(suffix) > len(full) {
		return false
	}
	offset := len(full) - len(suffix)
	for i, part := range suffix {
		if full[offset+i] != part {
			return false
		}
	}
	return true
}
