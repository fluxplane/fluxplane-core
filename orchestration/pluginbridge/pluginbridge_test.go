package pluginbridge

import (
	"context"
	"encoding/json"
	"testing"

	auth "github.com/fluxplane/fluxplane-auth"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	coreevidence "github.com/fluxplane/fluxplane-evidence"
	"github.com/fluxplane/fluxplane-operation"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/pluginruntime"
	"github.com/fluxplane/fluxplane-plugin/protocol"
	"github.com/fluxplane/fluxplane-secret"
)

func TestPluginContributesManifestOperations(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bundle, err := bridge.Contributions(context.Background(), contributions.Context{Ref: resource.PluginRef{Name: "echo"}})
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
	if len(bundle.Observers) != 1 || bundle.Observers[0].Name != "echo.environment" {
		t.Fatalf("observers = %#v", bundle.Observers)
	}
	if len(bundle.AssertionDerivers) != 1 || bundle.AssertionDerivers[0].Name != "echo.environment.assertions" {
		t.Fatalf("assertion derivers = %#v", bundle.AssertionDerivers)
	}
}

func TestPluginContributesAuthMethods(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	methods, err := bridge.AuthMethods(context.Background(), contributions.Context{Ref: resource.PluginRef{Name: "echo"}})
	if err != nil {
		t.Fatalf("AuthMethods: %v", err)
	}
	if len(methods) != 1 || methods[0].Name != "token" || methods[0].Method != auth.MethodStored {
		t.Fatalf("methods = %#v", methods)
	}
}

func TestPluginAuthTestInvokesRuntime(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reports := make(chan contributions.AuthTestReport, 1)
	err = bridge.TestConnection(
		context.Background(),
		contributions.Context{Ref: resource.PluginRef{Name: "echo", Instance: "work"}},
		contributions.AuthTestRequest{
			Ref:    resource.PluginRef{Name: "echo", Instance: "work"},
			Method: "token",
			Secrets: secret.ResolverFunc(func(_ context.Context, ref secret.Ref) (secret.Material, bool, error) {
				if ref != secret.Plugin("echo", "work", "token") {
					return secret.Material{}, false, nil
				}
				return secret.Material{Ref: ref, Value: []byte("abc123")}, true, nil
			}),
		},
		reports,
	)
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	report := <-reports
	if report.Plugin != "echo" || report.Instance != "work" || report.Method != "token" || report.Status != "ok" || report.Details["token"] != "abc123" {
		t.Fatalf("report = %#v", report)
	}
}

func TestPluginOperationInvokesRuntime(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ops, err := bridge.Operations(context.Background(), contributions.Context{Ref: resource.PluginRef{Name: "echo", Instance: "work"}})
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
	providers, err := bridge.ContextProviders(context.Background(), contributions.Context{Ref: resource.PluginRef{Name: "echo", Instance: "work"}})
	if err != nil {
		t.Fatalf("ContextProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].Spec().Name != "echo.context" {
		t.Fatalf("providers = %#v", providers)
	}
	blocks, err := providers[0].Build(context.Background(), corecontext.Request{
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		Reason:    corecontext.RenderTurn,
		InputText: "hello",
		Scope:     map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Provider != "echo.context" || blocks[0].Kind != corecontext.BlockText || blocks[0].Content != "context: hello thread-1 turn-1 turn prod" {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].Placement != corecontext.PlacementSystem || blocks[0].MediaType != "text/plain" || blocks[0].Freshness != corecontext.FreshnessDynamic {
		t.Fatalf("block metadata = %#v", blocks[0])
	}
}

func TestPluginDatasourceProviderInvokesRuntime(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	providers, err := bridge.DatasourceProviders(context.Background(), contributions.Context{Ref: resource.PluginRef{Name: "echo", Instance: "work"}})
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

func TestPluginEvidenceObserverInvokesRuntime(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	observers, err := bridge.EnvironmentObservers(context.Background(), contributions.Context{Ref: resource.PluginRef{Name: "echo", Instance: "work"}})
	if err != nil {
		t.Fatalf("EnvironmentObservers: %v", err)
	}
	if len(observers) != 1 || observers[0].Spec().Name != "echo.environment" {
		t.Fatalf("observers = %#v", observers)
	}
	observations, diagnostics := runtimeevidence.RunObservers(context.Background(), observers, runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(observations) != 1 {
		t.Fatalf("observations = %#v", observations)
	}
	observation := observations[0]
	if observation.Kind != "echo.ready" || observation.Source != "echo.environment" || observation.Environment.Name != "echo" {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestPluginAssertionDeriversUseManifestTemplates(t *testing.T) {
	bridge, err := New(pluginruntime.Direct(testSDKPlugin()), testSDKPlugin().Manifest())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	derivers, err := bridge.AssertionDerivers(context.Background(), contributions.Context{Ref: resource.PluginRef{Name: "echo", Instance: "work"}})
	if err != nil {
		t.Fatalf("AssertionDerivers: %v", err)
	}
	assertions, diagnostics := runtimeevidence.DeriveAssertions(context.Background(), derivers, runtimeevidence.AssertionDeriveRequest{
		Observations: []coreevidence.Observation{{
			ID:          "echo:ready",
			Kind:        "echo.ready",
			Scope:       "integration:echo",
			Environment: coreevidence.Ref{Name: "echo"},
		}},
	})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(assertions) != 1 {
		t.Fatalf("assertions = %#v", assertions)
	}
	assertion := assertions[0]
	if assertion.Kind != "integration.available" || assertion.Target != "echo" || assertion.Source != "echo.environment.assertions" {
		t.Fatalf("assertion = %#v", assertion)
	}
	if len(assertion.ObservationIDs) != 1 || assertion.ObservationIDs[0] != "echo:ready" {
		t.Fatalf("assertion observations = %#v", assertion.ObservationIDs)
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
	observerSpec := sdkmanifest.ObserverSpec{
		Name:            "echo.environment",
		Description:     "Echo environment.",
		Environment:     coreevidence.Ref{Name: "echo"},
		Phase:           coreevidence.PhaseTurn,
		ObservableKinds: []string{"echo.ready"},
		Dynamic:         true,
	}
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
			Observers:   []sdkmanifest.ObserverSpec{observerSpec},
			AssertionDerivers: []sdkmanifest.AssertionDeriverSpec{{
				Name:             "echo.environment.assertions",
				Description:      "Echo assertions.",
				ObservationKinds: []string{"echo.ready"},
				Assertions: []sdkmanifest.AssertionTemplate{{
					Kind:    "integration.available",
					Target:  "echo",
					Subject: coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: "echo"},
				}},
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
				ID:        "echo/context",
				Kind:      pluginbinding.ContextKindText,
				Placement: corecontext.PlacementSystem,
				Content:   "context: " + input.Query + " " + input.ThreadID + " " + input.TurnID + " " + string(input.Reason) + " " + input.Scope["env"],
				MediaType: "text/plain",
				Freshness: corecontext.FreshnessDynamic,
			}}}, nil
		}),
		pluginbinding.RegisterEvidenceObserver(observerSpec, func(ctx pluginbinding.Context, _ pluginbinding.EvidenceObserveInput) (pluginbinding.EvidenceObserveResult, error) {
			return pluginbinding.EvidenceObserveResult{Observations: []coreevidence.Observation{{
				ID:      "echo:ready:" + ctx.Request.Instance,
				Kind:    "echo.ready",
				Scope:   "integration:echo",
				Content: map[string]any{"ready": true},
			}}}, nil
		}),
		func(plugin *pluginbinding.Plugin) {
			plugin.Command(protocol.CommandAuthTest, func(ctx pluginbinding.Context) protocol.Response {
				material, err := protocol.DecodePayload[sdkmanifest.AuthMaterial](ctx.Request.Payload)
				if err != nil {
					return protocol.Fail("bad_auth_payload", err.Error())
				}
				if material.Method != "token" || material.Values["token"] != "abc123" {
					return protocol.Fail("bad_auth_material", "unexpected auth material")
				}
				return protocol.OK(map[string]any{
					"status":  "ok",
					"message": "token accepted",
					"details": map[string]string{"token": material.Values["token"]},
				})
			})
		},
	)
}
