package kubernetesplugin

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/resource"
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

var _ = metav1.NamespaceDefault
