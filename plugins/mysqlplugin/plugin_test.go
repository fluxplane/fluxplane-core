package mysqlplugin

import (
	"context"
	"testing"

	coreendpoint "github.com/fluxplane/agentruntime/core/endpoint"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	runtimeendpoint "github.com/fluxplane/agentruntime/runtime/endpoint"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

func TestQueryAccessUsesResolvedEndpointAndSecretRef(t *testing.T) {
	endpoints := runtimeendpoint.NewRegistry(0)
	secretRef := coresecret.Kubernetes("latest", "backend-db", "dsn").ResourceName()
	ref, err := endpoints.Put(runtimeendpoint.Record{Spec: coreendpoint.Spec{
		Name:    "mysql-backend",
		URL:     "mysql://mysql.latest.svc:3306/app",
		Product: "mysql",
		AuthRef: secretRef,
	}})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	plugin := Plugin{endpoints: endpoints}
	access, err := plugin.queryAccess(operation.NewContext(context.Background(), coreevent.Discard()), QueryInput{EndpointRef: string(ref)})
	if err != nil {
		t.Fatalf("queryAccess() error = %v", err)
	}
	assertAccess(t, access, policy.ResourceNetwork, "mysql://mysql.latest.svc:3306/app", policy.ActionNetworkConnect)
	assertAccess(t, access, policy.ResourceSecret, secretRef, policy.ActionSecretUse)
}

func TestMySQLTargetFromSecretDSNRedactsCredentials(t *testing.T) {
	target, err := mysqlTargetFrom(resolvedEndpoint{}, "mysql://user:pass@mysql.latest.svc:3306/app?sslmode=disable", "")
	if err != nil {
		t.Fatalf("mysqlTargetFrom() error = %v", err)
	}
	if target.DSN == "" || target.SafeURL != "mysql://mysql.latest.svc:3306/app" || target.Database != "app" {
		t.Fatalf("target = %#v, want dsn plus credential-free safe URL", target)
	}
	if target.SafeURL == target.DSN {
		t.Fatalf("SafeURL leaked DSN: %#v", target)
	}
}

func assertAccess(t *testing.T, access []operationruntime.AccessDescriptor, kind policy.ResourceKind, name string, action policy.Action) {
	t.Helper()
	for _, descriptor := range access {
		if descriptor.Resource.Kind == kind && descriptor.Resource.Name == name && descriptor.Action == action {
			return
		}
	}
	t.Fatalf("access missing %s:%s %s in %#v", kind, name, action, access)
}
