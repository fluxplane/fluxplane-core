package event

import (
	"context"
	"errors"
	"fmt"
)

// StreamID identifies an append-only event stream.
type StreamID string

// Sequence is a store-assigned, monotonically increasing position within a
// stream.
type Sequence int64

// Direction controls load order.
type Direction string

const (
	DirectionForward  Direction = "forward"
	DirectionBackward Direction = "backward"
)

// LoadOptions selects a window of records from one stream.
type LoadOptions struct {
	After     Sequence  `json:"after,omitempty"`
	Before    Sequence  `json:"before,omitempty"`
	Limit     int       `json:"limit,omitempty"`
	Direction Direction `json:"direction,omitempty"`
}

// StoredRecord is a record plus storage coordinates.
type StoredRecord struct {
	Stream   StreamID `json:"stream"`
	Sequence Sequence `json:"sequence"`
	Record   Record   `json:"record"`
}

// AppendRequest is one stream append inside an optional atomic batch.
type AppendRequest struct {
	Stream  StreamID      `json:"stream"`
	Options AppendOptions `json:"options,omitempty"`
	Records []Record      `json:"records,omitempty"`
}

// AppendResult is the stored result for one AppendRequest.
type AppendResult struct {
	Stream  StreamID       `json:"stream"`
	Records []StoredRecord `json:"records,omitempty"`
}

// AppendOptions controls append preconditions.
type AppendOptions struct {
	ExpectedSequence      Sequence `json:"expected_sequence,omitempty"`
	CheckExpectedSequence bool     `json:"check_expected_sequence,omitempty"`
}

// ExpectSequence returns append options requiring the stream to currently end
// at sequence. Sequence zero means the stream must be empty.
func ExpectSequence(sequence Sequence) AppendOptions {
	return AppendOptions{
		ExpectedSequence:      sequence,
		CheckExpectedSequence: true,
	}
}

// ErrAppendConflict identifies a failed optimistic concurrency check.

// ErrDuplicateRecord identifies an append containing a record ID that already
// exists in the store. Stores must reject duplicate IDs without appending any
// records from the append request or batch. Retrying a previously committed
// append with the same caller-supplied record IDs therefore resolves to this
// error; callers that intentionally use stable IDs can load/project the target
// stream to recover the committed outcome.
var ErrDuplicateRecord = errors.New("event: duplicate record")

// DuplicateRecord describes a duplicate event record ID rejected by a store.
type DuplicateRecord struct {
	Stream StreamID `json:"stream"`
	ID     string   `json:"id"`
}

func (e DuplicateRecord) Error() string {
	if e.Stream == "" {
		return fmt.Sprintf("event: duplicate record id %q", e.ID)
	}
	return fmt.Sprintf("event: duplicate record id %q on stream %q", e.ID, e.Stream)
}

func (e DuplicateRecord) Unwrap() error {
	return ErrDuplicateRecord
}

var ErrAppendConflict = errors.New("event: append conflict")

// AppendConflict describes an optimistic concurrency failure.
type AppendConflict struct {
	Stream   StreamID `json:"stream"`
	Expected Sequence `json:"expected"`
	Actual   Sequence `json:"actual"`
}

func (e AppendConflict) Error() string {
	return fmt.Sprintf("event: append conflict on stream %q: expected sequence %d, actual %d", e.Stream, e.Expected, e.Actual)
}

func (e AppendConflict) Unwrap() error {
	return ErrAppendConflict
}

// Store is the core append-only event stream port.
//
// Implementations assign record IDs, timestamps, schema defaults, and sequence
// numbers when missing. Core only defines the contract; storage backends live in
// runtime or adapters.
type Store interface {
	Append(context.Context, StreamID, AppendOptions, ...Record) ([]StoredRecord, error)
	AppendBatch(context.Context, ...AppendRequest) ([]AppendResult, error)
	Load(context.Context, StreamID, LoadOptions) ([]StoredRecord, error)
}
