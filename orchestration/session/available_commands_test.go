package session

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/resource"
)

func TestAvailableCommandSpecsIncludesDispatcherCommands(t *testing.T) {
	registry := command.NewRegistry()
	if err := registry.Register(command.Spec{
		Path:        command.Path{"registry"},
		Description: "registry command",
		Target:      invocation.Target{Kind: invocation.TargetSession},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	catalog := CommandCatalog{
		"embedded:bundle:catalog": {
			ID: resource.ResourceID{Origin: "embedded:bundle", Kind: "command", Name: "catalog"},
			Spec: command.Spec{
				Path:        command.Path{"catalog"},
				Description: "catalog command",
				Target:      invocation.Target{Kind: invocation.TargetSession},
			},
		},
	}
	sessionCommands := SessionCommandCatalog{
		"/goal": {Spec: command.Spec{Path: command.Path{"goal"}, Target: invocation.Target{Kind: invocation.TargetSession}}},
	}

	specs := AvailableCommandSpecs(registry, catalog, sessionCommands)
	paths := make([]string, 0, len(specs))
	for _, spec := range specs {
		paths = append(paths, spec.Path.String())
	}
	for _, want := range []string{"/activate", "/catalog", "/compact", "/context", "/env/explain", "/goal", "/registry", "/surface", "/whoami"} {
		if !containsCommandPath(paths, want) {
			t.Fatalf("AvailableCommandSpecs paths = %#v, missing %s", paths, want)
		}
	}
	if !reflect.DeepEqual(paths, sortedCopy(paths)) {
		t.Fatalf("paths = %#v, want sorted", paths)
	}
}

func TestAvailableCommandSpecsAddsBuiltInCompletionFlags(t *testing.T) {
	specs := AvailableCommandSpecs(nil, nil, nil)
	activate, ok := commandSpecByPath(specs, "/activate")
	if !ok {
		t.Fatal("missing /activate command")
	}
	if !containsCSV(activate.Annotations[command.CompletionFlagsAnnotation], "duration") {
		t.Fatalf("/activate completion flags = %q, missing duration", activate.Annotations[command.CompletionFlagsAnnotation])
	}

	compact, ok := commandSpecByPath(specs, "/compact")
	if !ok {
		t.Fatal("missing /compact command")
	}
	if !containsCSV(compact.Annotations[command.CompletionFlagsAnnotation], "dry-run") {
		t.Fatalf("/compact completion flags = %q, missing dry-run", compact.Annotations[command.CompletionFlagsAnnotation])
	}

	surface, ok := commandSpecByPath(specs, "/surface")
	if !ok {
		t.Fatal("missing /surface command")
	}
	if !containsCSV(surface.Annotations[command.CompletionFlagsAnnotation], "json") {
		t.Fatalf("/surface completion flags = %q, missing json", surface.Annotations[command.CompletionFlagsAnnotation])
	}
}

func commandSpecByPath(specs []command.Spec, path string) (command.Spec, bool) {
	for _, spec := range specs {
		if spec.Path.String() == path {
			return spec, true
		}
	}
	return command.Spec{}, false
}

func containsCommandPath(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func containsCSV(values string, want string) bool {
	for _, value := range strings.Split(values, ",") {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}
