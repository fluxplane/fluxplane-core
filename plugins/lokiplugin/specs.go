package lokiplugin

import (
	"github.com/fluxplane/agentruntime/core/operation"
)

func testSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: TestOp},
		Description: "Test a Loki endpoint and report readiness/build information.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskMedium,
		},
	}
}

func labelsSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: LabelsOp},
		Description: "List Loki label names or values for a bounded time window.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskMedium,
		},
	}
}

func querySpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: QueryOp},
		Description: "Run a bounded Loki LogQL range query.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskMedium,
		},
	}
}

func recentLogsSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: RecentLogsOp},
		Description: "Fetch recent Loki logs using namespace/app/pod/container filters.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskMedium,
		},
	}
}
