package awsplugin

import (
	"context"
	"testing"

	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestAWSPluginContributesObserverAndDeriver(t *testing.T) {
	ctx := context.Background()
	plugin := New(fakeSystem{env: fakeEnvironment{
		"AWS_PROFILE":           "dev-profile",
		"AWS_REGION":            "us-east-1",
		"AWS_ACCESS_KEY_ID":     "AKIAEXAMPLE",
		"AWS_SECRET_ACCESS_KEY": "secret",
		"AWS_SESSION_TOKEN":     "token",
	}})
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
	if len(bundle.SignalDerivers) != 1 || bundle.SignalDerivers[0].Name != deriverName {
		t.Fatalf("signal derivers = %#v, want AWS deriver spec", bundle.SignalDerivers)
	}
	observers, err := resolved.EnvironmentObservers(ctx, pluginhost.Context{Ref: ref})
	if err != nil {
		t.Fatalf("EnvironmentObservers: %v", err)
	}
	observations, diagnostics := runtimeenvironment.RunObservers(ctx, observers, runtimeenvironment.ObservationRequest{Phase: coreenvironment.PhaseTurn})
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
	derivers, err := resolved.SignalDerivers(ctx, pluginhost.Context{Ref: ref})
	if err != nil {
		t.Fatalf("SignalDerivers: %v", err)
	}
	signals, diagnostics := runtimeenvironment.DeriveSignals(ctx, derivers, runtimeenvironment.SignalDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("signal diagnostics = %#v, want none", diagnostics)
	}
	if !hasSignal(signals, "integration.configured", Name) || !hasSignal(signals, "integration.available", Name) {
		t.Fatalf("signals = %#v, want configured and available AWS signals", signals)
	}
}

func TestAWSObserverTreatsRegionOnlyAsConfiguredNotAvailable(t *testing.T) {
	ctx := context.Background()
	plugin := Plugin{
		system: fakeSystem{env: fakeEnvironment{"AWS_REGION": "eu-central-1"}},
		ref:    resource.PluginRef{Name: Name},
		cfg:    Config{},
	}
	observations, diagnostics := runtimeenvironment.RunObservers(ctx, []runtimeenvironment.Observer{observer{plugin: plugin}}, runtimeenvironment.ObservationRequest{Phase: coreenvironment.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	signals, diagnostics := runtimeenvironment.DeriveSignals(ctx, []runtimeenvironment.SignalDeriver{signalDeriver{}}, runtimeenvironment.SignalDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("signal diagnostics = %#v, want none", diagnostics)
	}
	if !hasSignal(signals, "integration.configured", Name) {
		t.Fatalf("signals = %#v, want configured AWS signal", signals)
	}
	if hasSignal(signals, "integration.available", Name) {
		t.Fatalf("signals = %#v, want no available AWS signal for region-only config", signals)
	}
}

type fakeEnvironment map[string]string

func (e fakeEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e[key]
	return value, ok, nil
}

type fakeSystem struct {
	env system.Environment
}

func (s fakeSystem) Workspace() system.Workspace     { return nil }
func (s fakeSystem) Network() system.Network         { return nil }
func (s fakeSystem) Process() system.ProcessManager  { return nil }
func (s fakeSystem) Browser() system.BrowserManager  { return nil }
func (s fakeSystem) Clarifier() system.Clarifier     { return nil }
func (s fakeSystem) Environment() system.Environment { return s.env }

func hasSignal(signals []coreenvironment.Signal, kind, target string) bool {
	for _, signal := range signals {
		if signal.Kind == kind && signal.Target == target {
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
