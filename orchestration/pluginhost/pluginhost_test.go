package pluginhost

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/operation"
	corereaction "github.com/fluxplane/agentruntime/core/reaction"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/core/skill"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
)

func TestHostResolvesPluginContributions(t *testing.T) {
	host, err := New(fakePlugin{name: "echo"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "echo"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Bundles) != 1 || len(resolution.Bundles[0].Commands) != 1 {
		t.Fatalf("resolution = %#v", resolution)
	}
}

func TestHostRejectsUnknownPlugin(t *testing.T) {
	host, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = host.Resolve(context.Background(), resource.PluginRef{Name: "missing"})
	if err == nil {
		t.Fatal("Resolve error is nil, want unknown plugin error")
	}
}

type fakePlugin struct {
	name string
}

func (p fakePlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakePlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Commands: []command.Spec{{Path: command.Path{"echo"}}},
	}, nil
}

func TestHostResolvesPluginOperations(t *testing.T) {
	host, err := New(fakeOperationPlugin{name: "echo"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "echo"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Operations) != 1 {
		t.Fatalf("operations len = %d, want 1", len(resolution.Operations))
	}
	if resolution.Operations[0].Operation.Spec().Ref.Name != "echo" {
		t.Fatalf("operation = %#v", resolution.Operations[0].Operation.Spec().Ref)
	}
	if resolution.Operations[0].Source.ID != "plugin:echo" {
		t.Fatalf("source ID = %q, want plugin:echo", resolution.Operations[0].Source.ID)
	}
}

func TestHostResolvesNamedPluginInstanceSource(t *testing.T) {
	host, err := New(fakeOperationPlugin{name: "echo"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "echo", Instance: "company-a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Bundles) != 1 {
		t.Fatalf("bundles len = %d, want 1", len(resolution.Bundles))
	}
	if got, want := resolution.Bundles[0].Source.ID, "plugin:echo/company-a"; got != want {
		t.Fatalf("source ID = %q, want %q", got, want)
	}
	if got, want := resolution.Bundles[0].Source.Location, "plugins/echo/company-a"; got != want {
		t.Fatalf("source location = %q, want %q", got, want)
	}
}

func TestHostInstantiatesPluginFactoryPerRef(t *testing.T) {
	host, err := New(fakeFactoryPlugin{name: "echo"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(),
		resource.PluginRef{Name: "echo", Instance: "company-a"},
		resource.PluginRef{Name: "echo", Instance: "company-b"},
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Bundles) != 2 {
		t.Fatalf("bundles len = %d, want 2", len(resolution.Bundles))
	}
	if got := resolution.Bundles[0].Commands[0].Path.String(); got != "/company-a" {
		t.Fatalf("first command = %q, want /company-a", got)
	}
	if got := resolution.Bundles[1].Commands[0].Path.String(); got != "/company-b" {
		t.Fatalf("second command = %q, want /company-b", got)
	}
}

func TestHostDecodesTypedPluginConfigBeforeInstantiate(t *testing.T) {
	host, err := New(fakeConfiguredFactoryPlugin{name: "echo"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{
		Name:     "echo",
		Instance: "company-a",
		Config:   map[string]any{"prefix": "configured"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Bundles) != 1 || len(resolution.Bundles[0].Commands) != 1 {
		t.Fatalf("resolution = %#v", resolution)
	}
	if got := resolution.Bundles[0].Commands[0].Path.String(); got != "/configured/company-a" {
		t.Fatalf("command = %q, want /configured/company-a", got)
	}
}

func TestHostRejectsInvalidTypedPluginConfig(t *testing.T) {
	host, err := New(fakeConfiguredFactoryPlugin{name: "echo"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = host.Resolve(context.Background(), resource.PluginRef{
		Name:   "echo",
		Config: map[string]any{"prefix": make(chan int)},
	})
	if err == nil {
		t.Fatal("Resolve error is nil, want config decode error")
	}
}

func TestHostResolvesConnectorProviders(t *testing.T) {
	host, err := New(fakeConnectorProviderPlugin{name: "openai"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "openai"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.ConnectorProviders) != 1 {
		t.Fatalf("connector providers len = %d, want 1", len(resolution.ConnectorProviders))
	}
	if resolution.ConnectorProviders[0].Provider.Name != "openai" {
		t.Fatalf("provider = %#v, want openai", resolution.ConnectorProviders[0].Provider)
	}
	if resolution.ConnectorProviders[0].Source.ID != "plugin:openai" {
		t.Fatalf("source ID = %q, want plugin:openai", resolution.ConnectorProviders[0].Source.ID)
	}
}

func TestHostResolvesDatasourceProviders(t *testing.T) {
	host, err := New(fakeDatasourceProviderPlugin{name: "docs"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "docs"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.DatasourceProviders) != 1 {
		t.Fatalf("datasource providers len = %d, want 1", len(resolution.DatasourceProviders))
	}
	if len(resolution.DatasourceProviders[0].Provider.Entities()) != 1 {
		t.Fatalf("provider = %#v, want one entity", resolution.DatasourceProviders[0].Provider)
	}
	if resolution.DatasourceProviders[0].Source.ID != "plugin:docs" {
		t.Fatalf("source ID = %q, want plugin:docs", resolution.DatasourceProviders[0].Source.ID)
	}
}

func TestHostResolvesAuthMethods(t *testing.T) {
	host, err := New(fakeAuthMethodPlugin{name: "gitlab"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "gitlab", Instance: "company-a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.AuthMethods) != 1 {
		t.Fatalf("auth methods len = %d, want 1", len(resolution.AuthMethods))
	}
	method := resolution.AuthMethods[0].Method
	if method.Name != "personal_access_token" || method.Method != coresecret.AuthMethodEnv || method.Env.Name != "GITLAB_PERSONAL_ACCESS_TOKEN" {
		t.Fatalf("auth method = %#v", method)
	}
	if resolution.AuthMethods[0].Source.ID != "plugin:gitlab/company-a" {
		t.Fatalf("source ID = %q, want plugin:gitlab/company-a", resolution.AuthMethods[0].Source.ID)
	}
}

func TestHostResolvesContextProviders(t *testing.T) {
	host, err := New(fakeContextProviderPlugin{name: "docs"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "docs"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.ContextProviders) != 1 {
		t.Fatalf("context providers len = %d, want 1", len(resolution.ContextProviders))
	}
	if resolution.ContextProviders[0].Provider.Spec().Name != "docs.catalog" {
		t.Fatalf("provider = %#v, want docs.catalog", resolution.ContextProviders[0].Provider.Spec())
	}
	if resolution.ContextProviders[0].Source.ID != "plugin:docs" {
		t.Fatalf("source ID = %q, want plugin:docs", resolution.ContextProviders[0].Source.ID)
	}
}

func TestHostResolvesEnvironmentObservers(t *testing.T) {
	host, err := New(fakeObserverPlugin{name: "kubernetes"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "kubernetes"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Observers) != 1 {
		t.Fatalf("observers len = %d, want 1", len(resolution.Observers))
	}
	if resolution.Observers[0].Observer.Spec().Name != "kubernetes.context" {
		t.Fatalf("observer spec = %#v", resolution.Observers[0].Observer.Spec())
	}
	if resolution.Observers[0].Source.ID != "plugin:kubernetes" {
		t.Fatalf("source ID = %q, want plugin:kubernetes", resolution.Observers[0].Source.ID)
	}
}

func TestHostResolvesSignalDerivers(t *testing.T) {
	host, err := New(fakeSignalDeriverPlugin{name: "kubernetes"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "kubernetes"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.SignalDerivers) != 1 {
		t.Fatalf("signal derivers len = %d, want 1", len(resolution.SignalDerivers))
	}
	if resolution.SignalDerivers[0].Deriver.Spec().Name != "kubernetes.signals" {
		t.Fatalf("deriver spec = %#v", resolution.SignalDerivers[0].Deriver.Spec())
	}
	if resolution.SignalDerivers[0].Source.ID != "plugin:kubernetes" {
		t.Fatalf("source ID = %q, want plugin:kubernetes", resolution.SignalDerivers[0].Source.ID)
	}
}

func TestHostResolvesReactions(t *testing.T) {
	host, err := New(fakeReactionPlugin{name: "skills"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolution, err := host.Resolve(context.Background(), resource.PluginRef{Name: "skills"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution.Reactions) != 1 {
		t.Fatalf("reactions len = %d, want 1", len(resolution.Reactions))
	}
	if resolution.Reactions[0].Rule.Name != "go-skill" {
		t.Fatalf("reaction = %#v", resolution.Reactions[0].Rule)
	}
	if resolution.Reactions[0].Source.ID != "plugin:skills" {
		t.Fatalf("source ID = %q, want plugin:skills", resolution.Reactions[0].Source.ID)
	}
}

type fakeOperationPlugin struct {
	name string
}

func (p fakeOperationPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

type fakeFactoryPlugin struct {
	name string
}

func (p fakeFactoryPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeFactoryPlugin) Instantiate(_ context.Context, ctx Context) (Plugin, error) {
	return fakeInstancePlugin{name: ctx.Ref.InstanceName()}, nil
}

func (p fakeFactoryPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

type fakeInstancePlugin struct {
	name string
}

func (p fakeInstancePlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeInstancePlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Commands: []command.Spec{{Path: command.Path{p.name}}},
	}, nil
}

type fakeConfiguredFactoryPlugin struct {
	Configurable[fakePluginConfig]
	name string
}

type fakePluginConfig struct {
	Prefix string `json:"prefix,omitempty"`
}

func (p fakeConfiguredFactoryPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeConfiguredFactoryPlugin) Instantiate(_ context.Context, ctx Context) (Plugin, error) {
	cfg, err := ConfigAs[fakePluginConfig](ctx)
	if err != nil {
		return nil, err
	}
	return fakeInstancePlugin{name: cfg.Prefix + "/" + ctx.Ref.InstanceName()}, nil
}

func (p fakeConfiguredFactoryPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeOperationPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeOperationPlugin) Operations(context.Context, Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operation.New(operation.Spec{Ref: operation.Ref{Name: operation.Name(p.name)}}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(input)
		}),
	}, nil
}

type fakeConnectorProviderPlugin struct {
	name string
}

func (p fakeConnectorProviderPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeConnectorProviderPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeConnectorProviderPlugin) ConnectorProviders(context.Context, Context) ([]ConnectorProvider, error) {
	return []ConnectorProvider{{Name: p.name}}, nil
}

type fakeDatasourceProviderPlugin struct {
	name string
}

func (p fakeDatasourceProviderPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeDatasourceProviderPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeDatasourceProviderPlugin) DatasourceProviders(context.Context, Context) ([]coredatasource.Provider, error) {
	return []coredatasource.Provider{fakeDatasourceProvider{}}, nil
}

type fakeDatasourceProvider struct{}

func (fakeDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{{Type: "docs.page"}}
}

func (fakeDatasourceProvider) Open(context.Context, coredatasource.Spec) (coredatasource.Accessor, error) {
	return nil, nil
}

type fakeContextProviderPlugin struct {
	name string
}

func (p fakeContextProviderPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeContextProviderPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeContextProviderPlugin) ContextProviders(context.Context, Context) ([]corecontext.Provider, error) {
	return []corecontext.Provider{fakeContextProvider{}}, nil
}

type fakeContextProvider struct{}

func (fakeContextProvider) Spec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{Name: "docs.catalog"}
}

func (fakeContextProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	return []corecontext.Block{{Provider: "docs.catalog", Kind: corecontext.BlockText, Content: "docs"}}, nil
}

type fakeObserverPlugin struct {
	name string
}

func (p fakeObserverPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeObserverPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeObserverPlugin) EnvironmentObservers(context.Context, Context) ([]runtimeenvironment.Observer, error) {
	return []runtimeenvironment.Observer{fakeObserver{}}, nil
}

type fakeObserver struct{}

func (fakeObserver) Spec() coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{Name: "kubernetes.context", Phase: coreenvironment.PhaseTurn}
}

func (fakeObserver) Observe(context.Context, runtimeenvironment.ObservationRequest) ([]coreenvironment.Observation, error) {
	return []coreenvironment.Observation{{Kind: "kubernetes.context"}}, nil
}

type fakeSignalDeriverPlugin struct {
	name string
}

func (p fakeSignalDeriverPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeSignalDeriverPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeSignalDeriverPlugin) SignalDerivers(context.Context, Context) ([]runtimeenvironment.SignalDeriver, error) {
	return []runtimeenvironment.SignalDeriver{fakeSignalDeriver{}}, nil
}

type fakeSignalDeriver struct{}

func (fakeSignalDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return coreenvironment.SignalDeriverSpec{Name: "kubernetes.signals", ObservationKinds: []string{"kubernetes.context"}}
}

func (fakeSignalDeriver) Derive(context.Context, runtimeenvironment.SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	return []coreenvironment.Signal{{Kind: "integration.available", Target: "kubernetes"}}, nil
}

type fakeReactionPlugin struct {
	name string
}

func (p fakeReactionPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeReactionPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeReactionPlugin) Reactions(context.Context, Context) ([]corereaction.Rule, error) {
	return []corereaction.Rule{{
		Name: "go-skill",
		When: corereaction.Matcher{Signal: "language.detected", Target: "go"},
		Actions: []corereaction.Action{{
			Kind:  corereaction.ActionActivateSkill,
			Skill: skill.Ref{Name: "go"},
		}},
	}}, nil
}

type fakeAuthMethodPlugin struct {
	name string
}

func (p fakeAuthMethodPlugin) Manifest() Manifest {
	return Manifest{Name: p.name}
}

func (p fakeAuthMethodPlugin) Contributions(context.Context, Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeAuthMethodPlugin) AuthMethods(context.Context, Context) ([]coresecret.AuthMethodSpec, error) {
	return []coresecret.AuthMethodSpec{{
		Name:   "personal_access_token",
		Method: coresecret.AuthMethodEnv,
		Kind:   coresecret.KindAPIKey,
		Env:    coresecret.EnvSpec{Name: "GITLAB_PERSONAL_ACCESS_TOKEN"},
	}}, nil
}
