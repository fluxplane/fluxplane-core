package discovery

import "github.com/fluxplane/fluxplane-operation"

func statusSpec() operation.Spec {
	return introspectionSpec(StatusOp, "Summarize discovery provider status and known endpoints.")
}

func discoverSpec() operation.Spec {
	return introspectionSpec(DiscoverOp, "Trigger an asynchronous endpoint discovery refresh.")
}

func providersSpec() operation.Spec {
	return introspectionSpec(ProvidersOp, "List registered endpoint discovery providers.")
}

func endpointListSpec() operation.Spec {
	return introspectionSpec(EndpointListOp, "List known endpoint refs and non-secret metadata.")
}

func endpointGetSpec() operation.Spec {
	return introspectionSpec(EndpointGetOp, "Resolve one endpoint ref to non-secret metadata.")
}

func introspectionSpec(name, description string) operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	}
}
