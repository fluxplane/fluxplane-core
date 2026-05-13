package resourcediscovery

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/resource"
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
