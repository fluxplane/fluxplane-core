package operationruntime

import (
	"strings"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-policy"
)

// AccessDescriptor describes one protected resource/action an operation will
// touch for a concrete input.
type AccessDescriptor struct {
	Action   policy.Action      `json:"action"`
	Resource policy.ResourceRef `json:"resource"`
	Reason   string             `json:"reason,omitempty"`
}

// AccessProvider is implemented by operations that can describe authorization
// targets directly from their typed input.
type AccessProvider interface {
	Access(operation.Context, operation.Value) ([]AccessDescriptor, error)
}

// AccessFor returns typed access descriptors for op when it provides them.
func AccessFor(ctx operation.Context, op operation.Operation, input operation.Value) ([]AccessDescriptor, bool, error) {
	provider, ok := op.(AccessProvider)
	if !ok {
		return nil, false, nil
	}
	access, err := provider.Access(ctx, input)
	return access, true, err
}

// AccessField derives zero or more access descriptors from typed input.
type AccessField[I any] func(operation.Context, I) ([]AccessDescriptor, error)

// AccessFields combines simple typed resource selectors into a TypedAccessHandler.
func AccessFields[I any](fields ...AccessField[I]) TypedAccessHandler[I] {
	return func(ctx operation.Context, input I) ([]AccessDescriptor, error) {
		var out []AccessDescriptor
		for _, field := range fields {
			if field == nil {
				continue
			}
			access, err := field(ctx, input)
			if err != nil {
				return nil, err
			}
			out = append(out, access...)
		}
		return out, nil
	}
}

// WithAccessFields attaches simple typed resource selectors to a typed
// operation.
func WithAccessFields[I any](fields ...AccessField[I]) TypedOption[I] {
	return WithAccess(AccessFields(fields...))
}

type accessConfig struct {
	defaultValue string
}

// AccessOption configures simple access descriptor helpers.
type AccessOption func(*accessConfig)

// AccessDefault configures the value used when a selector returns an empty
// string.
func AccessDefault(value string) AccessOption {
	return func(cfg *accessConfig) {
		cfg.defaultValue = value
	}
}

// StaticAccess always returns one descriptor.
func StaticAccess[I any](resource policy.ResourceRef, action policy.Action) AccessField[I] {
	return func(operation.Context, I) ([]AccessDescriptor, error) {
		return []AccessDescriptor{{Resource: resource, Action: action}}, nil
	}
}

// PathAccess derives one workspace path access descriptor.
func PathAccess[I any](selector func(I) string, action policy.Action, opts ...AccessOption) AccessField[I] {
	return func(_ operation.Context, input I) ([]AccessDescriptor, error) {
		return []AccessDescriptor{PathDescriptor(selectedValue(input, selector, "**", opts...), action)}, nil
	}
}

// PathListAccess derives one workspace path access descriptor per selected
// path. An empty selected list falls back to the configured default.
func PathListAccess[I any](selector func(I) []string, action policy.Action, opts ...AccessOption) AccessField[I] {
	return func(_ operation.Context, input I) ([]AccessDescriptor, error) {
		values := selectedValues(input, selector, "**", opts...)
		out := make([]AccessDescriptor, 0, len(values))
		for _, value := range values {
			out = append(out, PathDescriptor(value, action))
		}
		return out, nil
	}
}

// DatasourceAccess derives one datasource access descriptor.
func DatasourceAccess[I any](selector func(I) string, action policy.Action, opts ...AccessOption) AccessField[I] {
	return namedAccess(selector, policy.ResourceDatasource, action, "*", opts...)
}

// ProcessAccess derives one process access descriptor.
func ProcessAccess[I any](selector func(I) string, action policy.Action, opts ...AccessOption) AccessField[I] {
	return namedAccess(selector, policy.ResourceProcess, action, "*", opts...)
}

// NetworkAccess derives one network access descriptor.
func NetworkAccess[I any](selector func(I) string, action policy.Action, opts ...AccessOption) AccessField[I] {
	return namedAccess(selector, policy.ResourceNetwork, action, "*", opts...)
}

// TaskAccess derives one task access descriptor.
func TaskAccess[I any](selector func(I) string, action policy.Action, opts ...AccessOption) AccessField[I] {
	return func(_ operation.Context, input I) ([]AccessDescriptor, error) {
		return []AccessDescriptor{TaskDescriptor(selectedValue(input, selector, "*", opts...), action)}, nil
	}
}

// PathDescriptor returns one workspace path descriptor.
func PathDescriptor(path string, action policy.Action) AccessDescriptor {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "**"
	}
	return AccessDescriptor{
		Resource: policy.ResourceRef{Kind: policy.ResourcePath, Path: path},
		Action:   action,
	}
}

// DatasourceDescriptor returns one datasource descriptor.
func DatasourceDescriptor(name string, action policy.Action) AccessDescriptor {
	return namedDescriptor(policy.ResourceDatasource, name, action)
}

// ProcessDescriptor returns one process descriptor.
func ProcessDescriptor(name string, action policy.Action) AccessDescriptor {
	return namedDescriptor(policy.ResourceProcess, name, action)
}

// NetworkDescriptor returns one network descriptor.
func NetworkDescriptor(name string, action policy.Action) AccessDescriptor {
	return namedDescriptor(policy.ResourceNetwork, name, action)
}

// TaskDescriptor returns one task descriptor.
func TaskDescriptor(id string, action policy.Action) AccessDescriptor {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "*"
	}
	return AccessDescriptor{
		Resource: policy.ResourceRef{Kind: policy.ResourceTask, ID: id},
		Action:   action,
	}
}

func namedAccess[I any](selector func(I) string, kind policy.ResourceKind, action policy.Action, fallback string, opts ...AccessOption) AccessField[I] {
	return func(_ operation.Context, input I) ([]AccessDescriptor, error) {
		return []AccessDescriptor{namedDescriptor(kind, selectedValue(input, selector, fallback, opts...), action)}, nil
	}
}

func namedDescriptor(kind policy.ResourceKind, name string, action policy.Action) AccessDescriptor {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "*"
	}
	return AccessDescriptor{
		Resource: policy.ResourceRef{Kind: kind, Name: name},
		Action:   action,
	}
}

func selectedValue[I any](input I, selector func(I) string, fallback string, opts ...AccessOption) string {
	cfg := accessConfig{defaultValue: fallback}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	value := ""
	if selector != nil {
		value = strings.TrimSpace(selector(input))
	}
	if value == "" {
		return cfg.defaultValue
	}
	return value
}

func selectedValues[I any](input I, selector func(I) []string, fallback string, opts ...AccessOption) []string {
	cfg := accessConfig{defaultValue: fallback}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	var out []string
	if selector != nil {
		for _, value := range selector(input) {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
	}
	if len(out) == 0 {
		return []string{cfg.defaultValue}
	}
	return out
}
