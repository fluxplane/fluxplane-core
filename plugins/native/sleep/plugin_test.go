package sleep

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
)

func TestPluginContributesSleepOperation(t *testing.T) {
	bundle, err := New().Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Operations) != 1 || bundle.Operations[0].Ref.Name != Op {
		t.Fatalf("operations = %#v, want sleep", bundle.Operations)
	}
}

func TestSleepHandlerRejectsInvalidDuration(t *testing.T) {
	for _, duration := range []float64{-1, math.NaN(), math.Inf(1)} {
		result := handler(operation.NewContext(context.Background(), nil), Input{Duration: duration})
		if result.Status != operation.StatusFailed || result.Error == nil || result.Error.Code != "invalid_sleep_duration" {
			t.Fatalf("handler(%v) = %#v, want invalid duration failure", duration, result)
		}
	}
}

func TestSleepHandlerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := handler(operation.NewContext(ctx, nil), Input{Duration: time.Second.Seconds()})
	if result.Status != operation.StatusCanceled {
		t.Fatalf("handler canceled = %#v, want canceled", result)
	}
}
