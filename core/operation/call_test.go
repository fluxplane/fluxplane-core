package operation

import (
	"context"
	"testing"

	"github.com/fluxplane/engine/core/event"
)

func TestWithCallIDPreservesEventSink(t *testing.T) {
	sink := event.SinkFunc(func(event.Event) {})
	ctx := WithCallID(NewContext(context.Background(), sink), "call-1")

	if got := CallIDFromContext(ctx); got != "call-1" {
		t.Fatalf("call id = %q, want call-1", got)
	}
	if ctx.Events() == nil {
		t.Fatal("events sink is nil")
	}
}
