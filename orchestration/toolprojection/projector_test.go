package toolprojection

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/resourceaddr"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
)

func TestProjectIncludesReadOnlyOperationCommand(t *testing.T) {
	op := operation.New(operation.Spec{
		Ref:         operation.Ref{Name: "inspect"},
		Description: "Inspect repository state.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Risk:        operation.RiskLow,
		},
	}, func(operation.Context, operation.Value) operation.Result {
		return operation.OK(nil)
	})
	opID := resource.ResourceID{Kind: "operation", Origin: "embedded", Name: "inspect"}
	commandID := resource.ResourceID{Kind: "command", Origin: "embedded", Name: "inspect"}
	result := Project(Config{
		Operations: session.OperationCatalog{
			opID.Address(): {ID: opID, Operation: op},
		},
		Commands: session.CommandCatalog{
			commandID.Address(): {
				ID:          commandID,
				OperationID: opID,
				Spec: command.Spec{
					Path:        command.Path{"inspect"},
					Description: "Inspect.",
					Target: invocation.Target{
						Kind:      invocation.TargetOperation,
						Operation: operation.Ref{Name: "inspect"},
					},
				},
			},
		},
		Caller: policy.Caller{Kind: policy.CallerAgent},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})

	if len(result.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1: %#v", len(result.Tools), result.Diagnostics)
	}
	if result.Tools[0].Target.Kind != invocation.TargetOperation {
		t.Fatalf("target kind = %q, want operation", result.Tools[0].Target.Kind)
	}
	if result.Tools[0].TargetID != resourceaddr.Address(opID.Address()) {
		t.Fatalf("target id = %s, want %s", result.Tools[0].TargetID, opID.Address())
	}
}

func TestFilterOperationCatalogEnforcesNamedPluginInstances(t *testing.T) {
	opID := resource.ResourceID{Kind: "operation", Origin: "embedded", Name: "gitlab_mr"}
	catalog := session.OperationCatalog{
		opID.Address(): {
			ID: opID,
			Operation: operationruntime.AggregateNamedInstances("gitlab", []operationruntime.NamedInstanceBinding{
				{Instance: "staging", Operation: namedProjectionTestOperation("staging")},
				{Instance: "prod", Operation: namedProjectionTestOperation("prod")},
			}),
		},
	}
	filtered := FilterOperationCatalog(Config{
		NamedPluginInstances: map[string]map[string]bool{"gitlab": {"staging": true}},
	}, catalog)

	binding := filtered[opID.Address()]
	if binding.Operation == nil {
		t.Fatal("filtered operation is nil")
	}
	if result := binding.Operation.Run(operation.NewContext(context.Background(), nil), map[string]any{"instance": "prod"}); !result.IsError() {
		t.Fatalf("prod result = %#v, want error", result)
	}
	result := binding.Operation.Run(operation.NewContext(context.Background(), nil), map[string]any{"instance": "staging"})
	if result.IsError() {
		t.Fatalf("staging result error = %#v", result.Error)
	}
	if result.Output != "staging" {
		t.Fatalf("output = %#v, want staging", result.Output)
	}
}

func namedProjectionTestOperation(output string) operation.Operation {
	return operation.New(operation.Spec{Ref: operation.Ref{Name: "gitlab_mr"}}, func(operation.Context, operation.Value) operation.Result {
		return operation.OK(output)
	})
}

func TestProjectSkipsHiddenCommandButKeepsBareOperation(t *testing.T) {
	op := operation.New(operation.Spec{
		Ref:         operation.Ref{Name: "shell_exec"},
		Description: "Run a command.",
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(operation.Context, operation.Value) operation.Result {
		return operation.OK(nil)
	})
	opID := resource.ResourceID{Kind: "operation", Origin: "embedded", Name: "shell_exec"}
	commandID := resource.ResourceID{Kind: "command", Origin: "embedded", Name: "shell_exec"}
	result := Project(Config{
		Operations: session.OperationCatalog{
			opID.Address(): {ID: opID, Operation: op},
		},
		Commands: session.CommandCatalog{
			commandID.Address(): {
				ID:          commandID,
				OperationID: opID,
				Spec: command.Spec{
					Path:        command.Path{"shell", "exec"},
					Description: "Internal shell dispatch.",
					Target: invocation.Target{
						Kind:      invocation.TargetOperation,
						Operation: operation.Ref{Name: "shell_exec"},
					},
					Annotations: map[string]string{"tool_projection": "hidden"},
				},
			},
		},
		IncludeBareOperations: true,
		AllowSideEffects:      true,
		MaxRisk:               operation.RiskMedium,
		Caller:                policy.Caller{Kind: policy.CallerUser},
		Trust:                 policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})

	if len(result.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1: %#v", len(result.Tools), result.Diagnostics)
	}
	if result.Tools[0].Name != "shell_exec" {
		t.Fatalf("tool name = %q, want shell_exec", result.Tools[0].Name)
	}
}

func TestProjectRejectsSideEffectingOperationByDefault(t *testing.T) {
	op := operation.New(operation.Spec{
		Ref: operation.Ref{Name: "write"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectFilesystem, operation.EffectWriteExternal},
			Risk:    operation.RiskMedium,
		},
	}, func(operation.Context, operation.Value) operation.Result {
		return operation.OK(nil)
	})
	opID := resource.ResourceID{Kind: "operation", Origin: "embedded", Name: "write"}

	result := Project(Config{
		Operations: session.OperationCatalog{
			opID.Address(): {ID: opID, Operation: op},
		},
		IncludeBareOperations: true,
		Caller:                policy.Caller{Kind: policy.CallerAgent},
		Trust:                 policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})

	if len(result.Tools) != 0 {
		t.Fatalf("tools len = %d, want 0", len(result.Tools))
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Reason != "side_effecting_operation" {
		t.Fatalf("diagnostics = %#v, want side_effecting_operation", result.Diagnostics)
	}
}

func TestProjectAppliesCommandPolicy(t *testing.T) {
	id := resource.ResourceID{Kind: "command", Origin: "embedded", Name: "admin"}
	result := Project(Config{
		Commands: session.CommandCatalog{
			id.Address(): {
				ID: id,
				Spec: command.Spec{
					Path:   command.Path{"admin"},
					Target: invocation.Target{Kind: invocation.TargetPrompt, Prompt: "admin"},
					Policy: policy.InvocationPolicy{
						AllowedCallers: []policy.CallerKind{policy.CallerUser},
					},
				},
			},
		},
		Caller: policy.Caller{Kind: policy.CallerAgent},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})

	if len(result.Tools) != 0 {
		t.Fatalf("tools len = %d, want 0", len(result.Tools))
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Reason != "caller_not_allowed" {
		t.Fatalf("diagnostics = %#v, want caller_not_allowed", result.Diagnostics)
	}
}

func TestProjectCanIncludeApprovedBareOperations(t *testing.T) {
	op := operation.New(operation.Spec{
		Ref: operation.Ref{Name: "search"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectReadExternal},
			Risk:    operation.RiskLow,
		},
	}, func(operation.Context, operation.Value) operation.Result {
		return operation.OK(nil)
	})
	opID := resource.ResourceID{Kind: "operation", Origin: "explicit", Name: "search"}
	result := Project(Config{
		Operations: session.OperationCatalog{
			opID.Address(): {ID: opID, Operation: op},
		},
		IncludeBareOperations: true,
		Caller:                policy.Caller{Kind: policy.CallerAgent},
		Trust:                 policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})

	if len(result.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1: %#v", len(result.Tools), result.Diagnostics)
	}
	if result.Tools[0].Name == "" {
		t.Fatal("tool name is empty")
	}
}

func TestProjectRejectsUnboundOperationCommand(t *testing.T) {
	commandID := resource.ResourceID{Kind: "command", Origin: "embedded", Name: "missing"}
	result := Project(Config{
		Commands: session.CommandCatalog{
			commandID.Address(): {
				ID:          commandID,
				OperationID: resource.ResourceID{Kind: "operation", Origin: "embedded", Name: "missing"},
				Spec: command.Spec{
					Path:   command.Path{"missing"},
					Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "missing"}},
				},
			},
		},
		Caller: policy.Caller{Kind: policy.CallerAgent},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})

	if len(result.Tools) != 0 {
		t.Fatalf("tools len = %d, want 0", len(result.Tools))
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Reason != "operation_not_bound" {
		t.Fatalf("diagnostics = %#v, want operation_not_bound", result.Diagnostics)
	}
}

func TestProjectActionToolSetProjectsSingleToolAndCoversOperations(t *testing.T) {
	generate := operation.New(operation.Spec{
		Ref: operation.Ref{Name: "image_generate"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectNetwork, operation.EffectCreate},
			Risk:    operation.RiskMedium,
		},
	}, func(operation.Context, operation.Value) operation.Result { return operation.OK(nil) })
	info := operation.New(operation.Spec{
		Ref: operation.Ref{Name: "image_providers"},
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Risk:        operation.RiskLow,
		},
	}, func(operation.Context, operation.Value) operation.Result { return operation.OK(nil) })
	namespace := resource.NewNamespace("plugins/image")
	generateID := resource.ResourceID{Kind: "operation", Origin: "embedded", Namespace: namespace, Name: "image_generate"}
	infoID := resource.ResourceID{Kind: "operation", Origin: "embedded", Namespace: namespace, Name: "image_providers"}
	setID := resource.ResourceID{Kind: "tool_set", Origin: "embedded", Namespace: namespace, Name: "image"}

	result := Project(Config{
		Operations: session.OperationCatalog{
			generateID.Address(): {ID: generateID, Operation: generate},
			infoID.Address():     {ID: infoID, Operation: info},
		},
		ToolSets: session.ToolSetCatalog{
			setID.Address(): {
				ID: setID,
				Spec: tool.Set{
					Name: "image",
					Action: &tool.ActionProjection{
						Tool:        "image",
						ActionField: "action",
						Cases: []tool.ActionCase{
							{Action: "generate", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_generate"}}},
							{Action: "info", Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: "image_providers"}}},
						},
					},
				},
			},
		},
		IncludeBareOperations: true,
		AllowSideEffects:      true,
		MaxRisk:               operation.RiskMedium,
		Caller:                policy.Caller{Kind: policy.CallerAgent},
		Trust:                 policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})

	if len(result.Tools) != 1 {
		t.Fatalf("tools len = %d, want one action tool: %#v", len(result.Tools), result.Diagnostics)
	}
	if result.Tools[0].Name != "image" || result.Tools[0].Target.Kind != "" {
		t.Fatalf("tool = %#v, want dispatch-only image tool", result.Tools[0])
	}
	if result.Tools[0].Dispatch == nil || len(result.Tools[0].Dispatch.Cases) != 2 {
		t.Fatalf("dispatch = %#v, want two cases", result.Tools[0].Dispatch)
	}
}

func TestProjectAppliesAuthorizationPolicy(t *testing.T) {
	allowed := operation.New(operation.Spec{
		Ref:       operation.Ref{Name: "allowed"},
		Semantics: operation.Semantics{Risk: operation.RiskLow},
	}, nil)
	denied := operation.New(operation.Spec{
		Ref:       operation.Ref{Name: "denied"},
		Semantics: operation.Semantics{Risk: operation.RiskLow},
	}, nil)
	allowedID := resource.ResourceID{Kind: "operation", Origin: "embedded", Name: "allowed"}
	deniedID := resource.ResourceID{Kind: "operation", Origin: "embedded", Name: "denied"}
	result := Project(Config{
		Operations: session.OperationCatalog{
			allowedID.Address(): {ID: allowedID, Operation: allowed},
			deniedID.Address():  {ID: deniedID, Operation: denied},
		},
		IncludeBareOperations: true,
		AllowSideEffects:      true,
		Caller:                policy.Caller{Kind: policy.CallerUser},
		Trust:                 policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		Authorization: policy.AuthorizationContext{
			Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
				Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
				Resources: []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "allowed"}},
				Actions:   []policy.Action{policy.ActionOperationInvoke},
			}}},
			Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		},
	})
	if len(result.Tools) != 1 || result.Tools[0].Name != "allowed" {
		t.Fatalf("tools = %#v diagnostics = %#v, want only allowed", result.Tools, result.Diagnostics)
	}
}
