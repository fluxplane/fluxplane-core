package kubernetesplugin

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
)

func TestNormalizeConfigSplitsSortsAndDeduplicatesNamespaces(t *testing.T) {
	cfg := NormalizeConfig(Config{Namespaces: []string{"platform, default", "platform", " kube-system "}})
	want := []string{"default", "kube-system", "platform"}
	if strings.Join(cfg.Namespaces, ",") != strings.Join(want, ",") {
		t.Fatalf("namespaces = %v, want %v", cfg.Namespaces, want)
	}
}

func TestNamespacePolicyRejectsNamespaceOutsideAllowlist(t *testing.T) {
	policy := policyFromConfig(Config{Namespaces: []string{"default"}}, "")
	if err := policy.AuthorizeNamespace("default"); err != nil {
		t.Fatalf("AuthorizeNamespace(default): %v", err)
	}
	if err := policy.AuthorizeNamespace("kube-system"); err == nil {
		t.Fatalf("AuthorizeNamespace(kube-system) succeeded, want rejection")
	}
}

func TestDatasourcePolicyCannotExpandPluginAllowlist(t *testing.T) {
	base := policyFromConfig(Config{Namespaces: []string{"default"}}, "")
	_, err := intersectPolicy(base, namespacePolicy{AllNamespaces: true})
	if err == nil || !strings.Contains(err.Error(), "cannot expand") {
		t.Fatalf("intersectPolicy all namespaces error = %v, want cannot expand", err)
	}
	_, err = intersectPolicy(base, namespacePolicy{Namespaces: []string{"kube-system"}})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("intersectPolicy namespace error = %v, want not allowed", err)
	}
}

func TestKubernetesAccessorListsPodsWithinNamespacePolicy(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: objectMeta("api", "default"), Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		&corev1.Pod{ObjectMeta: objectMeta("hidden", "kube-system"), Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	)
	plugin := NewWithClient(nil, client)
	plugin.ref = resource.PluginRef{Name: Name}
	plugin.cfg = NormalizeConfig(Config{Namespaces: []string{"default"}})
	provider := kubernetesDatasourceProvider{plugin: plugin}
	accessor, err := provider.Open(ctx, coredatasource.Spec{
		Name:     coredatasource.Name(Name),
		Kind:     Name,
		Entities: []coredatasource.EntityType{PodEntity},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	lister := accessor.(coredatasource.Lister)
	result, err := lister.List(ctx, coredatasource.ListRequest{Entity: PodEntity})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "default/api" {
		t.Fatalf("records = %#v, want only default/api", result.Records)
	}
}

func TestKubernetesAccessorRejectsDatasourceNamespaceExpansion(t *testing.T) {
	ctx := context.Background()
	plugin := NewWithClient(nil, fake.NewSimpleClientset())
	plugin.ref = resource.PluginRef{Name: Name}
	plugin.cfg = NormalizeConfig(Config{Namespaces: []string{"default"}})
	provider := kubernetesDatasourceProvider{plugin: plugin}
	_, err := provider.Open(ctx, coredatasource.Spec{
		Name:     coredatasource.Name(Name),
		Kind:     Name,
		Entities: []coredatasource.EntityType{PodEntity},
		Config:   map[string]string{"all_namespaces": "true"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot expand") {
		t.Fatalf("Open error = %v, want cannot expand", err)
	}
}

func TestKubernetesPluginContributesObserverAndDeriver(t *testing.T) {
	ctx := context.Background()
	plugin := NewWithClient(nil, fake.NewSimpleClientset())
	ref := resource.PluginRef{Name: Name, Instance: "dev"}
	instantiated, err := plugin.Instantiate(ctx, pluginhostContext(ref, Config{Context: "k3d-ai", Namespaces: []string{"ai-bots"}}))
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	resolved := instantiated.(Plugin)
	bundle, err := resolved.Contributions(ctx, pluginhostContext(ref, nil))
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Observers) != 1 || bundle.Observers[0].Name != kubernetesObserverName {
		t.Fatalf("observers = %#v, want kubernetes observer spec", bundle.Observers)
	}
	if len(bundle.SignalDerivers) != 1 || bundle.SignalDerivers[0].Name != kubernetesDeriverName {
		t.Fatalf("signal derivers = %#v, want kubernetes deriver spec", bundle.SignalDerivers)
	}
	observers, err := resolved.EnvironmentObservers(ctx, pluginhostContext(ref, nil))
	if err != nil {
		t.Fatalf("EnvironmentObservers: %v", err)
	}
	observations, diagnostics := runtimeenvironment.RunObservers(ctx, observers, runtimeenvironment.ObservationRequest{Phase: bundle.Observers[0].Phase})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	if len(observations) != 1 || observations[0].Kind != kubernetesObservationKind {
		t.Fatalf("observations = %#v, want kubernetes context", observations)
	}
	content := observations[0].Content.(map[string]any)
	if content["context"] != "k3d-ai" || content["namespace"] != "ai-bots" || content["available"] != true {
		t.Fatalf("content = %#v, want configured k3d-ai ai-bots availability", content)
	}
	derivers, err := resolved.SignalDerivers(ctx, pluginhostContext(ref, nil))
	if err != nil {
		t.Fatalf("SignalDerivers: %v", err)
	}
	signals, diagnostics := runtimeenvironment.DeriveSignals(ctx, derivers, runtimeenvironment.SignalDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("signal diagnostics = %#v, want none", diagnostics)
	}
	if !hasSignal(signals, "integration.configured", Name) || !hasSignal(signals, "integration.available", Name) {
		t.Fatalf("signals = %#v, want configured and available kubernetes signals", signals)
	}
}

func TestKubernetesAccessorListsServices(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset(&corev1.Service{ObjectMeta: objectMeta("web", "default"), Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.1"}})
	plugin := NewWithClient(nil, client)
	plugin.ref = resource.PluginRef{Name: Name}
	plugin.cfg = NormalizeConfig(Config{Namespaces: []string{"default"}})
	provider := kubernetesDatasourceProvider{plugin: plugin}
	accessor, err := provider.Open(ctx, coredatasource.Spec{Name: coredatasource.Name(Name), Kind: Name, Entities: []coredatasource.EntityType{ServiceEntity}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Lister).List(ctx, coredatasource.ListRequest{Entity: ServiceEntity})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "default/web" {
		t.Fatalf("records = %#v, want default/web", result.Records)
	}
}

func TestKubernetesRecordsRedactEnvVarValuesFromRawPodsAndContainers(t *testing.T) {
	accessor := kubernetesAccessor{spec: coredatasource.Spec{Name: coredatasource.Name(Name)}}
	pod := corev1.Pod{
		ObjectMeta: objectMeta("api", "default"),
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Name: "init",
				Env:  []corev1.EnvVar{{Name: "INIT_SECRET", Value: "hidden-init"}},
			}},
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "example/app:latest",
				Env: []corev1.EnvVar{
					{Name: "API_TOKEN", Value: "super-secret"},
					{Name: "PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{Key: "password"}}},
				},
			}},
			EphemeralContainers: []corev1.EphemeralContainer{{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon{
					Name: "debug",
					Env:  []corev1.EnvVar{{Name: "DEBUG_SECRET", Value: "hidden-debug"}},
				},
			}},
		},
	}

	record := accessor.podRecord(pod)
	rawPod, ok := record.Raw.(corev1.Pod)
	if !ok {
		t.Fatalf("pod raw type = %T, want corev1.Pod", record.Raw)
	}
	assertEnvRedacted(t, rawPod.Spec.InitContainers[0].Env[0], "INIT_SECRET")
	assertEnvRedacted(t, rawPod.Spec.Containers[0].Env[0], "API_TOKEN")
	assertEnvRedacted(t, rawPod.Spec.Containers[0].Env[1], "PASSWORD")
	assertEnvRedacted(t, rawPod.Spec.EphemeralContainers[0].Env[0], "DEBUG_SECRET")

	containerRecords := accessor.containerRecords(pod)
	if len(containerRecords) != 1 {
		t.Fatalf("container records = %d, want 1", len(containerRecords))
	}
	rawContainer, ok := containerRecords[0].Raw.(corev1.Container)
	if !ok {
		t.Fatalf("container raw type = %T, want corev1.Container", containerRecords[0].Raw)
	}
	assertEnvRedacted(t, rawContainer.Env[0], "API_TOKEN")
}

func assertEnvRedacted(t *testing.T, env corev1.EnvVar, name string) {
	t.Helper()
	if env.Name != name {
		t.Fatalf("env name = %q, want %q", env.Name, name)
	}
	if env.Value != "" || env.ValueFrom != nil {
		t.Fatalf("env %q was not redacted: %#v", name, env)
	}
}

func pluginhostContext(ref resource.PluginRef, cfg any) pluginhost.Context {
	return pluginhost.Context{Ref: ref, Config: cfg}
}

func hasSignal(signals []coreenvironment.Signal, kind, target string) bool {
	for _, signal := range signals {
		if signal.Kind == kind && signal.Target == target {
			return true
		}
	}
	return false
}

var _ = metav1.NamespaceDefault
