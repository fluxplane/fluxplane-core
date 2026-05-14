package event

import (
	"errors"
	"testing"
)

func TestExpectSequence(t *testing.T) {
	opts := ExpectSequence(42)

	if !opts.CheckExpectedSequence {
		t.Fatal("CheckExpectedSequence = false, want true")
	}
	if opts.ExpectedSequence != 42 {
		t.Fatalf("ExpectedSequence = %d, want 42", opts.ExpectedSequence)
	}
}

func TestExpectSequenceZero(t *testing.T) {
	opts := ExpectSequence(0)

	if !opts.CheckExpectedSequence {
		t.Fatal("CheckExpectedSequence = false, want true")
	}
	if opts.ExpectedSequence != 0 {
		t.Fatalf("ExpectedSequence = %d, want 0", opts.ExpectedSequence)
	}
}

func TestAppendConflictError(t *testing.T) {
	conflict := AppendConflict{
		Stream:   "test-stream",
		Expected: 5,
		Actual:   10,
	}

	msg := conflict.Error()
	if msg == "" {
		t.Fatal("Error() returned empty string")
	}

	// Check that error message contains relevant information
	if !containsSubstring(msg, "append conflict") ||
		!containsSubstring(msg, "test-stream") ||
		!containsSubstring(msg, "5") ||
		!containsSubstring(msg, "10") {
		t.Fatalf("Error message missing expected content: %s", msg)
	}
}

func TestAppendConflictUnwrap(t *testing.T) {
	conflict := AppendConflict{
		Stream:   "test-stream",
		Expected: 5,
		Actual:   10,
	}

	if !errors.Is(conflict, ErrAppendConflict) {
		t.Fatal("Unwrap did not return ErrAppendConflict")
	}
}

func TestLoadOptionsDefaults(t *testing.T) {
	opts := LoadOptions{}

	if opts.After != 0 {
		t.Fatalf("After = %d, want 0", opts.After)
	}
	if opts.Before != 0 {
		t.Fatalf("Before = %d, want 0", opts.Before)
	}
	if opts.Limit != 0 {
		t.Fatalf("Limit = %d, want 0", opts.Limit)
	}
	if opts.Direction != "" {
		t.Fatalf("Direction = %q, want empty", opts.Direction)
	}
}

func TestAppendOptionsDefaults(t *testing.T) {
	opts := AppendOptions{}

	if opts.CheckExpectedSequence {
		t.Fatal("CheckExpectedSequence = true, want false")
	}
	if opts.ExpectedSequence != 0 {
		t.Fatalf("ExpectedSequence = %d, want 0", opts.ExpectedSequence)
	}
}

func TestStoredRecordFields(t *testing.T) {
	record := Record{ID: "rec-1"}
	stored := StoredRecord{
		Stream:   "stream-1",
		Sequence: 1,
		Record:   record,
	}

	if stored.Stream != "stream-1" {
		t.Fatalf("Stream = %q, want stream-1", stored.Stream)
	}
	if stored.Sequence != 1 {
		t.Fatalf("Sequence = %d, want 1", stored.Sequence)
	}
	if stored.Record.ID != "rec-1" {
		t.Fatalf("Record.ID = %q, want rec-1", stored.Record.ID)
	}
}

func TestAppendRequestFields(t *testing.T) {
	record := Record{ID: "rec-1"}
	opts := ExpectSequence(0)
	req := AppendRequest{
		Stream:  "stream-1",
		Options: opts,
		Records: []Record{record},
	}

	if req.Stream != "stream-1" {
		t.Fatalf("Stream = %q, want stream-1", req.Stream)
	}
	if !req.Options.CheckExpectedSequence {
		t.Fatal("Options.CheckExpectedSequence = false, want true")
	}
	if len(req.Records) != 1 {
		t.Fatalf("len(Records) = %d, want 1", len(req.Records))
	}
}

func TestAppendResultFields(t *testing.T) {
	record := Record{ID: "rec-1"}
	stored := StoredRecord{
		Stream:   "stream-1",
		Sequence: 1,
		Record:   record,
	}
	result := AppendResult{
		Stream:  "stream-1",
		Records: []StoredRecord{stored},
	}

	if result.Stream != "stream-1" {
		t.Fatalf("Stream = %q, want stream-1", result.Stream)
	}
	if len(result.Records) != 1 {
		t.Fatalf("len(Records) = %d, want 1", len(result.Records))
	}
	if result.Records[0].Record.ID != "rec-1" {
		t.Fatalf("Records[0].Record.ID = %q, want rec-1", result.Records[0].Record.ID)
	}
}

func TestDirectionConstants(t *testing.T) {
	if DirectionForward != "forward" {
		t.Fatalf("DirectionForward = %q, want forward", DirectionForward)
	}
	if DirectionBackward != "backward" {
		t.Fatalf("DirectionBackward = %q, want backward", DirectionBackward)
	}
}

// Helper function to check substring
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
