package aws

import (
	"context"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	"testing"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
)

func TestAWSPluginContributesObserverAndDeriver(t *testing.T) {
	ctx := context.Background()
	plugin := NewWithEnvironment(fakeEnvironment{
		"AWS_PROFILE":           "dev-profile",
		"AWS_REGION":            "us-east-1",
		"AWS_ACCESS_KEY_ID":     "AKIAEXAMPLE",
		"AWS_SECRET_ACCESS_KEY": "secret",
		"AWS_SESSION_TOKEN":     "token",
	})
	ref := resource.PluginRef{Name: Name, Instance: "dev"}
	instantiated, err := plugin.Instantiate(ctx, pluginhost.Context{
		Ref:    ref,
		Config: Config{},
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	resolved := instantiated.(Plugin)
	bundle, err := resolved.Contributions(ctx, pluginhost.Context{Ref: ref})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Observers) != 1 || bundle.Observers[0].Name != observerName {
		t.Fatalf("observers = %#v, want AWS observer spec", bundle.Observers)
	}
	if len(bundle.AssertionDerivers) != 1 || bundle.AssertionDerivers[0].Name != deriverName {
		t.Fatalf("assertion derivers = %#v, want AWS deriver spec", bundle.AssertionDerivers)
	}
	observers, err := resolved.EnvironmentObservers(ctx, pluginhost.Context{Ref: ref})
	if err != nil {
		t.Fatalf("EnvironmentObservers: %v", err)
	}
	observations, diagnostics := runtimeevidence.RunObservers(ctx, observers, runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	if len(observations) != 1 || observations[0].Kind != ObservationAWSEnvironment {
		t.Fatalf("observations = %#v, want AWS environment observation", observations)
	}
	content := observations[0].Content.(map[string]any)
	if content["profile"] != "dev-profile" || content["region"] != "us-east-1" {
		t.Fatalf("content = %#v, want profile and region", content)
	}
	if content["access_key_configured"] != true || content["secret_key_configured"] != true || content["session_token_configured"] != true {
		t.Fatalf("content = %#v, want credential presence booleans", content)
	}
	for _, secret := range []string{"AKIAEXAMPLE", "secret", "token"} {
		if containsValue(content, secret) {
			t.Fatalf("content leaked secret value %q: %#v", secret, content)
		}
	}
	derivers, err := resolved.AssertionDerivers(ctx, pluginhost.Context{Ref: ref})
	if err != nil {
		t.Fatalf("AssertionDerivers: %v", err)
	}
	assertions, diagnostics := runtimeevidence.DeriveAssertions(ctx, derivers, runtimeevidence.AssertionDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("assertion diagnostics = %#v, want none", diagnostics)
	}
	if !hasAssertion(assertions, "integration.configured", Name) || !hasAssertion(assertions, "integration.available", Name) {
		t.Fatalf("assertions = %#v, want configured and available AWS assertions", assertions)
	}
}

func TestAWSObserverTreatsRegionOnlyAsConfiguredNotAvailable(t *testing.T) {
	ctx := context.Background()
	plugin := Plugin{
		environment: fakeEnvironment{"AWS_REGION": "eu-central-1"},
		ref:         resource.PluginRef{Name: Name},
		cfg:         Config{},
	}
	observations, diagnostics := runtimeevidence.RunObservers(ctx, []runtimeevidence.Observer{observer{plugin: plugin}}, runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	assertions, diagnostics := runtimeevidence.DeriveAssertions(ctx, []runtimeevidence.AssertionDeriver{assertionDeriver{}}, runtimeevidence.AssertionDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("assertion diagnostics = %#v, want none", diagnostics)
	}
	if !hasAssertion(assertions, "integration.configured", Name) {
		t.Fatalf("assertions = %#v, want configured AWS assertion", assertions)
	}
	if hasAssertion(assertions, "integration.available", Name) {
		t.Fatalf("assertions = %#v, want no available AWS assertion for region-only config", assertions)
	}
}

type fakeEnvironment map[string]string

func (e fakeEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e[key]
	return value, ok, nil
}

type fakeSystem struct {
	env fpsystem.Environment
}

func (s fakeSystem) Workspace() runtimeworkspace.Workspace { return nil }
func (s fakeSystem) Network() fpsystem.Network             { return nil }
func (s fakeSystem) Process() fpsystem.ProcessManager      { return nil }
func (s fakeSystem) Environment() fpsystem.Environment     { return s.env }
func (s fakeSystem) FileSystem() fpsystem.FileSystem       { return nil }
func (s fakeSystem) Clock() fpsystem.Clock {
	sys, _ := systemkit.NewSystem().WithRealClock().Build()
	return sys.Clock()
}

func hasAssertion(assertions []coreevidence.Assertion, kind, target string) bool {
	for _, assertion := range assertions {
		if assertion.Kind == kind && assertion.Target == target {
			return true
		}
	}
	return false
}

func containsValue(content map[string]any, value string) bool {
	for _, candidate := range content {
		if candidate == value {
			return true
		}
	}
	return false
}
