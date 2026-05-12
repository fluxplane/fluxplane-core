package usage

import "testing"

func TestTrackerAccumulatesByStableSubject(t *testing.T) {
	tracker := NewTracker()
	tracker.Add(Recorded{
		Subject: Subject{Kind: SubjectLLM, Provider: "openai", Name: "gpt-test", ID: "resp_1"},
		Measurements: []Measurement{{
			Metric:   MetricLLMInputTokens,
			Quantity: 10,
			Unit:     UnitToken,
		}},
	})
	tracker.Add(Recorded{
		Subject: Subject{Kind: SubjectLLM, Provider: "openai", Name: "gpt-test", ID: "resp_2"},
		Measurements: []Measurement{{
			Metric:   MetricLLMInputTokens,
			Quantity: 7,
			Unit:     UnitToken,
		}},
	})

	snapshot := tracker.Snapshot()
	if len(snapshot.Subjects) != 1 {
		t.Fatalf("subjects = %d, want 1", len(snapshot.Subjects))
	}
	subject := snapshot.Subjects[0]
	if subject.Subject.ID != "" {
		t.Fatalf("subject ID = %q, want stable subject without response ID", subject.Subject.ID)
	}
	if len(subject.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(subject.Records))
	}
	if len(subject.Totals) != 1 || subject.Totals[0].Quantity != 17 {
		t.Fatalf("totals = %#v, want one total of 17", subject.Totals)
	}
}

func TestTrackerSeparatesMeasurementKeys(t *testing.T) {
	tracker := NewTracker()
	tracker.Add(Recorded{
		Subject: Subject{Kind: SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []Measurement{
			{Metric: MetricLLMInputTokens, Quantity: 10, Unit: UnitToken, Direction: DirectionInput},
			{Metric: MetricLLMInputTokens, Quantity: 3, Unit: UnitToken, Direction: DirectionCached},
			{Metric: MetricLLMInputTokens, Quantity: 2, Unit: UnitToken, Direction: DirectionInput, Dimensions: map[string]string{"bucket": "a"}},
			{Metric: MetricLLMInputTokens, Quantity: 4, Unit: UnitToken, Direction: DirectionInput, Dimensions: map[string]string{"bucket": "a"}},
		},
	})

	totals := tracker.Snapshot().Subjects[0].Totals
	if len(totals) != 3 {
		t.Fatalf("totals = %#v, want three distinct measurement totals", totals)
	}
	if totals[0].Quantity != 10 || totals[1].Quantity != 3 || totals[2].Quantity != 6 {
		t.Fatalf("totals = %#v, want grouped quantities 10, 3, 6", totals)
	}
}

func TestTrackerSeparatesSubjects(t *testing.T) {
	tracker := NewTracker()
	for _, subject := range []Subject{
		{Kind: SubjectLLM, Provider: "openai", Name: "gpt-test"},
		{Kind: SubjectLLM, Provider: "codex", Name: "gpt-test"},
		{Kind: SubjectNetwork, Provider: "openai", Name: "gpt-test"},
	} {
		tracker.Add(Recorded{
			Subject: subject,
			Measurements: []Measurement{{
				Metric:   MetricRequests,
				Quantity: 1,
				Unit:     UnitRequest,
			}},
		})
	}

	if got := len(tracker.Snapshot().Subjects); got != 3 {
		t.Fatalf("subjects = %d, want 3", got)
	}
}

func TestTrackerSnapshotIsDetached(t *testing.T) {
	tracker := NewTracker()
	tracker.Add(Recorded{
		Subject: Subject{Kind: SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []Measurement{{
			Metric:     MetricCost,
			Quantity:   0.12,
			Unit:       UnitCurrency,
			Dimensions: map[string]string{"currency": "USD"},
		}},
	})

	snapshot := tracker.Snapshot()
	snapshot.Subjects[0].Totals[0].Quantity = 99
	snapshot.Subjects[0].Totals[0].Dimensions["currency"] = "EUR"

	next := tracker.Snapshot()
	if next.Subjects[0].Totals[0].Quantity != 0.12 {
		t.Fatalf("quantity = %v, want detached snapshot", next.Subjects[0].Totals[0].Quantity)
	}
	if next.Subjects[0].Totals[0].Dimensions["currency"] != "USD" {
		t.Fatalf("currency = %q, want detached dimensions", next.Subjects[0].Totals[0].Dimensions["currency"])
	}
}

func TestTrackerReset(t *testing.T) {
	tracker := NewTracker()
	tracker.Add(Recorded{
		Subject: Subject{Kind: SubjectRuntime, Name: "test"},
		Measurements: []Measurement{{
			Metric:   MetricRequests,
			Quantity: 1,
			Unit:     UnitRequest,
		}},
	})
	tracker.Reset()
	if !tracker.Snapshot().Empty() {
		t.Fatalf("snapshot after reset is not empty")
	}
}
