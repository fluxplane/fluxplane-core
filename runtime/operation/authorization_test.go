package operationruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
)

func TestAuthorizationGateDeniesMissingGrant(t *testing.T) {
	ctx := operation.NewContext(policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "someoneelse@localhost"}},
			Resources: []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
			Actions:   []policy.Action{policy.ActionOperationInvoke},
		}}},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
	}), nil)
	op := operation.New(operation.Spec{Ref: operation.Ref{Name: "file_read"}}, nil)
	err := (AuthorizationGate{}).Authorize(ctx, op, nil)
	if err == nil || !strings.Contains(err.Error(), "no_matching_grant") {
		t.Fatalf("Authorize error = %v, want no_matching_grant", err)
	}
}

func TestAuthorizationGateAllowsOperationInvokeGrant(t *testing.T) {
	ctx := operation.NewContext(policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources: []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
			Actions:   []policy.Action{policy.ActionOperationInvoke},
		}}},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
	}), nil)
	op := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, nil)
	if err := (AuthorizationGate{}).Authorize(ctx, op, nil); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
}

func TestAuthorizationGateReturnsApprovalRequired(t *testing.T) {
	ctx := operation.NewContext(policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:         []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources:        []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
			Actions:          []policy.Action{policy.ActionOperationInvoke},
			RequiresApproval: true,
		}}},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
	}), nil)
	op := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, nil)
	err := (AuthorizationGate{}).Authorize(ctx, op, nil)
	if _, ok := err.(AuthorizationApprovalRequired); !ok {
		t.Fatalf("Authorize error = %T %v, want AuthorizationApprovalRequired", err, err)
	}
}

func TestSafetyEnvelopeRoutesAuthorizationApproval(t *testing.T) {
	approval := recordingApproval{}
	ctx := operation.NewContext(policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:         []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources:        []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
			Actions:          []policy.Action{policy.ActionOperationInvoke},
			RequiresApproval: true,
		}}},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
	}), nil)
	op := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(operation.Context, operation.Value) operation.Result {
		return operation.OK("ok")
	})
	result := NewExecutor(WithSafetyGate(SafetyEnvelope{
		ACL:       AuthorizationGate{},
		Approval:  &approval,
		AllowPure: true,
	})).Execute(ctx, op, nil)
	if result.IsError() {
		t.Fatalf("result = %#v, want ok", result)
	}
	if approval.calls != 1 || approval.last.Action != policy.ActionOperationInvoke {
		t.Fatalf("approval = calls %d last %#v, want authorization approval", approval.calls, approval.last)
	}
}

func TestAuthorizationGateChecksDatasourceTarget(t *testing.T) {
	ctx := operation.NewContext(policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:  []policy.SubjectRef{{Kind: policy.SubjectGroup, ID: "docs"}},
			Resources: []policy.ResourceRef{{Kind: policy.ResourceDatasource, Name: "local_docs"}},
			Actions:   []policy.Action{policy.ActionDatasourceRead},
		}}},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectGroup, ID: "docs"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	}), nil)
	op := operation.New(operation.Spec{Ref: operation.Ref{Name: "datasource_get"}}, nil)
	if err := (AuthorizationGate{}).Authorize(ctx, op, map[string]any{"datasource": "local_docs"}); err != nil {
		t.Fatalf("Authorize local_docs: %v", err)
	}
	err := (AuthorizationGate{}).Authorize(ctx, op, map[string]any{"datasource": "private_docs"})
	if err == nil || !strings.Contains(err.Error(), "no_matching_grant") {
		t.Fatalf("Authorize private_docs error = %v, want no_matching_grant", err)
	}
}

func TestAuthorizationGateAllowsDatasourceSearchWhenAnyDatasourceGranted(t *testing.T) {
	ctx := operation.NewContext(policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:  []policy.SubjectRef{{Kind: policy.SubjectGroup, ID: "docs"}},
			Resources: []policy.ResourceRef{{Kind: policy.ResourceDatasource, Name: "local_docs"}},
			Actions:   []policy.Action{policy.ActionDatasourceSearch},
		}}},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectGroup, ID: "docs"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	}), nil)
	op := operation.New(operation.Spec{Ref: operation.Ref{Name: "datasource_search"}}, nil)
	if err := (AuthorizationGate{}).Authorize(ctx, op, map[string]any{"query": "security"}); err != nil {
		t.Fatalf("Authorize datasource_search: %v", err)
	}
}
