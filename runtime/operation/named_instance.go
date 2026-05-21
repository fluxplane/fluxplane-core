package operationruntime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/engine/core/operation"
)

const (
	AnnotationNamedPluginKind     = "named_plugin.kind"
	AnnotationNamedPluginInstance = "named_plugin.instance"
	AnnotationRequiredAuthScope   = "auth.required_scope"
)

// NamedInstanceBinding is one concrete instance behind a logical operation.
type NamedInstanceBinding struct {
	Instance  string
	Operation operation.Operation
}

// NamedInstanceProvider exposes the concrete instances behind an operation.
type NamedInstanceProvider interface {
	NamedPluginKind() string
	NamedInstances() []NamedInstanceBinding
}

type namedInstanceOperation struct {
	kind               string
	instances          []NamedInstanceBinding
	forceInstanceInput bool
}

// NewNamedInstance marks an operation as a named-plugin instance contribution.
func NewNamedInstance(kind, instance string, op operation.Operation) operation.Operation {
	if op == nil {
		return nil
	}
	kind = strings.TrimSpace(kind)
	instance = strings.TrimSpace(instance)
	if instance == "" {
		instance = kind
	}
	return namedInstanceOperation{kind: kind, instances: []NamedInstanceBinding{{Instance: instance, Operation: op}}}
}

// AggregateNamedInstances collapses named-plugin instance contributions into one logical operation.
func AggregateNamedInstances(kind string, instances []NamedInstanceBinding) operation.Operation {
	cleaned := cleanNamedInstances(instances)
	if len(cleaned) == 0 {
		return nil
	}
	if kind = strings.TrimSpace(kind); kind == "" {
		kind = namedPluginKind(cleaned[0].Operation)
	}
	return namedInstanceOperation{kind: kind, instances: cleaned}
}

// FilterNamedInstances keeps only named-plugin instances accepted by allowed.
func FilterNamedInstances(op operation.Operation, allowed map[string]bool) operation.Operation {
	provider, ok := op.(NamedInstanceProvider)
	if !ok {
		return op
	}
	sourceInstances := provider.NamedInstances()
	forceInstanceInput := len(sourceInstances) > 1
	if named, ok := op.(namedInstanceOperation); ok && named.forceInstanceInput {
		forceInstanceInput = true
	}
	var filtered []NamedInstanceBinding
	for _, binding := range sourceInstances {
		if allowed[binding.Instance] {
			filtered = append(filtered, binding)
		}
	}
	aggregated := AggregateNamedInstances(provider.NamedPluginKind(), filtered)
	if named, ok := aggregated.(namedInstanceOperation); ok {
		named.forceInstanceInput = forceInstanceInput
		return named
	}
	return aggregated
}

func (o namedInstanceOperation) Spec() operation.Spec {
	if len(o.instances) == 0 || o.instances[0].Operation == nil {
		return operation.Spec{}
	}
	spec := o.instances[0].Operation.Spec()
	annotations := make(map[string]string, len(spec.Annotations)+2)
	for key, value := range spec.Annotations {
		annotations[key] = value
	}
	spec.Annotations = annotations
	if o.kind != "" {
		spec.Annotations[AnnotationNamedPluginKind] = o.kind
	}
	if len(o.instances) == 1 && !o.forceInstanceInput {
		spec.Annotations[AnnotationNamedPluginInstance] = o.instances[0].Instance
		return spec
	}
	delete(spec.Annotations, AnnotationNamedPluginInstance)
	spec.Input = withInstanceInput(spec.Input, instanceNames(o.instances))
	return spec
}

func (o namedInstanceOperation) Run(ctx operation.Context, input operation.Value) operation.Result {
	binding, stripped, err := o.selectInstance(input)
	if err != nil {
		name := string(o.Spec().Ref.Name)
		if name == "" {
			name = "named_plugin_operation"
		}
		return operation.Failed("invalid_"+name+"_input", err.Error(), nil)
	}
	return binding.Operation.Run(ctx, stripped)
}

func (o namedInstanceOperation) Access(ctx operation.Context, input operation.Value) ([]AccessDescriptor, error) {
	binding, stripped, err := o.selectInstance(input)
	if err != nil {
		return nil, err
	}
	accessor, ok := binding.Operation.(AccessProvider)
	if !ok {
		return nil, nil
	}
	return accessor.Access(ctx, stripped)
}

func (o namedInstanceOperation) Intent(ctx operation.Context, input operation.Value) (operation.IntentSet, error) {
	binding, stripped, err := o.selectInstance(input)
	if err != nil {
		return operation.IntentSet{}, err
	}
	provider, ok := binding.Operation.(operation.IntentProvider)
	if !ok {
		return operation.IntentSet{}, nil
	}
	return provider.Intent(ctx, stripped)
}

func (o namedInstanceOperation) NamedPluginKind() string {
	return o.kind
}

func (o namedInstanceOperation) NamedInstances() []NamedInstanceBinding {
	return append([]NamedInstanceBinding(nil), o.instances...)
}

func (o namedInstanceOperation) selectInstance(input operation.Value) (NamedInstanceBinding, operation.Value, error) {
	if len(o.instances) == 0 {
		return NamedInstanceBinding{}, input, fmt.Errorf("no instances are available")
	}
	if len(o.instances) == 1 && !o.forceInstanceInput {
		return o.instances[0], input, nil
	}
	values, err := inputObject(input)
	if err != nil {
		return NamedInstanceBinding{}, input, err
	}
	instance := strings.TrimSpace(fmt.Sprint(values["instance"]))
	if instance == "" {
		return NamedInstanceBinding{}, input, fmt.Errorf("instance is required")
	}
	for _, binding := range o.instances {
		if binding.Instance == instance {
			delete(values, "instance")
			return binding, values, nil
		}
	}
	return NamedInstanceBinding{}, input, fmt.Errorf("unknown instance %q", instance)
}

func cleanNamedInstances(instances []NamedInstanceBinding) []NamedInstanceBinding {
	out := make([]NamedInstanceBinding, 0, len(instances))
	seen := map[string]bool{}
	for _, binding := range instances {
		if binding.Operation == nil {
			continue
		}
		instance := strings.TrimSpace(binding.Instance)
		if instance == "" {
			instance = namedPluginInstance(binding.Operation)
		}
		if instance == "" || seen[instance] {
			continue
		}
		seen[instance] = true
		out = append(out, NamedInstanceBinding{Instance: instance, Operation: binding.Operation})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

func namedPluginKind(op operation.Operation) string {
	if op == nil {
		return ""
	}
	return strings.TrimSpace(op.Spec().Annotations[AnnotationNamedPluginKind])
}

func namedPluginInstance(op operation.Operation) string {
	if op == nil {
		return ""
	}
	return strings.TrimSpace(op.Spec().Annotations[AnnotationNamedPluginInstance])
}

func inputObject(input operation.Value) (map[string]any, error) {
	switch value := input.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, v := range value {
			out[k] = v
		}
		return out, nil
	default:
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string]any{}
		}
		return out, nil
	}
}

func instanceNames(instances []NamedInstanceBinding) []string {
	out := make([]string, 0, len(instances))
	for _, binding := range instances {
		out = append(out, binding.Instance)
	}
	sort.Strings(out)
	return out
}

func withInstanceInput(input operation.Type, instances []string) operation.Type {
	if input.Schema.Format != "" && input.Schema.Format != "json-schema" {
		return input
	}
	var root map[string]any
	if len(input.Schema.Data) == 0 || json.Unmarshal(input.Schema.Data, &root) != nil {
		root = map[string]any{"type": "object"}
	}
	properties, _ := root["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
		root["properties"] = properties
	}
	enum := make([]any, 0, len(instances))
	for _, instance := range instances {
		enum = append(enum, instance)
	}
	properties["instance"] = map[string]any{
		"type":        "string",
		"description": "Configured plugin instance to use.",
		"enum":        enum,
	}
	required := stringSlice(root["required"])
	if !containsString(required, "instance") {
		required = append(required, "instance")
		sort.Strings(required)
	}
	root["required"] = required
	raw, err := json.Marshal(root)
	if err != nil {
		return input
	}
	input.Schema = operation.Schema{Format: "json-schema", Data: raw}
	return input
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return append([]string(nil), strings...)
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
