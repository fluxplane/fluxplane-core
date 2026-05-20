package image

import (
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/tool"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

func generateSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: GenerateOp},
		Description: "Generate an image from a text prompt.",
		Input:       operationruntime.TypeOf[GenerateRequest]("ImageGenerateInput"),
		Output:      operationruntime.TypeOf[GenerateResult]("ImageGenerateOutput"),
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectFilesystem, operation.EffectCreate},
			Idempotency: operation.IdempotencyUnknown,
			Risk:        operation.RiskMedium,
		},
	}
}

func understandSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: UnderstandOp},
		Description: "Analyze one or more images using a vision-capable provider.",
		Input:       operationruntime.TypeOf[UnderstandRequest]("ImageUnderstandInput"),
		Output:      operationruntime.TypeOf[UnderstandResult]("ImageUnderstandOutput"),
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyUnknown,
			Risk:        operation.RiskMedium,
		},
	}
}

func providersSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: ProvidersOp},
		Description: "List image providers and their configuration state.",
		Input:       operationruntime.TypeOf[providersInput]("ImageProvidersInput"),
		Output:      operationruntime.TypeOf[providersOutput]("ImageProvidersOutput"),
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	}
}

func imageToolSet() tool.Set {
	return tool.Set{
		Name:        Name,
		Description: "One image tool with action variants for generation, understanding, and provider info.",
		Tools:       []tool.Name{Name},
		Action: &tool.ActionProjection{
			Tool:        Name,
			Description: "Generate images, understand images, or inspect configured image providers.",
			ActionField: "action",
			Input:       operationruntime.TypeOf[imageActionInput]("ImageToolInput"),
			Output:      operationruntime.TypeOf[map[string]any]("ImageToolOutput"),
			Cases: []tool.ActionCase{
				{Action: "generate", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: GenerateOp}}},
				{Action: "understand", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: UnderstandOp}}},
				{Action: "info", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: ProvidersOp}}},
			},
		},
	}
}
