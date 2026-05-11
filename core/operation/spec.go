package operation

import "encoding/json"

// Value is the generic value boundary for operation inputs and outputs.
//
// Runtime packages may bind Values to concrete Go structs, JSON values, or
// references to external blobs. Core keeps the boundary intentionally inert.
type Value = any

// Schema is an inert schema document. Core does not validate against it.
type Schema struct {
	// Format names the schema dialect, for example "json-schema".
	Format string `json:"format,omitempty"`

	// Data contains the raw schema document for the selected format.
	Data json.RawMessage `json:"data,omitempty"`
}

// Type describes an operation input or output contract.
type Type struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Schema      Schema `json:"schema,omitempty"`
}

// IsZero reports whether the type has no declared contract.
func (t Type) IsZero() bool {
	return t.Name == "" && t.Description == "" && len(t.Schema.Data) == 0 && t.Schema.Format == ""
}

// Example documents one representative input/output pair or value.
type Example struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Input       Value  `json:"input,omitempty"`
	Output      Value  `json:"output,omitempty"`
}

// Spec describes an operation without binding it to any particular runtime,
// model tool, command, workflow, channel, or plugin implementation.
type Spec struct {
	Ref         Ref               `json:"ref"`
	Description string            `json:"description,omitempty"`
	Input       Type              `json:"input,omitempty"`
	Output      Type              `json:"output,omitempty"`
	Semantics   Semantics         `json:"semantics,omitempty"`
	Examples    []Example         `json:"examples,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}
