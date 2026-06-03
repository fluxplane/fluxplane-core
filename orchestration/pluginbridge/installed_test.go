package pluginbridge

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-plugin/management"
	"github.com/fluxplane/fluxplane-plugin/pluginruntime"
)

func TestLoadInstalledPluginsResolveThroughCorePluginHost(t *testing.T) {
	store := fakeInstalledStore{
		plugins: []management.Plugin{
			{Ref: management.Ref{Name: "echo"}, Installed: true, Enabled: true, Runtime: management.RuntimeSpec{Kind: "direct"}},
			{Ref: management.Ref{Name: "disabled"}, Installed: true, Enabled: false, Runtime: management.RuntimeSpec{Kind: "direct"}},
		},
		instances: []management.Instance{
			{Plugin: management.Ref{Name: "echo"}, Name: management.DefaultInstance, Enabled: true},
			{Plugin: management.Ref{Name: "echo"}, Name: "work", Enabled: true, Config: map[string]any{"team": "runtime"}},
			{Plugin: management.Ref{Name: "echo"}, Name: "off", Enabled: false},
			{Plugin: management.Ref{Name: "disabled"}, Name: management.DefaultInstance, Enabled: true},
		},
	}
	result, err := LoadInstalled(
		context.Background(),
		WithInstalledStore(store),
		WithInstalledRuntimeFactory(fakeInstalledRuntime),
	)
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if len(result.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(result.Plugins))
	}
	if got, want := pluginRefKeys(result.Refs), []string{"echo/default", "echo/work"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("refs = %#v, want %#v", got, want)
	}
	if len(result.Bundle.Plugins) != 2 || result.Bundle.Source.ID != "fluxplane-plugin:installed" {
		t.Fatalf("bundle = %#v", result.Bundle)
	}
	if result.Bundle.Plugins[1].Config["team"] != "runtime" {
		t.Fatalf("work ref config = %#v", result.Bundle.Plugins[1].Config)
	}

	host, err := contributions.New(result.Plugins...)
	if err != nil {
		t.Fatalf("contributions.New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), result.Bundle.Plugins...)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Bundles) != 2 || len(resolution.Operations) != 2 || len(resolution.DatasourceProviders) != 2 {
		t.Fatalf("resolution = %#v", resolution)
	}
	runEcho := resolution.Operations[1].Operation.Run(operation.NewContext(context.Background(), nil), map[string]any{"text": "installed"})
	if runEcho.Status != operation.StatusOK {
		t.Fatalf("operation status = %s error = %#v", runEcho.Status, runEcho.Error)
	}
	output, ok := runEcho.Output.(map[string]any)
	if !ok || output["text"] != "installed!" {
		t.Fatalf("operation output = %#v", runEcho.Output)
	}
}

func TestLoadInstalledPluginsCanFilterByName(t *testing.T) {
	store := fakeInstalledStore{
		plugins: []management.Plugin{
			{Ref: management.Ref{Name: "echo"}, Installed: true, Enabled: true, Runtime: management.RuntimeSpec{Kind: "direct"}},
			{Ref: management.Ref{Name: "other"}, Installed: true, Enabled: true, Runtime: management.RuntimeSpec{Kind: "direct"}},
		},
	}
	result, err := LoadInstalled(
		context.Background(),
		WithInstalledStore(store),
		WithInstalledRuntimeFactory(fakeInstalledRuntime),
		WithInstalledPluginNames("echo"),
	)
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if got, want := pluginRefKeys(result.Refs), []string{"echo/default"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("refs = %#v, want %#v", got, want)
	}
}

func TestLoadInstalledPluginsReportsBrokenRecordsAsDiagnostics(t *testing.T) {
	store := fakeInstalledStore{
		plugins: []management.Plugin{
			{Ref: management.Ref{Name: "broken"}, Installed: true, Enabled: true, Runtime: management.RuntimeSpec{Kind: "direct"}},
			{Ref: management.Ref{Name: "echo"}, Installed: true, Enabled: true, Runtime: management.RuntimeSpec{Kind: "direct"}},
		},
	}
	result, err := LoadInstalled(
		context.Background(),
		WithInstalledStore(store),
		WithInstalledRuntimeFactory(func(plugin management.Plugin) (pluginruntime.Plugin, error) {
			if plugin.Ref.Name == "broken" {
				return nil, fmt.Errorf("boom")
			}
			return fakeInstalledRuntime(plugin)
		}),
	)
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if got, want := pluginRefKeys(result.Refs), []string{"echo/default"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("refs = %#v, want %#v", got, want)
	}
	if len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, `installed plugin "broken" runtime: boom`) {
		t.Fatalf("diagnostics = %#v, want broken runtime diagnostic", result.Diagnostics)
	}
	if len(result.Bundle.Diagnostics) != 1 {
		t.Fatalf("bundle diagnostics = %#v, want copied diagnostic", result.Bundle.Diagnostics)
	}
}

func TestLoadInstalledPluginsImportsStoredEndpoints(t *testing.T) {
	store := fakeInstalledStore{
		plugins: []management.Plugin{
			{Ref: management.Ref{Name: "echo"}, Installed: true, Enabled: true, Runtime: management.RuntimeSpec{Kind: "direct"}},
		},
		endpoints: []fpendpoint.Record{{
			EndpointRef: fpendpoint.EndpointRef{ID: "gitlab-main", URL: "https://gitlab.example.test", Product: "gitlab"},
		}},
	}
	registry := fpendpoint.NewRegistry(0)
	if _, err := LoadInstalled(
		context.Background(),
		WithInstalledStore(store),
		WithInstalledRuntimeFactory(fakeInstalledRuntime),
		WithInstalledEndpointRegistry(registry),
	); err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	resolved, ok := registry.Resolve(fpendpoint.NewRef("gitlab-main"))
	if !ok || resolved.URL != "https://gitlab.example.test" || resolved.Metadata["product"] != "gitlab" {
		t.Fatalf("resolved endpoint ok=%v endpoint=%#v", ok, resolved)
	}
}

func TestStdioRuntimeFromInstalledPluginRejectsUnsupportedRuntime(t *testing.T) {
	_, err := StdioRuntimeFromInstalledPlugin(management.Plugin{
		Ref:     management.Ref{Name: "echo"},
		Runtime: management.RuntimeSpec{Kind: "wasm", Command: "echo"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported runtime kind") {
		t.Fatalf("err = %v, want unsupported runtime kind", err)
	}
}

type fakeInstalledStore struct {
	plugins   []management.Plugin
	instances []management.Instance
	endpoints []fpendpoint.Record
}

func (s fakeInstalledStore) ListPlugins(context.Context, management.ListRequest) ([]management.Plugin, error) {
	return append([]management.Plugin(nil), s.plugins...), nil
}

func (s fakeInstalledStore) PluginStatus(context.Context, management.StatusRequest) (management.StatusResult, error) {
	return management.StatusResult{
		Plugins:   append([]management.Plugin(nil), s.plugins...),
		Instances: append([]management.Instance(nil), s.instances...),
	}, nil
}

func (s fakeInstalledStore) SetPluginEnabled(context.Context, management.SetEnabledRequest) (management.SetEnabledResult, error) {
	return management.SetEnabledResult{}, nil
}

func (s fakeInstalledStore) RemovePlugin(context.Context, management.RemoveRequest) (management.RemoveResult, error) {
	return management.RemoveResult{}, nil
}

func (s fakeInstalledStore) ListEndpoints(context.Context, management.EndpointListRequest) (management.EndpointListResult, error) {
	return management.EndpointListResult{Records: append([]fpendpoint.Record(nil), s.endpoints...)}, nil
}

func (s fakeInstalledStore) GetEndpoint(context.Context, management.EndpointGetRequest) (management.EndpointGetResult, error) {
	return management.EndpointGetResult{}, nil
}

func (s fakeInstalledStore) SaveEndpoint(context.Context, management.EndpointSaveRequest) (management.EndpointSaveResult, error) {
	return management.EndpointSaveResult{}, nil
}

func (s fakeInstalledStore) SaveEndpointHealth(context.Context, management.EndpointHealthRequest) (management.EndpointHealthResult, error) {
	return management.EndpointHealthResult{}, nil
}

func (s fakeInstalledStore) RemoveEndpoint(context.Context, management.EndpointRemoveRequest) (management.EndpointRemoveResult, error) {
	return management.EndpointRemoveResult{}, nil
}

func fakeInstalledRuntime(plugin management.Plugin) (pluginruntime.Plugin, error) {
	if plugin.Ref.Name != "echo" {
		return nil, fmt.Errorf("unexpected runtime load for %q", plugin.Ref.Name)
	}
	return pluginruntime.Direct(testSDKPlugin()), nil
}

func pluginRefKeys(refs []resource.PluginRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.Key())
	}
	return out
}
