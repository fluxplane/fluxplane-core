package session

import (
	"sort"
	"strings"

	"github.com/fluxplane/engine/core/command"
	runtimeoperation "github.com/fluxplane/engine/runtime/operation"
)

// AvailableCommandSpecs returns the command specs the session command dispatcher
// can resolve. This is the authoritative command list for presentation-layer
// command completion.
func AvailableCommandSpecs(registry *command.Registry, catalog CommandCatalog) []command.Spec {
	seen := map[string]command.Spec{}
	add := func(spec command.Spec) {
		key := spec.Path.String()
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = spec
	}
	for _, binding := range builtInSessionCommands() {
		add(completeBuiltInCommandSpec(binding.Spec))
	}
	for _, binding := range catalog {
		add(binding.Spec)
	}
	for _, spec := range registry.All() {
		add(spec)
	}
	out := make([]command.Spec, 0, len(seen))
	for _, spec := range seen {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path.String() < out[j].Path.String()
	})
	return out
}

func completeBuiltInCommandSpec(spec command.Spec) command.Spec {
	switch spec.Path.String() {
	case activateCommandSpec.Path.String():
		spec.Input = runtimeoperation.TypeOf[activateCommandInput]("activate_input")
		spec.Annotations = commandCompletionAnnotations(spec.Annotations, command.FlagNamesFor[activateCommandInput]())
	case contextCommandSpec.Path.String():
		spec.Input = runtimeoperation.TypeOf[contextPreviewInput]("context_input")
		spec.Annotations = commandCompletionAnnotations(spec.Annotations, command.FlagNamesFor[contextPreviewInput]())
	case compactCommandSpec.Path.String():
		spec.Input = runtimeoperation.TypeOf[compactCommandInput]("compact_input")
		spec.Annotations = commandCompletionAnnotations(spec.Annotations, command.FlagNamesFor[compactCommandInput]())
	case surfaceCommandSpec.Path.String():
		spec.Input = runtimeoperation.TypeOf[surfaceCommandInput]("surface_input")
		spec.Annotations = commandCompletionAnnotations(spec.Annotations, command.FlagNamesFor[surfaceCommandInput]())
	case goalCommandSpec.Path.String():
		spec.Input = runtimeoperation.TypeOf[goalCommandInput]("goal_input")
		spec.Annotations = commandCompletionAnnotations(spec.Annotations, command.FlagNamesFor[goalCommandInput]())
	}
	return spec
}

func commandCompletionAnnotations(base map[string]string, flags []string) map[string]string {
	if len(flags) == 0 {
		return base
	}
	out := map[string]string{}
	for key, value := range base {
		out[key] = value
	}
	out[command.CompletionFlagsAnnotation] = strings.Join(flags, ",")
	return out
}
