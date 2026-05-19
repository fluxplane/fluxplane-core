package kubernetesplugin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
	"github.com/fluxplane/agentruntime/runtime/system"
	"github.com/fluxplane/agentruntime/runtime/systemtest"
)

func TestPortForwardUsesManagedKubectlProcess(t *testing.T) {
	process := &recordingProcess{
		handle:  fakeProcessHandle{info: system.ProcessInfo{ID: "proc-1", Label: "custom-label", Command: "kubectl", Running: true}},
		started: true,
	}
	plugin := New(fakeSystem{MemorySystem: systemtest.NewMemory(), process: process})
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
	plugin := New(fakeSystem{MemorySystem: systemtest.NewMemory(), process: process})
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
	plugin := New(fakeSystem{MemorySystem: systemtest.NewMemory(), process: process})
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
	plugin := New(fakeSystem{MemorySystem: systemtest.NewMemory(), process: process})
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
		response: system.HTTPResponse{StatusCode: http.StatusOK, Body: []byte("ok")},
	}
	plugin := New(fakeSystem{MemorySystem: systemtest.NewMemory(), network: network})
	client, err := plugin.httpClientForRestConfig(&rest.Config{Host: "https://cluster.example", BearerToken: "token"})
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

type fakeSystem struct {
	*systemtest.MemorySystem
	process system.ProcessManager
	network system.Network
}

func (s fakeSystem) Process() system.ProcessManager { return s.process }

func (s fakeSystem) Network() system.Network {
	if s.network != nil {
		return s.network
	}
	return s.MemorySystem.Network()
}

type recordingProcess struct {
	ensureRequests []system.ProcessRequest
	handle         system.ProcessHandle
	started        bool
	err            error
}

type recordingNetwork struct {
	requests []system.HTTPRequest
	response system.HTTPResponse
	err      error
}

func (n *recordingNetwork) DoHTTP(_ context.Context, req system.HTTPRequest) (system.HTTPResponse, error) {
	n.requests = append(n.requests, req)
	if n.err != nil {
		return system.HTTPResponse{}, n.err
	}
	return n.response, nil
}

func (p *recordingProcess) Run(context.Context, system.ProcessRequest) (system.ProcessResult, error) {
	return system.ProcessResult{}, errors.New("not implemented")
}

func (p *recordingProcess) Start(context.Context, system.ProcessRequest) (system.ProcessHandle, error) {
	return nil, errors.New("not implemented")
}

func (p *recordingProcess) Ensure(_ context.Context, req system.ProcessRequest) (system.ProcessHandle, bool, error) {
	p.ensureRequests = append(p.ensureRequests, req)
	if p.err != nil {
		return nil, false, p.err
	}
	handle := p.handle
	if handle == nil {
		handle = fakeProcessHandle{info: system.ProcessInfo{ID: "proc-1", Label: req.Label, Command: req.Command, Args: req.Args, Running: true}}
	}
	return handle, p.started, nil
}

func (p *recordingProcess) List(context.Context) ([]system.ProcessInfo, error) {
	return nil, errors.New("not implemented")
}

func (p *recordingProcess) Status(context.Context, string) (system.ProcessInfo, error) {
	return system.ProcessInfo{}, errors.New("not implemented")
}

func (p *recordingProcess) Output(context.Context, string) (system.ProcessOutput, error) {
	return system.ProcessOutput{}, errors.New("not implemented")
}

func (p *recordingProcess) Wait(context.Context, string, time.Duration) (system.ProcessResult, error) {
	return system.ProcessResult{}, errors.New("not implemented")
}

func (p *recordingProcess) Stop(context.Context, string) error { return errors.New("not implemented") }

func (p *recordingProcess) Kill(context.Context, string) error { return errors.New("not implemented") }

type fakeProcessHandle struct {
	info system.ProcessInfo
}

func (h fakeProcessHandle) ID() string { return h.info.ID }

func (h fakeProcessHandle) Info() system.ProcessInfo { return h.info }

func (h fakeProcessHandle) Events() <-chan system.ProcessEvent {
	ch := make(chan system.ProcessEvent)
	close(ch)
	return ch
}

func (h fakeProcessHandle) Wait(context.Context) (system.ProcessResult, error) {
	return system.ProcessResult{Command: h.info.Command, Args: h.info.Args}, nil
}

var _ = metav1.NamespaceDefault
