package pluginbridge

import (
	"context"
	"encoding/json"
	"testing"

	auth "github.com/fluxplane/fluxplane-auth"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
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

func TestPluginContextProviderInvokesRuntime(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	providers, err := bridge.ContextProviders(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "echo", Instance: "work"}})
	if err != nil {
		t.Fatalf("ContextProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].Spec().Name != "echo.context" {
		t.Fatalf("providers = %#v", providers)
	}
	blocks, err := providers[0].Build(context.Background(), corecontext.Request{InputText: "hello"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Provider != "echo.context" || blocks[0].Kind != corecontext.BlockText || blocks[0].Content != "context: hello" {
		t.Fatalf("blocks = %#v", blocks)
	}
}

func TestPluginDatasourceProviderInvokesRuntime(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	providers, err := bridge.DatasourceProviders(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "echo", Instance: "work"}})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(providers))
	}
	entities := providers[0].Entities()
	if len(entities) != 1 || entities[0].Type != "echo.item" || !entities[0].Supports(coredatasource.EntityCapabilitySearch) {
		t.Fatalf("entities = %#v", entities)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "echo.items", Kind: "echo", Entities: []coredatasource.EntityType{"echo.item"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	searcher, ok := accessor.(coredatasource.Searcher)
	if !ok {
		t.Fatalf("accessor does not implement Searcher: %#v", accessor)
	}
	search, err := searcher.Search(context.Background(), coredatasource.SearchRequest{Entity: "echo.item", Query: "hello", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if search.Datasource != "echo.items" || search.Entity != "echo.item" || len(search.Records) != 1 || search.Records[0].ID != "hello" {
		t.Fatalf("search = %#v", search)
	}
	getter, ok := accessor.(coredatasource.Getter)
	if !ok {
		t.Fatalf("accessor does not implement Getter: %#v", accessor)
	}
	record, err := getter.Get(context.Background(), coredatasource.GetRequest{Entity: "echo.item", ID: "abc"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.Datasource != "echo.items" || record.Entity != "echo.item" || record.ID != "abc" || record.Title != "item abc" {
		t.Fatalf("record = %#v", record)
	}
}

type echoInput struct {
	Text string `json:"text"`
}

type echoOutput struct {
	Text string `json:"text"`
}

type echoDatasourceInput struct {
	Query string `json:"query,omitempty"`
	ID    string `json:"id,omitempty"`
}

type echoDatasourceOutput struct {
	Source  string                           `json:"source,omitempty"`
	Count   int                              `json:"count,omitempty"`
	Records []pluginbinding.DatasourceRecord `json:"records,omitempty"`
	Record  pluginbinding.DatasourceRecord   `json:"record,omitempty"`
}

func testSDKPlugin() *pluginbinding.Plugin {
	inputSchema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)
	datasourceSpec := sdkmanifest.DatasourceSpec{
		Name:         "echo.items",
		Entity:       "echo.item",
		Description:  "Echo items.",
		Capabilities: []string{pluginbinding.CapabilitySearch, pluginbinding.CapabilityGet},
	}
	contextSpec := pluginbinding.ContextSpec("echo.context", "Echo context.", pluginbinding.ContextKindText)
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
			Datasources: []sdkmanifest.DatasourceSpec{datasourceSpec},
			Context:     []sdkmanifest.ContextSpec{contextSpec},
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
		pluginbinding.RegisterDatasourceSearch(datasourceSpec, func(ctx pluginbinding.Context, input echoDatasourceInput) (echoDatasourceOutput, error) {
			record := pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "echo.item", input.Query, pluginbinding.RecordTitle("item "+input.Query))
			return echoDatasourceOutput{Source: "echo.items", Count: 1, Records: []pluginbinding.DatasourceRecord{record}}, nil
		}),
		pluginbinding.RegisterDatasourceGet(datasourceSpec, func(ctx pluginbinding.Context, input echoDatasourceInput) (echoDatasourceOutput, error) {
			record := pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "echo.item", input.ID, pluginbinding.RecordTitle("item "+input.ID))
			return echoDatasourceOutput{Source: "echo.items", Record: record}, nil
		}),
		pluginbinding.RegisterContextProvider(contextSpec, func(_ pluginbinding.Context, input pluginbinding.ContextBuildInput) (pluginbinding.ContextBuildResult, error) {
			return pluginbinding.ContextBuildResult{Blocks: []sdkmanifest.ContextBlock{{
				ID:      "echo/context",
				Kind:    pluginbinding.ContextKindText,
				Content: "context: " + input.Query,
			}}}, nil
		}),
	)
}
