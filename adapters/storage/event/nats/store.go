package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/eventcodec"
	"github.com/fluxplane/fluxplane-core/core/policy"
)

const (
	DefaultStream           = "FLUXPLANE_EVENTS"
	DefaultSubject          = "fluxplane.events.log"
	defaultMaxAppendRetries = 8
	defaultReplayBatchSize  = 500
)

// Config configures a NATS JetStream-backed event store.
type Config struct {
	URL              string
	Stream           string
	Subject          string
	CreateStream     bool
	MaxAppendRetries int
	ReplayBatchSize  int
}

type resolvedConfig struct {
	url              string
	stream           string
	subject          string
	createStream     bool
	maxAppendRetries int
	replayBatchSize  int
}

// Store is a NATS JetStream-backed event store.
type Store struct {
	mu       sync.Mutex
	nc       *nats.Conn
	owned    bool
	js       jetstream.JetStream
	stream   jetstream.Stream
	registry *event.Registry
	cfg      resolvedConfig
	project  projection
}

var _ event.Store = (*Store)(nil)

type projection struct {
	loaded       bool
	lastSeq      uint64
	expectedNext uint64
	streams      map[event.StreamID][]event.StoredRecord
	ids          map[string]event.StreamID
}

// Open connects to NATS and opens a JetStream event store.
func Open(ctx context.Context, cfg Config, registry *event.Registry) (*Store, error) {
	resolved := resolveConfig(cfg)
	nc, err := nats.Connect(resolved.url)
	if err != nil {
		return nil, fmt.Errorf("natseventstore: connect: %w", err)
	}
	store, err := OpenWithConnection(ctx, nc, cfg, registry)
	if err != nil {
		nc.Close()
		return nil, err
	}
	store.owned = true
	return store, nil
}

// OpenWithConnection opens a JetStream event store on an existing NATS
// connection. The caller owns the connection.
func OpenWithConnection(ctx context.Context, nc *nats.Conn, cfg Config, registry *event.Registry) (*Store, error) {
	if nc == nil {
		return nil, fmt.Errorf("natseventstore: nats connection is nil")
	}
	resolved := resolveConfig(cfg)
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("natseventstore: jetstream: %w", err)
	}
	var stream jetstream.Stream
	if resolved.createStream {
		stream, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:        resolved.stream,
			Subjects:    []string{resolved.subject},
			Retention:   jetstream.LimitsPolicy,
			Storage:     jetstream.FileStorage,
			Discard:     jetstream.DiscardOld,
			DenyDelete:  true,
			DenyPurge:   true,
			Description: "Fluxplane Engine event store",
		})
	} else {
		stream, err = js.Stream(ctx, resolved.stream)
	}
	if err != nil {
		return nil, fmt.Errorf("natseventstore: open stream %q: %w", resolved.stream, err)
	}
	return &Store{
		nc:       nc,
		js:       js,
		stream:   stream,
		registry: registry,
		cfg:      resolved,
		project:  newProjection(),
	}, nil
}

// Close closes the owned NATS connection when Open created it.
func (s *Store) Close() error {
	if s == nil || s.nc == nil || !s.owned {
		return nil
	}
	s.nc.Close()
	return nil
}

// Append writes records atomically to stream.
func (s *Store) Append(ctx context.Context, stream event.StreamID, opts event.AppendOptions, records ...event.Record) ([]event.StoredRecord, error) {
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

// AppendBatch writes requests atomically as one JetStream message.
func (s *Store) AppendBatch(ctx context.Context, requests ...event.AppendRequest) ([]event.AppendResult, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	if err := validateAppendRequests(requests); err != nil {
		return nil, err
	}
	normalized, err := normalizeRequests(requests)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	attempts := s.cfg.maxAppendRetries
	for attempt := 0; attempt < attempts; attempt++ {
		if err := s.syncProjection(ctx); err != nil {
			return nil, err
		}
		results, err := s.prepareResults(normalized)
		if err != nil {
			return nil, err
		}
		envelope, err := encodeBatch(results)
		if err != nil {
			return nil, err
		}
		ack, err := s.js.Publish(ctx, s.cfg.subject, envelope,
			jetstream.WithExpectStream(s.cfg.stream),
			jetstream.WithExpectLastSequence(s.project.lastSeq),
			jetstream.WithRetryAttempts(0),
		)
		if err == nil {
			s.applyResults(results)
			s.project.lastSeq = ack.Sequence
			s.project.expectedNext = ack.Sequence + 1
			return cloneAppendResults(results), nil
		}
		if !isWrongLastSequence(err) {
			return nil, fmt.Errorf("natseventstore: append publish: %w", err)
		}
		if err := s.reloadProjection(ctx); err != nil {
			return nil, err
		}
		if err := s.preconditionError(normalized); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("natseventstore: append exceeded %d retries", attempts)
}

// Load returns records from stream.
func (s *Store) Load(ctx context.Context, stream event.StreamID, opts event.LoadOptions) ([]event.StoredRecord, error) {
	if strings.TrimSpace(string(stream)) == "" {
		return nil, fmt.Errorf("natseventstore: stream is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.syncProjection(ctx); err != nil {
		return nil, err
	}
	var filtered []event.StoredRecord
	for _, stored := range s.project.streams[stream] {
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

type normalizedRequest struct {
	Stream  event.StreamID
	Options event.AppendOptions
	Records []event.Record
}

func (s *Store) prepareResults(requests []normalizedRequest) ([]event.AppendResult, error) {
	if err := s.preconditionError(requests); err != nil {
		return nil, err
	}
	results := make([]event.AppendResult, 0, len(requests))
	for _, request := range requests {
		current := event.Sequence(len(s.project.streams[request.Stream]))
		result := event.AppendResult{Stream: request.Stream}
		for i, record := range request.Records {
			result.Records = append(result.Records, event.StoredRecord{
				Stream:   request.Stream,
				Sequence: current + event.Sequence(i) + 1,
				Record:   record,
			})
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Store) preconditionError(requests []normalizedRequest) error {
	for _, request := range requests {
		current := event.Sequence(len(s.project.streams[request.Stream]))
		if request.Options.CheckExpectedSequence && request.Options.ExpectedSequence != current {
			return event.AppendConflict{
				Stream:   request.Stream,
				Expected: request.Options.ExpectedSequence,
				Actual:   current,
			}
		}
		for _, record := range request.Records {
			if existingStream, exists := s.project.ids[record.ID]; exists {
				return event.DuplicateRecord{Stream: existingStream, ID: record.ID}
			}
		}
	}
	return nil
}

func (s *Store) syncProjection(ctx context.Context) error {
	if !s.project.loaded {
		return s.reloadProjection(ctx)
	}
	info, err := s.stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("natseventstore: stream info: %w", err)
	}
	return s.replayRange(ctx, s.project.lastSeq+1, info.State.LastSeq)
}

func (s *Store) reloadProjection(ctx context.Context) error {
	s.project = newProjection()
	info, err := s.stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("natseventstore: stream info: %w", err)
	}
	s.project.loaded = true
	if info.State.Msgs == 0 {
		return nil
	}
	if info.State.FirstSeq != 1 {
		return fmt.Errorf("natseventstore: event log history is truncated: first sequence is %d", info.State.FirstSeq)
	}
	return s.replayRange(ctx, info.State.FirstSeq, info.State.LastSeq)
}

func (s *Store) replayRange(ctx context.Context, first, last uint64) error {
	if first == 0 || last == 0 || first > last {
		return nil
	}
	if s.project.expectedNext == 0 {
		s.project.expectedNext = first
	}
	for seq := first; seq <= last; seq++ {
		if seq != s.project.expectedNext {
			return fmt.Errorf("natseventstore: event log history is non-contiguous: expected sequence %d, got %d", s.project.expectedNext, seq)
		}
		raw, err := s.stream.GetMsg(ctx, seq)
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				return fmt.Errorf("natseventstore: event log message %d is missing", seq)
			}
			return fmt.Errorf("natseventstore: get event log message %d: %w", seq, err)
		}
		if raw.Subject != s.cfg.subject {
			s.project.lastSeq = raw.Sequence
			s.project.expectedNext = raw.Sequence + 1
			continue
		}
		results, err := decodeBatch(raw.Data, s.registry)
		if err != nil {
			return fmt.Errorf("natseventstore: decode event log message %d: %w", seq, err)
		}
		s.applyResults(results)
		s.project.lastSeq = raw.Sequence
		s.project.expectedNext = raw.Sequence + 1
	}
	return nil
}

func (s *Store) applyResults(results []event.AppendResult) {
	if s.project.streams == nil {
		s.project = newProjection()
		s.project.loaded = true
	}
	for _, result := range results {
		s.project.streams[result.Stream] = append(s.project.streams[result.Stream], eventcodec.CloneStoredRecords(result.Records)...)
		for _, stored := range result.Records {
			if stored.Record.ID != "" {
				s.project.ids[stored.Record.ID] = stored.Stream
			}
		}
	}
}

func validateAppendRequests(requests []event.AppendRequest) error {
	seen := map[event.StreamID]struct{}{}
	for _, request := range requests {
		if strings.TrimSpace(string(request.Stream)) == "" {
			return fmt.Errorf("natseventstore: stream is empty")
		}
		if _, ok := seen[request.Stream]; ok {
			return fmt.Errorf("natseventstore: duplicate stream %q in append batch", request.Stream)
		}
		seen[request.Stream] = struct{}{}
	}
	return nil
}

func normalizeRequests(requests []event.AppendRequest) ([]normalizedRequest, error) {
	now := time.Now().UTC()
	seenIDs := map[string]event.StreamID{}
	out := make([]normalizedRequest, 0, len(requests))
	for _, request := range requests {
		normalized := normalizedRequest{
			Stream:  request.Stream,
			Options: request.Options,
			Records: make([]event.Record, 0, len(request.Records)),
		}
		for _, record := range request.Records {
			next, err := eventcodec.NormalizeRecord(record, now)
			if err != nil {
				return nil, err
			}
			if existingStream, ok := seenIDs[next.ID]; ok {
				return nil, event.DuplicateRecord{Stream: existingStream, ID: next.ID}
			}
			seenIDs[next.ID] = request.Stream
			normalized.Records = append(normalized.Records, next)
		}
		out = append(out, normalized)
	}
	return out, nil
}

func resolveConfig(cfg Config) resolvedConfig {
	out := resolvedConfig{
		url:              strings.TrimSpace(cfg.URL),
		stream:           strings.TrimSpace(cfg.Stream),
		subject:          strings.TrimSpace(cfg.Subject),
		createStream:     cfg.CreateStream,
		maxAppendRetries: cfg.MaxAppendRetries,
		replayBatchSize:  cfg.ReplayBatchSize,
	}
	if out.url == "" {
		out.url = nats.DefaultURL
	}
	if out.stream == "" {
		out.stream = DefaultStream
	}
	if out.subject == "" {
		out.subject = DefaultSubject
	}
	if out.maxAppendRetries <= 0 {
		out.maxAppendRetries = defaultMaxAppendRetries
	}
	if out.replayBatchSize <= 0 {
		out.replayBatchSize = defaultReplayBatchSize
	}
	return out
}

func newProjection() projection {
	return projection{
		expectedNext: 1,
		streams:      map[event.StreamID][]event.StoredRecord{},
		ids:          map[string]event.StreamID{},
	}
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

type apiErrorer interface {
	APIError() *jetstream.APIError
}

func isWrongLastSequence(err error) bool {
	var apiErr apiErrorer
	if errors.As(err, &apiErr) && apiErr.APIError() != nil {
		return apiErr.APIError().ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence
	}
	return strings.Contains(err.Error(), "wrong last sequence")
}

type batchEnvelope struct {
	Version int                    `json:"version"`
	Time    time.Time              `json:"time"`
	Results []appendResultEnvelope `json:"results"`
}

type appendResultEnvelope struct {
	Stream  event.StreamID         `json:"stream"`
	Records []storedRecordEnvelope `json:"records,omitempty"`
}

type storedRecordEnvelope struct {
	Sequence event.Sequence `json:"sequence"`
	Record   recordEnvelope `json:"record"`
}

type recordEnvelope struct {
	ID            string             `json:"id,omitempty"`
	Name          event.Name         `json:"name"`
	SchemaVersion int                `json:"schema_version,omitempty"`
	Time          time.Time          `json:"time,omitempty"`
	Source        event.Source       `json:"source,omitempty"`
	Scope         event.Scope        `json:"scope,omitempty"`
	Attributes    map[string]string  `json:"attributes,omitempty"`
	Sensitivity   policy.Sensitivity `json:"sensitivity,omitempty"`
	CorrelationID string             `json:"correlation_id,omitempty"`
	CausationID   string             `json:"causation_id,omitempty"`
	Payload       json.RawMessage    `json:"payload,omitempty"`
}

func encodeBatch(results []event.AppendResult) ([]byte, error) {
	envelope := batchEnvelope{
		Version: 1,
		Time:    time.Now().UTC(),
		Results: make([]appendResultEnvelope, 0, len(results)),
	}
	for _, result := range results {
		encodedResult := appendResultEnvelope{Stream: result.Stream}
		for _, stored := range result.Records {
			payload, err := eventcodec.EncodePayload(stored.Record.Payload)
			if err != nil {
				return nil, err
			}
			encodedResult.Records = append(encodedResult.Records, storedRecordEnvelope{
				Sequence: stored.Sequence,
				Record: recordEnvelope{
					ID:            stored.Record.ID,
					Name:          stored.Record.Name,
					SchemaVersion: stored.Record.SchemaVersion,
					Time:          stored.Record.Time,
					Source:        stored.Record.Source,
					Scope:         stored.Record.Scope,
					Attributes:    eventcodec.CloneStringMap(stored.Record.Attributes),
					Sensitivity:   stored.Record.Sensitivity,
					CorrelationID: stored.Record.CorrelationID,
					CausationID:   stored.Record.CausationID,
					Payload:       payload,
				},
			})
		}
		envelope.Results = append(envelope.Results, encodedResult)
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("natseventstore: encode batch: %w", err)
	}
	return data, nil
}

func decodeBatch(data []byte, registry *event.Registry) ([]event.AppendResult, error) {
	var envelope batchEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode batch: %w", err)
	}
	if envelope.Version != 1 {
		return nil, fmt.Errorf("unsupported batch version %d", envelope.Version)
	}
	results := make([]event.AppendResult, 0, len(envelope.Results))
	for _, encodedResult := range envelope.Results {
		result := event.AppendResult{Stream: encodedResult.Stream}
		for _, encoded := range encodedResult.Records {
			payload, err := eventcodec.DecodePayload(registry, encoded.Record.Name, encoded.Record.Payload)
			if err != nil {
				return nil, err
			}
			result.Records = append(result.Records, event.StoredRecord{
				Stream:   encodedResult.Stream,
				Sequence: encoded.Sequence,
				Record: event.Record{
					ID:            encoded.Record.ID,
					Name:          encoded.Record.Name,
					SchemaVersion: encoded.Record.SchemaVersion,
					Time:          encoded.Record.Time,
					Source:        encoded.Record.Source,
					Scope:         encoded.Record.Scope,
					Attributes:    eventcodec.CloneStringMap(encoded.Record.Attributes),
					Sensitivity:   policy.NormalizeSensitivity(encoded.Record.Sensitivity),
					CorrelationID: encoded.Record.CorrelationID,
					CausationID:   encoded.Record.CausationID,
					Payload:       payload,
				},
			})
		}
		results = append(results, result)
	}
	return results, nil
}
