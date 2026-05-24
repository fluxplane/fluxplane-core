package sessionworkflow

import (
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
	coreworkflow "github.com/fluxplane/fluxplane-core/core/workflow"
)

func TestAgentTaskRendersStructuredInput(t *testing.T) {
	task := agentTask(coreworkflow.Step{ID: "summarize"}, operation.Value(map[string]any{
		"metrics": map[string]any{"stdout": "load average: 0.42"},
	}))
	if !strings.Contains(task, "Run workflow step summarize with this input:") {
		t.Fatalf("task = %q, want workflow step preface", task)
	}
	if !strings.Contains(task, "load average") {
		t.Fatalf("task = %q, want structured input JSON", task)
	}
}
