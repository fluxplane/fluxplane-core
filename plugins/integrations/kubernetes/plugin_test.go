package kubernetes

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	fpsystemtest "github.com/fluxplane/fluxplane-system/systemtest"
)

func TestPortForwardUsesManagedKubectlProcess(t *testing.T) {
	process := &recordingProcess{
		handle:  fakeProcessHandle{info: fpsystem.ProcessInfo{ID: "proc-1", Label: "custom-label", Command: "kubectl", Running: true}},
		started: true,
	}
	plugin := NewWithBoundaries(Boundaries{Process: process})
	plugin.cfg = NormalizeConfig(Config{Namespaces: []string{"default"}, Context: "dev", Kubeconfig: "/tmp/kubeconfig"})

	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	op := findOperation(t, ops, PortForwardOp)
	result := op.Run(operation.NewContext(context.Background(), nil), portForwardInput{
		Namespace:  "default",
		Kind:       "service",
		Name:       "api",
		LocalPort:  18080,
		RemotePort: 80,
		Label:      "custom-label",
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("Run status = %s error = %#v", result.Status, result.Error)
	}
	if len(process.ensureRequests) != 1 {
		t.Fatalf("ensure requests = %d, want 1", len(process.ensureRequests))
	}
	req := process.ensureRequests[0]
	wantArgs := "--context dev --kubeconfig /tmp/kubeconfig -n default port-forward --address 127.0.0.1 service/api 18080:80"
	if strings.Join(req.Args, " ") != wantArgs {
		t.Fatalf("args = %q, want %q", strings.Join(req.Args, " "), wantArgs)
	}
	if req.Command != "kubectl" || req.Label != "custom-label" {
		t.Fatalf("process request = %#v, want kubectl custom-label", req)
	}
	if req.Metadata["resource"] != "service/api" || req.Metadata["remote_port"] != "80" {
		t.Fatalf("metadata = %#v", req.Metadata)
	}
}
func TestPortForwardInfersSingleServicePort(t *testing.T) {
	process := &recordingProcess{}
	plugin := NewWithBoundaries(Boundaries{Process: process})
	plugin.client = fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
	})
	plugin.cfg = NormalizeConfig(Config{Namespaces: []string{"default"}})

	result := portForwardOperation(plugin).Run(operation.NewContext(context.Background(), nil), portForwardInput{
		Namespace: "default",
		Kind:      "service",
		Name:      "api",
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("Run status = %s error = %#v", result.Status, result.Error)
	}
	if len(process.ensureRequests) != 1 {
		t.Fatalf("ensure requests = %d, want 1", len(process.ensureRequests))
	}
	args := strings.Join(process.ensureRequests[0].Args, " ")
	if !strings.Contains(args, "service/api 8080:8080") {
		t.Fatalf("args = %q, want inferred 8080:8080", args)
	}
}

func TestPortForwardFailsWhenServicePortInferenceIsAmbiguous(t *testing.T) {
	process := &recordingProcess{}
	plugin := NewWithBoundaries(Boundaries{Process: process})
	plugin.client = fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}, {Port: 443}}},
	})
	plugin.cfg = NormalizeConfig(Config{Namespaces: []string{"default"}})

	result := portForwardOperation(plugin).Run(operation.NewContext(context.Background(), nil), portForwardInput{
		Namespace: "default",
		Kind:      "service",
		Name:      "api",
	})
	if result.Status != operation.StatusFailed {
		t.Fatalf("Run status = %s, want failed", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Message, "multiple ports") {
		t.Fatalf("error = %#v, want multiple ports", result.Error)
	}
	if len(process.ensureRequests) != 0 {
		t.Fatalf("ensure requests = %d, want 0", len(process.ensureRequests))
	}
}

func TestPortForwardRejectsNamespaceOutsidePolicy(t *testing.T) {
	process := &recordingProcess{}
	plugin := NewWithBoundaries(Boundaries{Process: process})
	plugin.cfg = NormalizeConfig(Config{Namespaces: []string{"default"}})

	result := portForwardOperation(plugin).Run(operation.NewContext(context.Background(), nil), portForwardInput{
		Namespace:  "kube-system",
		Name:       "api",
		RemotePort: 80,
	})
	if result.Status != operation.StatusFailed {
		t.Fatalf("Run status = %s, want failed", result.Status)
	}
	if len(process.ensureRequests) != 0 {
		t.Fatalf("ensure requests = %d, want 0", len(process.ensureRequests))
	}
}

func TestPortForwardIntentIncludesKubectlCommand(t *testing.T) {
	intents, err := portForwardIntent(operation.NewContext(context.Background(), nil), portForwardInput{
		Namespace:  "default",
		Kind:       "svc",
		Name:       "api",
		RemotePort: 80,
	})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if len(intents.Operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(intents.Operations))
	}
	target, ok := intents.Operations[0].Target.(operation.ProcessTarget)
	if !ok {
		t.Fatalf("target = %T, want operation.ProcessTarget", intents.Operations[0].Target)
	}
	if target.Command != "kubectl" || !strings.Contains(argumentsString(target.Args), "service/api") {
		t.Fatalf("target = %#v", target)
	}
}

func findOperation(t *testing.T, ops []operation.Operation, name string) operation.Operation {
	t.Helper()
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			return op
		}
	}
	t.Fatalf("operation %q not found", name)
	return nil
}

func TestKubernetesRestHTTPClientUsesSystemNetworkBoundary(t *testing.T) {
	network := &recordingNetwork{
		response: systemkit.HTTPResponse{StatusCode: http.StatusOK, Body: []byte("ok")},
	}
	plugin := NewWithBoundaries(Boundaries{Network: network})
	client, err := plugin.httpClientForRestConfig(&rest.Config{
		Host:        "https://cluster.example",
		BearerToken: "token",
		TLSClientConfig: rest.TLSClientConfig{
			ServerName: "kubernetes.example",
		},
	})
	if err != nil {
		t.Fatalf("httpClientForRestConfig: %v", err)
	}

	resp, err := client.Get("https://cluster.example/api/v1/namespaces/default/pods")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if len(network.requests) != 1 {
		t.Fatalf("network requests = %d, want 1", len(network.requests))
	}
	req := network.requests[0]
	if req.URL != "https://cluster.example/api/v1/namespaces/default/pods" {
		t.Fatalf("url = %q", req.URL)
	}
	if req.Headers["Authorization"] != "Bearer token" {
		t.Fatalf("authorization header = %q", req.Headers["Authorization"])
	}
	if req.TLSConfig == nil {
		t.Fatalf("TLS config was not forwarded to system network")
	}
	if req.TLSConfig.ServerName != "kubernetes.example" {
		t.Fatalf("TLS server name = %q, want kubernetes.example", req.TLSConfig.ServerName)
	}
	if req.TLSConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("TLS min version = %d, want at least TLS 1.2", req.TLSConfig.MinVersion)
	}
}

func argumentsString(args []operation.Argument) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, string(arg))
	}
	return strings.Join(parts, " ")
}

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
	plugin := NewWithBoundariesAndClient(Boundaries{}, client)
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

func TestKubernetesAccessorDefaultsToAllNamespacesAndHonorsRequestNamespace(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: objectMeta("api", "default"), Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		&corev1.Pod{ObjectMeta: objectMeta("slack-bot-abc", "ai-bots"), Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	)
	plugin := NewWithBoundariesAndClient(Boundaries{}, client)
	plugin.ref = resource.PluginRef{Name: Name}
	provider := kubernetesDatasourceProvider{plugin: plugin}
	accessor, err := provider.Open(ctx, coredatasource.Spec{
		Name:     coredatasource.Name(Name),
		Kind:     Name,
		Entities: []coredatasource.EntityType{PodEntity},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	all, err := accessor.(coredatasource.Lister).List(ctx, coredatasource.ListRequest{Entity: PodEntity})
	if err != nil {
		t.Fatalf("List all namespaces: %v", err)
	}
	if len(all.Records) != 2 {
		t.Fatalf("all namespace records = %#v, want 2 pods", all.Records)
	}
	filtered, err := accessor.(coredatasource.Searcher).Search(ctx, coredatasource.SearchRequest{
		Entity:  PodEntity,
		Query:   "slack-bot",
		Filters: map[string]string{"namespace": "ai-bots"},
	})
	if err != nil {
		t.Fatalf("Search ai-bots: %v", err)
	}
	if len(filtered.Records) != 1 || filtered.Records[0].ID != "ai-bots/slack-bot-abc" {
		t.Fatalf("filtered records = %#v, want ai-bots/slack-bot-abc", filtered.Records)
	}
}

func TestKubernetesAccessorRejectsDatasourceNamespaceExpansion(t *testing.T) {
	ctx := context.Background()
	plugin := NewWithBoundariesAndClient(Boundaries{}, fake.NewSimpleClientset())
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

func TestKubernetesAccessorListsDeployments(t *testing.T) {
	ctx := context.Background()
	replicas := int32(2)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: objectMeta("slack-bot", "ai-bots"),
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:  "app",
				Image: "example/slack-bot:latest",
			}}}},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 2, AvailableReplicas: 2, UpdatedReplicas: 2},
	})
	plugin := NewWithBoundariesAndClient(Boundaries{}, client)
	plugin.ref = resource.PluginRef{Name: Name}
	provider := kubernetesDatasourceProvider{plugin: plugin}
	accessor, err := provider.Open(ctx, coredatasource.Spec{Name: coredatasource.Name(Name), Kind: Name, Entities: []coredatasource.EntityType{DeploymentEntity}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Lister).List(ctx, coredatasource.ListRequest{Entity: DeploymentEntity, Filters: map[string]string{"namespace": "ai-bots"}})
	if err != nil {
		t.Fatalf("List deployments: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "ai-bots/slack-bot" || result.Records[0].Metadata["ready_replicas"] != "2" {
		t.Fatalf("deployment records = %#v, want ai-bots/slack-bot ready", result.Records)
	}
}

func TestKubernetesPluginContributesObserverAndDeriver(t *testing.T) {
	ctx := context.Background()
	plugin := NewWithBoundariesAndClient(Boundaries{}, fake.NewSimpleClientset())
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
	if len(bundle.AssertionDerivers) != 1 || bundle.AssertionDerivers[0].Name != kubernetesDeriverName {
		t.Fatalf("assertion derivers = %#v, want kubernetes deriver spec", bundle.AssertionDerivers)
	}
	observers, err := resolved.EnvironmentObservers(ctx, pluginhostContext(ref, nil))
	if err != nil {
		t.Fatalf("EnvironmentObservers: %v", err)
	}
	observations, diagnostics := runtimeevidence.RunObservers(ctx, observers, runtimeevidence.ObservationRequest{Phase: bundle.Observers[0].Phase})
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
	derivers, err := resolved.AssertionDerivers(ctx, pluginhostContext(ref, nil))
	if err != nil {
		t.Fatalf("AssertionDerivers: %v", err)
	}
	assertions, diagnostics := runtimeevidence.DeriveAssertions(ctx, derivers, runtimeevidence.AssertionDeriveRequest{Observations: observations})
	if len(diagnostics) != 0 {
		t.Fatalf("assertion diagnostics = %#v, want none", diagnostics)
	}
	if !hasAssertion(assertions, "integration.configured", Name) || !hasAssertion(assertions, "integration.available", Name) {
		t.Fatalf("assertions = %#v, want configured and available kubernetes assertions", assertions)
	}
}

func TestKubernetesAccessorListsServices(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset(&corev1.Service{ObjectMeta: objectMeta("web", "default"), Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.1"}})
	plugin := NewWithBoundariesAndClient(Boundaries{}, client)
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

func TestKubernetesAccessorListsAndGetsKubeconfigClusters(t *testing.T) {
	ctx := context.Background()
	kubeconfig := writeKubeconfig(t, `
apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: dev-cluster
  cluster:
    server: https://dev.example
    certificate-authority-data: c2VjcmV0LWNhLWRhdGE=
- name: prod-cluster
  cluster:
    server: https://prod.example
contexts:
- name: dev
  context:
    cluster: dev-cluster
    namespace: dev-ns
    user: dev-user
- name: prod
  context:
    cluster: prod-cluster
    namespace: prod-ns
    user: prod-user
users:
- name: dev-user
  user:
    token: hidden-token
- name: prod-user
  user:
    client-certificate-data: c2VjcmV0LWNlcnQtZGF0YQ==
    client-key-data: c2VjcmV0LWtleS1kYXRh
`)
	accessor := openKubernetesAccessor(t, Plugin{cfg: NormalizeConfig(Config{Kubeconfig: kubeconfig})}, []coredatasource.EntityType{ClusterEntity})

	result, err := accessor.(coredatasource.Lister).List(ctx, coredatasource.ListRequest{Entity: ClusterEntity})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Records) != 2 {
		t.Fatalf("records = %d, want 2: %#v", len(result.Records), result.Records)
	}
	dev := result.Records[0]
	if dev.ID != "dev" || dev.Metadata["cluster"] != "dev-cluster" || dev.Metadata["server"] != "https://dev.example" || dev.Metadata["namespace"] != "dev-ns" || dev.Metadata["current_context"] != "true" {
		t.Fatalf("dev record = %#v", dev)
	}
	prod, err := accessor.(coredatasource.Getter).Get(ctx, coredatasource.GetRequest{Entity: ClusterEntity, ID: "prod-cluster"})
	if err != nil {
		t.Fatalf("Get by cluster name: %v", err)
	}
	if prod.ID != "prod" || prod.Metadata["context"] != "prod" {
		t.Fatalf("prod record = %#v", prod)
	}
}

func TestKubernetesClusterRecordsDoNotLeakKubeconfigSecrets(t *testing.T) {
	ctx := context.Background()
	kubeconfig := writeKubeconfig(t, `
apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: dev-cluster
  cluster:
    server: https://dev.example
    certificate-authority-data: c2VjcmV0LWNhLWRhdGE=
contexts:
- name: dev
  context:
    cluster: dev-cluster
    namespace: dev-ns
    user: dev-user
users:
- name: dev-user
  user:
    token: secret-token
    client-certificate-data: c2VjcmV0LWNlcnQtZGF0YQ==
    client-key-data: c2VjcmV0LWtleS1kYXRh
    auth-provider:
      name: oidc
      config:
        id-token: secret-id-token
    exec:
      command: secret-command
      args: [secret-arg]
`)
	accessor := openKubernetesAccessor(t, Plugin{cfg: NormalizeConfig(Config{Kubeconfig: kubeconfig})}, []coredatasource.EntityType{ClusterEntity})
	result, err := accessor.(coredatasource.Lister).List(ctx, coredatasource.ListRequest{Entity: ClusterEntity})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	serialized := strings.Join([]string{result.Records[0].ID, result.Records[0].Title, result.Records[0].Content, metadataText(result.Records[0].Metadata)}, " ")
	for _, secret := range []string{"c2VjcmV0LWNhLWRhdGE=", "secret-token", "c2VjcmV0LWNlcnQtZGF0YQ==", "c2VjcmV0LWtleS1kYXRh", "secret-id-token", "secret-command", "secret-arg"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("cluster record leaked %q in %q", secret, serialized)
		}
	}
	if _, ok := result.Records[0].Raw.(clusterRecord); !ok {
		t.Fatalf("raw = %T, want sanitized clusterRecord", result.Records[0].Raw)
	}
}

func TestKubernetesClusterSearchAndEntitySelection(t *testing.T) {
	ctx := context.Background()
	kubeconfig := writeKubeconfig(t, `
apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: dev-cluster
  cluster:
    server: https://dev.example
- name: prod-cluster
  cluster:
    server: https://prod.example
contexts:
- name: dev
  context:
    cluster: dev-cluster
- name: prod
  context:
    cluster: prod-cluster
`)
	plugin := Plugin{cfg: NormalizeConfig(Config{Kubeconfig: kubeconfig})}
	accessor := openKubernetesAccessor(t, plugin, []coredatasource.EntityType{ClusterEntity})
	search, err := accessor.(coredatasource.Searcher).Search(ctx, coredatasource.SearchRequest{Entity: ClusterEntity, Query: "prod"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(search.Records) != 1 || search.Records[0].ID != "prod" {
		t.Fatalf("search records = %#v, want prod", search.Records)
	}
	_, err = accessor.(coredatasource.Lister).List(ctx, coredatasource.ListRequest{Entity: PodEntity})
	if err == nil || !strings.Contains(err.Error(), "does not expose") {
		t.Fatalf("List pod error = %v, want does not expose", err)
	}
}

func openKubernetesAccessor(t *testing.T, plugin Plugin, entities []coredatasource.EntityType) coredatasource.Accessor {
	t.Helper()
	plugin.ref = resource.PluginRef{Name: Name}
	provider := kubernetesDatasourceProvider{plugin: plugin}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{Name: coredatasource.Name(Name), Kind: Name, Entities: entities})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return accessor
}

func writeKubeconfig(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/config"
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func metadataText(metadata map[string]string) string {
	parts := make([]string, 0, len(metadata))
	for key, value := range metadata {
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
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

func TestKubernetesDeploymentRecordRedactsEnvAndLastAppliedAnnotation(t *testing.T) {
	accessor := kubernetesAccessor{spec: coredatasource.Spec{Name: coredatasource.Name(Name)}}
	deployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api",
			Namespace: "default",
			Annotations: map[string]string{
				"kubectl.kubernetes.io/last-applied-configuration": `{"env":[{"name":"TOKEN","value":"secret"}]}`,
				"safe": "kept",
			},
		},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app",
			Env:  []corev1.EnvVar{{Name: "TOKEN", Value: "secret"}},
		}}}}},
	}

	record := accessor.deploymentRecord(deployment)
	rawDeployment, ok := record.Raw.(appsv1.Deployment)
	if !ok {
		t.Fatalf("deployment raw type = %T, want appsv1.Deployment", record.Raw)
	}
	if _, ok := rawDeployment.Annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Fatalf("last-applied annotation was not redacted: %#v", rawDeployment.Annotations)
	}
	if rawDeployment.Annotations["safe"] != "kept" {
		t.Fatalf("safe annotation = %#v, want kept", rawDeployment.Annotations)
	}
	assertEnvRedacted(t, rawDeployment.Spec.Template.Spec.Containers[0].Env[0], "TOKEN")
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

func hasAssertion(assertions []coreevidence.Assertion, kind, target string) bool {
	for _, assertion := range assertions {
		if assertion.Kind == kind && assertion.Target == target {
			return true
		}
	}
	return false
}

type recordingProcess struct {
	ensureRequests []fpsystem.ProcessRequest
	handle         fpsystem.ProcessHandle
	started        bool
	err            error
}

type recordingNetwork struct {
	fpsystemtest.UnsupportedNetwork
	requests []systemkit.HTTPRequest
	response systemkit.HTTPResponse
	err      error
}

func (n *recordingNetwork) DoHTTP(_ context.Context, req systemkit.HTTPRequest) (systemkit.HTTPResponse, error) {
	n.requests = append(n.requests, req)
	if n.err != nil {
		return systemkit.HTTPResponse{}, n.err
	}
	return n.response, nil
}

func (p *recordingProcess) Run(context.Context, fpsystem.ProcessRequest) (fpsystem.ProcessResult, error) {
	return fpsystem.ProcessResult{}, errors.New("not implemented")
}

func (p *recordingProcess) Start(context.Context, fpsystem.ProcessRequest) (fpsystem.ProcessHandle, error) {
	return nil, errors.New("not implemented")
}

func (p *recordingProcess) Ensure(_ context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessHandle, bool, error) {
	p.ensureRequests = append(p.ensureRequests, req)
	if p.err != nil {
		return nil, false, p.err
	}
	handle := p.handle
	if handle == nil {
		handle = fakeProcessHandle{info: fpsystem.ProcessInfo{ID: "proc-1", Label: req.Label, Command: req.Command, Args: req.Args, Running: true}}
	}
	return handle, p.started, nil
}

func (p *recordingProcess) Group(string) fpsystem.ProcessGroup { return nil }

func (p *recordingProcess) List(context.Context) ([]fpsystem.ProcessInfo, error) {
	return nil, errors.New("not implemented")
}

type fakeProcessHandle struct {
	info fpsystem.ProcessInfo
}

func (h fakeProcessHandle) ID() string { return h.info.ID }

func (h fakeProcessHandle) Info() fpsystem.ProcessInfo { return h.info }

func (h fakeProcessHandle) Events() <-chan fpsystem.ProcessEvent {
	ch := make(chan fpsystem.ProcessEvent)
	close(ch)
	return ch
}

func (h fakeProcessHandle) Subscribe(context.Context) <-chan fpsystem.ProcessEvent { return h.Events() }

func (h fakeProcessHandle) Wait(context.Context) (fpsystem.ProcessResult, error) {
	return fpsystem.ProcessResult{Command: h.info.Command, Args: h.info.Args}, nil
}

func (h fakeProcessHandle) Stop(context.Context) error                              { return nil }
func (h fakeProcessHandle) Kill(context.Context) error                              { return nil }
func (h fakeProcessHandle) Signal(context.Context, fpsystem.ProcessSignal) error    { return nil }
func (h fakeProcessHandle) Interrupt(context.Context) error                         { return nil }
func (h fakeProcessHandle) Reload(context.Context) error                            { return nil }
func (h fakeProcessHandle) Pause(context.Context) error                             { return nil }
func (h fakeProcessHandle) Resume(context.Context) error                            { return nil }
func (h fakeProcessHandle) Write(context.Context, []byte) (int, error)              { return 0, nil }
func (h fakeProcessHandle) CloseInput(context.Context) error                        { return nil }
func (h fakeProcessHandle) Restart(context.Context) (fpsystem.ProcessHandle, error) { return h, nil }
func (h fakeProcessHandle) Detach(context.Context) error                            { return nil }

var _ = metav1.NamespaceDefault
