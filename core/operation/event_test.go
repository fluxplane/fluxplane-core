package operation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLifecycleEventsOmitMissingValueRefs(t *testing.T) {
	started, err := json.Marshal(OperationStarted{Operation: Ref{Name: "lookup"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(started), `"input":{}`) {
		t.Fatalf("started event = %s, want missing input ref omitted", started)
	}

	completed, err := json.Marshal(OperationCompleted{Operation: Ref{Name: "lookup"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(completed), `"output":{}`) {
		t.Fatalf("completed event = %s, want missing output ref omitted", completed)
	}
}
