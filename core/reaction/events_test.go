package reaction

import "testing"

func TestReactionEventNames(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"ActionPlanned", string(ActionPlanned{}.EventName()), string(EventActionPlanned)},
		{"ActionApplied", string(ActionApplied{}.EventName()), string(EventActionApplied)},
		{"ActionSkipped", string(ActionSkipped{}.EventName()), string(EventActionSkipped)},
		{"Diagnostic", string(Diagnostic{}.EventName()), string(EventDiagnostic)},
	}
	for _, tc := range tests {
		if got := tc.got; got != tc.want {
			t.Fatalf("%s EventName = %q, want %q", tc.name, got, tc.want)
		}
	}
}
