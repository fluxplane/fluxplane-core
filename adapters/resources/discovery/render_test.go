package discovery

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coreevent "github.com/fluxplane/fluxplane-event"
)

func TestRenderTreeShowsDiagnosticsWithoutResources(t *testing.T) {
	var out bytes.Buffer
	err := RenderTree(&out, Result{
		Root: "/repo",
		Diagnostics: []resource.Diagnostic{{
			Severity: resource.SeverityError,
			Message:  "malformed .agents manifest",
		}},
	})
	if err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	got := out.String()
	for _, want := range []string{"(no resources)", "Diagnostics:", "malformed .agents manifest"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderJSONIncludesSharedBundleContributionOutput(t *testing.T) {
	var out bytes.Buffer
	err := RenderJSON(&out, Result{
		Root: "/repo",
		Bundles: []resource.ContributionBundle{{
			Source: resource.SourceRef{ID: "project", Scope: resource.ScopeProject, Location: "/repo/fluxplane.yaml"},
			OperationSets: []operation.Set{{
				Name: "ops",
			}},
			ContextProviders: []corecontext.ProviderSpec{{
				Name: "workspace",
			}},
			EventTypes: []coreevent.Event{testEvent{}},
			Plugins: []resource.PluginRef{{
				Name: "skills",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var decoded Output
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if len(decoded.OperationSets) != 1 {
		t.Fatalf("operation_sets len = %d, want 1", len(decoded.OperationSets))
	}
	if len(decoded.ContextProviders) != 1 {
		t.Fatalf("context_providers len = %d, want 1", len(decoded.ContextProviders))
	}
	if len(decoded.EventTypes) != 1 || decoded.EventTypes[0] != "test.event" {
		t.Fatalf("event_types = %#v, want test.event", decoded.EventTypes)
	}
	if len(decoded.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(decoded.Plugins))
	}
}

type testEvent struct{}

func (testEvent) EventName() coreevent.Name { return "test.event" }
