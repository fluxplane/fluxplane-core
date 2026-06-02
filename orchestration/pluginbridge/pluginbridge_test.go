package pluginbridge

import (
	"context"
	"encoding/json"
	"testing"

	auth "github.com/fluxplane/fluxplane-auth"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/pluginruntime"
	"github.com/fluxplane/fluxplane-secret"
)

func TestPluginContributesManifestOperations(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bundle, err := bridge.Contributions(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "echo"}})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Operations) != 1 {
		t.Fatalf("operations len = %d, want 1", len(bundle.Operations))
	}
	spec := bundle.Operations[0]
	if spec.Ref.Name != "echo.say" || spec.Input.Schema.Format != "json-schema" {
		t.Fatalf("operation spec = %#v", spec)
	}
	if !spec.Semantics.Effects.Has(operation.EffectReadExternal) || spec.Semantics.Risk != operation.RiskLow {
		t.Fatalf("semantics = %#v", spec.Semantics)
	}
	if len(bundle.OperationSets) != 1 || bundle.OperationSets[0].Name != "echo" {
		t.Fatalf("operation sets = %#v", bundle.OperationSets)
	}
	if len(bundle.Datasources) != 1 || bundle.Datasources[0].Name != "echo.items" {
		t.Fatalf("datasources = %#v", bundle.Datasources)
	}
}

func TestPluginContributesAuthMethods(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	methods, err := bridge.AuthMethods(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "echo"}})
	if err != nil {
		t.Fatalf("AuthMethods: %v", err)
	}
	if len(methods) != 1 || methods[0].Name != "token" || methods[0].Method != auth.MethodStored {
		t.Fatalf("methods = %#v", methods)
	}
}

func TestPluginOperationInvokesRuntime(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ops, err := bridge.Operations(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "echo", Instance: "work"}})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("operations len = %d, want 1", len(ops))
	}
	result := ops[0].Run(operation.NewContext(context.Background(), nil), map[string]any{"text": "hello"})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v error = %#v", result, result.Error)
	}
	output, ok := result.Output.(map[string]any)
	if !ok || output["text"] != "hello!" {
		t.Fatalf("output = %#v", result.Output)
	}
}

type echoInput struct {
	Text string `json:"text"`
}

type echoOutput struct {
	Text string `json:"text"`
}

func testSDKPlugin() *pluginbinding.Plugin {
	inputSchema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)
	return pluginbinding.Define(
		pluginbinding.ManifestSpec{
			Name:        "echo",
			Description: "Echo test plugin.",
			Auth: []sdkmanifest.AuthMethod{{
				Name: "token",
				Kind: secret.KindBearerToken,
				Fields: []sdkmanifest.AuthField{{
					Name:      "token",
					Required:  true,
					Sensitive: true,
				}},
			}},
			Datasources: []sdkmanifest.DatasourceSpec{{
				Name:        "echo.items",
				Entity:      "echo.item",
				Description: "Echo items.",
			}},
		},
		pluginbinding.RegisterOperation(
			sdkmanifest.OperationSpec{
				Name:        "echo.say",
				Description: "Echo text.",
				Input:       inputSchema,
				ReadOnly:    true,
				Effects:     []sdkmanifest.OperationEffect{sdkmanifest.OperationEffectRead},
				Risk:        sdkmanifest.OperationRiskLow,
				Idempotency: sdkmanifest.OperationIdempotent,
			},
			func(_ pluginbinding.Context, input echoInput) (echoOutput, error) {
				return echoOutput{Text: input.Text + "!"}, nil
			},
		),
	)
}
