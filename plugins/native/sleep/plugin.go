package sleep

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
)

const (
	Name = "sleep"
	Op   = "sleep"
)

// Plugin contributes an interruptible local wait operation.
type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns the sleep plugin.
func New() Plugin { return Plugin{} }

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Interruptible local wait operation."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Operations: []operation.Spec{Spec()},
	}, nil
}

func (Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operationruntime.NewTypedResult[Input, map[string]any](Spec(), handler),
	}, nil
}

type Input struct {
	Duration float64 `json:"duration" jsonschema:"description=Sleep duration in seconds.,required"`
}

// Spec returns the local sleep operation spec.
func Spec() operation.Spec {
	return operationruntime.WithTypedContract[Input, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: Op},
		Description: "Sleep for a duration in seconds without spawning a shell process. The wait is interruptible by cancellation.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func handler(ctx operation.Context, req Input) operation.Result {
	if math.IsNaN(req.Duration) || math.IsInf(req.Duration, 0) {
		return operation.Failed("invalid_sleep_duration", "duration must be a finite number of seconds", nil)
	}
	if req.Duration < 0 {
		return operation.Failed("invalid_sleep_duration", "duration must be greater than or equal to zero", nil)
	}
	if req.Duration > float64(math.MaxInt64)/float64(time.Second) {
		return operation.Failed("invalid_sleep_duration", "duration is too large", nil)
	}

	duration := time.Duration(req.Duration * float64(time.Second))
	started := time.Now()
	if duration > 0 {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return operation.Canceled("sleep interrupted")
		case <-timer.C:
		}
	}

	elapsed := time.Since(started)
	data := map[string]any{
		"duration": req.Duration,
		"elapsed":  elapsed.Seconds(),
	}
	text := fmt.Sprintf("Slept %.3fs", req.Duration)
	return operation.OK(operation.Rendered{Text: text, Data: data})
}
