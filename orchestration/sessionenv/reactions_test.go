package sessionenv

import (
	"context"
	"testing"

	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-event"
)

func TestApplyReactionActionsActivatesDatasourceAndOperationSet(t *testing.T) {
	active := ActiveState{}
	var emitted []event.Event
	result := ApplyReactionActions([]ReactionAction{
		{
			Rule:           "kubernetes-available",
			IdempotencyKey: "opset-key",
			Action: corereaction.Action{
				Kind:         corereaction.ActionEnableOperationSet,
				OperationSet: "kubernetes-tools",
			},
		},
		{
			Rule:           "kubernetes-available",
			IdempotencyKey: "datasource-key",
			Action: corereaction.Action{
				Kind:       corereaction.ActionEnableDatasource,
				Datasource: coredatasource.Ref{Name: "kubernetes"},
			},
		},
		{
			Rule:           "kubernetes-available",
			IdempotencyKey: "context-key",
			Action: corereaction.Action{
				Kind:            corereaction.ActionEnableContext,
				ContextProvider: corecontext.ProviderRef{Name: "kubernetes.context"},
			},
		},
	}, Config{
		Active: &active,
		Events: event.SinkFunc(func(payload event.Event) {
			if payload != nil {
				emitted = append(emitted, payload)
			}
		}),
	})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", result.Diagnostics)
	}
	if len(result.AppliedKeys) != 3 {
		t.Fatalf("applied keys = %#v, want 3", result.AppliedKeys)
	}
	if !active.OperationSets["kubernetes-tools"] {
		t.Fatalf("operation sets = %#v, want kubernetes-tools active", active.OperationSets)
	}
	if !active.Datasources["kubernetes"] {
		t.Fatalf("datasources = %#v, want kubernetes active", active.Datasources)
	}
	if !active.ContextProviders["kubernetes.context"] {
		t.Fatalf("context providers = %#v, want kubernetes.context active", active.ContextProviders)
	}
	if len(emitted) != 3 {
		t.Fatalf("emitted len = %d, want 3", len(emitted))
	}
	applied, ok := emitted[0].(corereaction.ActionApplied)
	if !ok || applied.Target != "kubernetes-tools" {
		t.Fatalf("applied event = %#v, want operation-set target", emitted[0])
	}
	applied, ok = emitted[1].(corereaction.ActionApplied)
	if !ok || applied.Target != "kubernetes" {
		t.Fatalf("applied event = %#v, want datasource target", emitted[1])
	}
	applied, ok = emitted[2].(corereaction.ActionApplied)
	if !ok || applied.Target != "kubernetes.context" {
		t.Fatalf("applied event = %#v, want context-provider target", emitted[2])
	}
}

func TestContextProviderContextIncludesActiveDatasources(t *testing.T) {
	active := ActiveState{}
	active.EnableDatasource("kubernetes")
	ctx := ContextProviderContext(context.Background(), Config{Active: &active}, nil)
	policy, ok := coredatasource.AccessPolicyFromContext(ctx)
	if !ok {
		t.Fatal("access policy missing")
	}
	if len(policy.Datasources) != 1 || policy.Datasources[0] != "kubernetes" {
		t.Fatalf("datasources = %#v, want kubernetes", policy.Datasources)
	}
}
