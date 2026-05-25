package operationruntime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
)

func TestStringFieldIgnoresNonStringValues(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
		field string
		want  string
	}{
		{name: "string value", input: map[string]any{"channel": "alerts"}, field: "channel", want: "alerts"},
		{name: "trims whitespace", input: map[string]any{"channel": "  alerts  "}, field: "channel", want: "alerts"},
		{name: "json null", input: map[string]any{"channel": nil}, field: "channel", want: ""},
		{name: "boolean", input: map[string]any{"channel": true}, field: "channel", want: ""},
		{name: "number", input: map[string]any{"channel": 42}, field: "channel", want: ""},
		{name: "missing key", input: map[string]any{"other": "value"}, field: "channel", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stringField(tc.input, tc.field)
			if got != tc.want {
				t.Fatalf("stringField(%v, %q) = %q, want %q", tc.input, tc.field, got, tc.want)
			}
		})
	}
}

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
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{
			{
				Subjects:         []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
				Resources:        []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
				Actions:          []policy.Action{policy.ActionOperationInvoke},
				RequiresApproval: true,
			},
			{
				Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
				Resources: []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
				Actions:   []policy.Action{policy.ActionApprovalGrant},
			},
		}},
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
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{
			{
				Subjects:         []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
				Resources:        []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
				Actions:          []policy.Action{policy.ActionOperationInvoke},
				RequiresApproval: true,
			},
			{
				Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
				Resources: []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
				Actions:   []policy.Action{policy.ActionApprovalGrant},
			},
		}},
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
		t.Fatalf("result = %#v error=%#v, want ok", result, result.Error)
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

func TestAuthorizationGatePrefersTypedAccessDescriptor(t *testing.T) {
	ctx := operation.NewContext(policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "docs/**"}},
			Actions:   []policy.Action{policy.ActionWorkspaceRead},
		}}},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
	}), nil)
	op := NewTypedResult[pathReadInput, operation.Rendered](
		operation.Spec{Ref: operation.Ref{Name: "custom_file_read"}},
		func(operation.Context, pathReadInput) operation.Result { return operation.OK(nil) },
		WithAccess(func(_ operation.Context, input pathReadInput) ([]AccessDescriptor, error) {
			return []AccessDescriptor{{
				Resource: policy.ResourceRef{Kind: policy.ResourcePath, Path: input.Path},
				Action:   policy.ActionWorkspaceRead,
			}}, nil
		}),
	)
	if err := (AuthorizationGate{}).Authorize(ctx, op, map[string]any{"path": "docs/readme.md"}); err != nil {
		t.Fatalf("Authorize docs path: %v", err)
	}
	err := (AuthorizationGate{}).Authorize(ctx, op, map[string]any{"path": "private/notes.md"})
	if err == nil || !strings.Contains(err.Error(), "no_matching_grant") {
		t.Fatalf("Authorize private path error = %v, want no_matching_grant", err)
	}
}

func TestAuthorizationGateFailsClosedWhenTypedAccessFails(t *testing.T) {
	ctx := operation.NewContext(policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources: []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "*"}},
			Actions:   []policy.Action{policy.ActionOperationInvoke},
		}}},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
	}), nil)
	op := NewTypedResult[pathReadInput, operation.Rendered](
		operation.Spec{Ref: operation.Ref{Name: "custom_file_read"}},
		func(operation.Context, pathReadInput) operation.Result { return operation.OK(nil) },
		WithAccess(func(operation.Context, pathReadInput) ([]AccessDescriptor, error) {
			return nil, fmt.Errorf("path is required")
		}),
	)
	err := (AuthorizationGate{}).Authorize(ctx, op, map[string]any{"path": ""})
	if err == nil || !strings.Contains(err.Error(), "access_descriptor_failed") {
		t.Fatalf("Authorize error = %v, want access_descriptor_failed", err)
	}
}

type pathReadInput struct {
	Path string `json:"path"`
}
