package pluginhost

import (
	"context"
	"strings"
	"testing"

	coresecret "github.com/fluxplane/fluxplane-auth/authsecret"
	"github.com/fluxplane/fluxplane-core/core/resource"
)

func TestResolveAuthTargetsPreservesInstancesAndConfig(t *testing.T) {
	targets, err := ResolveAuthTargets(context.Background(), []resource.PluginRef{
		{Name: "issue", Instance: "company-b", Config: map[string]any{"prefix": "b"}},
		{Name: "issue", Instance: "company-a", Config: map[string]any{"prefix": "a"}},
	}, []Plugin{authFactoryPlugin{name: "issue"}})
	if err != nil {
		t.Fatalf("ResolveAuthTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets len = %d, want 2", len(targets))
	}
	if targets[0].Ref.Key() != "issue/company-a" || targets[1].Ref.Key() != "issue/company-b" {
		t.Fatalf("target order = %q, %q", targets[0].Ref.Key(), targets[1].Ref.Key())
	}
	if targets[0].Context.Config.(authTargetConfig).Prefix != "a" {
		t.Fatalf("context config = %#v, want prefix a", targets[0].Context.Config)
	}
	if targets[0].Plugin.Manifest().Name != "a/company-a" {
		t.Fatalf("resolved plugin name = %q, want a/company-a", targets[0].Plugin.Manifest().Name)
	}
	if targets[0].Methods[0].Secret.Slot != "company-a_token" {
		t.Fatalf("secret = %#v, want company-a_token", targets[0].Methods[0].Secret)
	}
}

func TestResolveAuthTargetsSkipsNonAuthPlugins(t *testing.T) {
	targets, err := ResolveAuthTargets(context.Background(), []resource.PluginRef{
		{Name: "plain"},
		{Name: "auth"},
	}, []Plugin{plainPlugin{name: "plain"}, authPlugin{name: "auth"}})
	if err != nil {
		t.Fatalf("ResolveAuthTargets: %v", err)
	}
	if len(targets) != 1 || targets[0].Ref.Name != "auth" {
		t.Fatalf("targets = %#v, want auth only", targets)
	}
}

func TestResolveAuthTargetsRejectsMissingPlugin(t *testing.T) {
	_, err := ResolveAuthTargets(context.Background(), []resource.PluginRef{{Name: "missing"}}, []Plugin{authPlugin{name: "auth"}})
	if err == nil || !strings.Contains(err.Error(), `pluginhost: plugin "missing" is not available`) {
		t.Fatalf("ResolveAuthTargets error = %v, want missing plugin", err)
	}
}

func TestResolveAuthTargetsRejectsInvalidMethod(t *testing.T) {
	_, err := ResolveAuthTargets(context.Background(), []resource.PluginRef{{Name: "bad"}}, []Plugin{invalidAuthPlugin{name: "bad"}})
	if err == nil || !strings.Contains(err.Error(), "auth method") {
		t.Fatalf("ResolveAuthTargets error = %v, want auth method validation", err)
	}
}

type authTargetConfig struct {
	Prefix string `json:"prefix"`
}

type authFactoryPlugin struct {
	Configurable[authTargetConfig]
	name string
}

func (p authFactoryPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p authFactoryPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p authFactoryPlugin) Instantiate(_ context.Context, ctx Context) (Plugin, error) {
	cfg, err := ConfigAs[authTargetConfig](ctx)
	if err != nil {
		return nil, err
	}
	return authPlugin{name: cfg.Prefix + "/" + ctx.Ref.InstanceName()}, nil
}

type authPlugin struct {
	name string
}

func (p authPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p authPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p authPlugin) AuthMethods(_ context.Context, ctx Context) ([]coresecret.AuthMethodSpec, error) {
	return []coresecret.AuthMethodSpec{{
		Name:   "token",
		Method: coresecret.AuthMethodStored,
		Kind:   coresecret.KindBearerToken,
		Secret: coresecret.Plugin(ctx.Ref.Name, ctx.Ref.InstanceName(), ctx.Ref.InstanceName()+"_token"),
	}}, nil
}

type invalidAuthPlugin struct {
	name string
}

func (p invalidAuthPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p invalidAuthPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p invalidAuthPlugin) AuthMethods(context.Context, Context) ([]coresecret.AuthMethodSpec, error) {
	return []coresecret.AuthMethodSpec{{Name: "broken", Method: coresecret.AuthMethodStored}}, nil
}

type plainPlugin struct {
	name string
}

func (p plainPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p plainPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}
