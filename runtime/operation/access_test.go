package operationruntime

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-policy"
)

func TestAccessFieldsBuildsTypedDescriptors(t *testing.T) {
	handler := AccessFields[pathPairInput](
		PathAccess(func(input pathPairInput) string { return input.Src }, policy.ActionWorkspaceRead),
		PathAccess(func(input pathPairInput) string { return input.Dst }, policy.ActionWorkspaceWrite),
	)
	access, err := handler(operation.NewContext(context.Background(), nil), pathPairInput{Src: "docs/in.md", Dst: "docs/out.md"})
	if err != nil {
		t.Fatalf("AccessFields: %v", err)
	}
	if len(access) != 2 {
		t.Fatalf("access length = %d, want 2", len(access))
	}
	if access[0].Resource.Path != "docs/in.md" || access[0].Action != policy.ActionWorkspaceRead {
		t.Fatalf("read descriptor = %#v", access[0])
	}
	if access[1].Resource.Path != "docs/out.md" || access[1].Action != policy.ActionWorkspaceWrite {
		t.Fatalf("write descriptor = %#v", access[1])
	}
}

func TestAccessFieldDefaults(t *testing.T) {
	handler := AccessFields[pathListInput](
		PathListAccess(func(input pathListInput) []string { return input.Paths }, policy.ActionWorkspaceRead, AccessDefault(".")),
		DatasourceAccess(func(pathListInput) string { return "" }, policy.ActionDatasourceSearch),
		TaskAccess(func(pathListInput) string { return "" }, policy.ActionTaskRun),
	)
	access, err := handler(operation.NewContext(context.Background(), nil), pathListInput{})
	if err != nil {
		t.Fatalf("AccessFields: %v", err)
	}
	if len(access) != 3 {
		t.Fatalf("access length = %d, want 3", len(access))
	}
	if access[0].Resource.Path != "." {
		t.Fatalf("path default = %#v, want .", access[0].Resource)
	}
	if access[1].Resource.Name != "*" {
		t.Fatalf("datasource default = %#v, want *", access[1].Resource)
	}
	if access[2].Resource.ID != "*" {
		t.Fatalf("task default = %#v, want *", access[2].Resource)
	}
}

type pathPairInput struct {
	Src string
	Dst string
}

type pathListInput struct {
	Paths []string
}
