package eventstore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-event/eventcodec"
)

// MemoryStore is an in-memory append-only event store.
type MemoryStore struct {
	mu      sync.Mutex
	streams map[event.StreamID][]event.StoredRecord
}

var _ event.Store = (*MemoryStore)(nil)

// NewMemoryStore returns an empty in-memory event store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{streams: map[event.StreamID][]event.StoredRecord{}}
}

// Append writes records atomically to stream.
func (s *MemoryStore) Append(ctx context.Context, stream event.StreamID, opts event.AppendOptions, records ...event.Record) ([]event.StoredRecord, error) {
	results, err := s.AppendBatch(ctx, event.AppendRequest{
		Stream:  stream,
		Options: opts,
		Records: records,
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0].Records, nil
}

// AppendBatch writes requests atomically.
func (s *MemoryStore) AppendBatch(ctx context.Context, requests ...event.AppendRequest) ([]event.AppendResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.streams == nil {
		s.streams = map[event.StreamID][]event.StoredRecord{}
	}
	now := time.Now().UTC()
	seenStreams := map[event.StreamID]struct{}{}
	seenIDs := map[string]event.StreamID{}
	for stream, stored := range s.streams {
		for _, record := range stored {
			if record.Record.ID != "" {
				seenIDs[record.Record.ID] = stream
			}
		}
	}
	results := make([]event.AppendResult, 0, len(requests))
	for _, request := range requests {
		if request.Stream == "" {
			return nil, fmt.Errorf("eventstore: stream is empty")
		}
		if _, exists := seenStreams[request.Stream]; exists {
			return nil, fmt.Errorf("eventstore: duplicate stream %q in append batch", request.Stream)
		}
		seenStreams[request.Stream] = struct{}{}
		current := s.streams[request.Stream]
		actual := event.Sequence(len(current))
		if request.Options.CheckExpectedSequence && request.Options.ExpectedSequence != actual {
			return nil, event.AppendConflict{
				Stream:   request.Stream,
				Expected: request.Options.ExpectedSequence,
				Actual:   actual,
			}
		}
		result := event.AppendResult{Stream: request.Stream}
		for _, record := range request.Records {
			normalized, err := eventcodec.NormalizeRecord(record, now)
			if err != nil {
				return nil, err
			}
			if existingStream, exists := seenIDs[normalized.ID]; exists {
				return nil, event.DuplicateRecord{Stream: existingStream, ID: normalized.ID}
			}
			seenIDs[normalized.ID] = request.Stream
			stored := event.StoredRecord{
				Stream:   request.Stream,
				Sequence: event.Sequence(len(current) + len(result.Records) + 1),
				Record:   normalized,
			}
			result.Records = append(result.Records, stored)
		}
		results = append(results, result)
	}
	for _, result := range results {
		s.streams[result.Stream] = append(s.streams[result.Stream], result.Records...)
	}
	return cloneAppendResults(results), nil
}

// Load returns records from stream in sequence order unless backward direction
// is requested.
func (s *MemoryStore) Load(ctx context.Context, stream event.StreamID, opts event.LoadOptions) ([]event.StoredRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stream == "" {
		return nil, fmt.Errorf("eventstore: stream is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var filtered []event.StoredRecord
	for _, stored := range s.streams[stream] {
		if opts.After > 0 && stored.Sequence <= opts.After {
			continue
		}
		if opts.Before > 0 && stored.Sequence >= opts.Before {
			continue
		}
		filtered = append(filtered, stored)
	}
	if opts.Direction == event.DirectionBackward {
		for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
			filtered[i], filtered[j] = filtered[j], filtered[i]
		}
	}
	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}
	return eventcodec.CloneStoredRecords(filtered), nil
}

func cloneAppendResults(results []event.AppendResult) []event.AppendResult {
	out := make([]event.AppendResult, len(results))
	for i, result := range results {
		out[i] = event.AppendResult{
			Stream:  result.Stream,
			Records: eventcodec.CloneStoredRecords(result.Records),
		}
	}
	return out
}
