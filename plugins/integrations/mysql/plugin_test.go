package mysql

import (
	"context"
	"strings"
	"testing"
	"time"

	coreendpoint "github.com/fluxplane/fluxplane-core/core/endpoint"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	runtimeendpoint "github.com/fluxplane/fluxplane-core/runtime/endpoint"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	coreevent "github.com/fluxplane/fluxplane-event"
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

func TestMySQLTargetFromJSONSecretAndDatabaseOverride(t *testing.T) {
	target, ok, err := targetFromSecret(`{"username":"app","password":"secret","host":"mysql.internal","port":3307,"database":"default"}`, "override")
	if err != nil {
		t.Fatalf("targetFromSecret() error = %v", err)
	}
	if !ok {
		t.Fatal("targetFromSecret() ok = false, want true")
	}
	if target.Database != "override" || target.SafeURL != "mysql://mysql.internal:3307/override" {
		t.Fatalf("target = %#v, want override database and credential-free safe URL", target)
	}
	if !strings.Contains(target.DSN, "app:secret@tcp(") || !strings.Contains(target.DSN, "parseTime=true") {
		t.Fatalf("target DSN = %q, want app credentials and parseTime", target.DSN)
	}
}

func TestMySQLTargetFromEndpointURLAndPasswordSecret(t *testing.T) {
	target, err := mysqlTargetFrom(resolvedEndpoint{URL: "mysql://db.example.com/app"}, "password-from-secret", "")
	if err != nil {
		t.Fatalf("mysqlTargetFrom() error = %v", err)
	}
	if target.Database != "app" || target.SafeURL != "mysql://db.example.com:3306/app" {
		t.Fatalf("target = %#v, want endpoint host and database", target)
	}
	if !strings.Contains(target.DSN, "root:password-from-secret@tcp(") {
		t.Fatalf("target DSN = %q, want default root user and secret password", target.DSN)
	}
}

func TestMySQLDriverDSNParsingAndRedaction(t *testing.T) {
	target, ok, err := targetFromSecret("user:pass@tcp(mysql.internal:3307)/app?parseTime=true", "")
	if err != nil {
		t.Fatalf("targetFromSecret() error = %v", err)
	}
	if !ok {
		t.Fatal("targetFromSecret() ok = false, want true")
	}
	if target.Database != "app" || target.SafeURL != "mysql://mysql.internal:3307)/app?parseTime=true" {
		t.Fatalf("target = %#v, want parsed database and redacted driver DSN", target)
	}
	if got := databaseFromDriverDSN("user:pass@tcp(mysql.internal:3307)/app?parseTime=true"); got != "app" {
		t.Fatalf("databaseFromDriverDSN() = %q, want app", got)
	}
	if got := redactDSN("not-a-dsn"); got != "mysql://redacted" {
		t.Fatalf("redactDSN() = %q, want generic redaction", got)
	}
}

func TestMySQLHelpersValidateAndRedact(t *testing.T) {
	if _, err := mysqlTargetFrom(resolvedEndpoint{URL: "mysql:///app"}, "", ""); err == nil || !strings.Contains(err.Error(), "no host") {
		t.Fatalf("mysqlTargetFrom missing host error = %v, want no host", err)
	}
	if got, err := duration("", 5*time.Second); err != nil || got != 5*time.Second {
		t.Fatalf("duration fallback = %v, %v; want 5s nil", got, err)
	}
	if _, err := duration("nope", time.Second); err == nil || !strings.Contains(err.Error(), "invalid timeout") {
		t.Fatalf("duration invalid error = %v, want invalid timeout", err)
	}
	if got := redactError(context.Canceled); got != context.Canceled.Error() {
		t.Fatalf("redactError safe = %q, want context canceled", got)
	}
	if got := redactError(assertErr("dial tcp mysql://user:pass@host/db")); got != "mysql operation failed" {
		t.Fatalf("redactError secret = %q, want generic message", got)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func assertAccess(t *testing.T, access []operationruntime.AccessDescriptor, kind policy.ResourceKind, name string, action policy.Action) {
	t.Helper()
	for _, descriptor := range access {
		if descriptor.Resource.Kind == kind && descriptor.Resource.Name == name && descriptor.Action == action {
			return
		}
	}
	t.Fatalf("access missing %s:%s %s in %#v", kind, name, action, access)
}
