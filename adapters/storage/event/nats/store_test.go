package nats

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/fluxplane/fluxplane-event"
)

type messageAdded struct {
	Text string `json:"text,omitempty"`
}

func (messageAdded) EventName() event.Name { return "message.added" }

func TestStoreAppendLoad(t *testing.T) {
	store := newTestStore(t, "append-load")
	ctx := context.Background()
	if _, err := store.Append(ctx, "test", event.ExpectSequence(0), event.Record{
		Payload:    messageAdded{Text: "hello"},
		Attributes: map[string]string{"k": "v"},
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	loaded, err := store.Load(ctx, "test", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("len(loaded) = %d, want 1", len(loaded))
	}
	payload, ok := loaded[0].Record.Payload.(messageAdded)
	if !ok {
		t.Fatalf("payload type = %T, want messageAdded", loaded[0].Record.Payload)
	}
	if payload.Text != "hello" {
		t.Fatalf("payload text = %q, want hello", payload.Text)
	}
	if loaded[0].Sequence != 1 {
		t.Fatalf("sequence = %d, want 1", loaded[0].Sequence)
	}
	if loaded[0].Record.Attributes["k"] != "v" {
		t.Fatalf("attribute k = %q, want v", loaded[0].Record.Attributes["k"])
	}
}

func TestStoreAppendConflict(t *testing.T) {
	store := newTestStore(t, "append-conflict")
	ctx := context.Background()
	if _, err := store.Append(ctx, "test", event.ExpectSequence(0), event.Record{Payload: messageAdded{Text: "one"}}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	_, err := store.Append(ctx, "test", event.ExpectSequence(0), event.Record{Payload: messageAdded{Text: "two"}})
	if err == nil {
		t.Fatal("Append returned nil error, want conflict")
	}
	if !errors.Is(err, event.ErrAppendConflict) {
		t.Fatalf("error = %v, want append conflict", err)
	}
}

func TestStoreDuplicateRecord(t *testing.T) {
	store := newTestStore(t, "duplicate-record")
	ctx := context.Background()
	record := event.Record{ID: "stable-id", Payload: messageAdded{Text: "one"}}
	if _, err := store.Append(ctx, "a", event.ExpectSequence(0), record); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	_, err := store.Append(ctx, "b", event.ExpectSequence(0), record)
	if err == nil {
		t.Fatal("Append returned nil error, want duplicate")
	}
	if !errors.Is(err, event.ErrDuplicateRecord) {
		t.Fatalf("error = %v, want duplicate record", err)
	}
}

func TestStoreAppendBatchIsAtomic(t *testing.T) {
	store := newTestStore(t, "append-batch-atomic")
	ctx := context.Background()
	_, err := store.AppendBatch(ctx,
		event.AppendRequest{
			Stream:  "a",
			Options: event.ExpectSequence(0),
			Records: []event.Record{{Payload: messageAdded{Text: "a"}}},
		},
		event.AppendRequest{
			Stream:  "b",
			Options: event.ExpectSequence(1),
			Records: []event.Record{{Payload: messageAdded{Text: "b"}}},
		},
	)
	if err == nil {
		t.Fatal("AppendBatch returned nil error, want conflict")
	}
	if !errors.Is(err, event.ErrAppendConflict) {
		t.Fatalf("error = %v, want append conflict", err)
	}
	loaded, err := store.Load(ctx, "a", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("len(loaded) = %d, want 0", len(loaded))
	}
}

func TestStoreLoadOptions(t *testing.T) {
	store := newTestStore(t, "load-options")
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := store.Append(ctx, "test", event.AppendOptions{}, event.Record{Payload: messageAdded{Text: fmt.Sprintf("%d", i+1)}}); err != nil {
			t.Fatalf("Append %d returned error: %v", i, err)
		}
	}
	loaded, err := store.Load(ctx, "test", event.LoadOptions{After: 1, Before: 5, Limit: 2})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := sequences(loaded); got != "2,3" {
		t.Fatalf("sequences = %s, want 2,3", got)
	}
	loaded, err = store.Load(ctx, "test", event.LoadOptions{Direction: event.DirectionBackward, Limit: 2})
	if err != nil {
		t.Fatalf("Load backward returned error: %v", err)
	}
	if got := sequences(loaded); got != "5,4" {
		t.Fatalf("backward sequences = %s, want 5,4", got)
	}
}

func TestStoreReopenReplaysJetStreamLog(t *testing.T) {
	ctx := context.Background()
	env := natsEnv(t)
	registry := testRegistry(t)
	cfg := Config{Stream: uniqueName(t, "reopen"), Subject: uniqueSubject(t, "reopen"), CreateStream: true}
	first, err := OpenWithConnection(ctx, env.conn, cfg, registry)
	if err != nil {
		t.Fatalf("OpenWithConnection first: %v", err)
	}
	if _, err := first.Append(ctx, "test", event.ExpectSequence(0), event.Record{Payload: messageAdded{Text: "hello"}}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	second, err := OpenWithConnection(ctx, env.conn, cfg, registry)
	if err != nil {
		t.Fatalf("OpenWithConnection second: %v", err)
	}
	loaded, err := second.Load(ctx, "test", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Sequence != 1 {
		t.Fatalf("loaded = %#v, want replayed record", loaded)
	}
}

func TestStoreCrossInstanceVisibility(t *testing.T) {
	ctx := context.Background()
	env := natsEnv(t)
	registry := testRegistry(t)
	cfg := Config{Stream: uniqueName(t, "cross-instance"), Subject: uniqueSubject(t, "cross-instance"), CreateStream: true}
	first, err := OpenWithConnection(ctx, env.conn, cfg, registry)
	if err != nil {
		t.Fatalf("OpenWithConnection first: %v", err)
	}
	second, err := OpenWithConnection(ctx, env.conn, cfg, registry)
	if err != nil {
		t.Fatalf("OpenWithConnection second: %v", err)
	}
	if _, err := first.Append(ctx, "test", event.AppendOptions{}, event.Record{Payload: messageAdded{Text: "one"}}); err != nil {
		t.Fatalf("first Append returned error: %v", err)
	}
	if _, err := second.Append(ctx, "test", event.AppendOptions{}, event.Record{Payload: messageAdded{Text: "two"}}); err != nil {
		t.Fatalf("second Append returned error: %v", err)
	}
	loaded, err := first.Load(ctx, "test", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := sequences(loaded); got != "1,2" {
		t.Fatalf("sequences = %s, want 1,2", got)
	}
}

func TestStoreConcurrentAppends(t *testing.T) {
	store := newTestStore(t, "concurrent-appends")
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.Append(ctx, "test", event.AppendOptions{}, event.Record{Payload: messageAdded{Text: fmt.Sprintf("%d", i)}})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Append returned error: %v", err)
		}
	}
	loaded, err := store.Load(ctx, "test", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 10 {
		t.Fatalf("len(loaded) = %d, want 10", len(loaded))
	}
	for i, stored := range loaded {
		want := event.Sequence(i + 1)
		if stored.Sequence != want {
			t.Fatalf("record %d sequence = %d, want %d", i, stored.Sequence, want)
		}
	}
}

func TestStoreRejectsEmptyStream(t *testing.T) {
	store := newTestStore(t, "empty-stream")
	_, err := store.Append(context.Background(), "", event.AppendOptions{}, event.Record{Payload: messageAdded{Text: "bad"}})
	if err == nil || !strings.Contains(err.Error(), "stream is empty") {
		t.Fatalf("Append error = %v, want empty stream", err)
	}
}

func TestResolveConfigDefaultsAndOverrides(t *testing.T) {
	defaults := resolveConfig(Config{})
	if defaults.url != nats.DefaultURL || defaults.stream != DefaultStream || defaults.subject != DefaultSubject {
		t.Fatalf("defaults = %#v, want NATS default URL and event stream defaults", defaults)
	}
	if defaults.maxAppendRetries != defaultMaxAppendRetries || defaults.replayBatchSize != defaultReplayBatchSize {
		t.Fatalf("defaults = %#v, want retry and replay defaults", defaults)
	}
	custom := resolveConfig(Config{
		URL:              " nats://example:4222 ",
		Stream:           " EVENTS ",
		Subject:          " events.log ",
		CreateStream:     true,
		MaxAppendRetries: 3,
		ReplayBatchSize:  10,
	})
	if custom.url != "nats://example:4222" || custom.stream != "EVENTS" || custom.subject != "events.log" || !custom.createStream {
		t.Fatalf("custom = %#v, want trimmed overrides", custom)
	}
	if custom.maxAppendRetries != 3 || custom.replayBatchSize != 10 {
		t.Fatalf("custom retry/replay = %#v, want explicit values", custom)
	}
}

func TestPureProjectionPrepareLoadAndPreconditions(t *testing.T) {
	store := &Store{project: newProjection()}
	store.project.loaded = true
	requests, err := normalizeRequests([]event.AppendRequest{{
		Stream:  "test",
		Options: event.ExpectSequence(0),
		Records: []event.Record{{ID: "one", Name: "message.added"}, {ID: "two", Name: "message.added"}},
	}})
	if err != nil {
		t.Fatalf("normalizeRequests: %v", err)
	}
	results, err := store.prepareResults(requests)
	if err != nil {
		t.Fatalf("prepareResults: %v", err)
	}
	if got := sequences(results[0].Records); got != "1,2" {
		t.Fatalf("prepared sequences = %s, want 1,2", got)
	}
	store.applyResults(results)
	if err := store.preconditionError([]normalizedRequest{{
		Stream:  "test",
		Options: event.ExpectSequence(1),
		Records: []event.Record{{ID: "three", Name: "message.added"}},
	}}); !errors.Is(err, event.ErrAppendConflict) {
		t.Fatalf("precondition conflict = %v, want append conflict", err)
	}
	if err := store.preconditionError([]normalizedRequest{{
		Stream:  "other",
		Records: []event.Record{{ID: "one", Name: "message.added"}},
	}}); !errors.Is(err, event.ErrDuplicateRecord) {
		t.Fatalf("precondition duplicate = %v, want duplicate", err)
	}
	if got := sequences(store.project.streams["test"]); got != "1,2" {
		t.Fatalf("projected sequences = %s, want 1,2", got)
	}
}

func TestNormalizeRequestsAndBatchCodec(t *testing.T) {
	_, err := normalizeRequests([]event.AppendRequest{
		{Stream: "a", Records: []event.Record{{ID: "same", Name: "message.added"}}},
		{Stream: "b", Records: []event.Record{{ID: "same", Name: "message.added"}}},
	})
	if !errors.Is(err, event.ErrDuplicateRecord) {
		t.Fatalf("normalize duplicate error = %v, want duplicate record", err)
	}
	if err := validateAppendRequests([]event.AppendRequest{{Stream: "a"}, {Stream: "a"}}); err == nil || !strings.Contains(err.Error(), "duplicate stream") {
		t.Fatalf("validate duplicate stream error = %v, want duplicate stream", err)
	}
	results := []event.AppendResult{{
		Stream: "test",
		Records: []event.StoredRecord{{
			Stream:   "test",
			Sequence: 7,
			Record: event.Record{
				ID:          "stable",
				Name:        "message.added",
				Attributes:  map[string]string{"k": "v"},
				Sensitivity: event.SensitivitySecret,
			},
		}},
	}}
	data, err := encodeBatch(results)
	if err != nil {
		t.Fatalf("encodeBatch: %v", err)
	}
	decoded, err := decodeBatch(data, nil)
	if err != nil {
		t.Fatalf("decodeBatch: %v", err)
	}
	if len(decoded) != 1 || len(decoded[0].Records) != 1 {
		t.Fatalf("decoded = %#v, want one result and record", decoded)
	}
	record := decoded[0].Records[0]
	if record.Sequence != 7 || record.Record.Attributes["k"] != "v" || record.Record.Sensitivity != event.SensitivitySecret {
		t.Fatalf("decoded record = %#v, want metadata preserved", record)
	}
	if _, err := decodeBatch([]byte(`{"version":2}`), nil); err == nil || !strings.Contains(err.Error(), "unsupported batch version") {
		t.Fatalf("decode version error = %v, want unsupported version", err)
	}
}

func TestIsWrongLastSequenceHandlesNilAPIError(t *testing.T) {
	if isWrongLastSequence(jetstream.ErrNoStreamResponse) {
		t.Fatal("ErrNoStreamResponse classified as wrong last sequence")
	}
	if isWrongLastSequence(jetstream.ErrInvalidJSAck) {
		t.Fatal("ErrInvalidJSAck classified as wrong last sequence")
	}
}

func TestIsWrongLastSequenceRecognizesAPIError(t *testing.T) {
	err := &jetstream.APIError{ErrorCode: jetstream.JSErrCodeStreamWrongLastSequence, Code: 400}
	if !isWrongLastSequence(err) {
		t.Fatal("wrong last sequence API error was not recognized")
	}
}

func TestStoreAppendReturnsPublishErrorForUnboundSubject(t *testing.T) {
	ctx := context.Background()
	env := natsEnv(t)
	registry := testRegistry(t)
	cfg := Config{Stream: uniqueName(t, "unbound-subject"), Subject: uniqueSubject(t, "unbound-subject"), CreateStream: true}
	if _, err := OpenWithConnection(ctx, env.conn, cfg, registry); err != nil {
		t.Fatalf("OpenWithConnection setup: %v", err)
	}
	store, err := OpenWithConnection(ctx, env.conn, Config{Stream: cfg.Stream, Subject: cfg.Subject + ".missing"}, registry)
	if err != nil {
		t.Fatalf("OpenWithConnection misconfigured subject: %v", err)
	}
	_, err = store.Append(ctx, "test", event.ExpectSequence(0), event.Record{Payload: messageAdded{Text: "bad"}})
	if err == nil {
		t.Fatal("Append returned nil error, want publish error")
	}
	if strings.Contains(err.Error(), "append conflict") {
		t.Fatalf("Append error = %v, want storage publish error", err)
	}
}

func TestStoreRejectsTruncatedJetStreamHistory(t *testing.T) {
	ctx := context.Background()
	env := natsEnv(t)
	registry := testRegistry(t)
	cfg := Config{Stream: uniqueName(t, "truncated-history"), Subject: uniqueSubject(t, "truncated-history"), CreateStream: true}
	js, err := jetstream.New(env.conn)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     cfg.Stream,
		Subjects: []string{cfg.Subject},
		MaxMsgs:  2,
		Storage:  jetstream.MemoryStorage,
	}); err != nil {
		t.Fatalf("CreateOrUpdateStream: %v", err)
	}
	cfg.CreateStream = false
	store, err := OpenWithConnection(ctx, env.conn, cfg, registry)
	if err != nil {
		t.Fatalf("OpenWithConnection: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.Append(ctx, "test", event.AppendOptions{}, event.Record{Payload: messageAdded{Text: fmt.Sprintf("%d", i)}}); err != nil {
			t.Fatalf("Append %d returned error: %v", i, err)
		}
	}
	reopened, err := OpenWithConnection(ctx, env.conn, Config{Stream: cfg.Stream, Subject: cfg.Subject}, registry)
	if err != nil {
		t.Fatalf("OpenWithConnection reopen: %v", err)
	}
	_, err = reopened.Load(ctx, "test", event.LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "history is truncated") {
		t.Fatalf("Load error = %v, want truncated history error", err)
	}
}

type testNATSEnv struct {
	conn *nats.Conn
}

func newTestStore(t *testing.T, name string) *Store {
	t.Helper()
	env := natsEnv(t)
	store, err := OpenWithConnection(context.Background(), env.conn, Config{
		Stream:       uniqueName(t, name),
		Subject:      uniqueSubject(t, name),
		CreateStream: true,
	}, testRegistry(t))
	if err != nil {
		t.Fatalf("OpenWithConnection: %v", err)
	}
	return store
}

func natsEnv(t *testing.T) testNATSEnv {
	t.Helper()
	if os.Getenv("TEST_INTEGRATION") != "1" {
		t.Skip("set TEST_INTEGRATION=1 to run NATS JetStream testcontainers event store tests")
	}
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "nats:2.11-alpine",
		Cmd:          []string{"-js"},
		ExposedPorts: []string{"4222/tcp"},
		WaitingFor:   wait.ForListeningPort("4222/tcp"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("nats container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "4222/tcp")
	if err != nil {
		t.Fatalf("container mapped port: %v", err)
	}
	conn, err := nats.Connect(fmt.Sprintf("nats://%s:%s", host, port.Port()))
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(conn.Close)
	return testNATSEnv{conn: conn}
}

func testRegistry(t *testing.T) *event.Registry {
	t.Helper()
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	return registry
}

func uniqueName(t *testing.T, name string) string {
	t.Helper()
	return strings.ToUpper(strings.ReplaceAll("AR_"+name+"_"+strings.ReplaceAll(t.Name(), "/", "_"), "-", "_"))
}

func uniqueSubject(t *testing.T, name string) string {
	t.Helper()
	return "fluxplane.tests." + strings.ReplaceAll(strings.ToLower(name+"."+strings.ReplaceAll(t.Name(), "/", ".")), "_", ".")
}

func sequences(records []event.StoredRecord) string {
	parts := make([]string, 0, len(records))
	for _, record := range records {
		parts = append(parts, fmt.Sprintf("%d", record.Sequence))
	}
	return strings.Join(parts, ",")
}
