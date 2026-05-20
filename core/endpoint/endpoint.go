package endpoint

import (
	"fmt"
	"strings"
)

// Ref identifies an endpoint stored or resolved by runtime code.
type Ref string

// Spec describes an explicitly configured endpoint.
type Spec struct {
	Name        string            `json:"name"`
	URL         string            `json:"url,omitempty"`
	Product     string            `json:"product,omitempty"`
	Protocol    string            `json:"protocol,omitempty"`
	AuthRef     string            `json:"auth_ref,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// SourceRef describes where an endpoint came from without importing the source
// system into core.
type SourceRef struct {
	Kind       string            `json:"kind,omitempty"`
	Name       string            `json:"name,omitempty"`
	Namespace  string            `json:"namespace,omitempty"`
	Cluster    string            `json:"cluster,omitempty"`
	UID        string            `json:"uid,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Resolved is the runtime-ready view of an endpoint.
type Resolved struct {
	Ref        Ref               `json:"ref,omitempty"`
	URL        string            `json:"url"`
	HeadersRef string            `json:"headers_ref,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	ExpiresAt  string            `json:"expires_at,omitempty"`
	Source     SourceRef         `json:"source,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// NewRef returns the canonical endpoint ref for id.
func NewRef(id string) Ref {
	id = strings.TrimSpace(strings.TrimPrefix(id, "@endpoint/"))
	if id == "" {
		return ""
	}
	return Ref("@endpoint/" + id)
}

// ID returns the ref id without the @endpoint/ prefix.
func (r Ref) ID() string {
	return strings.TrimSpace(strings.TrimPrefix(string(r), "@endpoint/"))
}

// Valid reports whether r is a non-empty endpoint ref.
func (r Ref) Valid() bool {
	return strings.HasPrefix(strings.TrimSpace(string(r)), "@endpoint/") && r.ID() != ""
}

// Validate checks the configured endpoint has an identity and target.
func (s Spec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("endpoint: name is empty")
	}
	if strings.TrimSpace(s.URL) == "" {
		return fmt.Errorf("endpoint: url is empty")
	}
	return nil
}
