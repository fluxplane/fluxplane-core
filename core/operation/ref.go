package operation

import "strings"

// Name identifies an operation within a registry or contribution bundle.
type Name string

// Version identifies a concrete operation contract version.
type Version string

// Ref identifies an operation by name and optional version.
type Ref struct {
	Name    Name    `json:"name"`
	Version Version `json:"version,omitempty"`
}

// String returns a stable display form for the operation reference.
func (r Ref) String() string {
	name := strings.TrimSpace(string(r.Name))
	version := strings.TrimSpace(string(r.Version))
	if version == "" {
		return name
	}
	if name == "" {
		return "@" + version
	}
	return name + "@" + version
}

// IsZero reports whether the reference has no name and no version.
func (r Ref) IsZero() bool {
	return strings.TrimSpace(string(r.Name)) == "" && strings.TrimSpace(string(r.Version)) == ""
}
