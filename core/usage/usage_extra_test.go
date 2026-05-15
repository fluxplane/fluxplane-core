package usage

import "testing"

func TestNewSnapshotFromRecords(t *testing.T) {
	recorded := Recorded{
		Source:  "test",
		Subject: Subject{Kind: SubjectLLM, Provider: "openai", Name: "gpt-4"},
		Measurements: []Measurement{
			{Metric: MetricLLMInputTokens, Quantity: 100, Unit: UnitToken, Direction: DirectionInput},
		},
	}
	snap := NewSnapshot(recorded)
	if snap.Empty() {
		t.Fatal("NewSnapshot returned empty snapshot")
	}
	if len(snap.Subjects) != 1 {
		t.Fatalf("Subjects len = %d, want 1", len(snap.Subjects))
	}
	if snap.Subjects[0].Totals[0].Quantity != 100 {
		t.Fatalf("Total quantity = %v, want 100", snap.Subjects[0].Totals[0].Quantity)
	}
}

func TestTrackerAddZeroMeasurement(t *testing.T) {
	tracker := NewTracker()
	tracker.Add(Recorded{
		Subject: Subject{Kind: SubjectLLM, Name: "m"},
		Measurements: []Measurement{
			{Metric: MetricLLMInputTokens, Quantity: 0, Unit: UnitToken},
		},
	})
	snap := tracker.Snapshot()
	if len(snap.Subjects) == 0 {
		return // zero-quantity measurements don't create totals — that's fine.
	}
	if len(snap.Subjects[0].Totals) != 0 {
		t.Fatal("Zero-quantity measurement should not appear in Totals")
	}
}

func TestTrackerNilAddAndSnapshot(t *testing.T) {
	var t2 *Tracker
	t2.Add(Recorded{}) // should not panic
	if !t2.Snapshot().Empty() {
		t.Fatal("nil Tracker.Snapshot() should be empty")
	}
}

func TestTrackerNilReset(t *testing.T) {
	var t2 *Tracker
	t2.Reset() // should not panic
}

func TestCloneSubjectPreservesAttributes(t *testing.T) {
	tracker := NewTracker()
	attrs := map[string]string{"x": "1"}
	tracker.Add(Recorded{
		Subject: Subject{Kind: SubjectLLM, Name: "m", Attributes: attrs},
		Measurements: []Measurement{
			{Metric: MetricLLMInputTokens, Quantity: 10, Unit: UnitToken},
		},
	})
	snap := tracker.Snapshot()
	// Mutate original attrs; snapshot should be unaffected.
	attrs["x"] = "changed"
	if snap.Subjects[0].Subject.Attributes["x"] != "1" {
		t.Fatal("Snapshot attributes not cloned correctly")
	}
}

func TestRecordedEventName(t *testing.T) {
	r := Recorded{}
	if r.EventName() != EventRecordedName {
		t.Fatalf("EventName = %q, want %q", r.EventName(), EventRecordedName)
	}
}
